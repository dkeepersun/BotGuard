package sealer

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func kek(id string, b byte) string {
	return id + ":" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32))
}

func TestSealOpenAndRotation(t *testing.T) {
	old, err := New(kek("k1", 1))
	if err != nil {
		t.Fatal(err)
	}
	id, ct, err := old.Seal("sid_a", []byte("secret-value"))
	if err != nil || id != "k1" {
		t.Fatalf("seal: %s %v", id, err)
	}
	rotated, _ := New(kek("k2", 2), kek("k1", 1))
	if got, err := rotated.Open(id, "sid_a", ct); err != nil || string(got) != "secret-value" {
		t.Fatalf("open with previous kek: %q %v", got, err)
	}
	if id2, _, _ := rotated.Seal("sid_b", []byte("x")); id2 != "k2" {
		t.Fatalf("sealed with %s", id2)
	}
	if _, err := rotated.Open(id, "sid_other", ct); err != ErrOpen {
		t.Fatalf("ciphertext moved to another row: %v", err)
	}
	tampered := append([]byte(nil), ct...)
	tampered[len(tampered)-1] ^= 1
	if _, err := rotated.Open(id, "sid_a", tampered); err != ErrOpen {
		t.Fatalf("tampered: %v", err)
	}
	if _, err := rotated.Open("k9", "sid_a", ct); err != ErrUnknownKEK {
		t.Fatalf("unknown kek: %v", err)
	}
	if _, err := rotated.Open(id, "sid_a", ct[:5]); err != ErrOpen {
		t.Fatalf("short: %v", err)
	}
}

func TestNewRejectsBadKEKs(t *testing.T) {
	for name, args := range map[string][]string{
		"no id":     {base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))},
		"short key": {"k1:" + base64.StdEncoding.EncodeToString([]byte("short"))},
		"bad b64":   {"k1:!!!"},
		"duplicate": {kek("k1", 1), kek("k1", 2)},
		"empty":     {""},
	} {
		if _, err := New(args[0], args[1:]...); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
