// Package credential generates app keys and secret pairs.
package credential

import (
	"crypto/rand"
	"encoding/base64"
)

func random(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// NewAppKey returns a public, non-enumerable app key (Yidun's captchaId).
func NewAppKey() (string, error) {
	r, err := random(16)
	if err != nil {
		return "", err
	}
	return "app_" + r, nil
}

// SecretPair is a freshly generated server credential (Yidun's
// secretId/secretKey). The secret key is shown to the operator once and
// stored only encrypted.
type SecretPair struct {
	SecretID  string
	SecretKey string
}

func NewSecretPair() (SecretPair, error) {
	id, err := random(12)
	if err != nil {
		return SecretPair{}, err
	}
	key, err := random(32)
	if err != nil {
		return SecretPair{}, err
	}
	return SecretPair{SecretID: "sid_" + id, SecretKey: key}, nil
}
