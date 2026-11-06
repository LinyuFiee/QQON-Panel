package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"net/url"
	"os"
)

func invalidInput(w http.ResponseWriter, message string) {
	writeFailure(w, &apiFailure{http.StatusBadRequest, "invalid_input", message})
}

func (s *Service) authWork(r *http.Request) *apiFailure {
	if failure := s.admitAttempt(r); failure != nil {
		return failure
	}
	return s.acquireKDF()
}

func (s *Service) handleSetup(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	initialized, closed := s.account != nil, s.closed
	digest := s.bootstrapDigest
	s.mu.Unlock()
	if closed {
		writeFailure(w, unavailable)
		return
	}
	if initialized {
		writeFailure(w, &apiFailure{http.StatusConflict, "already_initialized", "账户已初始化，请登录"})
		return
	}
	var input struct {
		Token    string `json:"token"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readBody(w, r, &input) {
		return
	}
	if failure := s.admitAttempt(r); failure != nil {
		writeFailure(w, failure)
		return
	}
	provided := sha256.Sum256([]byte(input.Token))
	if !validRandomToken(input.Token, 32) || subtle.ConstantTimeCompare(provided[:], digest[:]) != 1 {
		s.authFailure("auth.setup.failed", r)
		writeFailure(w, authFailed)
		return
	}
	if !validUsername(input.Username) || !validPassword(input.Password) {
		invalidInput(w, "用户名须为 3–32 位 ASCII 字母、数字或 ._-，以字母或数字开头；密码须为 12–128 字节")
		return
	}
	if failure := s.acquireKDF(); failure != nil {
		writeFailure(w, failure)
		return
	}
	defer s.releaseKDF()
	password, err := newPassword(input.Password)
	if err != nil {
		writeFailure(w, unavailable)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || r.Context().Err() != nil {
		writeFailure(w, unavailable)
		return
	}
	if s.account != nil {
		writeFailure(w, &apiFailure{http.StatusConflict, "already_initialized", "账户已初始化，请登录"})
		return
	}
	bearer, sess, err := prepareSession(r, s.now().UTC(), s.ttl)
	if err != nil {
		writeFailure(w, unavailable)
		return
	}
	candidate := accountRecord{Version: 1, Username: input.Username, Password: password, LastTOTPStep: -1}
	if err := s.saveAccountLocked(candidate); err != nil {
		writeFailure(w, storageError)
		return
	}
	s.credentialVersion++
	s.bootstrapDigest = [32]byte{}
	_ = os.Remove(s.tokenPath)
	s.addSessionLocked(bearer, sess)
	s.recordLocked("auth.setup", "管理员账户已初始化", remoteIP(r))
	s.setCookie(w, r, bearer, sess.ExpiresAt)
	writeJSON(w, http.StatusOK, s.statusLocked(sess))
}

func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Code     string `json:"code,omitempty"`
	}
	if !readBody(w, r, &input) {
		return
	}
	if failure := s.authWork(r); failure != nil {
		writeFailure(w, failure)
		return
	}
	defer s.releaseKDF()
	if !validUsername(input.Username) || !validPassword(input.Password) || len(input.Code) > 64 {
		s.authFailure("auth.login.failed", r)
		writeFailure(w, authFailed)
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		writeFailure(w, unavailable)
		return
	}
	if s.account == nil {
		s.mu.Unlock()
		s.authFailure("auth.login.failed", r)
		writeFailure(w, authFailed)
		return
	}
	snapshot, version := cloneAccount(s.account), s.credentialVersion
	s.mu.Unlock()
	// Even an unknown valid-shaped username pays the same password KDF cost.
	passwordOK := verifyPassword(input.Password, snapshot.Password)
	usernameOK := constantStringEqual(input.Username, snapshot.Username)
	if !passwordOK || !usernameOK {
		s.authFailure("auth.login.failed", r)
		writeFailure(w, authFailed)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || r.Context().Err() != nil {
		writeFailure(w, unavailable)
		return
	}
	if s.credentialVersion != version {
		writeFailure(w, authFailed)
		return
	}
	// Check replay state against the latest live account while holding the lock;
	// two simultaneous requests can never consume one code twice.
	candidate := cloneAccount(s.account)
	now := s.now().UTC()
	if !consumeSecondFactor(&candidate, input.Code, now) {
		s.recordLocked("auth.login.failed", "身份验证失败", remoteIP(r))
		writeFailure(w, authFailed)
		return
	}
	bearer, sess, err := prepareSession(r, now, s.ttl)
	if err != nil {
		writeFailure(w, unavailable)
		return
	}
	if len(candidate.TOTPSecret) != 0 {
		if err := s.saveAccountLocked(candidate); err != nil {
			writeFailure(w, storageError)
			return
		}
	}
	s.addSessionLocked(bearer, sess)
	s.recordLocked("auth.login", "登录成功", remoteIP(r))
	s.setCookie(w, r, bearer, sess.ExpiresAt)
	writeJSON(w, http.StatusOK, s.statusLocked(sess))
}

func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if !readBody(w, r, &input) {
		return
	}
	s.mu.Lock()
	key, sess := s.contextSessionLocked(r, s.now().UTC())
	if sess == nil {
		s.mu.Unlock()
		writeFailure(w, unauthorized)
		return
	}
	wipeEnrollment(sess)
	delete(s.sessions, key)
	s.recordLocked("auth.logout", "当前会话已退出", remoteIP(r))
	s.mu.Unlock()
	s.clearCookie(w, r)
	writeJSON(w, http.StatusOK, struct {
		OK bool `json:"ok"`
	}{true})
}

func (s *Service) handleAccount(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	_, sess := s.contextSessionLocked(r, s.now().UTC())
	if sess == nil || s.account == nil {
		s.mu.Unlock()
		writeFailure(w, unauthorized)
		return
	}
	result := struct {
		Username          string `json:"username"`
		TwoFactorEnabled  bool   `json:"twoFactorEnabled"`
		SessionTTLMinutes int    `json:"sessionTTLMinutes"`
	}{s.account.Username, len(s.account.TOTPSecret) != 0, int(s.ttl.Minutes())}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, result)
}

// recheckPassword performs only the password part outside the state mutex. The
// caller holds a KDF admission slot and rechecks version/session/factor under the
// mutex before committing. This also fences in-flight work against revocation.
func (s *Service) recheckPassword(r *http.Request, password string) (uint64, *apiFailure) {
	if !validPassword(password) {
		s.authFailure("account.verify.failed", r)
		return 0, authFailed
	}
	s.mu.Lock()
	_, sess := s.contextSessionLocked(r, s.now().UTC())
	if sess == nil || s.account == nil {
		s.mu.Unlock()
		return 0, unauthorized
	}
	snapshot, version := cloneAccount(s.account), s.credentialVersion
	s.mu.Unlock()
	if !verifyPassword(password, snapshot.Password) {
		s.authFailure("account.verify.failed", r)
		return 0, authFailed
	}
	return version, nil
}

func (s *Service) securitySessionLocked(r *http.Request, version uint64) (*session, *apiFailure) {
	if s.closed || r.Context().Err() != nil {
		return nil, unavailable
	}
	_, sess := s.contextSessionLocked(r, s.now().UTC())
	if sess == nil || s.account == nil || s.credentialVersion != version {
		return nil, unauthorized
	}
	return sess, nil
}

func securityChanged(w http.ResponseWriter, recoveryCodes []string) {
	if recoveryCodes != nil {
		writeJSON(w, http.StatusOK, struct {
			OK             bool     `json:"ok"`
			RecoveryCodes  []string `json:"recoveryCodes"`
			Reauthenticate bool     `json:"reauthenticate"`
		}{true, recoveryCodes, true})
		return
	}
	writeJSON(w, http.StatusOK, struct {
		OK             bool `json:"ok"`
		Reauthenticate bool `json:"reauthenticate"`
	}{true, true})
}

func (s *Service) handleAccountUpdate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		CurrentPassword string  `json:"currentPassword"`
		Code            string  `json:"code,omitempty"`
		Username        *string `json:"username,omitempty"`
		NewPassword     *string `json:"newPassword,omitempty"`
	}
	if !readBody(w, r, &input) {
		return
	}
	if input.Username == nil && input.NewPassword == nil ||
		input.Username != nil && !validUsername(*input.Username) ||
		input.NewPassword != nil && !validPassword(*input.NewPassword) || len(input.Code) > 64 {
		invalidInput(w, "请提供有效的新用户名或 12–128 字节的新密码")
		return
	}
	if failure := s.authWork(r); failure != nil {
		writeFailure(w, failure)
		return
	}
	defer s.releaseKDF()
	version, failure := s.recheckPassword(r, input.CurrentPassword)
	if failure != nil {
		writeFailure(w, failure)
		return
	}
	var password passwordRecord
	if input.NewPassword != nil {
		var err error
		password, err = newPassword(*input.NewPassword)
		if err != nil {
			writeFailure(w, unavailable)
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, failure := s.securitySessionLocked(r, version); failure != nil {
		writeFailure(w, failure)
		return
	}
	candidate := cloneAccount(s.account)
	if !consumeSecondFactor(&candidate, input.Code, s.now().UTC()) {
		s.recordLocked("account.verify.failed", "身份验证失败", remoteIP(r))
		writeFailure(w, authFailed)
		return
	}
	if input.Username != nil {
		candidate.Username = *input.Username
	}
	if input.NewPassword != nil {
		candidate.Password = password
	}
	if err := s.saveAccountLocked(candidate); err != nil {
		writeFailure(w, storageError)
		return
	}
	s.credentialVersion++
	s.clearSessionsLocked()
	s.recordLocked("account.credentials.changed", "账户凭据已更新，全部会话已撤销", remoteIP(r))
	s.clearCookie(w, r)
	securityChanged(w, nil)
}

func (s *Service) handleTOTPBegin(w http.ResponseWriter, r *http.Request) {
	var input struct {
		CurrentPassword string `json:"currentPassword"`
	}
	if !readBody(w, r, &input) {
		return
	}
	if failure := s.authWork(r); failure != nil {
		writeFailure(w, failure)
		return
	}
	defer s.releaseKDF()
	version, failure := s.recheckPassword(r, input.CurrentPassword)
	if failure != nil {
		writeFailure(w, failure)
		return
	}
	secret := make([]byte, totpSecretBytes)
	if _, err := rand.Read(secret); err != nil {
		writeFailure(w, unavailable)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, failure := s.securitySessionLocked(r, version)
	if failure != nil {
		clear(secret)
		writeFailure(w, failure)
		return
	}
	if len(s.account.TOTPSecret) != 0 {
		clear(secret)
		writeFailure(w, &apiFailure{http.StatusConflict, "totp_already_enabled", "两步验证已启用"})
		return
	}
	wipeEnrollment(sess)
	sess.Enrollment = &enrollment{Secret: secret, ExpiresAt: s.now().UTC().Add(enrollmentTTL)}
	encoded := secretString(secret)
	query := url.Values{"secret": {encoded}, "issuer": {"服务器面板"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	uri := "otpauth://totp/" + url.PathEscape("服务器面板:"+s.account.Username) + "?" + query.Encode()
	s.recordLocked("account.totp.begin", "已开始两步验证登记", remoteIP(r))
	writeJSON(w, http.StatusOK, struct {
		Secret     string `json:"secret"`
		OTPAuthURI string `json:"otpauthUri"`
	}{encoded, uri})
}

func (s *Service) handleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	var input struct {
		CurrentPassword string `json:"currentPassword"`
		Code            string `json:"code"`
	}
	if !readBody(w, r, &input) {
		return
	}
	if failure := s.authWork(r); failure != nil {
		writeFailure(w, failure)
		return
	}
	defer s.releaseKDF()
	version, failure := s.recheckPassword(r, input.CurrentPassword)
	if failure != nil {
		writeFailure(w, failure)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, failure := s.securitySessionLocked(r, version)
	if failure != nil {
		writeFailure(w, failure)
		return
	}
	if len(s.account.TOTPSecret) != 0 {
		writeFailure(w, &apiFailure{http.StatusConflict, "totp_already_enabled", "两步验证已启用"})
		return
	}
	if sess.Enrollment == nil {
		writeFailure(w, &apiFailure{http.StatusConflict, "enrollment_expired", "登记信息不存在或已过期，请重新开始"})
		return
	}
	step, ok := matchingTOTPStep(sess.Enrollment.Secret, input.Code, s.now().UTC(), -1)
	if !ok {
		sess.Enrollment.Tries++
		if sess.Enrollment.Tries >= maxEnrollmentTries {
			wipeEnrollment(sess)
		}
		s.recordLocked("account.totp.verify.failed", "两步验证登记校验失败", remoteIP(r))
		writeFailure(w, authFailed)
		return
	}
	codes, hashes, err := generateRecoveryCodes()
	if err != nil {
		writeFailure(w, unavailable)
		return
	}
	candidate := cloneAccount(s.account)
	candidate.TOTPSecret = append([]byte(nil), sess.Enrollment.Secret...)
	candidate.LastTOTPStep, candidate.RecoveryHashes = step, hashes
	if err := s.saveAccountLocked(candidate); err != nil {
		writeFailure(w, storageError)
		return
	}
	s.credentialVersion++
	s.clearSessionsLocked()
	s.recordLocked("account.totp.enabled", "两步验证已启用，全部会话已撤销", remoteIP(r))
	s.clearCookie(w, r)
	securityChanged(w, codes)
}

func (s *Service) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	var input struct {
		CurrentPassword string `json:"currentPassword"`
		Code            string `json:"code"`
	}
	if !readBody(w, r, &input) {
		return
	}
	if failure := s.authWork(r); failure != nil {
		writeFailure(w, failure)
		return
	}
	defer s.releaseKDF()
	version, failure := s.recheckPassword(r, input.CurrentPassword)
	if failure != nil {
		writeFailure(w, failure)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, failure := s.securitySessionLocked(r, version); failure != nil {
		writeFailure(w, failure)
		return
	}
	if len(s.account.TOTPSecret) == 0 {
		writeFailure(w, &apiFailure{http.StatusConflict, "totp_not_enabled", "尚未启用两步验证"})
		return
	}
	candidate := cloneAccount(s.account)
	if len(input.Code) > 64 || !consumeSecondFactor(&candidate, input.Code, s.now().UTC()) {
		s.recordLocked("account.verify.failed", "身份验证失败", remoteIP(r))
		writeFailure(w, authFailed)
		return
	}
	clear(candidate.TOTPSecret)
	candidate.TOTPSecret, candidate.RecoveryHashes, candidate.LastTOTPStep = nil, nil, -1
	if err := s.saveAccountLocked(candidate); err != nil {
		writeFailure(w, storageError)
		return
	}
	s.credentialVersion++
	s.clearSessionsLocked()
	s.recordLocked("account.totp.disabled", "两步验证已关闭，全部会话已撤销", remoteIP(r))
	s.clearCookie(w, r)
	securityChanged(w, nil)
}

func (s *Service) handleSessionDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validRandomToken(id, 16) {
		invalidInput(w, "会话标识无效")
		return
	}
	// DELETE has no request payload in the contract. An optional empty JSON
	// object is accepted for clients using a shared mutation helper.
	if r.Body != nil && (r.ContentLength != 0 || r.Header.Get("Content-Type") != "") {
		var input struct{}
		if !readBody(w, r, &input) {
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key, current := s.contextSessionLocked(r, s.now().UTC())
	if current == nil {
		writeFailure(w, unauthorized)
		return
	}
	found, reauthenticate := false, false
	for candidateKey, candidate := range s.sessions {
		if candidate.ID == id {
			found, reauthenticate = true, key == candidateKey
			wipeEnrollment(candidate)
			delete(s.sessions, candidateKey)
			break
		}
	}
	if !found {
		writeFailure(w, &apiFailure{http.StatusNotFound, "session_not_found", "会话不存在或已过期"})
		return
	}
	s.recordLocked("account.session.revoked", "已撤销所选会话", remoteIP(r))
	if reauthenticate {
		s.clearCookie(w, r)
	}
	writeJSON(w, http.StatusOK, struct {
		OK             bool `json:"ok"`
		Reauthenticate bool `json:"reauthenticate"`
	}{true, reauthenticate})
}
