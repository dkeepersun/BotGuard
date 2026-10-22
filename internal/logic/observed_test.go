package logic

import (
	"context"
	"net/netip"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/service/pow"
	"github.com/dkeepersun/BotGuard/internal/service/risk"
)

// solveFrom answers c from ip without advancing the clock.
func (h *harness) solveFrom(c *IssueOutput, ip netip.Addr, ans *SliderAnswer, s *risk.Signals) (*SolveOutput, error) {
	nonces, _ := pow.Solve(c.Algorithm, c.ChallengeID, c.Params)
	out, _, err := h.g.Solve(context.Background(), SolveInput{AppKey: appA, Challenge: c.Challenge, Nonces: nonces,
		Origin: originA, ClientIP: ip, Answer: ans, Signals: s})
	return out, err
}

func touch() *risk.Signals {
	s := clean()
	s.PointerType, s.PointerMovesBeforeClick = risk.PointerTouch, 0
	return s
}

// What the gateway sees between issue and solve is reported on verify.
func TestSolveObservations(t *testing.T) {
	h := newHarness(t)
	withPuzzle(h)
	n := 0
	reasons := func(out *SolveOutput, err error) []string {
		t.Helper()
		n++
		id := "r-observed-" + strconv.Itoa(n)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		v, err := h.verify(out.Token, "slide", id)
		if err != nil || !v.Success {
			t.Fatalf("%s verify: %+v %v", id, v, err)
		}
		return v.RiskReasons
	}
	drag := &SliderAnswer{X: answerX, Trajectory: humanDrag(answerX)}

	// Same network, a person's pace, the same pointer: nothing to report.
	c := h.issueFor(t, "slide", clean(), false)
	h.clock = h.clock.Add(3 * time.Second)
	if r := reasons(h.solveFrom(c, h.ip, drag, clean())); len(r) != 0 {
		t.Fatalf("human: %v", r)
	}
	// Answered from another /24 the instant it was issued.
	c = h.issueFor(t, "slide", clean(), false)
	r := reasons(h.solveFrom(c, netip.MustParseAddr("203.0.113.5"), drag, clean()))
	if !slices.Contains(r, risk.ReasonIPChanged) || !slices.Contains(r, risk.ReasonSolveTooFast) {
		t.Fatalf("relayed: %v", r)
	}
	// Another address in the same /24 is the same network.
	c = h.issueFor(t, "slide", clean(), false)
	h.clock = h.clock.Add(3 * time.Second)
	if r := reasons(h.solveFrom(c, netip.MustParseAddr("192.0.2.200"), drag, clean())); slices.Contains(r, risk.ReasonIPChanged) {
		t.Fatalf("same /24: %v", r)
	}

	// A touch tap then a touch drag is a phone.
	c = h.issueFor(t, "slide", touch(), false)
	h.clock = h.clock.Add(3 * time.Second)
	if r := reasons(h.solveFrom(c, h.ip, drag, touch())); !slices.Equal(r, []string{risk.ReasonTouchInput}) {
		t.Fatalf("phone: %v", r)
	}
	// Activated with a mouse, answered "by touch": the touch claim does not
	// relax anything. A perfectly level drag passes the touch rules, not the
	// mouse ones.
	level := humanDrag(answerX)
	for i := range level {
		level[i][2] = 0
	}
	c = h.issueFor(t, "slide", touch(), false)
	h.clock = h.clock.Add(3 * time.Second)
	if _, err := h.solveFrom(c, h.ip, &SliderAnswer{X: answerX, Trajectory: level}, touch()); err != nil {
		t.Fatalf("level touch drag: %v", err)
	}
	c = h.issueFor(t, "slide", clean(), false)
	h.clock = h.clock.Add(3 * time.Second)
	if _, err := h.solveFrom(c, h.ip, &SliderAnswer{X: answerX, Trajectory: level}, touch()); code(err) != "ANSWER_INVALID" {
		t.Fatalf("mouse then touch, level drag: %v", err)
	}
	c = h.issueFor(t, "slide", clean(), false)
	h.clock = h.clock.Add(3 * time.Second)
	r = reasons(h.solveFrom(c, h.ip, drag, touch()))
	if !slices.Contains(r, risk.ReasonPointerMismatch) || slices.Contains(r, risk.ReasonTouchInput) {
		t.Fatalf("mouse then touch: %v", r)
	}

	// Invisible challenges are PoW only: no reading time to check.
	inv := h.issueFor(t, "adaptive", clean(), false)
	if inv.Kind != domain.KindInvisible {
		t.Fatalf("adaptive clean: %s", inv.Kind)
	}
	out, err := h.solveFrom(inv, h.ip, nil, clean())
	if err != nil {
		t.Fatal(err)
	}
	v, _ := h.verify(out.Token, "adaptive", "r-invisible")
	if slices.Contains(v.RiskReasons, risk.ReasonSolveTooFast) {
		t.Fatalf("invisible: %v", v.RiskReasons)
	}
}

