// Package risk turns browser and server signals into an explainable score.
//
// Every rule has a fixed weight and a reason code; there is no model to train.
// Browser signals can be forged by a determined attacker: their job is to make
// cheap automation visible and expensive automation necessary. Server facts
// (failure history, request rate, header consistency) cannot be forged.
package risk

import (
	"sort"
	"strings"
)

// Signals is what the widget reports. Unknown fields are ignored on decode;
// a missing struct means the client sent no signals at all.
type Signals struct {
	// Environment, collected once when the widget loads.
	Webdriver          bool   `json:"webdriver"`
	HeadlessUA         bool   `json:"headless_ua"`
	SoftwareGL         bool   `json:"software_gl"`
	ZeroOuter          bool   `json:"zero_outer"`
	DesktopNoPlugins   bool   `json:"desktop_no_plugins"`
	NoLanguages        bool   `json:"no_languages"`
	UAPlatformMismatch bool   `json:"ua_platform_mismatch"`
	PermissionMismatch bool   `json:"permission_mismatch"`
	AutomationGlobals  bool   `json:"automation_globals"`
	UserAgent          string `json:"user_agent"`

	// Interaction, up to the click (issue) or the end of the drag (solve).
	MsToClick               int    `json:"ms_to_click"`
	PointerMovesBeforeClick int    `json:"pointer_moves_before_click"`
	UntrustedEvent          bool   `json:"untrusted_event"`
	PointerType             string `json:"pointer_type"`
}

// Facts are observed by the gateway itself.
type Facts struct {
	RecentFailures int
	NearRateLimit  bool
	HeaderUA       string
	Fallback       bool
}

// Reason codes. They go to the business server via verify, never to the browser.
const (
	ReasonWebdriver          = "WEBDRIVER"
	ReasonHeadlessUA         = "HEADLESS_UA"
	ReasonAutomationGlobals  = "AUTOMATION_GLOBALS"
	ReasonUntrustedEvent     = "UNTRUSTED_EVENT"
	ReasonSoftwareGL         = "SOFTWARE_GL"
	ReasonZeroOuter          = "ZERO_OUTER"
	ReasonPermissionMismatch = "PERMISSION_MISMATCH"
	ReasonUAPlatformMismatch = "UA_PLATFORM_MISMATCH"
	ReasonNoLanguages        = "NO_LANGUAGES"
	ReasonDesktopNoPlugins   = "DESKTOP_NO_PLUGINS"
	ReasonClickWithoutMove   = "CLICK_WITHOUT_MOVE"
	ReasonFastClick          = "FAST_CLICK"
	ReasonNoSignals          = "NO_SIGNALS"
	ReasonUAHeaderMismatch   = "UA_HEADER_MISMATCH"
	ReasonIPRecentFailures   = "IP_RECENT_FAILURES"
	ReasonIPHighRate         = "IP_HIGH_RATE"
	ReasonFallbackChosen     = "FALLBACK_CHOSEN"
	// The slider was completed with arrow keys. That path skips the drag
	// heuristics for accessibility, so a script can claim it; flag it.
	ReasonKeyboardInput = "KEYBOARD_INPUT"
	// The answer came from touch, which skips the mouse-only heuristics
	// (straight drags, clicks without moves). Informational, like fallback:
	// every relaxed path stays visible to the business server.
	ReasonTouchInput = "TOUCH_INPUT"
	// The action called for an interactive challenge but the gateway had no
	// image capacity for the app (its quota, or every generator busy), so the
	// client got harder PoW alone. Informational: the business server may
	// require another check.
	ReasonCapacityDegraded = "CAPACITY_DEGRADED"
)

// Pointer types the widget reports. Anything else is treated as a mouse, the
// strictest path, so an invented value never relaxes a check.
const (
	PointerMouse    = "mouse"
	PointerTouch    = "touch"
	PointerPen      = "pen"
	PointerKeyboard = "keyboard"
)

// NormalizePointer maps a client-reported pointer type onto the known set.
func NormalizePointer(pt string) string {
	switch pt {
	case PointerTouch, PointerPen, PointerKeyboard:
		return pt
	default:
		return PointerMouse
	}
}

