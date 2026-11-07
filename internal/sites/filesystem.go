package sites

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

func relativePath(value string, directory bool) (string, error) {
	if value == "" && directory {
		return ".", nil
	}
	if value == "" || len(value) > 1024 || !utf8.ValidString(value) || strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\\:\x00") {
		return "", errUnsafe
	}
	parts := strings.Split(value, "/")
	if len(parts) > 32 {
		return "", errUnsafe
	}
	for _, part := range parts {
		if len(part) == 0 || len(part) > 255 || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || strings.HasPrefix(strings.ToLower(part), ".qqon-") || strings.EqualFold(part, ".__qqon_health") {
			return "", errUnsafe
		}
		for _, c := range part {
			if unicode.IsControl(c) || strings.ContainsRune(`<>"|?*`, c) {
				return "", errUnsafe
			}
		}
		base, _, _ := strings.Cut(strings.ToUpper(part), ".")
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || base == "CONIN$" || base == "CONOUT$" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && strings.ContainsRune("123456789", rune(base[3]))) || base == "COM¹" || base == "COM²" || base == "COM³" || base == "LPT¹" || base == "LPT²" || base == "LPT³" {
			return "", errUnsafe
		}
	}
	return value, nil
}

// Every descent pins a Root and checks both sides of OpenRoot. Production
// directories are root-owned and not group/other-writable. Final data opens use
// platform no-follow handles, not a Clean+prefix pathname containment check.
func descend(parent *os.Root, name string, create bool, mode os.FileMode, strict bool) (*os.Root, error) {
	before, err := parent.Lstat(name)
	if errors.Is(err, os.ErrNotExist) && create {
		if err = parent.Mkdir(name, mode); err != nil {
			return nil, err
		}
		before, err = parent.Lstat(name)
	}
	if err != nil {
		return nil, err
	}
	if err := checkInfo(before, true, strict); err != nil {
		return nil, err
	}
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err1 := child.Lstat(".")
	after, err2 := parent.Lstat(name)
	if err1 != nil || err2 != nil || checkInfo(opened, true, strict) != nil || checkInfo(after, true, strict) != nil || !os.SameFile(before, opened) || !os.SameFile(after, opened) {
		child.Close()
		return nil, errUnsafe
	}
	return child, nil
}

func absoluteRoot(path string, create bool, mode os.FileMode, private, strict bool) (*os.Root, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) > 2048 || strings.ContainsAny(path, "\x00\r\n") {
		return nil, errUnsafe
	}
	volume := filepath.VolumeName(path)
	anchor := volume + string(filepath.Separator)
	root, err := os.OpenRoot(anchor)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimPrefix(path, anchor), string(filepath.Separator))
	for i, name := range parts {
		if name == "" {
			continue
		}
		perm := os.FileMode(0755)
		if i == len(parts)-1 {
			perm = mode
		}
		child, err := descend(root, name, create, perm, strict)
		root.Close()
		if err != nil {
			return nil, err
		}
		root = child
	}
	info, err := root.Lstat(".")
	if err != nil || checkInfo(info, true, strict) != nil || (private && strict && info.Mode().Perm()&0077 != 0) {
		root.Close()
		return nil, errUnsafe
	}
	return root, nil
}

func parentRoot(root *os.Root, name string, strict bool) (*os.Root, string, error) {
	parts := strings.Split(name, "/")
	parent, err := descend(root, ".", false, 0, strict)
	if err != nil {
		return nil, "", err
	}
	for _, part := range parts[:len(parts)-1] {
		if part == "" || part == "." || part == ".." {
			parent.Close()
			return nil, "", errUnsafe
		}
		next, err := descend(parent, part, false, 0, strict)
		parent.Close()
		if err != nil {
			return nil, "", err
		}
		parent = next
	}
	base := parts[len(parts)-1]
	if base == "" || base == "." || base == ".." || strings.ContainsAny(base, "\\:\x00") {
		parent.Close()
		return nil, "", errUnsafe
	}
	return parent, base, nil
}

func readFile(root *os.Root, name string, limit int64, strict bool) ([]byte, error) {
	parent, base, err := parentRoot(root, name, strict)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	file, info, err := openNative(parent, base, false, strict)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if info.Size() < 0 || info.Size() > limit {
		return nil, failure(413, "file_too_large", "文件超过大小限制")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, failure(413, "file_too_large", "文件超过大小限制")
	}
	after, err := file.Stat()
	if err != nil || checkOpenFile(file, after, false, strict) != nil || !os.SameFile(info, after) {
		return nil, errUnsafe
	}
	return data, nil
}

func currentHash(root *os.Root, name string, limit int64, strict bool) (string, error) {
	data, err := readFile(root, name, limit, strict)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return digest(data), nil
}

// atomicFile never opens an existing target for writing. expected=="" means
// create-only; otherwise the bounded, regular, single-link old file must match.
func atomicFile(root *os.Root, name string, data []byte, mode os.FileMode, expected string, limit int64, strict bool) error {
	if int64(len(data)) > limit {
		return errUnsafe
	}
	parent, base, err := parentRoot(root, name, strict)
	if err != nil {
		return err
	}
	defer parent.Close()
	old, err := currentHash(parent, base, limit, strict)
	if err != nil {
		return err
	}
	if old != expected {
		return errConflict
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	tmp := ".qqon-tmp-" + id
	file, err := parent.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() { file.Close(); _ = parent.Remove(tmp) }()
	if n, err := file.Write(data); err != nil || n != len(data) {
		return errors.New("sites: staged write failed")
	}
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if info, err := file.Stat(); err != nil || checkOpenFile(file, info, false, strict) != nil {
		return errUnsafe
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	// Recheck after staging; root-owned parent directories and the service writer
	// lock exclude unprivileged swaps. Linux create-only uses RENAME_NOREPLACE.
	old, err = currentHash(parent, base, limit, strict)
	if err != nil {
		return err
	}
	if old != expected {
		return errConflict
	}
	if err := renameNative(parent, tmp, base, expected == ""); err != nil {
		return err
	}
	return syncRoot(parent)
}

func removeOwned(root *os.Root, name, expected string, limit int64, strict bool) error {
	if expected == "" {
		return errUnsafe
	}
	parent, base, err := parentRoot(root, name, strict)
	if err != nil {
		return err
	}
	defer parent.Close()
	actual, err := currentHash(parent, base, limit, strict)
	if err != nil {
		return err
	}
	if actual != expected {
		return errConflict
	}
	if err := parent.Remove(base); err != nil {
		return err
	}
	return syncRoot(parent)
}

func textBytes(data []byte) bool {
	if !utf8.Valid(data) {
		return false
	}
	for _, c := range string(data) {
		if unicode.IsControl(c) && c != '\n' && c != '\r' && c != '\t' {
			return false
		}
	}
	return true
}

func (s *Service) ownedWebroot(r record) (*os.Root, error) {
	base, err := absoluteRoot(s.adapters.webRoot, false, 0755, false, s.adapters.strict)
	if err != nil {
		return nil, err
	}
	defer base.Close()
	root, err := descend(base, r.Site.ID, false, 0755, s.adapters.strict)
	if err != nil {
		return nil, err
	}
	marker, err := readFile(root, ".qqon-owned", 128, s.adapters.strict)
	if err != nil || string(marker) != "qqon-webroot-v1:"+r.Site.ID+"\n" {
		root.Close()
		return nil, errUnsafe
	}
	return root, nil
}
