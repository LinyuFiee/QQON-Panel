package sites

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"panel/internal/jobrunner"
)

const filePageSize = 200
const maxFileOffset = 10000

type fileEntry struct {
	Name     string    `json:"name"`
	Type     string    `json:"type"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

type fileView struct {
	Path       string      `json:"path"`
	Entries    []fileEntry `json:"entries"`
	NextOffset *int        `json:"nextOffset"`
}

func queryPath(r *http.Request, listing bool) (string, string, int, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", "", 0, failure(400, "invalid_query", "查询参数无效")
	}
	for key, values := range query {
		if len(values) != 1 || key != "path" && (key != "offset" || !listing) {
			return "", "", 0, failure(400, "invalid_query", "查询参数无效")
		}
	}
	path, err := relativePath(query.Get("path"), listing)
	if err != nil {
		return "", "", 0, failure(400, "invalid_path", "仅允许受管网站内不含链接或保留名称的相对路径")
	}
	offset := 0
	if raw, exists := query["offset"]; exists {
		if raw[0] == "" || len(raw[0]) > 5 || strings.Trim(raw[0], "0123456789") != "" {
			return "", "", 0, failure(400, "invalid_offset", "目录分页偏移无效")
		}
		offset, err = strconv.Atoi(raw[0])
		if err != nil || offset > maxFileOffset {
			return "", "", 0, failure(400, "scan_limit", "目录扫描已达到有限分页上限")
		}
	}
	return path, query.Get("path"), offset, nil
}

func (s *Service) fileRecord(r *http.Request) (record, error) {
	if err := s.ready(); err != nil {
		return record{}, err
	}
	return s.snapshot(r.PathValue("id"))
}

func (s *Service) handleFiles(w http.ResponseWriter, r *http.Request) {
	saved, err := s.fileRecord(r)
	if err != nil {
		respondError(w, err)
		return
	}
	path, display, offset, err := queryPath(r, true)
	if err != nil {
		respondError(w, err)
		return
	}
	root, err := s.ownedWebroot(saved)
	if err != nil {
		respondError(w, err)
		return
	}
	defer root.Close()
	dirRoot, err := descend(root, ".", false, 0, s.adapters.strict)
	if err != nil {
		respondError(w, err)
		return
	}
	if path != "." {
		for _, component := range strings.Split(path, "/") {
			next, err := descend(dirRoot, component, false, 0, s.adapters.strict)
			dirRoot.Close()
			if err != nil {
				respondError(w, err)
				return
			}
			dirRoot = next
		}
	}
	defer dirRoot.Close()
	dir, _, err := openNative(dirRoot, ".", true, s.adapters.strict)
	if err != nil {
		respondError(w, err)
		return
	}
	defer dir.Close()
	view := fileView{Path: display, Entries: []fileEntry{}}
	for remaining := offset; remaining > 0; {
		if r.Context().Err() != nil {
			return
		}
		names, err := dir.Readdirnames(min(filePageSize, remaining))
		remaining -= len(names)
		if err == io.EOF {
			respond(w, 200, view)
			return
		}
		if err != nil || len(names) == 0 {
			respondError(w, errUnsafe)
			return
		}
	}
	names, err := dir.Readdirnames(filePageSize + 1)
	if err != nil && err != io.EOF {
		respondError(w, err)
		return
	}
	if len(names) > filePageSize && offset+filePageSize <= maxFileOffset {
		next := offset + filePageSize
		view.NextOffset = &next
	}
	if len(names) > filePageSize {
		names = names[:filePageSize]
	}
	for _, name := range names {
		if r.Context().Err() != nil {
			return
		}
		if _, err := relativePath(name, false); err != nil {
			continue
		}
		info, err := dirRoot.Lstat(name)
		if err != nil {
			continue
		}
		if checkInfo(info, info.IsDir(), s.adapters.strict) != nil {
			continue
		}
		file, actual, err := openNative(dirRoot, name, info.IsDir(), s.adapters.strict)
		if err != nil {
			continue
		}
		file.Close()
		kind := "file"
		if actual.IsDir() {
			kind = "directory"
		}
		view.Entries = append(view.Entries, fileEntry{Name: name, Type: kind, Size: actual.Size(), Modified: actual.ModTime().UTC()})
	}
	respond(w, 200, view)
}

func (s *Service) handleContent(w http.ResponseWriter, r *http.Request) {
	saved, err := s.fileRecord(r)
	if err != nil {
		respondError(w, err)
		return
	}
	path, display, _, err := queryPath(r, false)
	if err != nil {
		respondError(w, err)
		return
	}
	root, err := s.ownedWebroot(saved)
	if err != nil {
		respondError(w, err)
		return
	}
	defer root.Close()
	data, err := readFile(root, path, EditLimitBytes, s.adapters.strict)
	if err != nil {
		respondError(w, err)
		return
	}
	if !textBytes(data) {
		respondError(w, failure(415, "not_text", "仅支持不含二进制控制字符的 UTF-8 文本"))
		return
	}
	respond(w, 200, map[string]string{"path": display, "content": string(data), "sha256": digest(data)})
}

func (s *Service) checkWrite(id, path, expected string, upload bool) error {
	saved, err := s.snapshot(id)
	if err != nil {
		return err
	}
	root, err := s.ownedWebroot(saved)
	if err != nil {
		return err
	}
	defer root.Close()
	// Existing uploads are refused even if huge. Content overwrite must read and
	// match the actual bounded old UTF-8 file; no blind truncation is possible.
	parent, base, err := parentRoot(root, path, s.adapters.strict)
	if err != nil {
		return err
	}
	defer parent.Close()
	_, statErr := parent.Lstat(base)
	if upload {
		if statErr == nil {
			return failure(409, "file_exists", "上传不会覆盖现有文件，请使用带 sha256 的文本编辑")
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		return nil
	}
	data, err := readFile(parent, base, EditLimitBytes, s.adapters.strict)
	if errors.Is(err, os.ErrNotExist) {
		if expected != "" {
			return errConflict
		}
		return nil
	}
	if err != nil {
		return err
	}
	if !textBytes(data) {
		return failure(415, "not_text", "已有目标不是受支持的有界 UTF-8 文本")
	}
	if expected == "" || digest(data) != expected {
		return failure(409, "content_conflict", "覆盖现有文本需提供最新 sha256；请重新读取文件")
	}
	return nil
}

func (s *Service) writeContent(ctx context.Context, id string, revision uint64, path string, data []byte, expected string, upload bool, p *jobrunner.Progress) error {
	if err := s.ready(); err != nil {
		return err
	}
	saved, err := s.revision(id, revision)
	if err != nil {
		return err
	}
	if err := s.checkWrite(id, path, expected, upload); err != nil {
		return err
	}
	root, err := s.ownedWebroot(saved)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.permission(); err != nil {
		return err
	}
	updateProgress(p, "write_file", 30, "有界写入网站文件；只在匹配目标状态后进行原子替换")
	limit := int64(EditLimitBytes)
	if upload {
		limit = UploadLimitBytes
	}
	if err := atomicFile(root, path, data, 0644, expected, limit, s.adapters.strict); err != nil {
		return err
	}
	updateProgress(p, "file_saved", 100, "文件已保存，未记录文件正文")
	return nil
}

func (s *Service) handleEdit(w http.ResponseWriter, r *http.Request) {
	saved, err := s.fileRecord(r)
	if err != nil {
		respondError(w, err)
		return
	}
	var request struct {
		Confirmed bool   `json:"confirmed"`
		Path      string `json:"path"`
		Content   string `json:"content"`
		SHA256    string `json:"sha256,omitempty"`
	}
	if err := boundedJSON(w, r, 512<<10, &request); err != nil {
		respondError(w, err)
		return
	}
	if err := requireConfirmed(request.Confirmed); err != nil {
		respondError(w, err)
		return
	}
	path, err := relativePath(request.Path, false)
	if err != nil {
		respondError(w, failure(400, "invalid_path", "文件路径必须是受管网站内的相对路径"))
		return
	}
	if len(request.Content) > EditLimitBytes {
		respondError(w, failure(413, "edit_too_large", "文本编辑限制为 256 KiB"))
		return
	}
	data := []byte(request.Content)
	request.Content = ""
	if !textBytes(data) {
		respondError(w, failure(415, "not_text", "仅支持 UTF-8 文本"))
		return
	}
	if request.SHA256 != "" && !hashPattern.MatchString(request.SHA256) {
		respondError(w, failure(400, "invalid_sha256", "sha256 必须是 64 位小写十六进制"))
		return
	}
	if err := s.checkWrite(saved.Site.ID, path, request.SHA256, false); err != nil {
		respondError(w, err)
		return
	}
	expected := request.SHA256
	s.accept(w, "sites.file.edit", saved.Site.ID, func(ctx context.Context, p *jobrunner.Progress) error {
		return s.writeContent(ctx, saved.Site.ID, saved.Site.Revision, path, data, expected, false, p)
	})
}

func (s *Service) handleUpload(w http.ResponseWriter, r *http.Request) {
	saved, err := s.fileRecord(r)
	if err != nil {
		respondError(w, err)
		return
	}
	if r.Header.Get("X-Confirm-Operation") != "true" {
		respondError(w, failure(400, "confirmation_required", "上传需 X-Confirm-Operation:true"))
		return
	}
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/octet-stream" || len(params) != 0 || r.Header.Get("Content-Encoding") != "" && r.Header.Get("Content-Encoding") != "identity" {
		respondError(w, failure(415, "unsupported_media_type", "上传只接受未压缩 application/octet-stream"))
		return
	}
	path, _, _, err := queryPath(r, false)
	if err != nil {
		respondError(w, err)
		return
	}
	if err := s.checkWrite(saved.Site.ID, path, "", true); err != nil {
		respondError(w, err)
		return
	}
	if r.Body == nil {
		respondError(w, failure(400, "missing_body", "上传需要文件正文"))
		return
	}
	body := http.MaxBytesReader(w, r.Body, UploadLimitBytes)
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		respondError(w, failure(413, "upload_too_large", "单个上传文件限制为 2 MiB"))
		return
	}
	s.accept(w, "sites.file.upload", saved.Site.ID, func(ctx context.Context, p *jobrunner.Progress) error {
		return s.writeContent(ctx, saved.Site.ID, saved.Site.Revision, path, data, "", true, p)
	})
}
