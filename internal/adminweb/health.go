package adminweb

import (
	"net/http"
	"sort"
	"time"

	"github.com/dkeepersun/BotGuard/internal/logic"
	"github.com/dkeepersun/BotGuard/internal/repository"
)

// healthMinutes is how far back the health page charts.
const healthMinutes = 60

type healthInstance struct {
	Name     string
	LastSeen time.Time
	// Stale: no summary in the last few minutes, so the gateway is probably
	// down or cannot reach Redis.
	Stale bool
	Last  repository.HealthMinute
	// Totals over the charted window.
	Requests, RateLimited, Unavailable, Degraded int64
	PoolMin, PoolSize                            int
	PoolKnown                                    bool
}

type healthView struct {
	Now       time.Time
	Instances []healthInstance
	Anomalies []repository.HealthAnomaly
	Trend     Trend
	// Totals across instances over the charted window.
	Requests, RateLimited, Unavailable, Degraded int64
}

var anomalyLabels = map[string]string{
	repository.AnomalyUnavailable: "服务不可用",
	repository.AnomalyDegraded:    "容量降级",
	repository.AnomalyRateLimited: "限流激增",
}

func (s *Server) healthPage(w http.ResponseWriter, r *http.Request, p *logic.Principal) {
	ctx := r.Context()
	now := s.Now()
	v := healthView{Now: now}
	instances, err := s.Health.Instances(ctx, now.Add(-24*time.Hour))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	// One bucket per minute across all instances, oldest first.
	start := now.Truncate(time.Minute).Add(-healthMinutes * time.Minute)
	labels := make([]string, healthMinutes)
	lines := map[string][]int64{"requests": make([]int64, healthMinutes), "rate_limited": make([]int64, healthMinutes),
		"unavailable": make([]int64, healthMinutes), "degraded": make([]int64, healthMinutes)}
	for i := range labels {
		labels[i] = start.Add(time.Duration(i) * time.Minute).In(cst).Format("15:04")
	}
	for _, in := range instances {
		minutes, err := s.Health.Recent(ctx, in.Name, healthMinutes+5)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		hi := healthInstance{Name: in.Name, LastSeen: in.LastSeen, Stale: now.Sub(in.LastSeen) > 3*time.Minute, PoolMin: -1}
		if len(minutes) > 0 {
			hi.Last = minutes[0]
		}
		for _, m := range minutes {
			i := int(time.Unix(m.At, 0).Sub(start) / time.Minute)
			if i < 0 || i >= healthMinutes {
				continue
			}
			req := sum(m.Requests)
			rl, un, dg := m.Codes["RATE_LIMITED"], m.Codes["UNAVAILABLE"], sum(m.Degraded)
			lines["requests"][i] += req
			lines["rate_limited"][i] += rl
			lines["unavailable"][i] += un
			lines["degraded"][i] += dg
			hi.Requests, hi.RateLimited, hi.Unavailable, hi.Degraded = hi.Requests+req, hi.RateLimited+rl, hi.Unavailable+un, hi.Degraded+dg
			if m.PoolSize > 0 {
				hi.PoolKnown, hi.PoolSize = true, m.PoolSize
				for _, n := range m.Pool {
					if hi.PoolMin < 0 || n < hi.PoolMin {
						hi.PoolMin = n
					}
				}
			}
		}
		v.Requests, v.RateLimited, v.Unavailable, v.Degraded = v.Requests+hi.Requests, v.RateLimited+hi.RateLimited, v.Unavailable+hi.Unavailable, v.Degraded+hi.Degraded
		v.Instances = append(v.Instances, hi)
	}
	sort.SliceStable(v.Instances, func(i, j int) bool { return !v.Instances[i].Stale && v.Instances[j].Stale })
	v.Trend = Trend{Labels: labels, Lines: lines}
	if v.Anomalies, err = s.Health.Anomalies(ctx, 100); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, "health.html", View{Title: "运行状况", Active: "health", Me: p, Data: v})
}

func sum(m map[string]int64) int64 {
	var n int64
	for _, v := range m {
		n += v
	}
	return n
}
