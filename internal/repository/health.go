package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/zeromicro/go-zero/core/stores/redis"
)

// HealthMinute is one gateway instance's counters for one minute, for the
// admin console's health page. Codes counts error responses by contract code
// (RATE_LIMITED, UNAVAILABLE ...); Degraded counts image challenges served
// as PoW by cause (quota, saturated).
type HealthMinute struct {
	At         int64            `json:"at"` // unix seconds, start of the minute
	Instance   string           `json:"instance"`
	Requests   map[string]int64 `json:"requests,omitempty"` // by route
	Codes      map[string]int64 `json:"codes,omitempty"`
	Degraded   map[string]int64 `json:"degraded,omitempty"`
	Pool       map[string]int   `json:"pool,omitempty"` // puzzles ready, by kind
	PoolSize   int              `json:"pool_size,omitempty"`
	Goroutines int              `json:"goroutines"`
	HeapMB     int              `json:"heap_mb"`
}

// HealthAnomaly is a minute that crossed a rule (docs/deploy.md 运行监控).
type HealthAnomaly struct {
	At       int64  `json:"at"`
	Instance string `json:"instance"`
	Kind     string `json:"kind"`
	Detail   string `json:"detail"`
}

// Anomaly kinds.
const (
	AnomalyUnavailable = "unavailable"
	AnomalyDegraded    = "degraded"
	AnomalyRateLimited = "rate_limited"
)

// HealthStore keeps the last day of minutes per instance and the last
// anomalies in Redis. All keys share the {health} hash tag so the write
// script stays single-slot under Redis Cluster.
type HealthStore struct {
	rds *redis.Redis
	// Minutes kept per instance; MaxAnomalies bounds the anomaly list.
	Minutes      int
	MaxAnomalies int
	TTL          time.Duration
}

func NewHealthStore(rds *redis.Redis) *HealthStore {
	return &HealthStore{rds: rds, Minutes: 24 * 60, MaxAnomalies: 500, TTL: 48 * time.Hour}
}

const (
	healthInstancesKey = "bg:{health}:instances"
	healthAnomaliesKey = "bg:{health}:anomalies"
)

func healthMinutesKey(instance string) string { return "bg:{health}:m:" + instance }

// healthScript stores one minute and its anomalies.
// KEYS[1] minutes list, KEYS[2] instances zset, KEYS[3] anomalies list
// ARGV[1] minute json, ARGV[2] max minutes, ARGV[3] ttl seconds,
// ARGV[4] instance, ARGV[5] at, ARGV[6] max anomalies, ARGV[7..] anomaly json
const healthScript = `
redis.call('LPUSH', KEYS[1], ARGV[1])
redis.call('LTRIM', KEYS[1], 0, tonumber(ARGV[2]) - 1)
redis.call('EXPIRE', KEYS[1], ARGV[3])
redis.call('ZADD', KEYS[2], ARGV[5], ARGV[4])
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', tonumber(ARGV[5]) - tonumber(ARGV[3]))
redis.call('EXPIRE', KEYS[2], ARGV[3])
if #ARGV >= 7 then
  for i = 7, #ARGV do redis.call('LPUSH', KEYS[3], ARGV[i]) end
  redis.call('LTRIM', KEYS[3], 0, tonumber(ARGV[6]) - 1)
  redis.call('EXPIRE', KEYS[3], ARGV[3])
end
return 1
`

// Put records a minute and its anomalies in one round trip.
func (s *HealthStore) Put(ctx context.Context, m HealthMinute, anomalies []HealthAnomaly) error {
	mj, err := json.Marshal(m)
	if err != nil {
		return err
	}
	args := []any{string(mj), s.Minutes, ceilSeconds(s.TTL), m.Instance, m.At, s.MaxAnomalies}
	for _, a := range anomalies {
		aj, err := json.Marshal(a)
		if err != nil {
			return err
		}
		args = append(args, string(aj))
	}
	_, err = s.rds.EvalCtx(ctx, healthScript, []string{healthMinutesKey(m.Instance), healthInstancesKey, healthAnomaliesKey}, args...)
	return err
}

// Instance is a gateway that reported recently.
type Instance struct {
	Name     string
	LastSeen time.Time
}

// Instances lists gateways that reported at or after since, newest first.
func (s *HealthStore) Instances(ctx context.Context, since time.Time) ([]Instance, error) {
	pairs, err := s.rds.ZrevrangebyscoreWithScoresCtx(ctx, healthInstancesKey, since.Unix(), time.Now().Add(time.Hour).Unix())
	if err != nil {
		return nil, err
	}
	out := make([]Instance, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, Instance{Name: p.Key, LastSeen: time.Unix(p.Score, 0)})
	}
	return out, nil
}

// Recent returns up to n of an instance's latest minutes, newest first.
func (s *HealthStore) Recent(ctx context.Context, instance string, n int) ([]HealthMinute, error) {
	raw, err := s.rds.LrangeCtx(ctx, healthMinutesKey(instance), 0, n-1)
	if err != nil {
		return nil, err
	}
	out := make([]HealthMinute, 0, len(raw))
	for _, r := range raw {
		var m HealthMinute
		if json.Unmarshal([]byte(r), &m) == nil {
			out = append(out, m)
		}
	}
	return out, nil
}

// Anomalies returns up to n of the latest anomalies, newest first.
func (s *HealthStore) Anomalies(ctx context.Context, n int) ([]HealthAnomaly, error) {
	raw, err := s.rds.LrangeCtx(ctx, healthAnomaliesKey, 0, n-1)
	if err != nil {
		return nil, err
	}
	out := make([]HealthAnomaly, 0, len(raw))
	for _, r := range raw {
		var a HealthAnomaly
		if json.Unmarshal([]byte(r), &a) == nil {
			out = append(out, a)
		}
	}
	return out, nil
}
