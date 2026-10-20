// Package challenge signs and opens stateless challenges.
//
// Format: v1.<kid>.<payload_b64>.<mac_b64>, where
// mac = HMAC-SHA256(key[kid], "v1." + kid + "." + payload_b64).
// The MAC covers the received bytes; the payload is never re-serialized
// before verification.
package challenge

import (
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
	MaxLen       = 1024
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

func (k *Keyring) Sign(p Payload) (string, error) {
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

// Open verifies the MAC before decoding anything else.
func (k *Keyring) Open(s string) (Payload, error) {
	var p Payload
	if len(s) > MaxLen {
		return p, ErrTooLong
	}
	parts := strings.Split(s, ".")
	if len(parts) != 4 || parts[0] != version {
		return p, ErrMalformed
	}
	secret, ok := k.keys[parts[1]]
	if !ok {
		return p, ErrUnknownKey
	}
	got, err := b64.DecodeString(parts[3])
	if err != nil {
		return p, ErrMalformed
	}
	if !hmac.Equal(got, mac(secret, parts[0]+"."+parts[1]+"."+parts[2])) {
		return p, ErrSignature
	}
	body, err := b64.DecodeString(parts[2])
	if err != nil {
		return p, ErrMalformed
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return p, ErrMalformed
	}
	return p, nil
}
