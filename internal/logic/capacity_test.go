package logic

import (
	"context"
	"net/netip"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/service/pow"
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

// One network cannot spend the app's image quota for everyone: past its own
// share it gets 429, not the cheaper degraded path.
func TestInteractiveQuotaPerPrefix(t *testing.T) {
	h := newHarness(t)
	withPuzzle(h)
	h.g.Settings.InteractivePerPrefixPerMinute = 2
	issue := func(ip string) (*IssueOutput, error) {
		out, _, err := h.g.Issue(context.Background(), IssueInput{AppKey: appA, Action: "slide", Origin: originA,
			ClientIP: netip.MustParseAddr(ip), Signals: clean()})
		return out, err
	}
	for _, ip := range []string{"192.0.2.10", "192.0.2.200"} {
		if c, err := issue(ip); err != nil || c.Kind != domain.KindSlider {
			t.Fatalf("%s: %+v %v", ip, c, err)
		}
	}
	if _, err := issue("192.0.2.33"); code(err) != "RATE_LIMITED" {
		t.Fatalf("same /24 over its share: %v", err)
	}
	if _, err := issue("2001:db8:7::1"); err != nil {
		t.Fatal(err)
	}
	if c, err := issue("198.51.100.1"); err != nil || c.Kind != domain.KindSlider {
		t.Fatalf("other network: %+v %v", c, err)
	}
	// Invisible challenges are not image challenges.
	if _, _, err := h.g.Issue(context.Background(), IssueInput{AppKey: appA, Action: "login", Origin: originA,
		ClientIP: netip.MustParseAddr("192.0.2.34"), Signals: clean()}); err != nil {
		t.Fatalf("invisible action: %v", err)
	}
}

// The degraded path has an absolute PoW floor, not only a multiple of the
// action's (here tiny) difficulty.
func TestDegradedDifficultyFloor(t *testing.T) {
	h := newHarness(t)
	withPuzzle(h)
	h.g.Settings.InteractivePerAppPerMinute = 1
	h.g.Settings.DegradedDifficultyFactor = 8
	h.g.Settings.DegradedMinDifficulty = 5000
	h.issueFor(t, "slide", clean(), false)
	c := h.issueFor(t, "slide", clean(), false)
	want, _ := pow.NewParams(2, 5000) // "slide" is 2 puzzles at difficulty 16: 16×8 = 128 < 5000
	if c.Kind != domain.KindInvisible || c.Params != want {
		t.Fatalf("degraded params %+v, want %+v", c.Params, want)
	}
	h.g.Settings.DegradedMinDifficulty = 1 << 30 // MaxDifficulty (1<<20) still caps it
	c = h.issueFor(t, "slide", clean(), false)
	if want, _ := pow.NewParams(2, h.g.Settings.MaxDifficulty); c.Params != want {
		t.Fatalf("capped params %+v, want %+v", c.Params, want)
	}
}

// The pool holds pictures from the default background source, so an app
// with its own background setting never takes from it: with every slot busy
// it degrades instead.
func TestPuzzlePoolOnlyServesDefaultBackgrounds(t *testing.T) {
	h := newHarness(t)
	withPuzzle(h)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.g.StartPuzzlePool(ctx, 3, 1)
	deadline := time.Now().Add(5 * time.Second)
	for h.g.pool.short() != "" {
		if time.Now().After(deadline) {
			t.Fatal("pool never filled")
		}
		time.Sleep(5 * time.Millisecond)
	}
	slots := h.g.slots()
	for range cap(slots) {
		slots <- struct{}{}
	}
	defer func() {
		for range cap(slots) {
			<-slots
		}
	}()
	h.apps.apps[appA].Background = domain.Background{Mode: domain.BackgroundProcedural}
	if c := h.issueFor(t, "slide", clean(), false); c.Kind != domain.KindInvisible {
		t.Fatalf("own background served from the pool: %+v", c.Kind)
	}
	h.apps.apps[appA].Background = domain.Background{}
	if c := h.issueFor(t, "slide", clean(), false); c.Kind != domain.KindSlider {
		t.Fatalf("default background not served from the pool: %+v", c.Kind)
	}
}
