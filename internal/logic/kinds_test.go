package logic

import (
	"context"
	"encoding/base64"
	"math"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/service/click"
	"github.com/dkeepersun/BotGuard/internal/service/jigsaw"
	"github.com/dkeepersun/BotGuard/internal/service/pow"
	"github.com/dkeepersun/BotGuard/internal/service/risk"
	"github.com/dkeepersun/BotGuard/internal/service/rotate"
	"github.com/dkeepersun/BotGuard/internal/service/slider"
)

const answerX = 150

// rotateAngle turns into a drag of 150 px on the 280 px track (192.857°).
const rotateAngle = answerX * 360.0 / rotate.Track

func clean() *risk.Signals {
	return &risk.Signals{MsToClick: 2000, PointerMovesBeforeClick: 30, PointerType: "mouse"}
}

// humanDrag mimics a recorded mouse drag: ease-in-out, jittered frame timing,
// vertical wobble and a small overshoot correction.
func humanDrag(target float64) [][3]float64 {
	var pts [][3]float64
	t := 0.0
	for i := 0; i <= 40; i++ {
		p := float64(i) / 40
		pts = append(pts, [3]float64{t, p * p * (3 - 2*p) * (target + 4), math.Sin(p*3)*2 + float64(i%3)*0.4})
		t += 14 + float64(i%5)
	}
	return append(pts, [3]float64{t + 80, target + 1, 1.5}, [3]float64{t + 140, target, 1.2})
}

var clickTargets = []click.Target{{X: 60, Y: 50, R: 20}, {X: 220, Y: 80, R: 18}, {X: 140, Y: 160, R: 22}}

func withPuzzle(h *harness) {
	h.g.Puzzles = func() (slider.Puzzle, error) {
		return slider.Puzzle{Background: "data:image/jpeg;base64,AA==", Piece: "data:image/png;base64,AA==", X: answerX, Y: 40}, nil
	}
	h.g.Rotates = func() (rotate.Puzzle, error) {
		return rotate.Puzzle{Background: "data:image/jpeg;base64,AA==", Disc: "data:image/png;base64,AA==", CX: 160, CY: 100, Radius: 68, Angle: rotateAngle}, nil
	}
	h.g.Clicks = func(kind string) (click.Puzzle, error) {
		p := click.Puzzle{Image: "data:image/jpeg;base64,AA==", Prompt: "请依次点击：" + kind, Width: 320, Height: 200, Targets: clickTargets}
		if kind == domain.KindClickIcon {
			p.PromptImage = "data:image/png;base64,AA=="
		}
		return p, nil
	}
	h.g.Jigsaws = func() (jigsaw.Puzzle, error) {
		return jigsaw.Puzzle{Image: "data:image/jpeg;base64,AA==", A: 1, B: 6}, nil
	}
}

func (h *harness) issueFor(t *testing.T, action string, s *risk.Signals, fallback bool) *IssueOutput {
	t.Helper()
	out, _, err := h.g.Issue(context.Background(), IssueInput{AppKey: appA, Action: action, Origin: originA, ClientIP: h.ip, Signals: s, Fallback: fallback})
	if err != nil {
		t.Fatalf("issue %s: %v", action, err)
	}
	return out
}

func (h *harness) solveWith(c *IssueOutput, ans *SliderAnswer, s *risk.Signals) (*SolveOutput, error) {
	nonces, _ := pow.Solve(c.Algorithm, c.ChallengeID, c.Params)
	out, _, err := h.g.Solve(context.Background(), SolveInput{AppKey: appA, Challenge: c.Challenge, Nonces: nonces,
		Origin: originA, ClientIP: h.ip, Answer: ans, Signals: s})
	return out, err
}

