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
	appKeyPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)
	actionPattern    = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)
	requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	secretIDPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)
)

func ValidAppKey(s string) bool    { return appKeyPattern.MatchString(s) }
func ValidAction(s string) bool    { return actionPattern.MatchString(s) }
func ValidRequestID(s string) bool { return requestIDPattern.MatchString(s) }
func ValidSecretID(s string) bool  { return secretIDPattern.MatchString(s) }

// Challenge modes (per action) and kinds (per issued challenge).
const (
	ModeInvisible   = "invisible"
	ModeInteractive = "interactive"
	ModeAdaptive    = "adaptive"
	// ModeSlider is the phase-one mode name, kept as an input alias for
	// interactive with only the slider (migration 0004 converts stored rows).
	ModeSlider = "slider"

	KindInvisible  = "invisible"
	KindSlider     = "slider"
	KindRotate     = "rotate"
	KindJigsaw     = "jigsaw"
	KindClickText  = "click_text"
	KindClickOrder = "click_order"
	KindClickIcon  = "click_icon"
	KindClickShape = "click_shape"
	KindSpatial    = "spatial"
	KindFallback   = "fallback"
)

func ValidMode(m string) bool { return m == ModeInvisible || m == ModeInteractive || m == ModeAdaptive }

// InteractiveKinds lists the kinds an action may offer, easiest first: the
// order is the hardness ranking adaptive mode uses at high risk.
var InteractiveKinds = []string{KindSlider, KindRotate, KindJigsaw, KindClickText, KindClickOrder, KindClickIcon, KindClickShape, KindSpatial}

// DragKind kinds are answered with a one-dimensional drag (end position plus
// trajectory); the jigsaw drag is two-dimensional and checked on its own.
func DragKind(k string) bool { return k == KindSlider || k == KindRotate }

// ClickKind kinds are answered with ordered clicks.
func ClickKind(k string) bool {
	return k == KindClickText || k == KindClickOrder || k == KindClickIcon || k == KindClickShape || k == KindSpatial
}

func ValidInteractiveKind(k string) bool {
	for _, v := range InteractiveKinds {
		if v == k {
			return true
		}
	}
	return false
}

// Hardest returns the hardest kind in kinds by the InteractiveKinds ranking.
func Hardest(kinds []string) string {
	best, rank := KindSlider, -1
	for _, k := range kinds {
		for i, v := range InteractiveKinds {
			if v == k && i > rank {
				best, rank = k, i
			}
		}
	}
	return best
}

// ActionPolicy is the per-action challenge configuration.
type ActionPolicy struct {
	Enabled bool
	Mode    string
	// Kinds are the interactive kinds this action may show; never empty.
	Kinds      []string
	Puzzles    int
	Difficulty uint64
}

type App struct {
	ID            int64
	AppKey        string
	Enabled       bool
	PolicyVersion int64
	// Origins are normalized with NormalizeOrigin.
	Origins []string
	Actions map[string]ActionPolicy
}

func (s *App) AllowsOrigin(origin string) bool {
	for _, o := range s.Origins {
		if o == origin {
			return true
		}
	}
	return false
}

// SecretPair is an app's server credential, like Yidun's secretId/secretKey.
// SecretKey is the decrypted HMAC key; it never leaves the gateway process.
type SecretPair struct {
	SecretID  string
	AppID     int64
	SecretKey []byte
	Enabled   bool
	ExpiresAt *time.Time
}

func (k *SecretPair) Usable(now time.Time) bool {
	return k.Enabled && len(k.SecretKey) > 0 && (k.ExpiresAt == nil || now.Before(*k.ExpiresAt))
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
