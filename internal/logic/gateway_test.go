package logic

import (
	"bytes"
	"context"
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
	appA    = "app_a_public"
	appB    = "app_b_public"
	originA = "https://a.example"
	secretA = "secret-for-app-a"
	keyA    = "key_app_a"
	keyB    = "key_app_b"
)

type fakeApps struct {
	apps    map[string]*domain.App
	secrets map[string]*domain.SecretPair
}

func (f *fakeApps) AppByKey(_ context.Context, k string) (*domain.App, error) {
	if s, ok := f.apps[k]; ok {
		return s, nil
	}
	return nil, domain.ErrNotFound
}

func (f *fakeApps) SecretPair(_ context.Context, id string) (*domain.SecretPair, error) {
	if k, ok := f.secrets[id]; ok {
		return k, nil
	}
	return nil, domain.ErrNotFound
}

type harness struct {
	redis *miniredis.Miniredis
	g     *Gateway
	apps  *fakeApps
	clock time.Time
	ip    netip.Addr
}

func newHarness(t *testing.T) *harness {
	mr := miniredis.RunT(t)
	rds := redis.New(mr.Addr(), redis.WithIdentity())
	keys, err := challenge.NewKeyring([]challenge.Key{{ID: "k1", Secret: bytes.Repeat([]byte("s"), 32), Active: true}})
	if err != nil {
		t.Fatal(err)
	}
	policy := map[string]domain.ActionPolicy{
		"login":    {Enabled: true, Mode: domain.ModeInvisible, Puzzles: 4, Difficulty: 16},
		"off":      {Enabled: false, Mode: domain.ModeInvisible, Puzzles: 4, Difficulty: 16},
		"adaptive": {Enabled: true, Mode: domain.ModeAdaptive, Kinds: []string{domain.KindSlider}, Puzzles: 2, Difficulty: 16},
		"slide":    {Enabled: true, Mode: domain.ModeInteractive, Kinds: []string{domain.KindSlider}, Puzzles: 2, Difficulty: 16},
		"clicks":   {Enabled: true, Mode: domain.ModeInteractive, Kinds: []string{domain.KindClickShape}, Puzzles: 2, Difficulty: 16},
		"turn":     {Enabled: true, Mode: domain.ModeInteractive, Kinds: []string{domain.KindRotate}, Puzzles: 2, Difficulty: 16},
		"swap":     {Enabled: true, Mode: domain.ModeInteractive, Kinds: []string{domain.KindJigsaw}, Puzzles: 2, Difficulty: 16},
		"icons":    {Enabled: true, Mode: domain.ModeInteractive, Kinds: []string{domain.KindClickIcon}, Puzzles: 2, Difficulty: 16},
		"order":    {Enabled: true, Mode: domain.ModeInteractive, Kinds: []string{domain.KindClickOrder}, Puzzles: 2, Difficulty: 16},
		"where":    {Enabled: true, Mode: domain.ModeInteractive, Kinds: []string{domain.KindSpatial}, Puzzles: 2, Difficulty: 16},
		"time":     {Enabled: true, Mode: domain.ModeInteractive, Kinds: []string{domain.KindClock}, Puzzles: 2, Difficulty: 16},
		"mixed":    {Enabled: true, Mode: domain.ModeAdaptive, Kinds: domain.InteractiveKinds, Puzzles: 2, Difficulty: 16},
	}
	apps := &fakeApps{
		apps: map[string]*domain.App{
			appA: {ID: 1, AppKey: appA, Enabled: true, Origins: []string{originA}, Actions: policy},
			appB: {ID: 2, AppKey: appB, Enabled: true, Origins: []string{"https://b.example"}, Actions: policy},
		},
		secrets: map[string]*domain.SecretPair{
			keyA: {SecretID: keyA, AppID: 1, SecretKey: []byte(secretA), Enabled: true},
			keyB: {SecretID: keyB, AppID: 2, SecretKey: []byte("secret-for-app-b"), Enabled: true},
		},
	}
	h := &harness{redis: mr, apps: apps, clock: time.Unix(1_790_000_000, 0), ip: netip.MustParseAddr("192.0.2.10")}
	h.g = &Gateway{
		Apps: apps, State: repository.NewState(rds), Stats: repository.NewStats(rds), Limiter: repository.NewRateLimiter(rds), Keys: keys,
		Settings: Settings{ChallengeTTL: 120 * time.Second, TokenTTL: 60 * time.Second, ResultTTL: 600 * time.Second,
			ClockSkew: 5 * time.Second, MaxDifficulty: 1 << 20, SignatureWindow: 300 * time.Second,
			ChallengePerIPPerMinute: 1000, SolvePerIPPerMinute: 1000, VerifyPerKeyPerMinute: 1000, FallbackPerIPPerMinute: 5, AuthFailuresPerIPPerMinute: 60,
			RiskMedium: 30, RiskHigh: 70, HighDifficultyFactor: 4, FallbackDifficultyFactor: 8, FailureWindow: 10 * time.Minute,
			SliderTolerance: 4, RotateToleranceDegrees: 7, IPHashKey: []byte("ip-salt")},
		Now: func() time.Time { return h.clock },
	}
	return h
}

