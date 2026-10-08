package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP (RFC 6238): HMAC-SHA1, 30-second steps, 6 digits — compatible with
// Google Authenticator, Authy, 1Password, etc. Implemented here to avoid a
// dependency.

const (
	totpDigits = 6
	totpPeriod = 30 * time.Second
	// Accept the current step plus one before/after to tolerate clock drift.
	totpSkew = 1
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a random 160-bit base32 secret.
func NewTOTPSecret() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b32.EncodeToString(b)
}

// TOTPURI builds the otpauth:// URL for QR enrollment.
func TOTPURI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprintf("%d", totpDigits))
	q.Set("period", "30")
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// code computes the TOTP for a given step counter.
func totpCode(secret string, counter uint64) (string, bool) {
	key, err := b32.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", false
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset]&0x7f) << 24) | (uint32(sum[offset+1]) << 16) | (uint32(sum[offset+2]) << 8) | uint32(sum[offset+3])
	mod := uint32(1)
	for i := 0; i < totpDigits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", totpDigits, value%mod), true
}

func currentCounter() uint64 {
	return uint64(time.Now().Unix()) / uint64(totpPeriod.Seconds())
}

// VerifyTOTP checks a user-entered code against the secret, allowing ±1 step.
func VerifyTOTP(secret, input string) bool {
	input = strings.TrimSpace(input)
	if len(input) != totpDigits {
		return false
	}
	counter := currentCounter()
	for i := -totpSkew; i <= totpSkew; i++ {
		c, ok := totpCode(secret, counter+uint64(int64(i)))
		if ok && subtle.ConstantTimeCompare([]byte(c), []byte(input)) == 1 {
			return true
		}
	}
	return false
}
