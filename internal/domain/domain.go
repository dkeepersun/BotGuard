// Package domain holds storage-independent types shared by logic and repositories.
package domain

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrDuplicate    = errors.New("already exists")
	ErrInvalidState = errors.New("invalid state for this operation")
)

var (
	siteKeyPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)
	actionPattern    = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)
	requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	keyIDPattern     = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)
)

func ValidSiteKey(s string) bool   { return siteKeyPattern.MatchString(s) }
func ValidAction(s string) bool    { return actionPattern.MatchString(s) }
func ValidRequestID(s string) bool { return requestIDPattern.MatchString(s) }
func ValidKeyID(s string) bool     { return keyIDPattern.MatchString(s) }

// ActionPolicy is the per-action PoW configuration.
type ActionPolicy struct {
	Enabled    bool
	Puzzles    int
	Difficulty uint64
}

type Site struct {
	ID            int64
	PublicKey     string
	Enabled       bool
	PolicyVersion int64
	// Origins are normalized with NormalizeOrigin.
	Origins []string
	Actions map[string]ActionPolicy
}

func (s *Site) AllowsOrigin(origin string) bool {
	for _, o := range s.Origins {
		if o == origin {
			return true
		}
	}
	return false
}

type APIKey struct {
	KeyID      string
	SiteID     int64
	SecretHash []byte // SHA-256 of the high-entropy secret
	Enabled    bool
	ExpiresAt  *time.Time
}

func (k *APIKey) Usable(now time.Time) bool {
	return k.Enabled && (k.ExpiresAt == nil || now.Before(*k.ExpiresAt))
}

// NormalizeOrigin returns scheme://host[:port] in lower case with default
// ports removed. Anything that is not a bare http(s) origin is rejected.
func NormalizeOrigin(raw string) (string, bool) {
	if raw == "" || raw == "null" || len(raw) > 255 {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", false
	}
	if u.Path != "" && u.Path != "/" {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", false
	}
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		return scheme + "://" + host + ":" + port, true
	}
	return scheme + "://" + host, true
}
