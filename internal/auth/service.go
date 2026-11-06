// Package auth implements the panel's single-account, local authentication boundary.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	cookieName         = "panel_session"
	defaultSessionTTL  = 12 * time.Hour
	maxSessionTTL      = 7 * 24 * time.Hour
	maxIdleTTL         = 30 * time.Minute
	maxSessions        = 32
	maxAttemptIPs      = 1024
	attemptWindowTTL   = 10 * time.Minute
	attemptsPerIP      = 20
	attemptsGlobal     = 200
	maxBodyBytes       = 8 << 10
	enrollmentTTL      = 5 * time.Minute
	maxEnrollmentTries = 5
)

// Options contains only local persistence and cookie/session settings. No proxy
// headers are trusted; callers must explicitly configure their own TLS boundary.
type Options struct {
	DataDir       string
	SecureCookies bool
	SessionTTL    time.Duration
}

type Service struct {
	mu                sync.Mutex
	dataDir           string
	accountPath       string
	tokenPath         string
	secureCookies     bool
	ttl               time.Duration
	idleTTL           time.Duration
	now               func() time.Time
	account           *accountRecord
	credentialVersion uint64
	bootstrapDigest   [32]byte
	sessions          map[[32]byte]*session
	attempts          map[string]attemptWindow
	globalAttempts    attemptWindow
	kdf               chan struct{}
	auditEvents       []auditEvent
	auditSegment      []auditEvent
	auditError        error
	closed            bool
}

type session struct {
	ID         string
	CSRF       string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	IP         string
	UserAgent  string
	Enrollment *enrollment
}

type enrollment struct {
	Secret    []byte
	ExpiresAt time.Time
	Tries     int
}

type attemptWindow struct {
	Start time.Time
	Count int
}

type sessionContextKey struct{}
type sessionIdentity struct{ Key [32]byte }

type userResponse struct {
	Username         string `json:"username"`
	TwoFactorEnabled bool   `json:"twoFactorEnabled"`
}

type statusResponse struct {
	Initialized   bool          `json:"initialized"`
	Authenticated bool          `json:"authenticated"`
	CSRFToken     string        `json:"csrfToken,omitempty"`
	User          *userResponse `json:"user,omitempty"`
}

type apiFailure struct {
	Status  int
	Code    string
	Message string
}

var (
	unauthorized = &apiFailure{http.StatusUnauthorized, "unauthorized", "请先登录"}
	authFailed   = &apiFailure{http.StatusUnauthorized, "authentication_failed", "用户名、密码或验证码不正确"}
	unavailable  = &apiFailure{http.StatusServiceUnavailable, "service_unavailable", "认证服务暂不可用"}
	storageError = &apiFailure{http.StatusServiceUnavailable, "storage_error", "无法安全保存账户状态，请稍后再试"}
	kdfBusy      = &apiFailure{http.StatusTooManyRequests, "auth_busy", "验证请求繁忙，请稍后再试"}
	rateLimited  = &apiFailure{http.StatusTooManyRequests, "rate_limited", "验证尝试过于频繁，请稍后再试"}
)

// New rejects malformed or unsafe persistent state instead of silently resetting
// it. Sessions are deliberately memory-only, so a restart requires fresh login.
func New(options Options) (*Service, error) {
	if options.DataDir == "" {
		return nil, errors.New("auth: data directory is required")
	}
	if options.SessionTTL == 0 {
		options.SessionTTL = defaultSessionTTL
	}
	if options.SessionTTL < time.Minute || options.SessionTTL > maxSessionTTL {
		return nil, errors.New("auth: session TTL must be between one minute and seven days")
	}
	dir, err := filepath.Abs(options.DataDir)
	if err != nil {
		return nil, errors.New("auth: invalid data directory")
	}
	if err := privateDirectory(dir); err != nil {
		return nil, err
	}
	s := &Service{
		dataDir: dir, accountPath: filepath.Join(dir, "auth.json"),
		tokenPath: filepath.Join(dir, "setup-token"), secureCookies: options.SecureCookies,
		ttl: options.SessionTTL, idleTTL: min(options.SessionTTL, maxIdleTTL), now: time.Now,
		sessions: make(map[[32]byte]*session), attempts: make(map[string]attemptWindow),
		kdf: make(chan struct{}, 2), auditEvents: make([]auditEvent, 0, maxAuditEvents),
	}
	var account accountRecord
	if err := readPrivateJSON(s.accountPath, maxStateBytes, &account); err == nil {
		if !validAccount(&account) {
			return nil, errors.New("auth: invalid account state")
		}
		s.account = &account
		s.credentialVersion = 1
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("auth: cannot read valid private account state")
	}
	if err := s.loadAudit(); err != nil {
		return nil, err
	}
	if s.account == nil {
		token, err := loadOrCreateBootstrap(s.tokenPath)
		if err != nil {
			return nil, err
		}
		s.bootstrapDigest = sha256.Sum256([]byte(token))
	} else {
		// A leftover token after a crash is never accepted once auth.json exists.
		_ = os.Remove(s.tokenPath)
	}
	return s, nil
}

