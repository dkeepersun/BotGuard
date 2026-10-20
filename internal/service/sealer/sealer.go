// Package sealer encrypts app secret keys at rest with AES-256-GCM.
//
// HMAC request signing needs the server to hold each secret key, so a hash is
// not enough. Secrets are sealed with a key-encryption key (KEK) injected from
// the environment; the database alone never yields a usable secret. The
// secret_id is bound as additional data, so a ciphertext copied onto another
// row does not decrypt.
package sealer

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const keyLen = 32

var (
	ErrUnknownKEK = errors.New("sealer: unknown kek id")
	ErrOpen       = errors.New("sealer: ciphertext does not authenticate")

	idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
)

// Sealer seals with the active KEK and opens with any configured KEK, so old
// ciphertexts keep working during a KEK rotation.
type Sealer struct {
	active string
	aeads  map[string]cipher.AEAD
}

// ParseKEK parses "id:base64(32 bytes)".
func ParseKEK(s string) (id string, key []byte, err error) {
	id, enc, ok := strings.Cut(s, ":")
	if !ok || !idPattern.MatchString(id) {
		return "", nil, errors.New("KEK 格式应为 id:base64(32 字节)")
	}
	key, err = base64.StdEncoding.DecodeString(enc)
	if err != nil || len(key) != keyLen {
		return "", nil, fmt.Errorf("KEK %q 必须是 base64 编码的 32 字节", id)
	}
	return id, key, nil
}

// New takes the active KEK and any previous ones, all as "id:base64".
func New(active string, previous ...string) (*Sealer, error) {
	s := &Sealer{aeads: map[string]cipher.AEAD{}}
	for i, raw := range append([]string{active}, previous...) {
		id, key, err := ParseKEK(raw)
		if err != nil {
			return nil, err
		}
		if _, dup := s.aeads[id]; dup {
			return nil, fmt.Errorf("KEK %q 重复", id)
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		s.aeads[id] = aead
		if i == 0 {
			s.active = id
		}
	}
	return s, nil
}

// Active is the id of the KEK that seals new secrets.
func (s *Sealer) Active() string { return s.active }

// Seal returns the active KEK id and nonce||ciphertext.
func (s *Sealer) Seal(secretID string, plaintext []byte) (string, []byte, error) {
	aead := s.aeads[s.active]
	nonce := make([]byte, aead.NonceSize(), aead.NonceSize()+len(plaintext)+aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return "", nil, err
	}
	return s.active, aead.Seal(nonce, nonce, plaintext, []byte(secretID)), nil
}

func (s *Sealer) Open(kekID, secretID string, sealed []byte) ([]byte, error) {
	aead, ok := s.aeads[kekID]
	if !ok {
		return nil, ErrUnknownKEK
	}
	if len(sealed) < aead.NonceSize()+aead.Overhead() {
		return nil, ErrOpen
	}
	n := aead.NonceSize()
	out, err := aead.Open(nil, sealed[:n], sealed[n:], []byte(secretID))
	if err != nil {
		return nil, ErrOpen
	}
	return out, nil
}
