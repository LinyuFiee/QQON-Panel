package auth

import (
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	maxAuditEvents     = 200
	auditSegmentEvents = 64
	auditBackups       = 2
	maxAuditBytes      = 64 << 10
)

type auditEvent struct {
	Time   time.Time `json:"time"`
	Action string    `json:"action"`
	Detail string    `json:"detail"`
	IP     string    `json:"ip,omitempty"`
}

type auditFile struct {
	Version int          `json:"version"`
	Events  []auditEvent `json:"events"`
}

func (s *Service) auditPath(index int) string {
	if index == 0 {
		return filepath.Join(s.dataDir, "auth-audit.json")
	}
	return filepath.Join(s.dataDir, "auth-audit."+strconv.Itoa(index)+".json")
}

func validAuditEvent(event auditEvent) bool {
	return !event.Time.IsZero() && event.Action != "" && len(event.Action) <= 64 &&
		len(event.Detail) <= 256 && event.Action == auditAction(event.Action) &&
		event.Detail == cleanText(event.Detail, 256) &&
		(event.IP == "" || event.IP == "unknown" || net.ParseIP(event.IP) != nil)
}

func (s *Service) loadAudit() error {
	for index := auditBackups; index >= 0; index-- {
		var file auditFile
		err := readPrivateJSON(s.auditPath(index), maxAuditBytes, &file)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || file.Version != 1 || file.Events == nil || len(file.Events) > auditSegmentEvents {
			return errors.New("auth: malformed private audit state")
		}
		for _, event := range file.Events {
			if !validAuditEvent(event) {
				return errors.New("auth: invalid audit record")
			}
			s.auditEvents = append(s.auditEvents, event)
		}
		if index == 0 {
			s.auditSegment = append([]auditEvent(nil), file.Events...)
		}
	}
	if len(s.auditEvents) > maxAuditEvents {
		s.auditEvents = append([]auditEvent(nil), s.auditEvents[len(s.auditEvents)-maxAuditEvents:]...)
	}
	return nil
}

// Record accepts factual non-secret audit text from the panel. Callers must never
// pass credentials, request bodies or command output. Authentication handlers
// below use fixed messages and never record the supplied username/password/code.
func (s *Service) Record(action, detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.recordLocked(action, detail, "")
}

func auditAction(value string) string {
	var builder strings.Builder
	for i := 0; i < len(value) && builder.Len() < 64; i++ {
		c := value[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' {
			builder.WriteByte(c)
		}
	}
	if builder.Len() == 0 {
		return "panel.event"
	}
	return builder.String()
}

func (s *Service) recordLocked(action, detail, ip string) {
	event := auditEvent{Time: s.now().UTC(), Action: auditAction(action), Detail: cleanText(detail, 256), IP: ip}
	if len(s.auditEvents) >= maxAuditEvents {
		copy(s.auditEvents, s.auditEvents[len(s.auditEvents)-maxAuditEvents+1:])
		s.auditEvents = s.auditEvents[:maxAuditEvents-1]
	}
	s.auditEvents = append(s.auditEvents, event)
	// A storage failure does not roll back an already-committed security change.
	// Retain the bounded event in memory, stop further disk writes and expose the
	// fixed storage error through Close rather than logging any request contents.
	if s.auditError != nil {
		return
	}
	if len(s.auditSegment) >= auditSegmentEvents {
		if err := s.rotateAuditLocked(); err != nil {
			s.auditError = errors.New("auth: cannot rotate private audit state")
			return
		}
		s.auditSegment = nil
	}
	s.auditSegment = append(s.auditSegment, event)
	if err := atomicJSON(s.auditPath(0), auditFile{Version: 1, Events: s.auditSegment}, maxAuditBytes); err != nil {
		s.auditError = errors.New("auth: cannot persist private audit state")
	}
}

func (s *Service) rotateAuditLocked() error {
	// Replace each bounded destination atomically. Never remove the source until
	// its replacement is durable; interrupted rotation may duplicate an event,
	// but cannot turn malformed/truncated JSON into an accepted record.
	for index := auditBackups; index >= 1; index-- {
		var previous auditFile
		if err := readPrivateJSON(s.auditPath(index-1), maxAuditBytes, &previous); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		if err := atomicJSON(s.auditPath(index), previous, maxAuditBytes); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) authFailure(action string, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.recordLocked(action, "身份验证失败", remoteIP(r))
	}
}

func (s *Service) handleAudit(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	_, current := s.contextSessionLocked(r, s.now().UTC())
	if current == nil {
		s.mu.Unlock()
		writeFailure(w, unauthorized)
		return
	}
	events := make([]auditEvent, len(s.auditEvents))
	for i := range s.auditEvents {
		events[i] = s.auditEvents[len(s.auditEvents)-1-i]
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, struct {
		Events []auditEvent `json:"events"`
	}{events})
}