func TestAdaptiveKinds(t *testing.T) {
	h := newHarness(t)
	withPuzzle(h)
	base := h.issueFor(t, "login", nil, false).Params.Target // invisible mode, factor 1

	if c := h.issueFor(t, "adaptive", clean(), false); c.Kind != domain.KindInvisible || c.Slider != nil {
		t.Fatalf("clean browser: %s", c.Kind)
	}
	if c := h.issueFor(t, "adaptive", nil, false); c.Kind != domain.KindSlider || c.Slider == nil || c.Slider.PieceY != 40 {
		t.Fatalf("no signals: %+v", c)
	}
	headless := clean()
	headless.Webdriver, headless.SoftwareGL = true, true
	c := h.issueFor(t, "adaptive", headless, false)
	if c.Kind != domain.KindSlider || c.Params.Target >= base {
		t.Fatalf("high risk must be slider with harder PoW: %s %s", c.Kind, c.Params.Target)
	}
	if c := h.issueFor(t, "slide", clean(), false); c.Kind != domain.KindSlider {
		t.Fatalf("slider mode: %s", c.Kind)
	}
	// Invisible mode ignores the fallback flag and never escalates.
	if c := h.issueFor(t, "login", headless, true); c.Kind != domain.KindInvisible {
		t.Fatalf("invisible mode: %s", c.Kind)
	}
	// The answer must not leak into anything the browser receives.
	sc := h.issueFor(t, "slide", clean(), false)
	body, err := base64.RawURLEncoding.DecodeString(strings.Split(sc.Challenge, ".")[2])
	if err != nil || strings.Contains(string(body), `"x"`) || !strings.Contains(string(body), `"sealed"`) {
		t.Fatalf("challenge payload exposes the answer: %s", body)
	}
}

func TestSliderSolve(t *testing.T) {
	h := newHarness(t)
	withPuzzle(h)

	c := h.issueFor(t, "slide", clean(), false)
	if _, err := h.solveWith(c, nil, clean()); code(err) != "INVALID_REQUEST" {
		t.Fatalf("missing answer: %v", err)
	}

	c = h.issueFor(t, "slide", clean(), false)
	if _, err := h.solveWith(c, &SliderAnswer{X: answerX + 20, Trajectory: humanDrag(answerX + 20)}, clean()); code(err) != "ANSWER_INVALID" {
		t.Fatalf("wrong position: %v", err)
	}

	// Right position, scripted straight constant-speed drag.
	var line [][3]float64
	for i := 0; i <= 30; i++ {
		line = append(line, [3]float64{float64(i) * 16, float64(answerX) * float64(i) / 30, 0})
	}
	c = h.issueFor(t, "slide", clean(), false)
	if _, err := h.solveWith(c, &SliderAnswer{X: answerX, Trajectory: line}, clean()); code(err) != "ANSWER_INVALID" {
		t.Fatalf("scripted drag: %v", err)
	}

	c = h.issueFor(t, "slide", clean(), false)
	out, err := h.solveWith(c, &SliderAnswer{X: answerX + 2, Trajectory: humanDrag(answerX + 2)}, clean())
	if err != nil {
		t.Fatalf("human drag: %v", err)
	}
	v, err := h.verify(out.Token, "slide", "r-slider")
	if err != nil || !v.Success || v.ChallengeKind != domain.KindSlider {
		t.Fatalf("verify: %+v %v", v, err)
	}
	// Two failed answers from this IP are reported to the business server.
	if v.RiskLevel != risk.LevelLow || !slices.Contains(v.RiskReasons, risk.ReasonIPRecentFailures) {
		t.Fatalf("risk: %s %v", v.RiskLevel, v.RiskReasons)
	}
}

func TestFailuresEscalate(t *testing.T) {
	h := newHarness(t)
	withPuzzle(h)
	h.ip = netip.MustParseAddr("198.51.100.77")
	for i := 0; i < 3; i++ {
		c := h.issueFor(t, "slide", clean(), false)
		_, _ = h.solveWith(c, &SliderAnswer{X: 10, Trajectory: humanDrag(10)}, clean())
	}
	if c := h.issueFor(t, "adaptive", clean(), false); c.Kind != domain.KindSlider {
		t.Fatalf("after failures a clean browser should still get a slider: %s", c.Kind)
	}
}

