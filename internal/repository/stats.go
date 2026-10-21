package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	red "github.com/redis/go-redis/v9"
	"github.com/zeromicro/go-zero/core/stores/redis"
)

// Stats keeps per-app hourly counters and a short list of recent events in
// Redis, for the admin console (docs/admin.md §4). Writes are best effort:
// callers log and ignore errors, so the numbers are approximate and never
// used for billing.
type Stats struct {
	rds *redis.Redis
	// HourTTL keeps hourly counters; MaxEvents bounds each app's event list.
	HourTTL   time.Duration
	MaxEvents int
}

func NewStats(rds *redis.Redis) *Stats {
	return &Stats{rds: rds, HourTTL: 32 * 24 * time.Hour, MaxEvents: 2000}
}

// Event is one solve or verify outcome as the console lists it. It never
// carries tokens, signatures, secrets or raw IPs.
type Event struct {
	At        int64    `json:"at"` // unix milliseconds
	Type      string   `json:"type"`
	Action    string   `json:"action"`
	Kind      string   `json:"kind,omitempty"`
	Result    string   `json:"result"` // pass or the reason / error code
	Level     string   `json:"level,omitempty"`
	Reasons   []string `json:"reasons,omitempty"`
	IP        string   `json:"ip,omitempty"` // salted hash prefix
	RequestID string   `json:"request_id,omitempty"`
	Pointer   string   `json:"pointer,omitempty"`
}

// Event types.
const (
	EventIssue  = "issue"
	EventSolve  = "solve"
	EventVerify = "verify"
)

// statScript adds 1 to each field of the hour hash and pushes the event.
// KEYS[1] hour hash, KEYS[2] event list (same {appID} slot)
// ARGV[1] hash ttl, ARGV[2] max events, ARGV[3] event json or ”, ARGV[4..] fields
const statScript = `
for i = 4, #ARGV do redis.call('HINCRBY', KEYS[1], ARGV[i], 1) end
redis.call('EXPIRE', KEYS[1], ARGV[1])
if ARGV[3] ~= '' then
  redis.call('LPUSH', KEYS[2], ARGV[3])
  redis.call('LTRIM', KEYS[2], 0, tonumber(ARGV[2]) - 1)
  redis.call('EXPIRE', KEYS[2], ARGV[1])
end
return 1
`

func hourKey(appID int64, at time.Time) string {
	return fmt.Sprintf("bg:{%d}:stat:h:%s", appID, at.UTC().Format("2006010215"))
}

func eventsKey(appID int64) string { return fmt.Sprintf("bg:{%d}:events", appID) }

// Record counts fields in the current hour and, when ev is non-nil, appends
// the event. One round trip; runs with EVAL for the same breaker reason as
// State.
func (s *Stats) Record(ctx context.Context, appID int64, at time.Time, fields []string, ev *Event) error {
	if len(fields) == 0 && ev == nil {
		return nil
	}
	evJSON := ""
	if ev != nil {
		b, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		evJSON = string(b)
	}
	args := make([]any, 0, 3+len(fields))
	args = append(args, int64(s.HourTTL/time.Second), s.MaxEvents, evJSON)
	for _, f := range fields {
		args = append(args, f)
	}
	_, err := s.rds.EvalCtx(ctx, statScript, []string{hourKey(appID, at), eventsKey(appID)}, args...)
	return err
}

// Hour is one hour's counters.
type Hour struct {
	Start  time.Time
	Counts map[string]int64
}

// Hours returns the counters of every hour in [from, to], oldest first;
// hours without data have empty maps.
func (s *Stats) Hours(ctx context.Context, appID int64, from, to time.Time) ([]Hour, error) {
	from, to = from.UTC().Truncate(time.Hour), to.UTC().Truncate(time.Hour)
	var out []Hour
	for t := from; !t.After(to); t = t.Add(time.Hour) {
		out = append(out, Hour{Start: t, Counts: map[string]int64{}})
	}
	if len(out) == 0 {
		return out, nil
	}
	// Commands queued in a pipeline run after the callback returns, so the
	// results are read afterwards.
	cmds := make([]*red.MapStringStringCmd, len(out))
	err := s.rds.PipelinedCtx(ctx, func(p redis.Pipeliner) error {
		for i := range out {
			cmds[i] = p.HGetAll(ctx, hourKey(appID, out[i].Start))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for i, c := range cmds {
		m, _ := c.Result() // a missing hash is an empty map
		for k, v := range m {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				out[i].Counts[k] = n
			}
		}
	}
	return out, err
}

// Events returns up to limit recent events, newest first.
func (s *Stats) Events(ctx context.Context, appID int64, limit int) ([]Event, error) {
	if limit <= 0 || limit > s.MaxEvents {
		limit = s.MaxEvents
	}
	raw, err := s.rds.LrangeCtx(ctx, eventsKey(appID), 0, limit-1)
	if err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(raw))
	for _, r := range raw {
		var e Event
		if json.Unmarshal([]byte(r), &e) == nil {
			out = append(out, e)
		}
	}
	return out, nil
}
