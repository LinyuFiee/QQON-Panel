package machine

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	maxDockerLogs      = 128 << 10
	maxDockerLogWire   = 256 << 10
	maxDockerLogFrames = 4096
)

type logsView struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

func invalidLogs() error {
	return problem(http.StatusBadGateway, "docker_invalid_logs", "Docker 日志帧无效或读取失败")
}

func cleanLogText(data []byte) string {
	text := strings.ToValidUTF8(string(data), "�")
	text = strings.Map(func(r rune) rune {
		if (r < 32 && r != '\n' && r != '\r' && r != '\t') || (r >= 127 && r <= 159) {
			return -1
		}
		return r
	}, text)
	if len(text) > maxDockerLogs {
		text = text[:maxDockerLogs]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
	}
	return text
}

// decodeLogs handles Docker's eight-byte stdout/stderr framing, or an actual
// TTY stream selected by container inspection, not a guess from the first byte.
// Both the decoded data and the input work are bounded, including empty frames.
func decodeLogs(body io.Reader, tty bool) (logsView, error) {
	if tty {
		data, err := io.ReadAll(io.LimitReader(body, maxDockerLogs+1))
		if err != nil {
			return logsView{}, invalidLogs()
		}
		truncated := len(data) > maxDockerLogs
		if truncated {
			data = data[:maxDockerLogs]
		}
		return logsView{cleanLogText(data), truncated}, nil
	}
	wire := &io.LimitedReader{R: body, N: maxDockerLogWire}
	var out bytes.Buffer
	for frames := 0; frames < maxDockerLogFrames; frames++ {
		var header [8]byte
		n, err := io.ReadFull(wire, header[:])
		if err == io.EOF && n == 0 {
			return logsView{cleanLogText(out.Bytes()), wire.N == 0}, nil
		}
		if err != nil {
			if wire.N == 0 {
				return logsView{cleanLogText(out.Bytes()), true}, nil
			}
			return logsView{}, invalidLogs()
		}
		if (header[0] != 1 && header[0] != 2) || header[1] != 0 || header[2] != 0 || header[3] != 0 {
			return logsView{}, invalidLogs()
		}
		length := int64(binary.BigEndian.Uint32(header[4:]))
		remaining := int64(maxDockerLogs - out.Len())
		if length > remaining {
			if _, err := io.CopyN(&out, wire, remaining); err != nil {
				if wire.N == 0 {
					return logsView{cleanLogText(out.Bytes()), true}, nil
				}
				return logsView{}, invalidLogs()
			}
			return logsView{cleanLogText(out.Bytes()), true}, nil
		}
		if _, err := io.CopyN(&out, wire, length); err != nil {
			if wire.N == 0 {
				return logsView{cleanLogText(out.Bytes()), true}, nil
			}
			return logsView{}, invalidLogs()
		}
	}
	return logsView{cleanLogText(out.Bytes()), true}, nil
}

func (s *Service) handleLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validContainerID(id) {
		writeError(w, problem(http.StatusBadRequest, "invalid_container_id", "必须提供完整的 64 位小写十六进制容器 ID"))
		return
	}
	s.dockerRead(w, r, func(ctx context.Context) (any, error) {
		var inspect struct {
			Config *struct{ Tty *bool }
		}
		if err := s.docker.getJSON(ctx, "/containers/"+id+"/json", nil, &inspect); err != nil {
			return nil, err
		}
		if inspect.Config == nil || inspect.Config.Tty == nil {
			return nil, problem(http.StatusBadGateway, "docker_invalid_response", "无法确定容器日志格式")
		}
		res, err := s.docker.request(ctx, http.MethodGet, "/containers/"+id+"/logs", url.Values{
			"stdout": {"1"}, "stderr": {"1"}, "follow": {"0"}, "tail": {"200"}, "timestamps": {"0"},
		})
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		return decodeLogs(res.Body, *inspect.Config.Tty)
	})
}
