package logic

import (
	"context"
	"testing"
	"time"

	"github.com/dkeepersun/BotGuard/internal/repository"
)

func TestMonitorCountsAndRules(t *testing.T) {
	var none *Monitor
	none.Request("challenges") // a nil Monitor counts nothing and never panics
	none.Fail("challenges", "UNAVAILABLE")
	none.Degraded(DegradeQuota)

	m := &Monitor{Instance: "gw:8090", RateLimitedPerMinute: 3,
		Pool: func() (map[string]int, int) { return map[string]int{"slider": 2}, 64 }}
	for range 5 {
		m.Request("challenges")
	}
	m.Request("verify")
	for range 3 {
		m.Fail("challenges", "RATE_LIMITED")
	}
	m.Fail("verify", "UNAVAILABLE")
	m.Degraded(DegradeQuota)
	m.Degraded(DegradeSaturated)
	m.Degraded(DegradeSaturated)

	at := time.Unix(1_790_000_040, 0)
	h := m.take(at)
	if h.At != at.Unix() || h.Instance != "gw:8090" || h.Requests["challenges"] != 5 || h.Requests["verify"] != 1 ||
		h.Codes["RATE_LIMITED"] != 3 || h.Codes["UNAVAILABLE"] != 1 || h.Degraded[DegradeSaturated] != 2 ||
		h.Pool["slider"] != 2 || h.PoolSize != 64 || h.Goroutines == 0 {
		t.Fatalf("minute: %+v", h)
	}
	found := map[string]bool{}
	for _, a := range m.anomalies(h) {
		found[a.Kind] = true
		if a.At != h.At || a.Instance != h.Instance || a.Detail == "" {
			t.Errorf("anomaly: %+v", a)
		}
	}
	if !found[repository.AnomalyUnavailable] || !found[repository.AnomalyDegraded] || !found[repository.AnomalyRateLimited] {
		t.Fatalf("anomalies: %v", found)
	}
	// The next minute starts empty, and a quiet minute is no anomaly.
	if h := m.take(at.Add(time.Minute)); len(h.Requests)+len(h.Codes)+len(h.Degraded) != 0 || len(m.anomalies(h)) != 0 {
		t.Fatalf("next minute: %+v", h)
	}
	m.RateLimitedPerMinute = 0
	if a := m.anomalies(repository.HealthMinute{Codes: map[string]int64{"RATE_LIMITED": 1 << 20}}); len(a) != 0 {
		t.Fatalf("threshold 0 must never fire: %+v", a)
	}
}

// Degraded challenges are counted by cause.
func TestMonitorCountsDegrades(t *testing.T) {
	h := newHarness(t)
	withPuzzle(h)
	h.g.Monitor = &Monitor{}
	h.g.Settings.InteractivePerAppPerMinute = 1
	h.issueFor(t, "slide", clean(), false)
	h.issueFor(t, "slide", clean(), false)
	if got := h.g.Monitor.take(time.Now()).Degraded[DegradeQuota]; got != 1 {
		t.Fatalf("quota degrades: %d", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.g.StartPuzzlePool(ctx, 2, 1)
	ready, size := h.g.PoolLevels()
	if size != 2 || len(ready) == 0 {
		t.Fatalf("pool levels: %v %d", ready, size)
	}
}
