// Package password hashes admin passwords with PBKDF2-SHA256 (standard
// library only) and checks password strength.
package password

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Iterations follows the OWASP 2023 recommendation for PBKDF2-HMAC-SHA256.
// A variable only so tests can make hashing cheap.
var Iterations = 600_000

const prefix = "pbkdf2-sha256"

var b64 = base64.RawStdEncoding

// Hash returns "pbkdf2-sha256$iterations$salt$hash".
func Hash(pw string) (string, error) {
	return hashWith(pw, Iterations)
}

func hashWith(pw string, iter int) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, pw, salt, iter, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s$%d$%s$%s", prefix, iter, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// Verify reports whether pw matches the stored hash, in constant time.
func Verify(stored, pw string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != prefix {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1000 || iter > 10_000_000 {
		return false
	}
	salt, err1 := b64.DecodeString(parts[2])
	want, err2 := b64.DecodeString(parts[3])
	if err1 != nil || err2 != nil || len(want) != 32 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, 32)
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

// dummy is verified against when the user does not exist, so a login for
// an unknown name costs the same time as one for a real name.
var (
	dummyOnce sync.Once
	dummy     string
)

// Burn spends the time of one verification.
func Burn(pw string) {
	dummyOnce.Do(func() { dummy, _ = Hash("botguard-dummy-password") })
	Verify(dummy, pw)
}

// ErrWeak describes why a new password is refused.
var ErrWeak = errors.New("weak password")

// Check enforces the minimum policy: 10–128 characters, at least three of
// lower case, upper case, digits and symbols, and not containing the
// username.
func Check(pw, username string) error {
	n := utf8.RuneCountInString(pw)
	if n < 10 || n > 128 {
		return fmt.Errorf("%w: 密码长度须为 10–128 个字符", ErrWeak)
	}
	var lower, upper, digit, other bool
	for _, r := range pw {
		switch {
		case unicode.IsLower(r):
			lower = true
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsDigit(r):
			digit = true
		default:
			other = true
		}
	}
	classes := 0
	for _, b := range []bool{lower, upper, digit, other} {
		if b {
			classes++
		}
	}
	if classes < 3 {
		return fmt.Errorf("%w: 密码须包含大写字母、小写字母、数字、符号中的至少三类", ErrWeak)
	}
	if username != "" && strings.Contains(strings.ToLower(pw), strings.ToLower(username)) {
		return fmt.Errorf("%w: 密码不能包含用户名", ErrWeak)
	}
	return nil
}

// Generate returns a random password that passes Check: 20 characters from
// an alphabet without look-alikes, with each class present.
func Generate() (string, error) {
	const (
		lower  = "abcdefghijkmnpqrstuvwxyz"
		upper  = "ABCDEFGHJKLMNPQRSTUVWXYZ"
		digits = "23456789"
		syms   = "-_.@#%+="
	)
	all := lower + upper + digits + syms
	for {
		b := make([]byte, 20)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		out := make([]byte, len(b))
		for i, v := range b {
			out[i] = all[int(v)%len(all)]
		}
		pw := string(out)
		if strings.ContainsAny(pw, lower) && strings.ContainsAny(pw, upper) && strings.ContainsAny(pw, digits) && strings.ContainsAny(pw, syms) {
			return pw, nil
		}
	}
}
