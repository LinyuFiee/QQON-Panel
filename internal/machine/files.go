package machine

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	filePageSize  = 200
	maxFileOffset = 10000
	maxPreview    = 256 << 10
)

var errUnsafeFile = errors.New("unsafe file")

type fileEntry struct {
	Name     string    `json:"name"`
	Type     string    `json:"type"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

type filesView struct {
	Path       string      `json:"path"`
	Root       string      `json:"root"`
	Entries    []fileEntry `json:"entries"`
	NextOffset *int        `json:"nextOffset"`
}

// Use a portable relative namespace, even on Linux. In particular, a Windows
// drive/UNC path, alternate data stream, reserved device or backslash is never
// allowed to become a path if a deployment later moves between platforms.
func cleanRelativePath(value string) (string, error) {
	if len(value) > 4096 || !utf8.ValidString(value) || strings.ContainsAny(value, "\\:\x00") || strings.HasPrefix(value, "/") {
		return "", errUnsafeFile
	}
	if value == "" {
		return ".", nil
	}
	parts := strings.Split(value, "/")
	if len(parts) > 128 {
		return "", errUnsafeFile
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return "", errUnsafeFile
		}
		for _, r := range part {
			if unicode.IsControl(r) {
				return "", errUnsafeFile
			}
		}
		base, _, _ := strings.Cut(part, ".")
		base = strings.ToUpper(base)
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || base == "CONIN$" || base == "CONOUT$" ||
			(len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && strings.ContainsRune("123456789", rune(base[3]))) ||
			base == "COM¹" || base == "COM²" || base == "COM³" || base == "LPT¹" || base == "LPT²" || base == "LPT³" {
			return "", errUnsafeFile
		}
	}
	return value, nil
}

func fileQuery(r *http.Request, listing bool) (string, string, int, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", "", 0, problem(http.StatusBadRequest, "invalid_query", "查询参数无效")
	}
	for key, values := range query {
		if len(values) != 1 || (key != "path" && (key != "offset" || !listing)) {
			return "", "", 0, problem(http.StatusBadRequest, "invalid_query", "查询参数无效")
		}
	}
	value := query.Get("path")
	name, err := cleanRelativePath(value)
	if err != nil {
		return "", "", 0, problem(http.StatusBadRequest, "invalid_path", "仅允许根目录内的相对路径")
	}
	offset := 0
	if raw, ok := query["offset"]; ok {
		if raw[0] == "" || len(raw[0]) > 5 {
			return "", "", 0, problem(http.StatusBadRequest, "invalid_offset", "分页偏移无效")
		}
		for _, c := range raw[0] {
			if c < '0' || c > '9' {
				return "", "", 0, problem(http.StatusBadRequest, "invalid_offset", "分页偏移无效")
			}
		}
		offset, err = strconv.Atoi(raw[0])
		if err != nil || offset > maxFileOffset {
			return "", "", 0, problem(http.StatusBadRequest, "directory_scan_limit", "目录过大，已达到安全分页扫描上限")
		}
	}
	return name, value, offset, nil
}

func fileError(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return problem(http.StatusNotFound, "file_not_found", "文件或目录不存在")
	}
	return problem(http.StatusForbidden, "file_access_denied", "无法安全访问该路径（权限、链接越界或文件类型不支持）")
}

func (s *Service) requireFiles(w http.ResponseWriter) bool {
	if s.root == nil {
		writeError(w, problem(http.StatusServiceUnavailable, "files_disabled", s.fileReason))
		return false
	}
	return true
}

func entryType(info os.FileInfo) string {
	if isReparseOrSymlink(info) {
		return "symlink"
	}
	if info.IsDir() {
		return "directory"
	}
	if info.Mode().IsRegular() {
		return "file"
	}
	return "other"
}

func (s *Service) handleFiles(w http.ResponseWriter, r *http.Request) {
	if !s.requireFiles(w) {
		return
	}
	name, display, offset, err := fileQuery(r, true)
	if err != nil {
		writeError(w, err)
		return
	}
	dir, _, err := openRootItem(s.root, name, true)
	if err != nil {
		writeError(w, fileError(err))
		return
	}
	defer dir.Close()
	view := filesView{Path: display, Root: s.rootName, Entries: []fileEntry{}}
	// Readdirnames never sorts/materializes the whole directory. Every request
	// examines at most offset + 201 names and stats at most 200 entries.
	remaining := offset
	for remaining > 0 {
		if r.Context().Err() != nil || s.ctx.Err() != nil {
			return
		}
		n := min(remaining, filePageSize)
		names, readErr := dir.Readdirnames(n)
		remaining -= len(names)
		if readErr == io.EOF {
			writeJSON(w, http.StatusOK, view)
			return
		}
		if readErr != nil || len(names) == 0 {
			writeError(w, fileError(readErr))
			return
		}
	}
	names, err := dir.Readdirnames(filePageSize + 1)
	if err != nil && err != io.EOF {
		writeError(w, fileError(err))
		return
	}
	if len(names) > filePageSize {
		next := offset + filePageSize
		view.NextOffset = &next
		names = names[:filePageSize]
	}
	for _, child := range names {
		if r.Context().Err() != nil || s.ctx.Err() != nil {
			return
		}
		childPath := child
		if name != "." {
			childPath = name + "/" + child
		}
		// Metadata is also obtained through Root, not DirEntry.Info which may
		// reopen an uncontained pathname after a parent directory rename.
		info, err := s.root.Lstat(childPath)
		if err != nil {
			continue // a concurrently removed/renamed entry is simply absent
		}
		view.Entries = append(view.Entries, fileEntry{child, entryType(info), info.Size(), info.ModTime().UTC()})
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Service) handlePreview(w http.ResponseWriter, r *http.Request) {
	if !s.requireFiles(w) {
		return
	}
	name, display, _, err := fileQuery(r, false)
	if err != nil {
		writeError(w, err)
		return
	}
	file, info, err := openRootItem(s.root, name, false)
	if err != nil {
		writeError(w, fileError(err))
		return
	}
	defer file.Close()
	if info.Size() < 0 || info.Size() > maxPreview {
		writeError(w, problem(http.StatusRequestEntityTooLarge, "preview_too_large", "仅可预览不超过 256 KiB 的文本文件"))
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, maxPreview+1))
	if err != nil {
		writeError(w, fileError(err))
		return
	}
	if len(data) > maxPreview {
		writeError(w, problem(http.StatusRequestEntityTooLarge, "preview_too_large", "仅可预览不超过 256 KiB 的文本文件"))
		return
	}
	if !utf8.Valid(data) {
		writeError(w, problem(http.StatusUnsupportedMediaType, "preview_not_text", "仅支持 UTF-8 文本预览"))
		return
	}
	for _, r := range string(data) {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			writeError(w, problem(http.StatusUnsupportedMediaType, "preview_not_text", "该文件包含二进制或控制字符"))
			return
		}
	}
	writeJSON(w, http.StatusOK, struct {
		Path      string `json:"path"`
		Content   string `json:"content"`
		Truncated bool   `json:"truncated"`
		Encoding  string `json:"encoding"`
	}{display, string(data), false, "utf-8"})
}