func TestFallbackPath(t *testing.T) {
	h := newHarness(t)
	withPuzzle(h)
	normal := h.issueFor(t, "slide", clean(), false)
	c := h.issueFor(t, "slide", clean(), true)
	if c.Kind != domain.KindFallback || c.Slider != nil || c.Params.Target >= normal.Params.Target {
		t.Fatalf("fallback: %+v", c)
	}
	out, err := h.solveWith(c, nil, clean())
	if err != nil {
		t.Fatalf("fallback solve: %v", err)
	}
	v, err := h.verify(out.Token, "slide", "r-fallback")
	if err != nil || v.ChallengeKind != domain.KindFallback || !slices.Contains(v.RiskReasons, risk.ReasonFallbackChosen) {
		t.Fatalf("verify fallback: %+v %v", v, err)
	}
	for i := 0; i < 4; i++ {
		h.issueFor(t, "slide", clean(), true)
	}
	if _, _, err := h.g.Issue(context.Background(), IssueInput{AppKey: appA, Action: "slide", Origin: originA, ClientIP: h.ip,
		Signals: clean(), Fallback: true}); code(err) != "RATE_LIMITED" {
		t.Fatalf("fallback limit: %v", err)
	}
}

func TestUntrustedEventsAddRisk(t *testing.T) {
	h := newHarness(t)
	c := h.issueFor(t, "adaptive", clean(), false)
	s := clean()
	s.UntrustedEvent = true
	out, err := h.solveWith(c, nil, s)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := h.verify(out.Token, "adaptive", "r-untrusted")
	if v.RiskLevel != risk.LevelMedium || !slices.Contains(v.RiskReasons, risk.ReasonUntrustedEvent) {
		t.Fatalf("%+v", v)
	}
}

func TestKeyboardSliderIsFlagged(t *testing.T) {
	h := newHarness(t)
	withPuzzle(h)
	c := h.issueFor(t, "slide", clean(), false)
	var keys [][3]float64
	for i := 0; i <= 75; i++ {
		keys = append(keys, [3]float64{float64(i) * 60, float64(min(i*2, answerX)), 0})
	}
	s := clean()
	s.PointerType = "keyboard"
	out, err := h.solveWith(c, &SliderAnswer{X: answerX, Trajectory: keys}, s)
	if err != nil {
		t.Fatalf("keyboard slider: %v", err)
	}
	v, _ := h.verify(out.Token, "slide", "r-keyboard")
	if !slices.Contains(v.RiskReasons, risk.ReasonKeyboardInput) {
		t.Fatalf("%+v", v)
	}
}

func TestChooseKind(t *testing.T) {
	g := &Gateway{Settings: Settings{HighDifficultyFactor: 4, FallbackDifficultyFactor: 8}}
	all := domain.ActionPolicy{Mode: domain.ModeInteractive, Kinds: domain.InteractiveKinds}
	seen := map[string]bool{}
	for i := 0; i < len(all.Kinds); i++ {
		kind, _ := g.chooseKind(all, risk.LevelLow, false, nil, func(int) int { return i })
		seen[kind] = true
	}
	if len(seen) != len(domain.InteractiveKinds) {
		t.Fatalf("interactive must be able to pick every kind: %v", seen)
	}
	adaptive := all
	adaptive.Mode = domain.ModeAdaptive
	if kind, f := g.chooseKind(adaptive, risk.LevelHigh, false, nil, func(int) int { return 0 }); kind != domain.KindSpatial || f != 4 {
		t.Fatalf("high risk: %s ×%d", kind, f)
	}
	if kind, _ := g.chooseKind(adaptive, risk.LevelLow, false, nil, nil); kind != domain.KindInvisible {
		t.Fatalf("low risk: %s", kind)
	}
	textOnly := domain.ActionPolicy{Mode: domain.ModeAdaptive, Kinds: []string{domain.KindSlider, domain.KindClickText}}
	if kind, _ := g.chooseKind(textOnly, risk.LevelHigh, false, nil, nil); kind != domain.KindClickText {
		t.Fatalf("hardest offered: %s", kind)
	}
	if kind, f := g.chooseKind(adaptive, risk.LevelMedium, true, nil, nil); kind != domain.KindFallback || f != 8 {
		t.Fatalf("fallback: %s ×%d", kind, f)
	}
	// Fallback must not be a discount for high risk: the factors compound.
	if kind, f := g.chooseKind(adaptive, risk.LevelHigh, true, nil, nil); kind != domain.KindFallback || f != 32 {
		t.Fatalf("high-risk fallback: %s ×%d", kind, f)
	}
}

