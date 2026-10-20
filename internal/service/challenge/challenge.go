// Package challenge signs and opens stateless challenges.
//
// Format: v1.<kid>.<payload_b64>.<mac_b64>, where
// mac = HMAC-SHA256(key[kid], "v1." + kid + "." + payload_b64).
// The MAC covers the received bytes; the payload is never re-serialized
// before verification.
package challenge

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/dkeepersun/BotGuard/internal/service/pow"
)

const (
	version      = "v1"
	MaxLen       = 2048
	MinSecretLen = 32
)

var (
	ErrTooLong    = errors.New("challenge: too long")
	ErrMalformed  = errors.New("challenge: malformed")
	ErrUnknownKey = errors.New("challenge: unknown key id")
	ErrSignature  = errors.New("challenge: bad signature")

	kidPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
	b64        = base64.RawURLEncoding
)

// Payload is signed but not encrypted: never put internal IDs or IPs here.
type Payload struct {
	ChallengeID   string     `json:"challenge_id"`
	AppKey        string     `json:"app_key"`
	Action        string     `json:"action"`
	Origin        string     `json:"origin"`
	Algorithm     string     `json:"algorithm"`
	Params        pow.Params `json:"params"`
	PolicyVersion int64      `json:"policy_version"`
	IssuedAt      int64      `json:"iat"`
	ExpiresAt     int64      `json:"exp"`
	// Kind is the challenge type the browser must complete (invisible, slider, fallback).
	Kind string `json:"kind"`
	// Sealed is server-only data (answers, risk) encrypted under a key derived
	// from the signing key; the browser can carry it but not read it.
	Sealed string `json:"sealed,omitempty"`
}

type Key struct {
	ID     string
	Secret []byte
	Active bool
}

// Keyring holds exactly one active signing key plus verify-only keys kept
// during rotation.
type Keyring struct {
	active string
	keys   map[string][]byte
}

func NewKeyring(keys []Key) (*Keyring, error) {
	k := &Keyring{keys: make(map[string][]byte, len(keys))}
	for _, key := range keys {
		if !kidPattern.MatchString(key.ID) {
			return nil, fmt.Errorf("challenge key id %q is invalid", key.ID)
		}
		if len(key.Secret) < MinSecretLen {
			return nil, fmt.Errorf("challenge key %q must be at least %d bytes", key.ID, MinSecretLen)
		}
		if _, dup := k.keys[key.ID]; dup {
			return nil, fmt.Errorf("challenge key %q is duplicated", key.ID)
		}
		k.keys[key.ID] = append([]byte(nil), key.Secret...)
		if key.Active {
			if k.active != "" {
				return nil, errors.New("exactly one challenge key must be active")
			}
			k.active = key.ID
		}
	}
	if k.active == "" {
		return nil, errors.New("exactly one challenge key must be active")
	}
	return k, nil
}

// NewID returns a random 128-bit challenge identifier.
func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return b64.EncodeToString(b[:]), nil
}

func mac(secret []byte, signed string) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(signed))
	return m.Sum(nil)
}

// sealKey derives the AES-256 key for Sealed from a signing secret, so no
// extra key needs to be configured or rotated.
func sealKey(secret []byte) (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, secret, nil, "botguard challenge seal v1", 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Sign signs p, first sealing private (if any) into p.Sealed with the
// challenge id as additional data.
func (k *Keyring) Sign(p Payload, private []byte) (string, error) {
	if len(private) > 0 {
		aead, err := sealKey(k.keys[k.active])
		if err != nil {
			return "", err
		}
		nonce := make([]byte, aead.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return "", err
		}
		p.Sealed = b64.EncodeToString(aead.Seal(nonce, nonce, private, []byte(p.ChallengeID)))
	}
	body, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	signed := version + "." + k.active + "." + b64.EncodeToString(body)
	s := signed + "." + b64.EncodeToString(mac(k.keys[k.active], signed))
	if len(s) > MaxLen {
		return "", ErrTooLong
	}
	return s, nil
}

// Open verifies the MAC before decoding anything else, and returns the
// unsealed private data (nil when none was sealed).
func (k *Keyring) Open(s string) (Payload, []byte, error) {
	p, secret, err := k.open(s)
	if err != nil || p.Sealed == "" {
		return p, nil, err
	}
	sealed, err := b64.DecodeString(p.Sealed)
	if err != nil {
		return p, nil, ErrMalformed
	}
	aead, err := sealKey(secret)
	if err != nil {
		return p, nil, err
	}
	n := aead.NonceSize()
	if len(sealed) < n+aead.Overhead() {
		return p, nil, ErrMalformed
	}
	private, err := aead.Open(nil, sealed[:n], sealed[n:], []byte(p.ChallengeID))
	if err != nil {
		return p, nil, ErrSignature
	}
	return p, private, nil
}

func (k *Keyring) open(s string) (Payload, []byte, error) {
	var p Payload
	if len(s) > MaxLen {
		return p, nil, ErrTooLong
	}
	parts := strings.Split(s, ".")
	if len(parts) != 4 || parts[0] != version {
		return p, nil, ErrMalformed
	}
	secret, ok := k.keys[parts[1]]
	if !ok {
		return p, nil, ErrUnknownKey
	}
	got, err := b64.DecodeString(parts[3])
	if err != nil {
		return p, nil, ErrMalformed
	}
	if !hmac.Equal(got, mac(secret, parts[0]+"."+parts[1]+"."+parts[2])) {
		return p, nil, ErrSignature
	}
	body, err := b64.DecodeString(parts[2])
	if err != nil {
		return p, nil, ErrMalformed
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return p, nil, ErrMalformed
	}
	return p, secret, nil
}
