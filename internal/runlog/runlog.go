// Package runlog stores bounded, private runtime events. Callers MUST supply
// safe lifecycle messages, never request bodies, credentials, headers or URLs.
// Control-character cleanup is not credential redaction.
package runlog

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"panel/internal/platform"
)

const (
	maxFileBytes = 1 << 20
	maxLineBytes = 8 << 10
	backupCount  = 3
	truncated    = " [已截断]"
)

var logNames = [...]string{"runtime.log", "runtime.log.1", "runtime.log.2", "runtime.log.3"}

// Writer serializes writes, rotation and Close without background goroutines.
// A Write is one or more complete events; an unterminated event gets a newline.
// On a storage error it fails closed until reopened, without logging its input.
type Writer struct {
	mu        sync.Mutex
	directory string
	file      *os.File
	size      int64
	closed    bool
	failed    error
}

var _ io.WriteCloser = (*Writer)(nil)

// Open creates a private directory if needed, then appends to fixed runtime.log.
// Existing unsafe files/permissions are rejected, never silently repaired.
func Open(directory string) (*Writer, error) {
	if directory == "" || strings.ContainsRune(directory, 0) {
		return nil, errors.New("运行日志目录无效")
	}
	directory, err := filepath.Abs(directory)
	if err != nil || filepath.Dir(directory) == directory {
		return nil, errors.New("运行日志需要明确的本地目录")
	}
	if err := checkDirectory(directory); err != nil {
		return nil, err
	}
	if err := platform.PreparePrivateDirectory(directory); err != nil {
		return nil, errors.New("无法创建或验证私有运行日志目录")
	}
	w := &Writer{directory: directory}
	if err := w.checkFiles(); err != nil {
		return nil, err
	}
	file, size, err := w.openCurrent()
	if err != nil {
		return nil, err
	}
	w.file, w.size = file, size
	return w, nil
}

func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.file == nil {
		return 0, errors.New("运行日志已关闭")
	}
	if w.failed != nil {
		return 0, w.failed
	}
	consumed := 0
	for consumed < len(p) {
		end := consumed
		for end < len(p) && p[end] != '\n' {
			end++
		}
		line := safeLine(p[consumed:end])
		if err := w.writeLine(line); err != nil {
			w.failed = err
			return consumed, err
		}
		consumed = end
		if consumed < len(p) {
			consumed++
		}
	}
	return len(p), nil
}

func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	if w.file == nil {
		return w.failed
	}
	syncErr := w.file.Sync()
	closeErr := w.file.Close()
	w.file = nil
	if w.failed != nil {
		return w.failed
	}
	if syncErr != nil || closeErr != nil {
		return errors.New("无法同步或关闭运行日志")
	}
	return nil
}

func safeLine(input []byte) []byte {
	line := make([]byte, 0, min(len(input)+1, maxLineBytes))
	limit := maxLineBytes - len(truncated) - 1
	cut := false
	for len(input) > 0 {
		r, n := utf8.DecodeRune(input)
		input = input[n:]
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029' {
			r = ' '
		}
		if len(line)+utf8.RuneLen(r) > limit {
			cut = true
			break
		}
		line = utf8.AppendRune(line, r)
	}
	if cut {
		line = append(line, truncated...)
	}
	return append(line, '\n')
}

func (w *Writer) writeLine(line []byte) error {
	if w.size+int64(len(line)) > maxFileBytes {
		if err := w.rotate(); err != nil {
			return err
		}
	}
	n, err := w.file.Write(line)
	w.size += int64(n)
	if err != nil || n != len(line) {
		return errors.New("运行日志写入失败")
	}
	return nil
}

func (w *Writer) checkFiles() error {
	for _, name := range logNames {
		path := filepath.Join(w.directory, name)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxFileBytes {
			return errors.New("运行日志目标不是普通文件，或已超过大小限制")
		}
		if err := platform.CheckPrivateFile(path); err != nil {
			return errors.New("运行日志文件的私有权限验证失败")
		}
		if err := checkFileLinks(path, info); err != nil {
			return err
		}
	}
	return nil
}

func (w *Writer) openCurrent() (*os.File, int64, error) {
	path := filepath.Join(w.directory, logNames[0])
	before, err := os.Lstat(path)
	flags := os.O_WRONLY | os.O_APPEND
	if errors.Is(err, os.ErrNotExist) {
		flags |= os.O_CREATE | os.O_EXCL
	} else if err != nil || !before.Mode().IsRegular() {
		return nil, 0, errors.New("无法安全打开运行日志目标")
	}
	file, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return nil, 0, errors.New("无法打开私有运行日志")
	}
	fail := func() (*os.File, int64, error) {
		_ = file.Close()
		return nil, 0, errors.New("运行日志文件或继承权限不安全")
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || opened.Size() > maxFileBytes || (before != nil && !os.SameFile(before, opened)) {
		return fail()
	}
	if before == nil {
		if err := file.Chmod(0600); err != nil {
			return fail()
		}
	}
	if err := platform.CheckPrivateFile(path); err != nil {
		return fail()
	}
	if err := checkFileLinks(path, opened); err != nil {
		return fail()
	}
	return file, opened.Size(), nil
}

func (w *Writer) rotate() error {
	if err := w.checkFiles(); err != nil {
		return err
	}
	if err := w.file.Sync(); err != nil {
		return errors.New("无法同步待轮转的运行日志")
	}
	if err := w.file.Close(); err != nil {
		w.file = nil
		return errors.New("无法关闭待轮转的运行日志")
	}
	w.file = nil
	oldest := filepath.Join(w.directory, logNames[backupCount])
	if err := os.Remove(oldest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("无法移除最旧的运行日志备份")
	}
	for i := backupCount - 1; i >= 0; i-- {
		from, to := filepath.Join(w.directory, logNames[i]), filepath.Join(w.directory, logNames[i+1])
		if err := os.Rename(from, to); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("无法轮转运行日志备份")
		}
	}
	file, size, err := w.openCurrent()
	if err != nil {
		return err
	}
	w.file, w.size = file, size
	return nil
}
