// Package totp implements RFC 6238 time-based one-time passwords (SHA-1,
// 6 digits, 30-second steps), the variant every authenticator app supports.
package totp

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

const (
	Step   = 30
	Digits = 6
	// Skew accepts one step on either side for clock drift.
	Skew = 1
)

var enc = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret returns a random 160-bit secret in base32.
func NewSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return enc.EncodeToString(b), nil
}

// URI is the otpauth:// link authenticator apps import.
func URI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{"secret": {secret}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// Group formats a secret in blocks of four for manual entry.
func Group(secret string) string {
	var parts []string
	for i := 0; i < len(secret); i += 4 {
		parts = append(parts, secret[i:min(i+4, len(secret))])
	}
	return strings.Join(parts, " ")
}

func code(key []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", v%1_000_000)
}

// Code returns the code for time t (for tests and enrolment checks).
func Code(secret string, t time.Time) (string, error) {
	key, err := enc.DecodeString(strings.ToUpper(strings.ReplaceAll(secret, " ", "")))
	if err != nil {
		return "", err
	}
	return code(key, t.Unix()/Step), nil
}

// Verify checks input against the steps around now. It returns the matched
// step; callers store it and pass it as lastStep next time, so a code is
// never accepted twice (RFC 6238 §5.2).
func Verify(secret, input string, now time.Time, lastStep int64) (int64, bool) {
	input = strings.ReplaceAll(strings.TrimSpace(input), " ", "")
	if len(input) != Digits {
		return 0, false
	}
	key, err := enc.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return 0, false
	}
	cur := now.Unix() / Step
	for s := cur - Skew; s <= cur+Skew; s++ {
		if s <= lastStep {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(code(key, s)), []byte(input)) == 1 {
			return s, true
		}
	}
	return 0, false
}
