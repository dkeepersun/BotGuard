package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/zeromicro/go-zero/core/stores/redis"
)

func TestStatsCountsHoursAndKeepsRecentEvents(t *testing.T) {
	mr := miniredis.RunT(t)
	s := NewStats(redis.New(mr.Addr(), redis.WithIdentity()))
	s.MaxEvents = 3
	ctx := context.Background()
	h0 := time.Date(2026, 10, 21, 8, 15, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		ev := &Event{At: int64(i), Type: EventSolve, Action: "login", Result: "pass"}
		if err := s.Record(ctx, 7, h0, []string{"solve_ok", "solve_ok:slider"}, ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Record(ctx, 7, h0.Add(2*time.Hour), []string{"verify_pass"}, nil); err != nil {
		t.Fatal(err)
	}
	hours, err := s.Hours(ctx, 7, h0, h0.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(hours) != 3 || hours[0].Counts["solve_ok:slider"] != 5 || len(hours[1].Counts) != 0 || hours[2].Counts["verify_pass"] != 1 {
		t.Fatalf("%+v", hours)
	}
	evs, err := s.Events(ctx, 7, 10)
	if err != nil || len(evs) != 3 || evs[0].At != 4 {
		t.Fatalf("%+v %v", evs, err)
	}
	// Other apps are separate.
	if evs, _ := s.Events(ctx, 8, 10); len(evs) != 0 {
		t.Fatal(evs)
	}
	if ttl := mr.TTL("bg:{7}:stat:h:2026102108"); ttl <= 0 {
		t.Fatalf("no ttl: %v", ttl)
	}
}
