package logic

import (
	"bytes"
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/repository"
	"github.com/dkeepersun/BotGuard/internal/service/challenge"
	"github.com/dkeepersun/BotGuard/internal/service/pow"
	"github.com/dkeepersun/BotGuard/internal/testdb"
	"github.com/zeromicro/go-zero/core/stores/redis"
)

// TestEndToEndWithMySQL drives the gateway against sites written by the
// management use case, so the read queries are checked against the real schema.
func TestEndToEndWithMySQL(t *testing.T) {
	_, conn := testdb.Open(t)
	ctx := context.Background()
	clock := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }

	admin := &SiteAdmin{Repo: repository.NewSiteAdmin(conn), Now: now}
	site, err := admin.CreateSite(ctx, "e2e")
	if err != nil {
		t.Fatal(err)
	}
	const origin = "https://shop.example"
	if _, err := admin.AddOrigin(ctx, site, origin); err != nil {
		t.Fatal(err)
	}
	if err := admin.SetAction(ctx, site, "login", domain.ActionPolicy{Enabled: true, Puzzles: 3, Difficulty: 20}); err != nil {
		t.Fatal(err)
	}
	oldKey, _, err := admin.CreateKey(ctx, site, 0)
	if err != nil {
		t.Fatal(err)
	}

	mr := miniredis.RunT(t)
	rds := redis.New(mr.Addr(), redis.WithIdentity())
	keys, _ := challenge.NewKeyring([]challenge.Key{{ID: "k1", Secret: bytes.Repeat([]byte("e"), 32), Active: true}})
	g := &Gateway{
		Sites: repository.NewMySQLSites(conn), State: repository.NewState(rds), Limiter: repository.NewRateLimiter(rds), Keys: keys,
		Settings: Settings{ChallengeTTL: 2 * time.Minute, TokenTTL: time.Minute, ResultTTL: 10 * time.Minute, ClockSkew: 5 * time.Second,
			MaxDifficulty: 1 << 20, ChallengePerIPPerMinute: 1000, SolvePerIPPerMinute: 1000, VerifyPerKeyPerMinute: 1000},
		Now: now,
	}
	ip := netip.MustParseAddr("198.51.100.4")
	token := func() string {
		t.Helper()
		c, _, err := g.Issue(ctx, IssueInput{SiteKey: site, Action: "login", Origin: origin, ClientIP: ip})
		if err != nil {
			t.Fatalf("issue: %v", err)
		}
		if c.Params.Puzzles != 3 {
			t.Fatalf("policy not read from MySQL: %+v", c.Params)
		}
		nonces, _ := pow.Solve(c.Algorithm, c.ChallengeID, c.Params)
		out, _, err := g.Solve(ctx, SolveInput{SiteKey: site, Challenge: c.Challenge, Nonces: nonces, Origin: origin, ClientIP: ip})
		if err != nil {
			t.Fatalf("solve: %v", err)
		}
		return out.Token
	}
	verify := func(header, tok, rid string) (*VerifyOutput, error) {
		return g.Verify(ctx, VerifyInput{Authorization: header, SiteKey: site, Token: tok, Action: "login", RequestID: rid})
	}

	if out, err := verify(oldKey.Header(), token(), "r1"); err != nil || !out.Success {
		t.Fatalf("first verify: %+v %v", out, err)
	}

	// Rotation: both keys work during the grace period, only the new one after.
	newKey, _, err := admin.RotateKey(ctx, oldKey.KeyID, time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := verify(oldKey.Header(), token(), "r2"); err != nil || !out.Success {
		t.Fatalf("old key in grace: %+v %v", out, err)
	}
	clock = clock.Add(time.Hour)
	if _, err := verify(oldKey.Header(), token(), "r3"); code(err) != "UNAUTHORIZED" {
		t.Fatalf("old key after grace: %v", err)
	}
	if out, err := verify(newKey.Header(), token(), "r4"); err != nil || !out.Success {
		t.Fatalf("new key: %+v %v", out, err)
	}

	// Removing the origin rejects tokens already issued for it.
	pending := token()
	if _, err := admin.RemoveOrigin(ctx, site, origin); err != nil {
		t.Fatal(err)
	}
	if out, err := verify(newKey.Header(), pending, "r5"); err != nil || out.Reason != "ORIGIN_NOT_ALLOWED" {
		t.Fatalf("removed origin: %+v %v", out, err)
	}
	if _, err := admin.AddOrigin(ctx, site, origin); err != nil {
		t.Fatal(err)
	}

	if err := admin.SetSiteEnabled(ctx, site, false); err != nil {
		t.Fatal(err)
	}
	if _, err := verify(newKey.Header(), pending, "r6"); code(err) != "SITE_FORBIDDEN" {
		t.Fatalf("disabled site: %v", err)
	}
	if err := admin.SetSiteEnabled(ctx, site, true); err != nil {
		t.Fatal(err)
	}

	if err := admin.RevokeKey(ctx, newKey.KeyID); err != nil {
		t.Fatal(err)
	}
	if _, err := verify(newKey.Header(), pending, "r7"); code(err) != "UNAUTHORIZED" {
		t.Fatalf("revoked key: %v", err)
	}
}

// TestCachedRevocationTakesEffectWithinTTL checks the staleness bound the docs promise.
func TestCachedRevocationTakesEffectWithinTTL(t *testing.T) {
	_, conn := testdb.Open(t)
	ctx := context.Background()
	admin := &SiteAdmin{Repo: repository.NewSiteAdmin(conn), Now: time.Now}
	site, _ := admin.CreateSite(ctx, "cache")
	key, _, err := admin.CreateKey(ctx, site, 0)
	if err != nil {
		t.Fatal(err)
	}
	cached, err := repository.NewCachedSites(repository.NewMySQLSites(conn), time.Second, 100)
	if err != nil {
		t.Fatal(err)
	}
	if k, err := cached.APIKey(ctx, key.KeyID); err != nil || !k.Usable(time.Now()) {
		t.Fatalf("before revoke: %v", err)
	}
	if err := admin.RevokeKey(ctx, key.KeyID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if k, err := cached.APIKey(ctx, key.KeyID); err != nil || k.Usable(time.Now()) {
		t.Fatalf("after TTL the key must be unusable: %+v %v", k, err)
	}
}