// SetupTokenPath reveals a filename, never the bootstrap credential itself.
func (s *Service) SetupTokenPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.account != nil || s.closed {
		return ""
	}
	return s.tokenPath
}

// Register owns exactly the documented authentication and account paths.
// Method checking is explicit so even 405 responses use the JSON error contract.
func (s *Service) Register(mux *http.ServeMux) {
	register := func(path, method string, protected bool, f http.HandlerFunc) {
		var h http.Handler = f
		if protected {
			h = s.Protect(h)
		}
		mux.Handle(path, onlyMethod(method, h))
	}
	register("/api/auth/status", http.MethodGet, false, s.handleStatus)
	register("/api/auth/setup", http.MethodPost, false, s.handleSetup)
	register("/api/auth/login", http.MethodPost, false, s.handleLogin)
	register("/api/auth/logout", http.MethodPost, true, s.handleLogout)
	// GET and PATCH deliberately share one exact route.
	mux.Handle("/api/account", s.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.handleAccount(w, r)
		case http.MethodPatch:
			s.handleAccountUpdate(w, r)
		default:
			w.Header().Set("Allow", "GET, PATCH")
			writeFailure(w, &apiFailure{http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不受支持"})
		}
	})))
	register("/api/account/totp/begin", http.MethodPost, true, s.handleTOTPBegin)
	register("/api/account/totp/enable", http.MethodPost, true, s.handleTOTPEnable)
	register("/api/account/totp/disable", http.MethodPost, true, s.handleTOTPDisable)
	register("/api/account/sessions", http.MethodGet, true, s.handleSessions)
	register("/api/account/sessions/{id}", http.MethodDelete, true, s.handleSessionDelete)
	register("/api/account/audit", http.MethodGet, true, s.handleAudit)
}

func onlyMethod(method string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.Header().Set("Allow", method)
			writeFailure(w, &apiFailure{http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不受支持"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Protect authenticates cookie sessions and requires a session-specific CSRF
// token on every non-safe method. Main separately enforces Host/Origin/fetch-site.
func (s *Service) Protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			writeFailure(w, unavailable)
			return
		}
		now := s.now().UTC()
		s.pruneSessionsLocked(now)
		key, sess := s.lookupSessionLocked(r)
		if sess == nil {
			s.mu.Unlock()
			writeFailure(w, unauthorized)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			values := r.Header.Values("X-CSRF-Token")
			if len(values) != 1 || subtle.ConstantTimeCompare([]byte(values[0]), []byte(sess.CSRF)) != 1 {
				s.mu.Unlock()
				writeFailure(w, &apiFailure{http.StatusForbidden, "csrf_failed", "安全校验失败，请刷新页面后重试"})
				return
			}
		}
		sess.LastSeenAt = now
		s.mu.Unlock()
		ctx := context.WithValue(r.Context(), sessionContextKey{}, sessionIdentity{Key: key})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.clearSessionsLocked()
	s.bootstrapDigest = [32]byte{}
	return s.auditError
}

func (s *Service) lookupSessionLocked(r *http.Request) ([32]byte, *session) {
	var raw string
	count := 0
	for _, cookie := range r.Cookies() {
		if cookie.Name == cookieName {
			raw = cookie.Value
			count++
		}
	}
	if count != 1 || !validRandomToken(raw, 32) {
		return [32]byte{}, nil
	}
	key := sha256.Sum256([]byte(raw))
	return key, s.sessions[key]
}

func (s *Service) contextSessionLocked(r *http.Request, now time.Time) ([32]byte, *session) {
	s.pruneSessionsLocked(now)
	identity, ok := r.Context().Value(sessionContextKey{}).(sessionIdentity)
	if !ok || s.closed {
		return [32]byte{}, nil
	}
	return identity.Key, s.sessions[identity.Key]
}

func (s *Service) pruneSessionsLocked(now time.Time) {
	for key, sess := range s.sessions {
		if !now.Before(sess.ExpiresAt) || !now.Before(sess.LastSeenAt.Add(s.idleTTL)) {
			wipeEnrollment(sess)
			delete(s.sessions, key)
		} else if sess.Enrollment != nil && !now.Before(sess.Enrollment.ExpiresAt) {
			wipeEnrollment(sess)
		}
	}
}

func wipeEnrollment(sess *session) {
	if sess.Enrollment != nil {
		clear(sess.Enrollment.Secret)
		sess.Enrollment = nil
	}
}

func (s *Service) clearSessionsLocked() {
	for key, sess := range s.sessions {
		wipeEnrollment(sess)
		delete(s.sessions, key)
	}
}

func prepareSession(r *http.Request, now time.Time, ttl time.Duration) (string, *session, error) {
	bearer, err := randomToken(32)
	if err != nil {
		return "", nil, err
	}
	id, err := randomToken(16)
	if err != nil {
		return "", nil, err
	}
	csrf, err := randomToken(32)
	if err != nil {
		return "", nil, err
	}
	return bearer, &session{
		ID: id, CSRF: csrf, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(ttl),
		IP: remoteIP(r), UserAgent: cleanText(r.UserAgent(), 160),
	}, nil
}

func (s *Service) addSessionLocked(bearer string, sess *session) {
	s.pruneSessionsLocked(sess.CreatedAt)
	if len(s.sessions) >= maxSessions {
		var oldestKey [32]byte
		var oldest *session
		for key, existing := range s.sessions {
			if oldest == nil || existing.LastSeenAt.Before(oldest.LastSeenAt) ||
				(existing.LastSeenAt.Equal(oldest.LastSeenAt) && existing.ID < oldest.ID) {
				oldestKey, oldest = key, existing
			}
		}
		wipeEnrollment(oldest)
		delete(s.sessions, oldestKey)
	}
	s.sessions[sha256.Sum256([]byte(bearer))] = sess
}

func (s *Service) setCookie(w http.ResponseWriter, r *http.Request, bearer string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: bearer, Path: "/", Expires: expires, MaxAge: int(s.ttl.Seconds()),
		HttpOnly: true, Secure: s.secureCookies || r.TLS != nil, SameSite: http.SameSiteStrictMode,
	})
}

