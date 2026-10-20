package challenge

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dkeepersun/BotGuard/internal/service/pow"
)

var (
	secretA = bytes.Repeat([]byte("a"), 32)
	secretB = bytes.Repeat([]byte("b"), 32)
)

func sample() Payload {
	params, _ := pow.NewParams(8, 1000)
	return Payload{ChallengeID: "id", SiteKey: "site", Action: "login", Origin: "https://a.example",
		Algorithm: pow.AlgSHA256Target, Params: params, PolicyVersion: 3, IssuedAt: 100, ExpiresAt: 220}
}

func TestRoundTripAndRotation(t *testing.T) {
	old, _ := NewKeyring([]Key{{ID: "k1", Secret: secretA, Active: true}})
	s, err := old.Sign(sample())
	if err != nil {
		t.Fatal(err)
	}
	// k2 active, k1 verify-only: old challenges still open.
	rotated, _ := NewKeyring([]Key{{ID: "k2", Secret: secretB, Active: true}, {ID: "k1", Secret: secretA}})
	if p, err := rotated.Open(s); err != nil || p != sample() {
		t.Fatalf("rotated open: %v %+v", err, p)
	}
	fresh, _ := rotated.Sign(sample())
	if !strings.HasPrefix(fresh, "v1.k2.") {
		t.Fatalf("signed with %q", fresh[:6])
	}
	// k1 removed: rejected.
	removed, _ := NewKeyring([]Key{{ID: "k2", Secret: secretB, Active: true}})
	if _, err := removed.Open(s); err != ErrUnknownKey {
		t.Fatalf("removed key: %v", err)
	}
}

func TestTamperRejected(t *testing.T) {
	k, _ := NewKeyring([]Key{{ID: "k1", Secret: secretA, Active: true}})
	s, _ := k.Sign(sample())
	parts := strings.Split(s, ".")

	easier := sample()
	easier.Params, _ = pow.NewParams(1, 1)
	forged, _ := (&Keyring{active: "k1", keys: map[string][]byte{"k1": secretB}}).Sign(easier)
	fp := strings.Split(forged, ".")

	for name, c := range map[string]string{
		"payload swapped": parts[0] + "." + parts[1] + "." + fp[2] + "." + parts[3],
		"wrong secret":    forged,
		"mac truncated":   s[:len(s)-2],
		"kid swapped":     parts[0] + ".k9." + parts[2] + "." + parts[3],
		"version":         "v2" + s[2:],
		"extra part":      s + ".x",
		"too long":        s + strings.Repeat("A", MaxLen),
	} {
		if _, err := k.Open(c); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestKeyringValidation(t *testing.T) {
	for name, keys := range map[string][]Key{
		"none active":  {{ID: "k1", Secret: secretA}},
		"two active":   {{ID: "k1", Secret: secretA, Active: true}, {ID: "k2", Secret: secretB, Active: true}},
		"short secret": {{ID: "k1", Secret: []byte("short"), Active: true}},
		"bad id":       {{ID: "k.1", Secret: secretA, Active: true}},
		"duplicate":    {{ID: "k1", Secret: secretA, Active: true}, {ID: "k1", Secret: secretB}},
	} {
		if _, err := NewKeyring(keys); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
