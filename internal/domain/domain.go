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
	// Origins are patterns normalized with NormalizeOriginPattern.
	Origins []string
	Actions map[string]ActionPolicy
}

// AllowsOrigin reports whether a normalized request origin matches one of
// the app's origin patterns (see NormalizeOriginPattern).
func (s *App) AllowsOrigin(origin string) bool {
	for _, p := range s.Origins {
		if OriginMatches(p, origin) {
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

// appID is an app's package name or bundle id: dot, dash or underscore
// separated lower-case words, e.g. com.example.shop.
var appID = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*$`)

// NormalizeOrigin returns scheme://host[:port] in lower case with default
// ports removed. Anything that is not a bare http(s) origin is rejected,
// except app://<package name>: the origin a native app (or the logic layer
// of a uni-app App) claims, since its WebView has no usable origin of its
// own. Browsers never send such an Origin, so web pages cannot claim it.
func NormalizeOrigin(raw string) (string, bool) {
	if raw == "" || raw == "null" || len(raw) > 255 {
		return "", false
	}
	if id, ok := strings.CutPrefix(strings.ToLower(raw), "app://"); ok {
		id = strings.TrimSuffix(id, "/")
		if !appID.MatchString(id) {
			return "", false
		}
		return "app://" + id, true
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

// AnyOrigin is the pattern allowing every origin, including app:// ones.
const AnyOrigin = "*"

// wildcardHost marks a subdomain wildcard: scheme://*.example.com[:port].
const wildcardHost = "://*."

// NormalizeOriginPattern accepts an exact origin (see NormalizeOrigin), the
// subdomain wildcard scheme://*.domain[:port], or AnyOrigin. The wildcard
// matches any depth of subdomain but not the domain itself, with the same
// scheme and port, like a wildcard TLS certificate covering more levels. The
// domain must have at least two labels, so *.com is refused.
func NormalizeOriginPattern(raw string) (string, bool) {
	if raw == AnyOrigin {
		return AnyOrigin, true
	}
	if !strings.Contains(raw, "*") {
		return NormalizeOrigin(raw)
	}
	scheme, rest, ok := strings.Cut(strings.ToLower(raw), wildcardHost)
	if !ok || strings.Contains(rest, "*") {
		return "", false
	}
	// Normalize with a stand-in label, then put the wildcard back.
	const stand = "wildcard."
	n, ok := NormalizeOrigin(scheme + "://" + stand + rest)
	if !ok || strings.HasPrefix(n, "app://") || !strings.HasPrefix(n, scheme+"://"+stand) {
		return "", false
	}
	domain := n[len(scheme+"://"+stand):]
	host, _, _ := strings.Cut(domain, ":")
	if !strings.Contains(host, ".") || strings.HasPrefix(host, "[") || strings.HasPrefix(host, ".") {
		return "", false
	}
	return scheme + wildcardHost + domain, true
}

// OriginMatches reports whether a normalized origin matches a normalized
// pattern. verifyScript in the repository package mirrors this in Lua; keep
// them in step.
func OriginMatches(pattern, origin string) bool {
	if pattern == AnyOrigin {
		return true
	}
	i := strings.Index(pattern, wildcardHost)
	if i < 0 {
		return pattern == origin
	}
	prefix, suffix := pattern[:i+len("://")], pattern[i+len(wildcardHost)-1:]
	return len(origin) > len(prefix)+len(suffix) && strings.HasPrefix(origin, prefix) && strings.HasSuffix(origin, suffix)
}
