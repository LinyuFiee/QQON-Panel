package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const maxStateBytes = 64 << 10

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeFailure(w http.ResponseWriter, failure *apiFailure) {
	if failure.Status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "60")
	}
	writeJSON(w, failure.Status, struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{Error: struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{Code: failure.Code, Message: failure.Message}})
}

func readBody(w http.ResponseWriter, r *http.Request, target any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeFailure(w, &apiFailure{http.StatusUnsupportedMediaType, "unsupported_media_type", "请使用 application/json 请求正文"})
		return false
	}
	if r.Header.Get("Content-Encoding") != "" && r.Header.Get("Content-Encoding") != "identity" {
		writeFailure(w, &apiFailure{http.StatusUnsupportedMediaType, "unsupported_encoding", "不支持压缩请求正文"})
		return false
	}
	if r.ContentLength > maxBodyBytes {
		writeFailure(w, &apiFailure{http.StatusRequestEntityTooLarge, "body_too_large", "请求正文过大"})
		return false
	}
	body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeFailure(w, &apiFailure{http.StatusRequestEntityTooLarge, "body_too_large", "请求正文过大"})
		} else {
			writeFailure(w, &apiFailure{http.StatusBadRequest, "invalid_json", "请求正文不是有效的 JSON 对象"})
		}
		return false
	}
	if err := strictJSON(data, target); err != nil {
		writeFailure(w, &apiFailure{http.StatusBadRequest, "invalid_json", "请求正文不是符合要求的 JSON 对象"})
		return false
	}
	return true
}

// strictJSON rejects duplicate keys (including case-insensitive aliases accepted
// by encoding/json), trailing values, unknown fields, nulls and excessive nesting.
// The token walk precedes typed decoding and is always called on bounded bytes.
func strictJSON(data []byte, target any) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' || !utf8.Valid(data) {
		return errors.New("expected a UTF-8 JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := walkJSON(decoder, 0); err != nil {
		return err
	}
	if err := validJSONStringEscapes(data); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON content")
	}
	if err := exactJSONFields(data, reflect.TypeOf(target)); err != nil {
		return err
	}
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("unexpected JSON field or value")
	}
	return nil
}

// encoding/json deliberately accepts case-insensitive field aliases and replaces
// unpaired Unicode surrogates. Neither is suitable for a credential boundary:
// the API has exact camelCase keys and passwords must not be silently changed.
func exactJSONFields(data []byte, typ reflect.Type) error {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == reflect.TypeOf(time.Time{}) {
		return nil
	}
	switch typ.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if err := json.Unmarshal(data, &object); err != nil {
			return err
		}
		fields := make(map[string]reflect.Type, typ.NumField())
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if !field.IsExported() {
				continue
			}
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			fields[name] = field.Type
		}
		for name, value := range object {
			fieldType, ok := fields[name]
			if !ok {
				return errors.New("unexpected JSON field")
			}
			if err := exactJSONFields(value, fieldType); err != nil {
				return err
			}
		}
	case reflect.Array, reflect.Slice:
		if typ.Elem().Kind() == reflect.Uint8 {
			return nil // []byte is a JSON base64 string.
		}
		var items []json.RawMessage
		if err := json.Unmarshal(data, &items); err != nil {
			return err
		}
		for _, value := range items {
			if err := exactJSONFields(value, typ.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}

func validJSONStringEscapes(data []byte) error {
	for i := 0; i < len(data); i++ {
		if data[i] != '"' {
			continue
		}
		for i++; i < len(data) && data[i] != '"'; i++ {
			if data[i] != '\\' {
				continue
			}
			i++
			if i >= len(data) || data[i] != 'u' {
				continue
			}
			if i+4 >= len(data) {
				return errors.New("invalid Unicode escape")
			}
			value, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
			if err != nil {
				return errors.New("invalid Unicode escape")
			}
			i += 4
			if value >= 0xdc00 && value <= 0xdfff {
				return errors.New("unpaired Unicode surrogate")
			}
			if value < 0xd800 || value > 0xdbff {
				continue
			}
			if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
				return errors.New("unpaired Unicode surrogate")
			}
			low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return errors.New("unpaired Unicode surrogate")
			}
			i += 6
		}
	}
	return nil
}

