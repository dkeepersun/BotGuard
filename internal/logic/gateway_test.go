package logic

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/errx"
	"github.com/dkeepersun/BotGuard/internal/repository"
	"github.com/dkeepersun/BotGuard/internal/service/challenge"
	"github.com/dkeepersun/BotGuard/internal/service/pow"
	"github.com/zeromicro/go-zero/core/stores/redis"
)

const (
	siteA   = "site_a_public"
	siteB   = "site_b_public"
	originA = "https://a.example"
	secretA = "secret-for-site-a"
	keyA    = "key_site_a"
	keyB    = "key_site_b"
)

type fakeSites struct {
	sites map[string]*domain.Site
	keys  map[string]*domain.APIKey
}

func (f *fakeSites) SiteByKey(_ context.Context, k string) (*domain.Site, error) {
	if s, ok := f.sites[k]; ok {
		return s, nil
	}
	return nil, domain.ErrNotFound
}

func (f *fakeSites) APIKey(_ context.Context, id string) (*domain.APIKey, error) {
	if k, ok := f.keys[id]; ok {
		return k, nil
	}
	return nil, domain.ErrNotFound
}

type harness struct {
	g     *Gateway
	sites *fakeSites
	clock time.Time
	ip    netip.Addr
}

func hash(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

func newHarness(t *testing.T) *harness {
	mr := miniredis.RunT(t)
	rds := redis.New(mr.Addr(), redis.WithIdentity())
	keys, err := challenge.NewKeyring([]challenge.Key{{ID: "k1", Secret: bytes.Repeat([]byte("s"), 32), Active: true}})
	if err != nil {
		t.Fatal(err)
	}
	policy := map[string]domain.ActionPolicy{"login": {Enabled: true, Puzzles: 4, Difficulty: 16}, "off": {Enabled: false, Puzzles: 4, Difficulty: 16}}
	sites := &fakeSites{
		sites: map[string]*domain.Site{
			siteA: {ID: 1, PublicKey: siteA, Enabled: true, Origins: []string{originA}, Actions: policy},
			siteB: {ID: 2, PublicKey: siteB, Enabled: true, Origins: []string{"https://b.example"}, Actions: policy},
		},
		keys: map[string]*domain.APIKey{
			keyA: {KeyID: keyA, SiteID: 1, SecretHash: hash(secretA), Enabled: true},
			keyB: {KeyID: keyB, SiteID: 2, SecretHash: hash("secret-for-site-b"), Enabled: true},
		},
	}
	h := &harness{sites: sites, clock: time.Unix(1_790_000_000, 0), ip: netip.MustParseAddr("192.0.2.10")}
	h.g = &Gateway{
		Sites: sites, State: repository.NewState(rds), Limiter: repository.NewRateLimiter(rds), Keys: keys,
		Settings: Settings{ChallengeTTL: 120 * time.Second, TokenTTL: 60 * time.Second, ResultTTL: 600 * time.Second,
			ClockSkew: 5 * time.Second, MaxDifficulty: 1 << 20,
			ChallengePerIPPerMinute: 1000, SolvePerIPPerMinute: 1000, VerifyPerKeyPerMinute: 1000},
		Now: func() time.Time { return h.clock },
	}
	return h
}

func (h *harness) issue(t *testing.T) *IssueOutput {
	t.Helper()
	out, _, err := h.g.Issue(context.Background(), IssueInput{SiteKey: siteA, Action: "login", Origin: originA, ClientIP: h.ip})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	return out
}

func (h *harness) solve(c *IssueOutput, nonces []string) (*SolveOutput, error) {
	out, _, err := h.g.Solve(context.Background(), SolveInput{SiteKey: siteA, Challenge: c.Challenge, Nonces: nonces, Origin: originA, ClientIP: h.ip})
	return out, err
}

func (h *harness) token(t *testing.T) string {
	t.Helper()
	c := h.issue(t)
	nonces, _ := pow.Solve(c.Algorithm, c.ChallengeID, c.Params)
	out, err := h.solve(c, nonces)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	return out.Token
}

func (h *harness) verify(token, action, requestID string) (*VerifyOutput, error) {
	return h.g.Verify(context.Background(), VerifyInput{Authorization: "Bearer " + keyA + "." + secretA,
		SiteKey: siteA, Token: token, Action: action, RequestID: requestID})
}

func code(err error) string {
	var e *errx.Error
	if errors.As(err, &e) {
		return e.Code
	}
	if err == nil {
		return ""
	}
	return "untyped:" + err.Error()
}

func TestHappyPathAndReplay(t *testing.T) {
	h := newHarness(t)
	tok := h.token(t)
	out, err := h.verify(tok, "login", "req-1")
	if err != nil || !out.Success || out.SiteKey != siteA || out.Action != "login" || out.VerifiedAt != h.clock.Unix() {
		t.Fatalf("verify: %+v %v", out, err)
	}
	h.clock = h.clock.Add(30 * time.Second)
	again, err := h.verify(tok, "login", "req-1")
	if err != nil || !again.Success || again.VerifiedAt != out.VerifiedAt {
		t.Fatalf("replay: %+v %v", again, err)
	}
	other, err := h.verify(tok, "login", "req-2")
	if err != nil || other.Success || other.Reason != "TOKEN_CONSUMED" {
		t.Fatalf("second request: %+v %v", other, err)
	}
}

func TestVerifyRejectionsAre200(t *testing.T) {
	h := newHarness(t)
	tok := h.token(t)
	forged := "bgt1." + siteA + "." + strings.Repeat("A", 43)
	crossSite := "bgt1." + siteB + "." + strings.Repeat("A", 43)
	for name, c := range map[string]struct{ token, action, want string }{
		"empty":           {"", "login", "TOKEN_INVALID"},
		"garbage":         {"not-a-token", "login", "TOKEN_INVALID"},
		"forged":          {forged, "login", "TOKEN_INVALID"},
		"other site":      {crossSite, "login", "SITE_MISMATCH"},
		"action mismatch": {tok, "signup", "ACTION_MISMATCH"},
	} {
		out, err := h.verify(c.token, c.action, "req-"+strings.ReplaceAll(name, " ", "-"))
		if err != nil || out.Success || out.Reason != c.want {
			t.Errorf("%s: %+v %v", name, out, err)
		}
	}
	h.clock = h.clock.Add(61 * time.Second)
	if out, _ := h.verify(tok, "login", "req-late"); out.Reason != "TOKEN_EXPIRED" {
		t.Fatalf("expired: %+v", out)
	}
}

func TestVerifyCallerErrors(t *testing.T) {
	h := newHarness(t)
	tok := h.token(t)
	if _, err := h.verify(tok, "login", "bad id with spaces"); code(err) != "INVALID_REQUEST" {
		t.Fatalf("bad request_id: %v", err)
	}
	if _, err := h.g.Verify(context.Background(), VerifyInput{Authorization: "Bearer " + keyA + ".wrong",
		SiteKey: siteA, Token: tok, Action: "login", RequestID: "r"}); code(err) != "UNAUTHORIZED" {
		t.Fatalf("wrong secret: %v", err)
	}
	if _, err := h.g.Verify(context.Background(), VerifyInput{Authorization: "Bearer " + keyB + ".secret-for-site-b",
		SiteKey: siteA, Token: tok, Action: "login", RequestID: "r"}); code(err) != "SITE_FORBIDDEN" {
		t.Fatalf("key for other site: %v", err)
	}
	if _, err := h.verify(tok, "login", "same"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.verify("garbage", "login", "same"); code(err) != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("conflict with malformed token: %v", err)
	}
}

func TestRevocationBeatsCachedPass(t *testing.T) {
	h := newHarness(t)
	tok := h.token(t)
	if out, err := h.verify(tok, "login", "req"); err != nil || !out.Success {
		t.Fatal(err)
	}
	h.sites.keys[keyA].Enabled = false
	if _, err := h.verify(tok, "login", "req"); code(err) != "UNAUTHORIZED" {
		t.Fatalf("revoked key replay: %v", err)
	}
	h.sites.keys[keyA].Enabled = true
	h.sites.sites[siteA].Enabled = false
	if _, err := h.verify(tok, "login", "req"); code(err) != "SITE_FORBIDDEN" {
		t.Fatalf("disabled site replay: %v", err)
	}
}

func TestSolveRules(t *testing.T) {
	h := newHarness(t)
	c := h.issue(t)
	nonces, _ := pow.Solve(c.Algorithm, c.ChallengeID, c.Params)

	// Client-side copies of params are not trusted: only the signed payload counts.
	easy, _ := pow.NewParams(1, 1)
	tampered := *c
	tampered.Params = easy
	if _, err := h.solve(&tampered, []string{"0"}); code(err) != "INVALID_REQUEST" {
		t.Fatalf("count from client params: %v", err)
	}

	wrong := append([]string(nil), nonces...)
	wrong[0] = "99999999999"
	for pow.Verify(c.Algorithm, c.ChallengeID, c.Params, wrong) == nil {
		wrong[0] += "1"
	}
	if _, err := h.solve(c, wrong); code(err) != "PROOF_INVALID" {
		t.Fatalf("wrong proof: %v", err)
	}
	// One attempt per challenge, even after a wrong proof.
	if _, err := h.solve(c, nonces); code(err) != "CHALLENGE_USED" {
		t.Fatalf("reuse: %v", err)
	}

	c2 := h.issue(t)
	parts := strings.Split(c2.Challenge, ".")
	parts[2] = parts[2][:len(parts[2])-2] + "AA"
	bad := *c2
	bad.Challenge = strings.Join(parts, ".")
	if _, err := h.solve(&bad, nonces); code(err) != "CHALLENGE_INVALID" {
		t.Fatalf("tampered payload: %v", err)
	}

	h.clock = h.clock.Add(126 * time.Second)
	n2, _ := pow.Solve(c2.Algorithm, c2.ChallengeID, c2.Params)
	if _, err := h.solve(c2, n2); code(err) != "CHALLENGE_EXPIRED" {
		t.Fatalf("expired: %v", err)
	}
}

func TestSolveBindsSiteAndOrigin(t *testing.T) {
	h := newHarness(t)
	c := h.issue(t)
	nonces, _ := pow.Solve(c.Algorithm, c.ChallengeID, c.Params)
	ctx := context.Background()
	if _, _, err := h.g.Solve(ctx, SolveInput{SiteKey: siteB, Challenge: c.Challenge, Nonces: nonces, Origin: "https://b.example", ClientIP: h.ip}); code(err) != "SITE_FORBIDDEN" {
		t.Fatalf("other site: %v", err)
	}
	if _, _, err := h.g.Solve(ctx, SolveInput{SiteKey: siteA, Challenge: c.Challenge, Nonces: nonces, Origin: "https://evil.example", ClientIP: h.ip}); code(err) != "ORIGIN_NOT_ALLOWED" {
		t.Fatalf("other origin: %v", err)
	}
	// Removing the origin after issuance takes effect immediately.
	h.sites.sites[siteA].Origins = nil
	if _, err := h.solve(c, nonces); code(err) != "ORIGIN_NOT_ALLOWED" {
		t.Fatalf("removed origin: %v", err)
	}
}

func TestIssueRules(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	for name, c := range map[string]struct {
		in   IssueInput
		want string
	}{
		"missing origin": {IssueInput{SiteKey: siteA, Action: "login"}, "ORIGIN_NOT_ALLOWED"},
		"wrong origin":   {IssueInput{SiteKey: siteA, Action: "login", Origin: "https://evil.example"}, "ORIGIN_NOT_ALLOWED"},
		"unknown site":   {IssueInput{SiteKey: "unknown_site", Action: "login", Origin: originA}, "SITE_FORBIDDEN"},
		"disabled":       {IssueInput{SiteKey: siteA, Action: "off", Origin: originA}, "ACTION_FORBIDDEN"},
		"unknown action": {IssueInput{SiteKey: siteA, Action: "nope", Origin: originA}, "ACTION_FORBIDDEN"},
		"bad action":     {IssueInput{SiteKey: siteA, Action: "Bad Action", Origin: originA}, "INVALID_REQUEST"},
	} {
		c.in.ClientIP = h.ip
		if _, _, err := h.g.Issue(ctx, c.in); code(err) != c.want {
			t.Errorf("%s: %v", name, err)
		}
	}
	h.g.Settings.ChallengePerIPPerMinute = 1
	h.ip = netip.MustParseAddr("2001:db8::1")
	h.issue(t)
	// Same /64 shares the bucket.
	h.ip = netip.MustParseAddr("2001:db8::2")
	if _, _, err := h.g.Issue(ctx, IssueInput{SiteKey: siteA, Action: "login", Origin: originA, ClientIP: h.ip}); code(err) != "RATE_LIMITED" {
		t.Fatalf("rate limit: %v", err)
	}
}
