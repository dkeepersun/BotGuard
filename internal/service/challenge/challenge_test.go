package challenge

import (
	"bytes"
	"encoding/json"
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
	return Payload{ChallengeID: "id", AppKey: "app", Action: "login", Origin: "https://a.example",
		Algorithm: pow.AlgSHA256Target, Params: params, PolicyVersion: 3, IssuedAt: 100, ExpiresAt: 220}
}

func TestRoundTripAndRotation(t *testing.T) {
	old, _ := NewKeyring([]Key{{ID: "k1", Secret: secretA, Active: true}})
	s, err := old.Sign(sample(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// k2 active, k1 verify-only: old challenges still open.
	rotated, _ := NewKeyring([]Key{{ID: "k2", Secret: secretB, Active: true}, {ID: "k1", Secret: secretA}})
	if p, _, err := rotated.Open(s); err != nil || p != sample() {
		t.Fatalf("rotated open: %v %+v", err, p)
	}
	fresh, _ := rotated.Sign(sample(), nil)
	if !strings.HasPrefix(fresh, "v1.k2.") {
		t.Fatalf("signed with %q", fresh[:6])
	}
	// k1 removed: rejected.
	removed, _ := NewKeyring([]Key{{ID: "k2", Secret: secretB, Active: true}})
	if _, _, err := removed.Open(s); err != ErrUnknownKey {
		t.Fatalf("removed key: %v", err)
	}
}

func TestTamperRejected(t *testing.T) {
	k, _ := NewKeyring([]Key{{ID: "k1", Secret: secretA, Active: true}})
	s, _ := k.Sign(sample(), nil)
	parts := strings.Split(s, ".")

	easier := sample()
	easier.Params, _ = pow.NewParams(1, 1)
	forged, _ := (&Keyring{active: "k1", keys: map[string][]byte{"k1": secretB}}).Sign(easier, nil)
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
		if _, _, err := k.Open(c); err == nil {
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

func TestSealedPrivateData(t *testing.T) {
	k, _ := NewKeyring([]Key{{ID: "k1", Secret: secretA, Active: true}})
	s, err := k.Sign(sample(), []byte(`{"x":183}`))
	if err != nil {
		t.Fatal(err)
	}
	p, private, err := k.Open(s)
	if err != nil || string(private) != `{"x":183}` || p.Sealed == "" {
		t.Fatalf("open: %q %v", private, err)
	}
	if strings.Contains(s, "183") {
		t.Fatal("private data visible in the challenge")
	}
	// Rotation: a verify-only key still unseals what it sealed.
	rotated, _ := NewKeyring([]Key{{ID: "k2", Secret: secretB, Active: true}, {ID: "k1", Secret: secretA}})
	if _, private, err := rotated.Open(s); err != nil || string(private) != `{"x":183}` {
		t.Fatalf("rotated: %q %v", private, err)
	}
	// Moving a sealed blob onto another challenge (different id) must fail,
	// even when the attacker could re-sign the payload.
	other := sample()
	other.ChallengeID = "other"
	other.Sealed = p.Sealed
	body, _ := json.Marshal(other)
	signed := "v1.k1." + b64.EncodeToString(body)
	forged := signed + "." + b64.EncodeToString(mac(secretA, signed))
	if _, _, err := k.Open(forged); err == nil {
		t.Fatal("sealed data accepted on another challenge")
	}
	if _, private, err := k.Open(mustSign(t, k)); err != nil || private != nil {
		t.Fatalf("no private data: %q %v", private, err)
	}
}

func mustSign(t *testing.T, k *Keyring) string {
	t.Helper()
	s, err := k.Sign(sample(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
