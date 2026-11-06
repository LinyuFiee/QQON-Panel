package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTOTPRFC6238VectorsAndHMAC(t *testing.T) {
	secret := []byte("12345678901234567890")
	for _, tc := range []struct {
		seconds   int64
		sixDigits string
	}{
		{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"},
		{1234567890, "005924"}, {2000000000, "279037"}, {20000000000, "353130"},
	} {
		if totpCode(secret, tc.seconds/30) != tc.sixDigits {
			t.Fatalf("TOTP RFC 6238 mismatch at time %d", tc.seconds)
		}
	}
	for _, key := range [][]byte{secret, bytes.Repeat([]byte{0x42}, 100)} {
		message := []byte("standard TOTP HMAC compatibility")
		expected := hmac.New(sha1.New, key)
		_, _ = expected.Write(message)
		actual := sha1HMAC(key, message)
		if !bytes.Equal(actual[:], expected.Sum(nil)) {
			t.Fatal("RFC 2104 HMAC implementation mismatch")
		}
	}
	now := time.Unix(1234567890, 0).UTC()
	step := now.Unix() / 30
	for _, candidate := range []int64{step - 1, step, step + 1} {
		code := totpCode(secret, candidate)
		matched, ok := matchingTOTPStep(secret, code, now, candidate-1)
		if !ok || matched != candidate {
			t.Fatal("valid TOTP window was rejected")
		}
		if _, ok := matchingTOTPStep(secret, code, now, candidate); ok {
			t.Fatal("TOTP replay was accepted")
		}
	}
	for _, code := range []string{"", "12345", "1234567", "abcdef", totpCode(secret, step-2), totpCode(secret, step+2)} {
		if _, ok := matchingTOTPStep(secret, code, now, -1); ok {
			t.Fatal("malformed or out-of-window TOTP was accepted")
		}
	}
}

func beginEnrollment(t *testing.T, h *testHarness, client *testClient) ([]byte, string) {
	t.Helper()
	result := h.request(http.MethodPost, "/api/account/totp/begin", encode(t, map[string]string{"currentPassword": testPassword}), client)
	requireStatus(t, result, http.StatusOK)
	body := decode[struct {
		Secret string `json:"secret"`
		URI    string `json:"otpauthUri"`
	}](t, result)
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(body.Secret)
	if err != nil || len(secret) != totpSecretBytes {
		t.Fatal("invalid TOTP enrollment secret")
	}
	uri, err := url.Parse(body.URI)
	if err != nil || uri.Scheme != "otpauth" || uri.Host != "totp" || uri.Query().Get("secret") != body.Secret ||
		uri.Query().Get("algorithm") != "SHA1" || uri.Query().Get("digits") != "6" || uri.Query().Get("period") != "30" {
		t.Fatal("invalid otpauth URI")
	}
	return secret, body.Secret
}

func enableEnrollment(t *testing.T, h *testHarness, client *testClient, secret []byte) []string {
	t.Helper()
	result := h.request(http.MethodPost, "/api/account/totp/enable", encode(t, map[string]string{
		"currentPassword": testPassword, "code": totpCode(secret, h.now.Unix()/30),
	}), client)
	requireStatus(t, result, http.StatusOK)
	body := decode[struct {
		OK             bool     `json:"ok"`
		RecoveryCodes  []string `json:"recoveryCodes"`
		Reauthenticate bool     `json:"reauthenticate"`
	}](t, result)
	if !body.OK || !body.Reauthenticate || len(body.RecoveryCodes) != recoveryCodeCount {
		t.Fatal("enable response omitted one-time recovery codes or reauthentication")
	}
	return body.RecoveryCodes
}

func TestTOTPEnrollmentReplayRecoveryRevocationAndPersistence(t *testing.T) {
	h := newHarness(t, true)
	first := h.issue("192.0.2.1:1111")
	second := h.issue("192.0.2.2:1111")
	secret, encodedSecret := beginEnrollment(t, h, first)
	code := totpCode(secret, h.now.Unix()/30)
	if len(h.s.account.TOTPSecret) != 0 {
		t.Fatal("begin activated TOTP before verification")
	}
	// Enrollment is session-bound and has not been persisted.
	requireStatus(t, h.request(http.MethodPost, "/api/account/totp/enable", encode(t, map[string]string{
		"currentPassword": testPassword, "code": code,
	}), second), http.StatusConflict)
	data, err := os.ReadFile(h.s.accountPath)
	if err != nil || bytes.Contains(data, []byte(encodedSecret)) || bytes.Contains(data, secret) {
		t.Fatal("temporary enrollment leaked to persistent account")
	}
	recovery := enableEnrollment(t, h, first, secret)
	if len(h.s.sessions) != 0 || len(h.s.account.RecoveryHashes) != recoveryCodeCount || h.s.account.LastTOTPStep != h.now.Unix()/30 {
		t.Fatal("TOTP enable did not persist anti-replay state or revoke sessions")
	}
	for _, client := range []*testClient{first, second} {
		requireStatus(t, h.request(http.MethodGet, "/api/account", "", client), http.StatusUnauthorized)
	}
	requireStatus(t, h.login("admin", testPassword, ""), http.StatusUnauthorized)
	requireStatus(t, h.login("admin", testPassword, code), http.StatusUnauthorized)
	*h.now = h.now.Add(30 * time.Second)
	code = totpCode(secret, h.now.Unix()/30)
	first = responseClient(t, h.login("admin", testPassword, code))
	requireStatus(t, h.login("admin", testPassword, code), http.StatusUnauthorized)
	second = responseClient(t, h.login("admin", testPassword, recovery[0]))
	requireStatus(t, h.login("admin", testPassword, recovery[0]), http.StatusUnauthorized)
	if len(h.s.account.RecoveryHashes) != recoveryCodeCount-1 {
		t.Fatal("recovery code was not consumed")
	}
	for _, path := range []string{h.s.accountPath, h.s.auditPath(0)} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, oneTime := range recovery {
			if bytes.Contains(data, []byte(oneTime)) || bytes.Contains(data, []byte(normalizeRecoveryCode(oneTime))) {
				t.Fatal("plaintext recovery code persisted")
			}
		}
		if bytes.Contains(data, []byte(code)) || bytes.Contains(data, []byte(testPassword)) || bytes.Contains(data, []byte(first.Cookie.Value)) {
			t.Fatal("credential leaked into persistence")
		}
	}
	accountResponse := h.request(http.MethodGet, "/api/account", "", first)
	requireStatus(t, accountResponse, http.StatusOK)
	if strings.Contains(accountResponse.Body.String(), encodedSecret) || strings.Contains(accountResponse.Body.String(), recovery[1]) {
		t.Fatal("account response re-disclosed two-factor secrets")
	}
	auditResponse := h.request(http.MethodGet, "/api/account/audit", "", first)
	requireStatus(t, auditResponse, http.StatusOK)
	if strings.Contains(auditResponse.Body.String(), encodedSecret) || strings.Contains(auditResponse.Body.String(), recovery[1]) {
		t.Fatal("audit response exposed two-factor secrets")
	}
	dir, now := h.s.dataDir, *h.now
	if err := h.s.Close(); err != nil {
		t.Fatal(err)
	}
	h = harnessIn(t, dir, false)
	*h.now = now
	requireStatus(t, h.request(http.MethodGet, "/api/account", "", first), http.StatusUnauthorized)
	requireStatus(t, h.request(http.MethodGet, "/api/account", "", second), http.StatusUnauthorized)
	requireStatus(t, h.login("admin", testPassword, code), http.StatusUnauthorized)
	requireStatus(t, h.login("admin", testPassword, recovery[0]), http.StatusUnauthorized)
	fresh := responseClient(t, h.login("admin", testPassword, recovery[1]))
	// Reauthentication is mandatory for both credentials and TOTP security changes.
	requireStatus(t, h.request(http.MethodPatch, "/api/account", encode(t, map[string]string{
		"currentPassword": testPassword, "username": "updated_admin",
	}), fresh), http.StatusUnauthorized)
	requireStatus(t, h.request(http.MethodPost, "/api/account/totp/disable", encode(t, map[string]string{
		"currentPassword": "wrong-current-password", "code": recovery[2],
	}), fresh), http.StatusUnauthorized)
	if len(h.s.account.RecoveryHashes) != recoveryCodeCount-2 {
		t.Fatal("wrong password consumed a recovery code")
	}
	disabled := h.request(http.MethodPost, "/api/account/totp/disable", encode(t, map[string]string{
		"currentPassword": testPassword, "code": recovery[2],
	}), fresh)
	requireStatus(t, disabled, http.StatusOK)
	if len(h.s.account.TOTPSecret) != 0 || len(h.s.account.RecoveryHashes) != 0 || h.s.account.LastTOTPStep != -1 || len(h.s.sessions) != 0 {
		t.Fatal("disable retained two-factor credentials or sessions")
	}
	requireStatus(t, h.request(http.MethodGet, "/api/account", "", fresh), http.StatusUnauthorized)
	responseClient(t, h.login("admin", testPassword, ""))
}

