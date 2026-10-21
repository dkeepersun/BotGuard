package totp

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

// RFC 6238 Appendix B, SHA-1, truncated to 6 digits.
func TestRFCVectors(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	for unix, want := range map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"} {
		got, err := Code(secret, time.Unix(unix, 0))
		if err != nil || got != want {
			t.Errorf("%d: %s %v", unix, got, err)
		}
	}
}

func TestVerifyWindowAndReplay(t *testing.T) {
	s, _ := NewSecret()
	now := time.Unix(1_790_000_000, 0)
	c, _ := Code(s, now.Add(-30*time.Second))
	step, ok := Verify(s, c, now, 0)
	if !ok {
		t.Fatal("previous step rejected")
	}
	if _, ok := Verify(s, c, now, step); ok {
		t.Fatal("replayed code accepted")
	}
	old, _ := Code(s, now.Add(-90*time.Second))
	if _, ok := Verify(s, old, now, 0); ok {
		t.Fatal("code three steps old accepted")
	}
	if _, ok := Verify(s, "12345", now, 0); ok {
		t.Fatal("short code accepted")
	}
	if !strings.HasPrefix(URI("BotGuard", "admin", s), "otpauth://totp/BotGuard:admin?") || len(strings.Fields(Group(s))) != 8 {
		t.Fatal(URI("BotGuard", "admin", s), Group(s))
	}
}
