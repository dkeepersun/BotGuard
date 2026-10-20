// Package credential generates site keys and server API keys.
package credential

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

func random(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// NewSiteKey returns a public, non-enumerable site key.
func NewSiteKey() (string, error) {
	r, err := random(16)
	if err != nil {
		return "", err
	}
	return "pk_" + r, nil
}

// APIKey is a freshly generated server credential. Secret is shown to the
// operator once and only its hash is stored.
type APIKey struct {
	KeyID      string
	Secret     string
	SecretHash []byte
}

// Header is the Authorization value the business server sends to /v1/verify.
func (k APIKey) Header() string { return "Bearer " + k.KeyID + "." + k.Secret }

func NewAPIKey() (APIKey, error) {
	id, err := random(9)
	if err != nil {
		return APIKey{}, err
	}
	secret, err := random(32)
	if err != nil {
		return APIKey{}, err
	}
	sum := sha256.Sum256([]byte(secret))
	return APIKey{KeyID: "sk_" + id, Secret: secret, SecretHash: sum[:]}, nil
}