func TestTOTPEnrollmentExpiresAndHasAttemptBound(t *testing.T) {
	h := newHarness(t, true)
	client := h.issue("192.0.2.1:1111")
	secret, _ := beginEnrollment(t, h, client)
	*h.now = h.now.Add(enrollmentTTL)
	requireStatus(t, h.request(http.MethodPost, "/api/account/totp/enable", encode(t, map[string]string{
		"currentPassword": testPassword, "code": totpCode(secret, h.now.Unix()/30),
	}), client), http.StatusConflict)
	secret, _ = beginEnrollment(t, h, client)
	for range maxEnrollmentTries {
		requireStatus(t, h.request(http.MethodPost, "/api/account/totp/enable", encode(t, map[string]string{
			"currentPassword": testPassword, "code": "not-otp",
		}), client), http.StatusUnauthorized)
	}
	requireStatus(t, h.request(http.MethodPost, "/api/account/totp/enable", encode(t, map[string]string{
		"currentPassword": testPassword, "code": totpCode(secret, h.now.Unix()/30),
	}), client), http.StatusConflict)
	if len(h.s.account.TOTPSecret) != 0 {
		t.Fatal("expired or invalid enrollment activated two-factor authentication")
	}
}

func TestSimultaneousSecondFactorConsumptionIsSingleUse(t *testing.T) {
	h := newHarness(t, true)
	secret := []byte("12345678901234567890")
	codes, hashes, err := generateRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	candidate := cloneAccount(h.s.account)
	candidate.TOTPSecret, candidate.LastTOTPStep, candidate.RecoveryHashes = secret, h.now.Unix()/30-1, hashes
	if err := h.s.saveAccountLocked(candidate); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{totpCode(secret, h.now.Unix()/30), codes[0]} {
		start := make(chan struct{})
		results := make(chan int, 2)
		for range 2 {
			go func() {
				<-start
				result := h.login("admin", testPassword, code)
				results <- result.Code
			}()
		}
		close(start)
		counts := map[int]int{}
		for range 2 {
			select {
			case status := <-results:
				counts[status]++
			case <-time.After(20 * time.Second):
				t.Fatal("concurrent second-factor login timed out")
			}
		}
		if counts[http.StatusOK] != 1 || counts[http.StatusUnauthorized] != 1 {
			t.Fatalf("second factor not single-use under concurrency: statuses %v", counts)
		}
	}
}