// Weights are the whole policy; tests pin them so changes are deliberate.
var Weights = map[string]int{
	ReasonWebdriver:          60,
	ReasonHeadlessUA:         60,
	ReasonAutomationGlobals:  50,
	ReasonUntrustedEvent:     50,
	ReasonNoSignals:          40,
	ReasonUAHeaderMismatch:   30,
	ReasonSoftwareGL:         25,
	ReasonZeroOuter:          25,
	ReasonClickWithoutMove:   20,
	ReasonPermissionMismatch: 20,
	ReasonUAPlatformMismatch: 20,
	ReasonNoLanguages:        15,
	ReasonFastClick:          15,
	ReasonIPHighRate:         15,
	ReasonDesktopNoPlugins:   10,
	ReasonKeyboardInput:      15,
	ReasonFallbackChosen:     0, // informational: the business server decides
	ReasonTouchInput:         0, // informational
	ReasonCapacityDegraded:   0, // informational
}

const (
	failureWeight = 10
	failureCap    = 30
	fastClickMs   = 300
)

type Assessment struct {
	Score   int
	Reasons []string
}

// Add merges another assessment, capping at 100 and de-duplicating reasons.
func (a Assessment) Add(b Assessment) Assessment {
	seen := map[string]bool{}
	out := Assessment{Score: min(100, a.Score+b.Score)}
	for _, r := range append(append([]string{}, a.Reasons...), b.Reasons...) {
		if !seen[r] {
			seen[r] = true
			out.Reasons = append(out.Reasons, r)
		}
	}
	return out
}

// Assess scores one request. s is nil when the client sent no signals.
func Assess(s *Signals, f Facts) Assessment {
	var hits []string
	extra := 0
	hit := func(cond bool, reason string) {
		if cond {
			hits = append(hits, reason)
		}
	}
	if s == nil {
		hit(true, ReasonNoSignals)
	} else {
		hit(s.Webdriver, ReasonWebdriver)
		hit(s.HeadlessUA, ReasonHeadlessUA)
		hit(s.AutomationGlobals, ReasonAutomationGlobals)
		hit(s.UntrustedEvent, ReasonUntrustedEvent)
		hit(s.SoftwareGL, ReasonSoftwareGL)
		hit(s.ZeroOuter, ReasonZeroOuter)
		hit(s.PermissionMismatch, ReasonPermissionMismatch)
		hit(s.UAPlatformMismatch, ReasonUAPlatformMismatch)
		hit(s.NoLanguages, ReasonNoLanguages)
		hit(s.DesktopNoPlugins, ReasonDesktopNoPlugins)
		// Touch taps and keyboard activation legitimately have no prior moves.
		hit(NormalizePointer(s.PointerType) == PointerMouse && s.PointerMovesBeforeClick == 0, ReasonClickWithoutMove)
		hit(s.MsToClick > 0 && s.MsToClick < fastClickMs, ReasonFastClick)
		// Both sides go through CleanUA: the header copy already has.
		ua := CleanUA(s.UserAgent)
		hit(ua != "" && f.HeaderUA != "" && ua != CleanUA(f.HeaderUA), ReasonUAHeaderMismatch)
	}
	if f.RecentFailures > 0 {
		hits = append(hits, ReasonIPRecentFailures)
		extra += min(failureCap, f.RecentFailures*failureWeight)
	}
	hit(f.NearRateLimit, ReasonIPHighRate)
	hit(f.Fallback, ReasonFallbackChosen)

	score := extra
	for _, r := range hits {
		score += Weights[r]
	}
	sort.Strings(hits)
	return Assessment{Score: min(100, score), Reasons: hits}
}

const (
	LevelLow    = "low"
	LevelMedium = "medium"
	LevelHigh   = "high"
)

func Level(score, medium, high int) string {
	switch {
	case score >= high:
		return LevelHigh
	case score >= medium:
		return LevelMedium
	default:
		return LevelLow
	}
}

// CleanUA bounds a client-reported user agent before comparison.
func CleanUA(ua string) string {
	if len(ua) > 512 {
		ua = ua[:512]
	}
	return strings.TrimSpace(ua)
}