func TestFailureBlock(t *testing.T) {
	h := newHarness(t)
	withPuzzle(h)
	h.g.Settings.FailureBlockThreshold = 3
	for i := 0; i < 3; i++ {
		c := h.issueFor(t, "slide", clean(), false)
		if _, err := h.solveWith(c, &SliderAnswer{X: 10, Trajectory: humanDrag(10)}, clean()); code(err) != "ANSWER_INVALID" {
			t.Fatalf("wrong answer %d: %v", i, err)
		}
	}
	_, _, err := h.g.Issue(context.Background(), IssueInput{AppKey: appA, Action: "slide", Origin: originA, ClientIP: h.ip, Signals: clean()})
	if code(err) != "RATE_LIMITED" {
		t.Fatalf("after failures: %v", err)
	}
	// The failures age out with the window.
	h.redis.FastForward(h.g.Settings.FailureWindow + time.Second)
	h.issueFor(t, "slide", clean(), false)
}

// Rotating addresses within one network neither resets the issue limit nor
// hides its failures.
func TestNetworkPrefix(t *testing.T) {
	h := newHarness(t)
	withPuzzle(h)
	h.g.Settings.ChallengePerPrefixPerMinute = 30
	ip := func(i int) netip.Addr { return netip.AddrFrom4([4]byte{198, 51, 100, byte(i)}) }
	for i := 1; i <= 10; i++ {
		h.ip = ip(i)
		c := h.issueFor(t, "slide", clean(), false)
		if _, err := h.solveWith(c, &SliderAnswer{X: 10, Trajectory: humanDrag(10)}, clean()); code(err) != "ANSWER_INVALID" {
			t.Fatalf("wrong answer %d: %v", i, err)
		}
	}
	h.ip = ip(50)
	c := h.issueFor(t, "slide", clean(), false)
	out, err := h.solveWith(c, &SliderAnswer{X: answerX, Trajectory: humanDrag(answerX)}, clean())
	if err != nil {
		t.Fatal(err)
	}
	v, _ := h.verify(out.Token, "slide", "r-prefix")
	if !slices.Contains(v.RiskReasons, risk.ReasonPrefixFailures) || slices.Contains(v.RiskReasons, risk.ReasonIPRecentFailures) {
		t.Fatalf("fresh address in a failing /24: %v", v.RiskReasons)
	}
	// 11 challenges so far; the rest of the 30 from yet more addresses.
	for i := 12; i <= 30; i++ {
		h.ip = ip(100 + i)
		h.issueFor(t, "login", nil, false)
	}
	h.ip = ip(200)
	if _, _, err := h.g.Issue(context.Background(), IssueInput{AppKey: appA, Action: "login", Origin: originA, ClientIP: h.ip}); code(err) != "RATE_LIMITED" {
		t.Fatalf("31st challenge from the /24: %v", err)
	}
	// Another network is not affected.
	h.ip = netip.MustParseAddr("203.0.113.9")
	h.issueFor(t, "login", nil, false)
}