func TestFailedPersistenceDoesNotGrantSessionOrConsumeFactor(t *testing.T) {
	h := newHarness(t, true)
	codes, hashes, err := generateRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	candidate := cloneAccount(h.s.account)
	candidate.TOTPSecret = []byte("12345678901234567890")
	candidate.LastTOTPStep, candidate.RecoveryHashes = h.now.Unix()/30-1, hashes
	if err := h.s.saveAccountLocked(candidate); err != nil {
		t.Fatal(err)
	}
	originalPath := h.s.accountPath
	h.s.accountPath = h.s.dataDir // replacing a directory must always fail
	requireStatus(t, h.login("admin", testPassword, codes[0]), http.StatusServiceUnavailable)
	if len(h.s.sessions) != 0 || len(h.s.account.RecoveryHashes) != recoveryCodeCount {
		t.Fatal("failed persistence granted login or consumed factor in memory")
	}
	h.s.accountPath = originalPath
	responseClient(t, h.login("admin", testPassword, codes[0]))
}

func TestRecoveryCodesAreIndependentHashedAndNotTOTPs(t *testing.T) {
	codes, hashes, err := generateRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != recoveryCodeCount || len(hashes) != recoveryCodeCount {
		t.Fatal("incorrect recovery code count")
	}
	seen := make(map[string]bool)
	for i, code := range codes {
		if seen[code] || len(normalizeRecoveryCode(code)) != 27 || len(hashes[i]) != 32 {
			t.Fatal("invalid random recovery code")
		}
		seen[code] = true
		digest := recoveryDigest(normalizeRecoveryCode(code))
		if !bytes.Equal(hashes[i], digest[:]) || bytes.Equal(hashes[i], []byte(code)) {
			t.Fatal("recovery code is not stored as a digest")
		}
	}
	account := accountRecord{TOTPSecret: []byte("12345678901234567890"), LastTOTPStep: 1, RecoveryHashes: hashes}
	if !consumeSecondFactor(&account, strings.ToLower(codes[0]), time.Now()) || consumeSecondFactor(&account, codes[0], time.Now()) {
		t.Fatal("normalized recovery code was not single-use")
	}
}
