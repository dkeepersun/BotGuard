package logic

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/service/risk"
	"github.com/dkeepersun/BotGuard/internal/service/slider"
)

// An app over its interactive quota gets harder PoW flagged for the business
// server, not an error; other apps keep their image challenges.
func TestInteractiveQuotaDegradesPerApp(t *testing.T) {
	h := newHarness(t)
	withPuzzle(h)
	h.g.Settings.InteractivePerAppPerMinute = 2
	h.g.Settings.DegradedDifficultyFactor = 8
	normal := h.issueFor(t, "slide", clean(), false)
	h.issueFor(t, "slide", clean(), false)
	c := h.issueFor(t, "slide", clean(), false)
	if c.Kind != domain.KindInvisible || c.Slider != nil || c.Params.Target >= normal.Params.Target {
		t.Fatalf("over quota: %+v", c)
	}
	out, err := h.solveWith(c, nil, clean())
	if err != nil {
		t.Fatalf("degraded solve: %v", err)
	}
	v, err := h.verify(out.Token, "slide", "r-degraded")
	if err != nil || !v.Success || v.ChallengeKind != domain.KindInvisible || !slices.Contains(v.RiskReasons, risk.ReasonCapacityDegraded) {
		t.Fatalf("verify degraded: %+v %v", v, err)
	}
	other, _, err := h.g.Issue(context.Background(), IssueInput{AppKey: appB, Action: "slide", Origin: "https://b.example",
		ClientIP: h.ip, Signals: clean()})
	if err != nil || other.Kind != domain.KindSlider {
		t.Fatalf("other app: %+v %v", other, err)
	}
	// Invisible actions never count against the quota.
	if c := h.issueFor(t, "login", clean(), false); c.Kind != domain.KindInvisible {
		t.Fatalf("login: %+v", c)
	}
}

// With every generation slot busy and no pool, an image challenge degrades
// instead of answering 503.
func TestSaturationDegrades(t *testing.T) {
	h := newHarness(t)
	withPuzzle(h)
	slots := h.g.slots()
	for range cap(slots) {
		slots <- struct{}{}
	}
	c := h.issueFor(t, "slide", clean(), false)
	if c.Kind != domain.KindInvisible || c.Slider != nil {
		t.Fatalf("saturated: %+v", c)
	}
	for range cap(slots) {
		<-slots
	}
	if c := h.issueFor(t, "slide", clean(), false); c.Kind != domain.KindSlider {
		t.Fatalf("after saturation: %+v", c)
	}
}

// Pooled puzzles are served while every slot is busy, each exactly once,
// and the pool refills as it is drained.
func TestPuzzlePoolServesBursts(t *testing.T) {
	h := newHarness(t)
	withPuzzle(h)
	var made atomic.Int64
	h.g.Puzzles = func() (slider.Puzzle, error) {
		n := made.Add(1)
		return slider.Puzzle{Background: "data:image/jpeg;base64,AA==", Piece: "data:image/png;base64,AA==", X: answerX, Y: int(n)}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.g.StartPuzzlePool(ctx, 3, 1)
	waitFull := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for h.g.pool.short() != "" {
			if time.Now().After(deadline) {
				t.Fatal("pool never filled")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitFull()

	slots := h.g.slots()
	for range cap(slots) {
		slots <- struct{}{}
	}
	seen := map[int]bool{}
	for range 3 {
		c := h.issueFor(t, "slide", clean(), false)
		if c.Kind != domain.KindSlider || seen[c.Slider.PieceY] {
			t.Fatalf("pooled: %+v", c)
		}
		seen[c.Slider.PieceY] = true
	}
	if c := h.issueFor(t, "slide", clean(), false); c.Kind != domain.KindInvisible {
		t.Fatalf("drained pool with slots busy: %+v", c)
	}
	for range cap(slots) {
		<-slots
	}
	waitFull()
	if c := h.issueFor(t, "slide", clean(), false); c.Kind != domain.KindSlider || seen[c.Slider.PieceY] {
		t.Fatalf("refilled: %+v", c)
	}
}
