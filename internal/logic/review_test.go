package logic

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/service/pow"
	"github.com/dkeepersun/BotGuard/internal/service/risk"
)

// An invented pointer type must never unlock the touch or keyboard paths.
func TestUnknownPointerTypeGetsMouseRules(t *testing.T) {
	h := newHarness(t)
	withPuzzle(h)
	teleport := &SliderAnswer{Clicks: [][3]float64{{1700, 62, 48}, {2600, 224, 83}, {3350, 137, 158}}, MovesBetween: []int{0, 0, 0}}
	for _, pt := range []string{"keyboard", "bogus", "MOUSE", ""} {
		s := clean()
		s.PointerType = pt
		c := h.issueFor(t, "clicks", s, false)
		if _, err := h.solveWith(c, teleport, s); code(err) != "ANSWER_INVALID" {
			t.Errorf("clicks with pointer %q: %v", pt, err)
		}
	}
	var straight [][3]float64
	for i := 0; i <= 40; i++ {
		p := float64(i) / 40
		straight = append(straight, [3]float64{float64(i)*14 + float64(i%5), p * p * (3 - 2*p) * answerX, 0})
	}
	s := clean()
	s.PointerType = "bogus"
	c := h.issueFor(t, "slide", s, false)
	if _, err := h.solveWith(c, &SliderAnswer{X: answerX, Trajectory: straight}, s); code(err) != "ANSWER_INVALID" {
		t.Fatalf("straight slider with pointer bogus: %v", err)
	}
}

func TestTouchAnswersAreVisible(t *testing.T) {
	h := newHarness(t)
	withPuzzle(h)
	touch := clean()
	touch.PointerType = "touch"
	c := h.issueFor(t, "clicks", touch, false)
	taps := &SliderAnswer{Clicks: [][3]float64{{1700, 62, 48}, {2600, 224, 83}, {3350, 137, 158}}, MovesBetween: []int{0, 0, 0}}
	out, err := h.solveWith(c, taps, touch)
	if err != nil {
		t.Fatalf("touch taps: %v", err)
	}
	if out.ExpiresIn != 60 {
		t.Fatalf("expires_in: %d", out.ExpiresIn)
	}
	v, _ := h.verify(out.Token, "clicks", "r-touch")
	if !v.Success || !slices.Contains(v.RiskReasons, risk.ReasonTouchInput) || v.RiskLevel != risk.LevelLow {
		t.Fatalf("%+v", v)
	}
}

// Another replica may run up to ClockSkew behind and still accept the
// challenge, so the used-mark must outlive exp+2*skew on our clock.
func TestUsedMarkOutlivesSkewedReplica(t *testing.T) {
	h := newHarness(t)
	c := h.issue(t)
	nonces, _ := pow.Solve(c.Algorithm, c.ChallengeID, c.Params)
	h.clock = time.Unix(c.ExpiresAt, 0)
	if _, err := h.solve(c, nonces); err != nil {
		t.Fatal(err)
	}
	mr := h.redis
	mr.FastForward(6 * time.Second)
	h.clock = time.Unix(c.ExpiresAt+3, 0) // a replica whose clock is behind
	if _, err := h.solve(c, nonces); code(err) != "CHALLENGE_USED" {
		t.Fatalf("second solve on a lagging replica: %v", err)
	}
}

func TestEarlyErrorsCarryCORSForKnownApps(t *testing.T) {
	h := newHarness(t)
	h.g.Settings.ChallengePerIPPerMinute = 1
	h.g.Apps = &peekingApps{h.apps}
	h.issue(t)
	_, origin, err := h.g.Issue(context.Background(), IssueInput{AppKey: appA, Action: "login", Origin: originA, ClientIP: h.ip})
	if code(err) != "RATE_LIMITED" || origin != originA {
		t.Fatalf("rate limited: %q %v", origin, err)
	}
	_, origin, err = h.g.Issue(context.Background(), IssueInput{AppKey: appA, Action: "login", Origin: "https://evil.example", ClientIP: h.ip})
	if code(err) != "RATE_LIMITED" || origin != "" {
		t.Fatalf("other origin must get no CORS: %q %v", origin, err)
	}

	h.g.Settings.ChallengePerIPPerMinute = 1000
	_, origin, err = h.g.Solve(context.Background(), SolveInput{AppKey: appA, Challenge: "v1.k1.broken.mac", Origin: originA, ClientIP: h.ip})
	if code(err) != "CHALLENGE_INVALID" || origin != originA {
		t.Fatalf("broken challenge: %q %v", origin, err)
	}
	c := h.issue(t)
	h.clock = h.clock.Add(time.Hour)
	_, origin, err = h.g.Solve(context.Background(), SolveInput{AppKey: appA, Challenge: c.Challenge, Origin: originA, ClientIP: h.ip})
	if code(err) != "CHALLENGE_EXPIRED" || origin != originA {
		t.Fatalf("expired: %q %v", origin, err)
	}
}

