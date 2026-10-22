package logic

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/service/risk"
)

func TestSelectionCandidates(t *testing.T) {
	adaptive := domain.ActionPolicy{Mode: domain.ModeAdaptive, Kinds: []string{domain.KindSlider, domain.KindRotate, domain.KindSpatial}}
	interactive := adaptive
	interactive.Mode = domain.ModeInteractive
	ladder := Selection{Levels: [][]string{{domain.KindInvisible}, {domain.KindSlider, domain.KindRotate}, {domain.KindSpatial}}}
	cases := []struct {
		name   string
		policy domain.ActionPolicy
		sel    Selection
		level  string
		rung   int
		want   []string
	}{
		{"none", adaptive, Selection{}, risk.LevelLow, 0, nil},
		{"fixed", adaptive, Selection{Kinds: []string{domain.KindRotate}}, risk.LevelLow, 0, []string{domain.KindRotate}},
		{"subset", adaptive, Selection{Kinds: []string{domain.KindRotate, domain.KindSlider, domain.KindRotate}}, risk.LevelLow, 0, []string{domain.KindRotate, domain.KindSlider}},
		{"all", adaptive, Selection{Kinds: []string{KindAll}}, risk.LevelMedium, 0, adaptive.Kinds},
		{"not offered or unknown", adaptive, Selection{Kinds: []string{domain.KindClickText, "future_kind"}}, risk.LevelLow, 0, nil},
		{"invisible at low risk", adaptive, Selection{Kinds: []string{domain.KindInvisible}}, risk.LevelLow, 0, []string{domain.KindInvisible}},
		{"invisible at medium risk", adaptive, Selection{Kinds: []string{domain.KindInvisible, domain.KindSlider}}, risk.LevelMedium, 0, []string{domain.KindSlider}},
		{"invisible in interactive mode", interactive, Selection{Kinds: []string{domain.KindInvisible}}, risk.LevelLow, 0, nil},
		{"ladder bottom", adaptive, ladder, risk.LevelLow, 0, []string{domain.KindInvisible}},
		{"ladder rung", adaptive, ladder, risk.LevelLow, 1, []string{domain.KindSlider, domain.KindRotate}},
		{"ladder clamps", adaptive, ladder, risk.LevelLow, 7, []string{domain.KindSpatial}},
		{"ladder medium starts higher", adaptive, ladder, risk.LevelMedium, 0, []string{domain.KindSlider, domain.KindRotate}},
		// The invisible tier is empty in interactive mode: the next one up serves.
		{"ladder skips empty tiers up", interactive, ladder, risk.LevelLow, 0, []string{domain.KindSlider, domain.KindRotate}},
		{"ladder skips empty tiers down", adaptive, Selection{Levels: [][]string{{domain.KindSlider}, {domain.KindClickIcon}}}, risk.LevelLow, 1, []string{domain.KindSlider}},
		{"ladder all filtered", adaptive, Selection{Levels: [][]string{{domain.KindClickIcon}}}, risk.LevelLow, 0, nil},
	}
	for _, c := range cases {
		if got := c.sel.candidates(c.policy, c.level, c.rung); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSelectionValidate(t *testing.T) {
	many := make([]string, MaxSelectKinds+1)
	bad := map[string]Selection{
		"both":          {Kinds: []string{domain.KindSlider}, Levels: [][]string{{domain.KindSlider}}},
		"too many":      {Kinds: many},
		"too many tier": {Levels: [][]string{many}},
		"too many rung": {Levels: make([][]string, MaxLevels+1)},
	}
	for name, s := range bad {
		if code(s.Validate()) != "INVALID_REQUEST" {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := (Selection{Kinds: []string{KindAll}}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) issueSel(t *testing.T, action string, s *risk.Signals, sel Selection) *IssueOutput {
	t.Helper()
	out, _, err := h.g.Issue(context.Background(), IssueInput{AppKey: appA, Action: action, Origin: originA, ClientIP: h.ip, Signals: s, Select: sel})
	if err != nil {
		t.Fatalf("issue %s %+v: %v", action, sel, err)
	}
	return out
}

// allowSelect turns on ClientSelect for actions of the harness apps.
func allowSelect(h *harness, actions ...string) {
	for _, app := range h.apps.apps {
		for _, a := range actions {
			p := app.Actions[a]
			p.ClientSelect = true
			app.Actions[a] = p
		}
	}
}

func TestIssueSelection(t *testing.T) {
	h := newHarness(t)
	withPuzzle(h)
	allowSelect(h, "mixed", "slide", "login")
	// Fixed: a clean browser on an adaptive action would get invisible.
	if c := h.issueSel(t, "mixed", clean(), Selection{Kinds: []string{domain.KindRotate}}); c.Kind != domain.KindRotate || c.Rotate == nil {
		t.Fatalf("fixed kind: %s", c.Kind)
	}
	seen := map[string]bool{}
	for i := 0; i < 40; i++ {
		seen[h.issueSel(t, "mixed", clean(), Selection{Kinds: []string{domain.KindSlider, domain.KindClickText}}).Kind] = true
	}
	if len(seen) != 2 || !seen[domain.KindSlider] || !seen[domain.KindClickText] {
		t.Fatalf("random subset: %v", seen)
	}
	// High risk keeps the action's hardest kind, whatever the page asks.
	headless := clean()
	headless.Webdriver, headless.SoftwareGL = true, true
	if c := h.issueSel(t, "mixed", headless, Selection{Kinds: []string{domain.KindSlider}}); c.Kind != domain.KindSpatial {
		t.Fatalf("high risk: %s", c.Kind)
	}
	// Kinds the action does not offer are dropped; the policy decides.
	if c := h.issueSel(t, "slide", clean(), Selection{Kinds: []string{domain.KindSpatial}}); c.Kind != domain.KindSlider {
		t.Fatalf("not offered: %s", c.Kind)
	}
	// Invisible mode never shows a picture.
	if c := h.issueSel(t, "login", clean(), Selection{Kinds: []string{domain.KindSlider}}); c.Kind != domain.KindInvisible {
		t.Fatalf("invisible mode: %s", c.Kind)
	}
	// Interactive mode cannot be talked down to invisible.
	if c := h.issueSel(t, "slide", clean(), Selection{Kinds: []string{domain.KindInvisible}}); c.Kind != domain.KindSlider {
		t.Fatalf("interactive asked invisible: %s", c.Kind)
	}
	_, _, err := h.g.Issue(context.Background(), IssueInput{AppKey: appA, Action: "mixed", Origin: originA, ClientIP: h.ip,
		Select: Selection{Kinds: []string{domain.KindSlider}, Levels: [][]string{{domain.KindSlider}}}})
	if code(err) != "INVALID_REQUEST" {
		t.Fatalf("kinds and levels together: %v", err)
	}
}

func TestLadder(t *testing.T) {
	h := newHarness(t)
	withPuzzle(h)
	allowSelect(h, "mixed", "slide")
	h.g.Settings.LadderDowngradePasses, h.g.Settings.LadderWindow = 3, 30*time.Minute
	sel := Selection{Levels: [][]string{{domain.KindSlider}, {domain.KindSpatial}}}
	human := &SliderAnswer{Clicks: [][3]float64{{1700, 62, 48}, {2600, 224, 83}, {3350, 137, 158}}, MovesBetween: []int{3, 12, 9}}

	c := h.issueSel(t, "mixed", clean(), sel)
	if c.Kind != domain.KindSlider {
		t.Fatalf("bottom rung: %s", c.Kind)
	}
	if _, err := h.solveWith(c, &SliderAnswer{X: answerX + 40, Trajectory: humanDrag(answerX + 40)}, clean()); code(err) != "ANSWER_INVALID" {
		t.Fatalf("wrong slider: %v", err)
	}
	// One failure: one rung up.
	for i := 0; i < 3; i++ {
		c := h.issueSel(t, "mixed", clean(), sel)
		if c.Kind != domain.KindSpatial {
			t.Fatalf("after failure, pass %d: %s", i, c.Kind)
		}
		if _, err := h.solveWith(c, human, clean()); err != nil {
			t.Fatalf("spatial: %v", err)
		}
	}
	// Three passes in a row: back down.
	if c := h.issueSel(t, "mixed", clean(), sel); c.Kind != domain.KindSlider {
		t.Fatalf("after passes: %s", c.Kind)
	}
	// The rung belongs to the app; another app starts at the bottom.
	if rung, _ := h.g.State.LadderLevel(context.Background(), 2, "192.0.2.0/24"); rung != 0 {
		t.Fatalf("other app rung: %d", rung)
	}
	// Without a ladder the solve leaves the rung alone.
	c = h.issueSel(t, "slide", clean(), Selection{})
	if _, err := h.solveWith(c, &SliderAnswer{X: answerX + 40, Trajectory: humanDrag(answerX + 40)}, clean()); code(err) != "ANSWER_INVALID" {
		t.Fatalf("wrong slider: %v", err)
	}
	if c := h.issueSel(t, "mixed", clean(), sel); c.Kind != domain.KindSlider {
		t.Fatalf("non-ladder failure moved the rung: %s", c.Kind)
	}
}

// Without the action's opt-in the page's kinds and levels are ignored, not
// refused: the server picks as if the page had sent nothing.
func TestSelectionNeedsOptIn(t *testing.T) {
	h := newHarness(t)
	withPuzzle(h)
	if c := h.issueSel(t, "mixed", clean(), Selection{Kinds: []string{domain.KindSlider}}); c.Kind != domain.KindInvisible {
		t.Fatalf("kinds without opt-in: %s", c.Kind)
	}
	if c := h.issueSel(t, "mixed", clean(), Selection{Levels: [][]string{{domain.KindSlider}}}); c.Kind != domain.KindInvisible {
		t.Fatalf("levels without opt-in: %s", c.Kind)
	}
	// Medium risk (no signals) draws from every kind again, not only the page's pick.
	seen := map[string]bool{}
	for i := 0; i < 60; i++ {
		seen[h.issueSel(t, "mixed", nil, Selection{Kinds: []string{domain.KindSlider}}).Kind] = true
	}
	if len(seen) < 3 {
		t.Fatalf("medium risk honoured the page's kinds: %v", seen)
	}
}