func TestClickSolve(t *testing.T) {
	h := newHarness(t)
	withPuzzle(h)
	c := h.issueFor(t, "clicks", clean(), false)
	if c.Kind != domain.KindClickShape || c.Click == nil || c.Click.Targets != 3 || c.Slider != nil {
		t.Fatalf("issue: %+v", c)
	}
	body, _ := base64.RawURLEncoding.DecodeString(strings.Split(c.Challenge, ".")[2])
	if strings.Contains(string(body), "220") {
		t.Fatalf("target coordinates visible: %s", body)
	}
	if _, err := h.solveWith(c, nil, clean()); code(err) != "INVALID_REQUEST" {
		t.Fatalf("missing answer: %v", err)
	}

	human := &SliderAnswer{Clicks: [][3]float64{{1700, 62, 48}, {2600, 224, 83}, {3350, 137, 158}}, MovesBetween: []int{3, 12, 9}}
	wrongOrder := &SliderAnswer{Clicks: [][3]float64{{1700, 224, 83}, {2600, 62, 48}, {3350, 137, 158}}, MovesBetween: []int{3, 12, 9}}
	teleport := &SliderAnswer{Clicks: human.Clicks, MovesBetween: []int{0, 0, 0}}
	for name, ans := range map[string]*SliderAnswer{"wrong order": wrongOrder, "teleport": teleport} {
		c := h.issueFor(t, "clicks", clean(), false)
		if _, err := h.solveWith(c, ans, clean()); code(err) != "ANSWER_INVALID" {
			t.Errorf("%s: %v", name, err)
		}
	}
	c = h.issueFor(t, "clicks", clean(), false)
	out, err := h.solveWith(c, human, clean())
	if err != nil {
		t.Fatalf("human clicks: %v", err)
	}
	v, err := h.verify(out.Token, "clicks", "r-clicks")
	if err != nil || !v.Success || v.ChallengeKind != domain.KindClickShape {
		t.Fatalf("verify: %+v %v", v, err)
	}
	// Touch taps have no hover moves between them.
	touch := clean()
	touch.PointerType = "touch"
	c = h.issueFor(t, "clicks", touch, false)
	if _, err := h.solveWith(c, teleport, touch); err != nil {
		t.Fatalf("touch taps: %v", err)
	}
}

func TestRotateSolve(t *testing.T) {
	h := newHarness(t)
	withPuzzle(h)
	c := h.issueFor(t, "turn", clean(), false)
	if c.Kind != domain.KindRotate || c.Rotate == nil || c.Rotate.Track != rotate.Track || c.Slider != nil || c.Click != nil {
		t.Fatalf("issue: %+v", c)
	}
	body, _ := base64.RawURLEncoding.DecodeString(strings.Split(c.Challenge, ".")[2])
	if strings.Contains(string(body), "192") || strings.Contains(string(body), `"x"`) {
		t.Fatalf("answer visible: %s", body)
	}
	// 10° off is outside the 7° tolerance.
	off := answerX + 10.0/360*rotate.Track
	if _, err := h.solveWith(c, &SliderAnswer{X: off, Trajectory: humanDrag(off)}, clean()); code(err) != "ANSWER_INVALID" {
		t.Fatalf("10° off: %v", err)
	}
	c = h.issueFor(t, "turn", clean(), false)
	near := answerX + 4.0/360*rotate.Track
	out, err := h.solveWith(c, &SliderAnswer{X: near, Trajectory: humanDrag(near)}, clean())
	if err != nil {
		t.Fatalf("4° off: %v", err)
	}
	v, err := h.verify(out.Token, "turn", "r-rotate")
	if err != nil || !v.Success || v.ChallengeKind != domain.KindRotate {
		t.Fatalf("verify: %+v %v", v, err)
	}
	// Arrow keys work too, and are flagged like the keyboard slider.
	c = h.issueFor(t, "turn", clean(), false)
	var keys [][3]float64
	for i := 0; i <= 75; i++ {
		keys = append(keys, [3]float64{float64(i) * 60, float64(min(i*2, answerX)), 0})
	}
	s := clean()
	s.PointerType = "keyboard"
	out, err = h.solveWith(c, &SliderAnswer{X: answerX, Trajectory: keys}, s)
	if err != nil {
		t.Fatalf("keyboard: %v", err)
	}
	v, _ = h.verify(out.Token, "turn", "r-rotate-keys")
	if !slices.Contains(v.RiskReasons, risk.ReasonKeyboardInput) {
		t.Fatalf("%+v", v)
	}
}