type peekingApps struct{ *fakeApps }

func (p *peekingApps) PeekApp(k string) (*domain.App, bool) {
	a, ok := p.apps[k]
	return a, ok
}

func TestVerifyAuthFailuresAreLimitedPerIP(t *testing.T) {
	h := newHarness(t)
	h.g.Settings.AuthFailuresPerIPPerMinute = 3
	for i := 0; i < 3; i++ {
		in := signed("key_unknown_"+string(rune('a'+i)), "x", h.clock, appA, "t", "login", "r-auth")
		in.ClientIP = h.ip
		if _, err := h.g.Verify(context.Background(), in); code(err) != "UNAUTHORIZED" {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	in := signed(keyA, secretA, h.clock, appA, h.token(t), "login", "r-auth-ok")
	in.ClientIP = h.ip
	if _, err := h.g.Verify(context.Background(), in); code(err) != "RATE_LIMITED" {
		t.Fatalf("after failures: %v", err)
	}
	// Other callers are unaffected.
	if v, err := h.verify(h.token(t), "login", "r-other-ip"); err != nil || !v.Success {
		t.Fatalf("other ip: %+v %v", v, err)
	}
	// A clock problem stays visible and does not count.
	h.g.Settings.AuthFailuresPerIPPerMinute = 100
	old := signed(keyA, secretA, h.clock.Add(-time.Hour), appA, "t", "login", "r-old")
	if _, err := h.g.Verify(context.Background(), old); code(err) != "TIMESTAMP_EXPIRED" {
		t.Fatalf("old timestamp: %v", err)
	}
}

// The console counts every stage and lists solve and verify events, with
// the IP only as a salted tag.
func TestConsoleStatistics(t *testing.T) {
	h := newHarness(t)
	withPuzzle(h)
	token := h.token(t)
	if v, err := h.verify(token, "login", "r-stat"); err != nil || !v.Success {
		t.Fatal(v, err)
	}
	if v, _ := h.verify(token, "login", "r-stat-2"); v.Success {
		t.Fatal("consumed token passed")
	}
	c := h.issueFor(t, "slide", clean(), false)
	if _, err := h.solveWith(c, &SliderAnswer{X: 10, Trajectory: humanDrag(10)}, clean()); code(err) != "ANSWER_INVALID" {
		t.Fatal(err)
	}
	hours, err := h.g.Stats.Hours(context.Background(), 1, h.clock, h.clock)
	if err != nil {
		t.Fatal(err)
	}
	got := hours[0].Counts
	for field, want := range map[string]int64{
		"issue": 2, "issue:invisible": 1, "issue:slider": 1, "risk:low": 1, "risk:medium": 1, "reason:NO_SIGNALS": 1,
		"solve_ok:invisible": 1, "solve_fail:ANSWER_INVALID": 1, "solve_fail_kind:slider": 1,
		"verify_pass": 1, "verify_pass:invisible": 1, "verify_reject:TOKEN_CONSUMED": 1,
	} {
		if got[field] != want {
			t.Errorf("%s = %d, want %d (%v)", field, got[field], want, got)
		}
	}
	evs, _ := h.g.Stats.Events(context.Background(), 1, 10)
	if len(evs) != 4 || evs[0].Type != "solve" || evs[0].Result != "ANSWER_INVALID" || evs[0].Pointer != "mouse" ||
		evs[1].Result != "TOKEN_CONSUMED" || evs[2].Result != "pass" || evs[2].RequestID != "r-stat" {
		t.Fatalf("%+v", evs)
	}
	if evs[0].IP == "" || len(evs[0].IP) != 12 || strings.Contains(evs[0].IP, "192.0.2") {
		t.Fatalf("ip tag %q", evs[0].IP)
	}
}