func (s *Service) clearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: "", Path: "/", Expires: time.Unix(1, 0).UTC(), MaxAge: -1,
		HttpOnly: true, Secure: s.secureCookies || r.TLS != nil, SameSite: http.SameSiteStrictMode,
	})
}

func (s *Service) statusLocked(sess *session) statusResponse {
	result := statusResponse{Initialized: s.account != nil}
	if sess != nil && s.account != nil {
		result.Authenticated = true
		result.CSRFToken = sess.CSRF
		result.User = &userResponse{Username: s.account.Username, TwoFactorEnabled: len(s.account.TOTPSecret) != 0}
	}
	return result
}

func (s *Service) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		writeFailure(w, unavailable)
		return
	}
	now := s.now().UTC()
	s.pruneSessionsLocked(now)
	_, sess := s.lookupSessionLocked(r)
	if sess != nil {
		sess.LastSeenAt = now
	}
	result := s.statusLocked(sess)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, result)
}

// Both per-IP and global windows are bounded. A full table rejects new identities
// rather than evicting an attacker-selected entry and resetting its allowance.
func (s *Service) admitAttempt(r *http.Request) *apiFailure {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return unavailable
	}
	now := s.now().UTC()
	for ip, window := range s.attempts {
		if !now.Before(window.Start.Add(attemptWindowTTL)) {
			delete(s.attempts, ip)
		}
	}
	if s.globalAttempts.Start.IsZero() || !now.Before(s.globalAttempts.Start.Add(attemptWindowTTL)) {
		s.globalAttempts = attemptWindow{Start: now}
	}
	ip := remoteIP(r)
	window, exists := s.attempts[ip]
	if !exists {
		if len(s.attempts) >= maxAttemptIPs {
			return rateLimited
		}
		window.Start = now
	}
	if window.Count >= attemptsPerIP || s.globalAttempts.Count >= attemptsGlobal {
		return rateLimited
	}
	window.Count++
	s.attempts[ip] = window
	s.globalAttempts.Count++
	return nil
}

// RemoteAddr is supplied by net/http. X-Forwarded-For and Forwarded are ignored.
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return "unknown"
}

func randomToken(n int) (string, error) {
	value := make([]byte, n)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func validRandomToken(value string, n int) bool {
	if len(value) != base64.RawURLEncoding.EncodedLen(n) {
		return false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	return err == nil && len(decoded) == n
}

type sessionView struct {
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"userAgent"`
	Current    bool      `json:"current"`
}

func (s *Service) handleSessions(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	key, current := s.contextSessionLocked(r, s.now().UTC())
	if current == nil {
		s.mu.Unlock()
		writeFailure(w, unauthorized)
		return
	}
	views := make([]sessionView, 0, len(s.sessions))
	for candidateKey, candidate := range s.sessions {
		views = append(views, sessionView{
			ID: candidate.ID, CreatedAt: candidate.CreatedAt, LastSeenAt: candidate.LastSeenAt,
			ExpiresAt: candidate.ExpiresAt, IP: candidate.IP, UserAgent: candidate.UserAgent, Current: key == candidateKey,
		})
	}
	s.mu.Unlock()
	sort.Slice(views, func(i, j int) bool {
		if views[i].CreatedAt.Equal(views[j].CreatedAt) {
			return views[i].ID < views[j].ID
		}
		return views[i].CreatedAt.After(views[j].CreatedAt)
	})
	writeJSON(w, http.StatusOK, struct {
		Sessions []sessionView `json:"sessions"`
	}{views})
}

func cleanText(value string, limit int) string {
	value = strings.ToValidUTF8(value, "")
	var builder strings.Builder
	for _, char := range value {
		if char < 32 || char == 127 || (char >= 0x80 && char <= 0x9f) ||
			char == '\u2028' || char == '\u2029' || (char >= '\u202a' && char <= '\u202e') ||
			(char >= '\u2066' && char <= '\u2069') {
			char = ' '
		}
		if builder.Len()+len(string(char)) > limit {
			break
		}
		builder.WriteRune(char)
	}
	return builder.String()
}