func (h *harness) issue(t *testing.T) *IssueOutput {
	t.Helper()
	out, _, err := h.g.Issue(context.Background(), IssueInput{AppKey: appA, Action: "login", Origin: originA, ClientIP: h.ip})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	return out
}

func (h *harness) solve(c *IssueOutput, nonces []string) (*SolveOutput, error) {
	out, _, err := h.g.Solve(context.Background(), SolveInput{AppKey: appA, Challenge: c.Challenge, Nonces: nonces, Origin: originA, ClientIP: h.ip})
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
	return h.g.Verify(context.Background(), signed(keyA, secretA, h.clock, appA, token, action, requestID))
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
	if err != nil || !out.Success || out.AppKey != appA || out.Action != "login" || out.VerifiedAt != h.clock.Unix() {
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
	forged := "bgt1." + appA + "." + strings.Repeat("A", 43)
	crossApp := "bgt1." + appB + "." + strings.Repeat("A", 43)
	for name, c := range map[string]struct{ token, action, want string }{
		"empty":           {"", "login", "TOKEN_INVALID"},
		"garbage":         {"not-a-token", "login", "TOKEN_INVALID"},
		"forged":          {forged, "login", "TOKEN_INVALID"},
		"other app":       {crossApp, "login", "APP_MISMATCH"},
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
	if _, err := h.g.Verify(context.Background(), signed(keyA, "wrong", h.clock, appA, tok, "login", "r")); code(err) != "UNAUTHORIZED" {
		t.Fatalf("wrong secret: %v", err)
	}
	if _, err := h.g.Verify(context.Background(), signed(keyB, "secret-for-app-b", h.clock, appA, tok, "login", "r")); code(err) != "APP_FORBIDDEN" {
		t.Fatalf("key for other app: %v", err)
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
	h.apps.secrets[keyA].Enabled = false
	if _, err := h.verify(tok, "login", "req"); code(err) != "UNAUTHORIZED" {
		t.Fatalf("revoked key replay: %v", err)
	}
	h.apps.secrets[keyA].Enabled = true
	h.apps.apps[appA].Enabled = false
	if _, err := h.verify(tok, "login", "req"); code(err) != "APP_FORBIDDEN" {
		t.Fatalf("disabled app replay: %v", err)
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

func TestSolveBindsAppAndOrigin(t *testing.T) {
	h := newHarness(t)
	c := h.issue(t)
	nonces, _ := pow.Solve(c.Algorithm, c.ChallengeID, c.Params)
	ctx := context.Background()
	if _, _, err := h.g.Solve(ctx, SolveInput{AppKey: appB, Challenge: c.Challenge, Nonces: nonces, Origin: "https://b.example", ClientIP: h.ip}); code(err) != "APP_FORBIDDEN" {
		t.Fatalf("other app: %v", err)
	}
	if _, _, err := h.g.Solve(ctx, SolveInput{AppKey: appA, Challenge: c.Challenge, Nonces: nonces, Origin: "https://evil.example", ClientIP: h.ip}); code(err) != "ORIGIN_NOT_ALLOWED" {
		t.Fatalf("other origin: %v", err)
	}
	// Removing the origin after issuance takes effect immediately.
	h.apps.apps[appA].Origins = nil
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
		"missing origin": {IssueInput{AppKey: appA, Action: "login"}, "ORIGIN_NOT_ALLOWED"},
		"wrong origin":   {IssueInput{AppKey: appA, Action: "login", Origin: "https://evil.example"}, "ORIGIN_NOT_ALLOWED"},
		"unknown app":    {IssueInput{AppKey: "unknown_app", Action: "login", Origin: originA}, "APP_FORBIDDEN"},
		"disabled":       {IssueInput{AppKey: appA, Action: "off", Origin: originA}, "ACTION_FORBIDDEN"},
		"unknown action": {IssueInput{AppKey: appA, Action: "nope", Origin: originA}, "ACTION_FORBIDDEN"},
		"bad action":     {IssueInput{AppKey: appA, Action: "Bad Action", Origin: originA}, "INVALID_REQUEST"},
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
	if _, _, err := h.g.Issue(ctx, IssueInput{AppKey: appA, Action: "login", Origin: originA, ClientIP: h.ip}); code(err) != "RATE_LIMITED" {
		t.Fatalf("rate limit: %v", err)
	}
}

func TestSignatureRules(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	tok := h.token(t)

	tampered := signed(keyA, secretA, h.clock, appA, tok, "login", "r-tamper")
	tampered.Body = append([]byte(nil), tampered.Body...)
	tampered.Body[len(tampered.Body)-2] = 'X'
	if _, err := h.g.Verify(ctx, tampered); code(err) != "UNAUTHORIZED" {
		t.Fatalf("tampered body: %v", err)
	}

	for name, skew := range map[string]time.Duration{"too old": -301 * time.Second, "too new": 301 * time.Second} {
		in := signed(keyA, secretA, h.clock.Add(skew), appA, tok, "login", "r-ts")
		if _, err := h.g.Verify(ctx, in); code(err) != "TIMESTAMP_EXPIRED" {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := h.g.Verify(ctx, signed(keyA, secretA, h.clock.Add(-299*time.Second), appA, tok, "login", "r-edge")); err != nil {
		t.Fatalf("inside window: %v", err)
	}

	in := signed(keyA, secretA, h.clock, appA, tok, "login", "r-replay")
	if _, err := h.g.Verify(ctx, in); err != nil {
		t.Fatal(err)
	}
	if _, err := h.g.Verify(ctx, in); code(err) != "NONCE_REUSED" {
		t.Fatalf("replayed signed request: %v", err)
	}
	// An SDK retry re-signs with a new nonce and gets the idempotent result.
	if out, err := h.g.Verify(ctx, signed(keyA, secretA, h.clock, appA, tok, "login", "r-replay")); err != nil || out.Reason != "TOKEN_CONSUMED" {
		t.Fatalf("re-signed retry: %+v %v", out, err)
	}

	for name, mutate := range map[string]func(*VerifyInput){
		"unknown secret": func(v *VerifyInput) { v.SecretID = "sid_unknown_1" },
		"short nonce":    func(v *VerifyInput) { v.Nonce = "short" },
		"bad timestamp":  func(v *VerifyInput) { v.Timestamp = "soon" },
		"upper hex sig":  func(v *VerifyInput) { v.Signature = strings.ToUpper(v.Signature) },
		"missing sig":    func(v *VerifyInput) { v.Signature = "" },
	} {
		v := signed(keyA, secretA, h.clock, appA, tok, "login", "r-fmt")
		mutate(&v)
		if _, err := h.g.Verify(ctx, v); code(err) != "UNAUTHORIZED" {
			t.Errorf("%s: %v", name, err)
		}
	}
}
