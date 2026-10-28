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
	Webdriver          bool `json:"webdriver"`
	HeadlessUA         bool `json:"headless_ua"`
	SoftwareGL         bool `json:"software_gl"`
	ZeroOuter          bool `json:"zero_outer"`
	DesktopNoPlugins   bool `json:"desktop_no_plugins"`
	NoLanguages        bool `json:"no_languages"`
	UAPlatformMismatch bool `json:"ua_platform_mismatch"`
	PermissionMismatch bool `json:"permission_mismatch"`
	AutomationGlobals  bool `json:"automation_globals"`
	// CDP: the page's console is being read over the DevTools protocol, as
	// Puppeteer, Playwright and an open DevTools window do.
	CDP bool `json:"cdp"`
	// NativeTampered: a native function or property the widget checks has
	// been replaced or redefined (navigator.webdriver spoofing and the like).
	NativeTampered bool   `json:"native_tampered"`
	UserAgent      string `json:"user_agent"`

	// Interaction, up to the click (issue) or the end of the drag (solve).
	MsToClick               int    `json:"ms_to_click"`
	PointerMovesBeforeClick int    `json:"pointer_moves_before_click"`
	UntrustedEvent          bool   `json:"untrusted_event"`
	PointerType             string `json:"pointer_type"`
	// Activation is "auto" when the widget started without a click (inline
	// mode, the auto attribute): the click rules have nothing to judge.
	Activation string `json:"activation"`
}

// AutoStart reports a widget that started without a click. The gateway then
// never lets the attempt pass invisibly, so claiming it gains a bot nothing.
func (s *Signals) AutoStart() bool { return s != nil && s.Activation == ActivationAuto }

// ActivationAuto is Signals.Activation for a start without a click.
const ActivationAuto = "auto"

// Facts are observed by the gateway itself.
type Facts struct {
	RecentFailures int
	// PrefixFailures counts failed solves from the client's whole network
	// prefix (IPv4 /24, IPv6 /48), which rotating addresses does not reset.
	PrefixFailures int
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
	// Signals came without any interaction telemetry: the widget always
	// reports how long the page was open before activation.
	ReasonNoInteraction = "NO_INTERACTION"
	// A Windows or desktop Linux user agent claimed a touch or pen pointer,
	// which relaxes the mouse-only checks.
	ReasonDesktopTouch = "DESKTOP_TOUCH"
	// Many failed solves from the client's network prefix.
	ReasonPrefixFailures = "PREFIX_RECENT_FAILURES"
	// The rest are observed by the gateway between issue and solve.
	//
	// The solve came from another network prefix than the issue: a relay to a
	// solving farm, or a phone that switched networks.
	ReasonIPChanged = "IP_CHANGED"
	// An image challenge came back sooner after issue than a person can look
	// at it and answer.
	ReasonSolveTooFast = "SOLVE_TOO_FAST"
	// The answer claimed touch or pen although the challenge was requested
	// with another pointer; the mouse rules applied.
	ReasonPointerMismatch = "POINTER_MISMATCH"
	ReasonCDP             = "CDP"
	ReasonNativeTampered  = "NATIVE_TAMPERED"
	// ReasonTrajectorySmooth: a long drag whose speed never wavers the way a
	// hand's does, the mark of a generated easing curve.
	ReasonTrajectorySmooth = "TRAJECTORY_SMOOTH"
	// ReasonAppUnderAttack: the app's failure rate spiked, so every risk
	// level is raised one step for a while (informational, no weight).
	ReasonAppUnderAttack = "APP_UNDER_ATTACK"
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
	ReasonNoInteraction:      20,
	ReasonDesktopTouch:       10,
	ReasonPrefixFailures:     15,
	ReasonIPChanged:          15,
	ReasonSolveTooFast:       25,
	ReasonPointerMismatch:    15,
	// Also an open DevTools window: a puzzle for the developer, not a block.
	ReasonCDP:              25,
	ReasonNativeTampered:   40,
	ReasonTrajectorySmooth: 15,
	ReasonAppUnderAttack:   0, // informational: the level is raised instead
	ReasonFallbackChosen:   0, // informational: the business server decides
	ReasonTouchInput:       0, // informational
	ReasonCapacityDegraded: 0, // informational
}

const (
	failureWeight = 10
	failureCap    = 40
	fastClickMs   = 300
	// prefixFailures failed solves from one network prefix within the
	// failure window add ReasonPrefixFailures.
	prefixFailures = 10
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
		hit(s.CDP, ReasonCDP)
		hit(s.NativeTampered, ReasonNativeTampered)
		hit(s.UntrustedEvent, ReasonUntrustedEvent)
		hit(s.SoftwareGL, ReasonSoftwareGL)
		hit(s.ZeroOuter, ReasonZeroOuter)
		hit(s.PermissionMismatch, ReasonPermissionMismatch)
		hit(s.UAPlatformMismatch, ReasonUAPlatformMismatch)
		hit(s.NoLanguages, ReasonNoLanguages)
		hit(s.DesktopNoPlugins, ReasonDesktopNoPlugins)
		// Touch taps and keyboard activation legitimately have no prior
		// moves; an automatic start has no click at all.
		click := !s.AutoStart()
		hit(click && NormalizePointer(s.PointerType) == PointerMouse && s.PointerMovesBeforeClick == 0, ReasonClickWithoutMove)
		hit(click && s.MsToClick > 0 && s.MsToClick < fastClickMs, ReasonFastClick)
		hit(s.MsToClick <= 0, ReasonNoInteraction)
		pointer := NormalizePointer(s.PointerType)
		hit((pointer == PointerTouch || pointer == PointerPen) && DesktopUA(f.HeaderUA), ReasonDesktopTouch)
		// Both sides go through CleanUA: the header copy already has.
		ua := CleanUA(s.UserAgent)
		hit(ua != "" && f.HeaderUA != "" && ua != CleanUA(f.HeaderUA), ReasonUAHeaderMismatch)
	}
	if f.RecentFailures > 0 {
		hits = append(hits, ReasonIPRecentFailures)
		extra += min(failureCap, f.RecentFailures*failureWeight)
	}
	hit(f.PrefixFailures >= prefixFailures, ReasonPrefixFailures)
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

// Raise is the level one step above level (high stays high).
func Raise(level string) string {
	if level == LevelLow {
		return LevelMedium
	}
	return LevelHigh
}

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

// DesktopUA reports a Windows or desktop Linux user agent. Macs are left out:
// iPadOS Safari presents itself as a Mac, and ChromeOS devices often have
// touch screens.
func DesktopUA(ua string) bool {
	if strings.Contains(ua, "Mobile") || strings.Contains(ua, "Android") || strings.Contains(ua, "CrOS") {
		return false
	}
	return strings.Contains(ua, "Windows NT") || strings.Contains(ua, "X11; Linux") || strings.Contains(ua, "X11; Ubuntu")
}

// CleanUA bounds a client-reported user agent before comparison.
func CleanUA(ua string) string {
	if len(ua) > 512 {
		ua = ua[:512]
	}
	return strings.TrimSpace(ua)
}
