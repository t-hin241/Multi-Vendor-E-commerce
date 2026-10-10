package domain

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 TOTP uses HMAC-SHA1, which authenticator apps expect.
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// PW-028: a time-based one-time password (RFC 6238: HMAC-SHA1, 30-second
// steps, 6 digits) is the second factor of an admin's reauthentication for
// money operations. The secret only exists encrypted at rest.

const (
	TOTPStep   = 30 * time.Second
	totpDigits = 6
	// TOTPSkew accepts the step before and after the current one (clock
	// drift between the phone and the server).
	TOTPSkew = 1
	// RecoveryCodeCount codes are given at enrollment, each usable once.
	RecoveryCodeCount = 8
)

var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns 20 random bytes (160 bits, RFC 4226 §4).
func NewTOTPSecret() ([]byte, error) {
	secret := make([]byte, 20)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	return secret, nil
}

// TOTPSecretText is the secret as an authenticator app expects it.
func TOTPSecretText(secret []byte) string { return totpEncoding.EncodeToString(secret) }

// TOTPSecretBytes reads TOTPSecretText back.
func TOTPSecretBytes(text string) ([]byte, error) { return totpEncoding.DecodeString(text) }

// TOTPURI is the otpauth:// link an authenticator app imports.
func TOTPURI(issuer, account string, secret []byte) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{"secret": {TOTPSecretText(secret)}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// TOTPStepAt is the step number of a time.
func TOTPStepAt(t time.Time) int64 { return t.Unix() / int64(TOTPStep/time.Second) }

// TOTPCode is the code of one step.
func TOTPCode(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1_000_000)
}

// VerifyTOTP checks a code against the steps around now and returns the
// step it matched; a step at or before lastUsed is refused (a code is
// used once).
func VerifyTOTP(secret []byte, code string, now time.Time, lastUsed int64) (int64, bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != totpDigits {
		return 0, false
	}
	current := TOTPStepAt(now)
	for step := current - TOTPSkew; step <= current+TOTPSkew; step++ {
		if step <= lastUsed {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(TOTPCode(secret, step)), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

// IsRecoveryCode tells a recovery code ("xxxxx-xxxxx") from a TOTP code.
func IsRecoveryCode(code string) bool {
	code = strings.TrimSpace(code)
	return len(code) == 11 && code[5] == '-'
}

// NewRecoveryCode is ten random characters, grouped "xxxxx-xxxxx".
func NewRecoveryCode() (string, error) {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	// Rejection sampling: only bytes below a multiple of the alphabet size,
	// so every character is equally likely.
	limit := byte(256 - 256%len(alphabet))
	out := make([]byte, 0, 11)
	buf := make([]byte, 16)
	for len(out) < 11 {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if len(out) == 11 {
				break
			}
			if len(out) == 5 {
				out = append(out, '-')
			}
			if b < limit {
				out = append(out, alphabet[int(b)%len(alphabet)])
			}
		}
	}
	return string(out), nil
}
