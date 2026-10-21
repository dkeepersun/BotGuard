// Package click defines click-in-order challenges and checks answers.
//
// A puzzle is an image plus a prompt naming 3–4 targets; the user clicks them
// in order. Research on multimodal LLM solvers (COGNITION, 2025) found
// ordered clicking and counting among the hardest task types, because models
// recognise objects well but localise and count poorly. That raises cost for
// general AI agents; it does not stop solvers trained for a specific style,
// so answers also have to look like a hand made them.
package click

import "math"

// Target is a circle in image pixels. The puzzle's answer is the ordered list
// of targets; it never reaches the browser except sealed in the challenge.
type Target struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	R float64 `json:"r"`
}

// Puzzle is one generated challenge.
type Puzzle struct {
	Image  string // data URL
	Prompt string
	// PromptImage, when set, shows the targets as pictures (icon kinds).
	PromptImage string
	Width       int
	Height      int
	Targets     []Target
}

const (
	// Slack added to every target radius (image pixels).
	Tolerance = 4.0
	// TouchTolerance is wider: a fingertip covers far more than a cursor,
	// and compact H5 layouts show the image at about 0.6×. Touch answers
	// carry TOUCH_INPUT, so this relaxed path stays visible.
	TouchTolerance = 9.0
	MaxClicks      = 8

	minFirstMs    = 400.0
	minGapMs      = 150.0
	maxTotalMs    = 60_000.0
	equalGapMs    = 15.0
	minMovesMouse = 2
)

// Rejection reasons, logged server-side only; the browser always sees
// ANSWER_INVALID.
const (
	ReasonCount     = "COUNT"
	ReasonPosition  = "POSITION"
	ReasonTooSoon   = "TOO_SOON"
	ReasonTooFast   = "TOO_FAST"
	ReasonTooSlow   = "TOO_SLOW"
	ReasonTimeOrder = "TIME_ORDER"
	ReasonEqualGaps = "EQUAL_GAPS"
	ReasonTeleport  = "TELEPORT"
)

// Check validates clicks ([ms since the panel appeared, x, y]) against the
// ordered targets. movesBetween[i] is the pointer moves seen before click i.
// input is the pointer type; touch has no hover moves, so it skips that rule.
func Check(targets []Target, clicks [][3]float64, movesBetween []int, input string) string {
	if len(targets) == 0 || len(clicks) != len(targets) {
		return ReasonCount
	}
	slack := Tolerance
	if input == "touch" {
		slack = TouchTolerance
	}
	for i, c := range clicks {
		t := targets[i]
		if math.Hypot(c[1]-t.X, c[2]-t.Y) > t.R+slack {
			return ReasonPosition
		}
	}
	if clicks[0][0] < minFirstMs {
		return ReasonTooSoon
	}
	var gaps []float64
	for i := 1; i < len(clicks); i++ {
		gap := clicks[i][0] - clicks[i-1][0]
		if gap < 0 {
			return ReasonTimeOrder
		}
		if gap < minGapMs {
			return ReasonTooFast
		}
		gaps = append(gaps, gap)
	}
	if clicks[len(clicks)-1][0] > maxTotalMs {
		return ReasonTooSlow
	}
	if len(gaps) >= 2 {
		lo, hi := gaps[0], gaps[0]
		for _, g := range gaps {
			lo, hi = math.Min(lo, g), math.Max(hi, g)
		}
		if hi-lo < equalGapMs {
			return ReasonEqualGaps
		}
	}
	if input == "mouse" || input == "pen" {
		if len(movesBetween) != len(clicks) {
			return ReasonTeleport
		}
		// The first click may follow a move made before the panel opened.
		for i := 1; i < len(movesBetween); i++ {
			if movesBetween[i] < minMovesMouse {
				return ReasonTeleport
			}
		}
	}
	return ""
}

// Overlaps reports whether a circle at (x, y, r) intersects any placed one
// with the given gap in between.
func Overlaps(placed []Target, x, y, r, gap float64) bool {
	for _, p := range placed {
		if math.Hypot(p.X-x, p.Y-y) < p.R+r+gap {
			return true
		}
	}
	return false
}
