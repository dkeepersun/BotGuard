package logic

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/repository"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/metric"
)

// Degrade causes.
const (
	DegradeQuota     = "quota"
	DegradeSaturated = "saturated"
)

// Prometheus metrics, exported by go-zero's DevServer when it is enabled.
var (
	metricRequests = metric.NewCounterVec(&metric.CounterVecOpts{Namespace: "botguard", Subsystem: "gateway",
		Name: "requests_total", Help: "Gateway API requests by route.", Labels: []string{"route"}})
	metricErrors = metric.NewCounterVec(&metric.CounterVecOpts{Namespace: "botguard", Subsystem: "gateway",
		Name: "errors_total", Help: "Gateway error responses by route and contract code.", Labels: []string{"route", "code"}})
	metricDegraded = metric.NewCounterVec(&metric.CounterVecOpts{Namespace: "botguard", Subsystem: "gateway",
		Name: "degraded_total", Help: "Image challenges served as PoW for lack of capacity, by cause.", Labels: []string{"cause"}})
	metricPool = metric.NewGaugeVec(&metric.GaugeVecOpts{Namespace: "botguard", Subsystem: "gateway",
		Name: "puzzle_pool_ready", Help: "Pre-generated puzzles ready, by kind.", Labels: []string{"kind"}})
)

// Monitor counts what the gateway answers, per minute, for the admin health
// page (and as Prometheus metrics). Counting is in memory; Run writes one
// summary per minute, so a flood costs Redis nothing extra. A nil Monitor
// counts nothing.
type Monitor struct {
	// Instance names this gateway in the summaries, e.g. host:port.
	Instance string
	Store    *repository.HealthStore
	// RateLimitedPerMinute is the RATE_LIMITED count that makes a minute an
	// anomaly; 0 never does.
	RateLimitedPerMinute int64
	// Pool reports the puzzles ready per kind and the per-kind capacity;
	// nil when the pool is off.
	Pool func() (ready map[string]int, size int)
	Now  func() time.Time

	mu       sync.Mutex
	minute   int64
	requests map[string]int64
	codes    map[string]int64
	degraded map[string]int64
}

// Request counts one call to route.
func (m *Monitor) Request(route string) {
	if m == nil {
		return
	}
	metricRequests.Inc(route)
	m.mu.Lock()
	m.bump(&m.requests, route)
	m.mu.Unlock()
}

// Fail counts an error response with its contract code.
func (m *Monitor) Fail(route, code string) {
	if m == nil {
		return
	}
	metricErrors.Inc(route, code)
	m.mu.Lock()
	m.bump(&m.codes, code)
	m.mu.Unlock()
}

// Degraded counts an image challenge served as PoW.
func (m *Monitor) Degraded(cause string) {
	if m == nil {
		return
	}
	metricDegraded.Inc(cause)
	m.mu.Lock()
	m.bump(&m.degraded, cause)
	m.mu.Unlock()
}

func (m *Monitor) bump(c *map[string]int64, key string) {
	if *c == nil {
		*c = make(map[string]int64)
	}
	(*c)[key]++
}

// take returns the counts so far and starts a new minute.
func (m *Monitor) take(at time.Time) repository.HealthMinute {
	m.mu.Lock()
	out := repository.HealthMinute{At: at.Unix(), Instance: m.Instance, Requests: m.requests, Codes: m.codes, Degraded: m.degraded}
	m.requests, m.codes, m.degraded = nil, nil, nil
	m.mu.Unlock()
	if m.Pool != nil {
		out.Pool, out.PoolSize = m.Pool()
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	out.Goroutines, out.HeapMB = runtime.NumGoroutine(), int(ms.HeapAlloc>>20)
	return out
}

// anomalies applies the rules to a finished minute.
func (m *Monitor) anomalies(h repository.HealthMinute) []repository.HealthAnomaly {
	var out []repository.HealthAnomaly
	add := func(kind, detail string) {
		out = append(out, repository.HealthAnomaly{At: h.At, Instance: h.Instance, Kind: kind, Detail: detail})
	}
	if n := h.Codes["UNAVAILABLE"]; n > 0 {
		add(repository.AnomalyUnavailable, fmt.Sprintf("返回 503 共 %d 次（Redis / MySQL 故障或过载）", n))
	}
	if q, s := h.Degraded[DegradeQuota], h.Degraded[DegradeSaturated]; q+s > 0 {
		add(repository.AnomalyDegraded, fmt.Sprintf("图片题降级为 PoW 共 %d 次：应用超配额 %d 次，生成满载 %d 次", q+s, q, s))
	}
	if n := h.Codes["RATE_LIMITED"]; m.RateLimitedPerMinute > 0 && n >= m.RateLimitedPerMinute {
		add(repository.AnomalyRateLimited, fmt.Sprintf("限流拒绝 %d 次（阈值 %d）", n, m.RateLimitedPerMinute))
	}
	return out
}

// Run writes a summary at the end of every minute and refreshes the pool
// gauge every 15 seconds, until ctx ends.
func (m *Monitor) Run(ctx context.Context) {
	now := m.Now
	if now == nil {
		now = time.Now
	}
	start := now().Truncate(time.Minute)
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		m.refreshPoolGauge()
		t := now().Truncate(time.Minute)
		if !t.After(start) {
			continue
		}
		h := m.take(start)
		start = t
		found := m.anomalies(h)
		for _, a := range found {
			logx.Alert(fmt.Sprintf("health anomaly on %s: %s", a.Instance, a.Detail))
		}
		if m.Store == nil {
			continue
		}
		wctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		if err := m.Store.Put(wctx, h, found); err != nil {
			logx.Errorf("store health minute: %v", err)
		}
		cancel()
	}
}

func (m *Monitor) refreshPoolGauge() {
	if m.Pool == nil {
		return
	}
	ready, _ := m.Pool()
	for k, n := range ready {
		metricPool.Set(float64(n), k)
	}
}

// PoolLevels reports the pooled puzzles ready per kind, for the Monitor;
// nil maps and zero size when the pool is off.
func (g *Gateway) PoolLevels() (map[string]int, int) {
	p := g.pool
	if p == nil {
		return nil, 0
	}
	ready := make(map[string]int, len(p.queues))
	size := 0
	for _, k := range domain.InteractiveKinds {
		q := p.queues[k]
		ready[k] = len(q)
		size = cap(q)
	}
	return ready, size
}