func walkJSON(decoder *json.Decoder, depth int) error {
	if depth > 12 {
		return errors.New("excessive JSON nesting")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return errors.New("null is not accepted")
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok || seen[strings.ToLower(key)] {
				return errors.New("duplicate JSON key")
			}
			seen[strings.ToLower(key)] = true
			if err := walkJSON(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("unclosed JSON object")
		}
	case '[':
		for decoder.More() {
			if err := walkJSON(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("unclosed JSON array")
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	return nil
}

func privateDirectory(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errors.New("auth: cannot create private data directory")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("auth: data directory must be a real directory")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return errors.New("auth: cannot restrict data directory permissions")
	}
	return nil
}

func readPrivateFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > limit {
		return nil, errors.New("auth: unsafe or oversized state file")
	}
	// Windows chmod does not implement POSIX ownership bits. Deployment there
	// must restrict the data directory's NTFS ACL to the panel's service account.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("auth: state file permissions must be owner-only")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, errors.New("auth: state file changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("auth: cannot read bounded state file")
	}
	return data, nil
}

func readPrivateJSON(path string, limit int64, target any) error {
	data, err := readPrivateFile(path, limit)
	if err != nil {
		return err
	}
	return strictJSON(data, target)
}

// atomicJSON uses a private same-directory file, fsync and rename. No account
// secret is written with a permissive intermediate mode or to a shared temp dir.
func atomicJSON(path string, value any, limit int) error {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	// These private files are never served as HTML. Avoid HTML-escape expansion
	// turning a bounded valid audit segment into an oversized state write.
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil || buffer.Len() > limit {
		return errors.New("auth: cannot encode bounded state")
	}
	data := buffer.Bytes()
	if existing, err := os.Lstat(path); err == nil {
		if !existing.Mode().IsRegular() || existing.Mode()&os.ModeSymlink != 0 {
			return errors.New("auth: unsafe state target")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("auth: cannot inspect state target")
	}
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".auth-write-*")
	if err != nil {
		return errors.New("auth: cannot create private temporary state")
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return errors.New("auth: cannot restrict state permissions")
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return errors.New("auth: cannot write state")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return errors.New("auth: cannot sync state")
	}
	if err := file.Close(); err != nil {
		return errors.New("auth: cannot close state")
	}
	if err := os.Rename(temporary, path); err != nil {
		return errors.New("auth: cannot replace state")
	}
	// The rename is the commit point. Directory sync is best-effort because some
	// filesystems (notably Windows) do not implement it; never report a rollback
	// after a successful rename and then keep replayable in-memory credentials.
	if directory, err := os.Open(dir); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

func loadOrCreateBootstrap(path string) (string, error) {
	if data, err := readPrivateFile(path, 64); err == nil {
		value := strings.TrimSuffix(string(data), "\n")
		if !validRandomToken(value, 32) {
			return "", errors.New("auth: invalid setup token file")
		}
		return value, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("auth: cannot read private setup token file")
	}
	value, err := randomToken(32)
	if err != nil {
		return "", errors.New("auth: cannot generate setup credential")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return loadOrCreateBootstrap(path)
	}
	if err != nil {
		return "", errors.New("auth: cannot create private setup token file")
	}
	failed := true
	defer func() {
		_ = file.Close()
		if failed {
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(0600); err != nil {
		return "", errors.New("auth: cannot restrict setup token permissions")
	}
	if _, err := fmt.Fprintln(file, value); err != nil {
		return "", errors.New("auth: cannot write setup token file")
	}
	if err := file.Sync(); err != nil {
		return "", errors.New("auth: cannot sync setup token file")
	}
	if err := file.Close(); err != nil {
		return "", errors.New("auth: cannot close setup token file")
	}
	failed = false
	return value, nil
}

func (s *Service) saveAccountLocked(candidate accountRecord) error {
	if !validAccount(&candidate) {
		return errors.New("auth: invalid candidate state")
	}
	if err := atomicJSON(s.accountPath, &candidate, maxStateBytes); err != nil {
		return err
	}
	s.account = &candidate
	return nil
}
