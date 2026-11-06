package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha1" // TOTP interoperability only, never password hashing.
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"hash"
	"strings"
	"time"
)

const (
	passwordIterations = 600000
	passwordSaltBytes  = 32
	passwordHashBytes  = 32
	recoveryCodeCount  = 10
	totpSecretBytes    = 20
)

type passwordRecord struct {
	Salt       []byte `json:"salt"`
	Hash       []byte `json:"hash"`
	Iterations int    `json:"iterations"`
}

type accountRecord struct {
	Version        int            `json:"version"`
	Username       string         `json:"username"`
	Password       passwordRecord `json:"password"`
	TOTPSecret     []byte         `json:"totpSecret,omitempty"`
	LastTOTPStep   int64          `json:"lastTotpStep"`
	RecoveryHashes [][]byte       `json:"recoveryHashes,omitempty"`
}

func validUsername(value string) bool {
	if len(value) < 3 || len(value) > 32 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			continue
		}
		if i == 0 || c != '_' && c != '-' && c != '.' {
			return false
		}
	}
	return true
}

func validPassword(value string) bool { return len(value) >= 12 && len(value) <= 128 }

func validAccount(value *accountRecord) bool {
	if value.Version != 1 || !validUsername(value.Username) ||
		value.Password.Iterations != passwordIterations || len(value.Password.Salt) != passwordSaltBytes ||
		len(value.Password.Hash) != passwordHashBytes || len(value.RecoveryHashes) > recoveryCodeCount {
		return false
	}
	if len(value.TOTPSecret) == 0 {
		return value.LastTOTPStep == -1 && len(value.RecoveryHashes) == 0
	}
	if len(value.TOTPSecret) != totpSecretBytes || value.LastTOTPStep < 0 {
		return false
	}
	seen := make(map[[32]byte]bool, len(value.RecoveryHashes))
	for _, recovery := range value.RecoveryHashes {
		if len(recovery) != sha256.Size {
			return false
		}
		var digest [32]byte
		copy(digest[:], recovery)
		if seen[digest] {
			return false
		}
		seen[digest] = true
	}
	return true
}

func cloneAccount(value *accountRecord) accountRecord {
	result := *value
	result.Password.Salt = append([]byte(nil), value.Password.Salt...)
	result.Password.Hash = append([]byte(nil), value.Password.Hash...)
	result.TOTPSecret = append([]byte(nil), value.TOTPSecret...)
	result.RecoveryHashes = make([][]byte, len(value.RecoveryHashes))
	for i, recovery := range value.RecoveryHashes {
		result.RecoveryHashes[i] = append([]byte(nil), recovery...)
	}
	return result
}

func newPassword(password string) (passwordRecord, error) {
	salt := make([]byte, passwordSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return passwordRecord{}, err
	}
	derived, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, passwordHashBytes)
	if err != nil {
		return passwordRecord{}, err
	}
	return passwordRecord{Salt: salt, Hash: derived, Iterations: passwordIterations}, nil
}

func verifyPassword(password string, record passwordRecord) bool {
	derived, err := pbkdf2.Key(sha256.New, password, record.Salt, record.Iterations, passwordHashBytes)
	if err != nil {
		return false
	}
	defer clear(derived)
	return subtle.ConstantTimeCompare(derived, record.Hash) == 1
}

func (s *Service) acquireKDF() *apiFailure {
	select {
	case s.kdf <- struct{}{}:
		return nil
	default:
		return kdfBusy
	}
}

func (s *Service) releaseKDF() { <-s.kdf }

func constantStringEqual(a, b string) bool {
	left, right := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(left[:], right[:]) == 1
}