// jigsawDrag drags from the middle of tile from to the middle of tile to,
// eased and wobbly, in image coordinates.
func jigsawDrag(from, to int) [][3]float64 {
	x0, y0 := float64(from%jigsaw.Cols*jigsaw.TileW+40), float64(from/jigsaw.Cols*jigsaw.TileH+50)
	x1, y1 := float64(to%jigsaw.Cols*jigsaw.TileW+40), float64(to/jigsaw.Cols*jigsaw.TileH+50)
	var out [][3]float64
	t := 0.0
	for i := 0; i <= 30; i++ {
		p := float64(i) / 30
		e := p * p * (3 - 2*p)
		out = append(out, [3]float64{t, x0 + (x1-x0)*e + math.Sin(p*5)*3, y0 + (y1-y0)*e + math.Cos(p*4)*2})
		t += 12 + float64(i%4)*3
	}
	return out
}

func TestPhaseThreeKinds(t *testing.T) {
	h := newHarness(t)
	withPuzzle(h)

	c := h.issueFor(t, "swap", clean(), false)
	if c.Kind != domain.KindJigsaw || c.Jigsaw == nil || c.Jigsaw.Rows != 2 || c.Jigsaw.Cols != 4 {
		t.Fatalf("jigsaw issue: %+v", c)
	}
	body, _ := base64.RawURLEncoding.DecodeString(strings.Split(c.Challenge, ".")[2])
	if strings.Contains(string(body), `"w"`) {
		t.Fatalf("swap visible: %s", body)
	}
	if _, err := h.solveWith(c, &SliderAnswer{Swap: []int{1, 5}, Trajectory: jigsawDrag(1, 5)}, clean()); code(err) != "ANSWER_INVALID" {
		t.Fatalf("wrong pair: %v", err)
	}
	if _, err := h.solveWith(h.issueFor(t, "swap", clean(), false), &SliderAnswer{Swap: []int{1}}, clean()); code(err) != "INVALID_REQUEST" {
		t.Fatalf("one tile: %v", err)
	}
	c = h.issueFor(t, "swap", clean(), false)
	out, err := h.solveWith(c, &SliderAnswer{Swap: []int{6, 1}, Trajectory: jigsawDrag(6, 1)}, clean())
	if err != nil {
		t.Fatalf("jigsaw solve: %v", err)
	}
	if v, _ := h.verify(out.Token, "swap", "r-swap"); !v.Success || v.ChallengeKind != domain.KindJigsaw {
		t.Fatalf("%+v", v)
	}

	human := &SliderAnswer{Clicks: [][3]float64{{1700, 62, 48}, {2600, 224, 83}, {3350, 137, 158}}, MovesBetween: []int{3, 12, 9}}
	for action, kind := range map[string]string{"icons": domain.KindClickIcon, "order": domain.KindClickOrder, "where": domain.KindSpatial} {
		c := h.issueFor(t, action, clean(), false)
		if c.Kind != kind || c.Click == nil || c.Click.Targets != 3 {
			t.Fatalf("%s issue: %+v", kind, c)
		}
		if (kind == domain.KindClickIcon) != (c.Click.PromptImage != "") {
			t.Fatalf("%s prompt image %q", kind, c.Click.PromptImage)
		}
		out, err := h.solveWith(c, human, clean())
		if err != nil {
			t.Fatalf("%s solve: %v", kind, err)
		}
		if v, _ := h.verify(out.Token, action, "r-"+action); !v.Success || v.ChallengeKind != kind {
			t.Fatalf("%s verify: %+v", kind, v)
		}
	}
}