// sha1HMAC is the RFC 2104 construction used only by RFC 6238 TOTP. Go's
// crypto/hmac SHA-1 is disallowed in some FIPS-only builds despite this standard
// interoperability purpose. Passwords always use crypto/pbkdf2 with SHA-256.
func sha1HMAC(key, message []byte) [sha1.Size]byte {
	var block [sha1.BlockSize]byte
	if len(key) > len(block) {
		digest := sha1.Sum(key)
		copy(block[:], digest[:])
	} else {
		copy(block[:], key)
	}
	var inner, outer [sha1.BlockSize]byte
	for i, b := range block {
		inner[i], outer[i] = b^0x36, b^0x5c
	}
	var h hash.Hash = sha1.New()
	_, _ = h.Write(inner[:])
	_, _ = h.Write(message)
	inside := h.Sum(nil)
	h.Reset()
	_, _ = h.Write(outer[:])
	_, _ = h.Write(inside)
	var result [sha1.Size]byte
	copy(result[:], h.Sum(nil))
	clear(block[:])
	clear(inner[:])
	clear(outer[:])
	clear(inside)
	return result
}

func totpCode(secret []byte, step int64) string {
	var message [8]byte
	binary.BigEndian.PutUint64(message[:], uint64(step))
	digest := sha1HMAC(secret, message[:])
	offset := digest[len(digest)-1] & 0x0f
	value := binary.BigEndian.Uint32(digest[offset:offset+4]) & 0x7fffffff
	value %= 1000000
	var code [6]byte
	for i := len(code) - 1; i >= 0; i-- {
		code[i] = byte(value%10) + '0'
		value /= 10
	}
	return string(code[:])
}

func matchingTOTPStep(secret []byte, code string, now time.Time, lastStep int64) (int64, bool) {
	if len(code) != 6 {
		return 0, false
	}
	for i := range code {
		if code[i] < '0' || code[i] > '9' {
			return 0, false
		}
	}
	step := now.Unix() / 30
	matched := int64(-1)
	// Check all allowed windows even after a match. A rare collision chooses the
	// largest matching step, so it cannot be reused in another allowed window.
	for candidate := step - 1; candidate <= step+1; candidate++ {
		if candidate < 0 {
			continue
		}
		expected := totpCode(secret, candidate)
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 && candidate > lastStep {
			matched = candidate
		}
	}
	return matched, matched >= 0
}

func secretString(secret []byte) string {
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
}

func recoveryDigest(code string) [32]byte {
	return sha256.Sum256([]byte("panel-recovery-v1\x00" + code))
}

func generateRecoveryCodes() ([]string, [][]byte, error) {
	codes := make([]string, recoveryCodeCount)
	hashes := make([][]byte, recoveryCodeCount)
	for i := range codes {
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return nil, nil, err
		}
		encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:])
		// Each code has 128 bits of random entropy, independently of its format.
		codes[i] = "R-" + encoded[:5] + "-" + encoded[5:10] + "-" + encoded[10:15] + "-" + encoded[15:20] + "-" + encoded[20:]
		digest := recoveryDigest(normalizeRecoveryCode(codes[i]))
		hashes[i] = append([]byte(nil), digest[:]...)
	}
	return codes, hashes, nil
}

func normalizeRecoveryCode(value string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), "-", ""))
}

// consumeSecondFactor mutates a private candidate copy, not live state. The
// caller must atomically persist that candidate before granting any authority.
func consumeSecondFactor(account *accountRecord, code string, now time.Time) bool {
	if len(account.TOTPSecret) == 0 {
		return true
	}
	if step, ok := matchingTOTPStep(account.TOTPSecret, code, now, account.LastTOTPStep); ok {
		account.LastTOTPStep = step
		return true
	}
	if len(code) > 64 {
		return false
	}
	normalized := normalizeRecoveryCode(code)
	if len(normalized) != 27 || normalized[0] != 'R' {
		return false
	}
	digest := recoveryDigest(normalized)
	matched := -1
	for i, expected := range account.RecoveryHashes {
		if subtle.ConstantTimeCompare(digest[:], expected) == 1 {
			matched = i
		}
	}
	if matched < 0 {
		return false
	}
	account.RecoveryHashes = append(account.RecoveryHashes[:matched], account.RecoveryHashes[matched+1:]...)
	return true
}
