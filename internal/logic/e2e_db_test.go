package logic

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/repository"
	"github.com/dkeepersun/BotGuard/internal/service/challenge"
	"github.com/dkeepersun/BotGuard/internal/service/credential"
	"github.com/dkeepersun/BotGuard/internal/service/pow"
	"github.com/dkeepersun/BotGuard/internal/service/sealer"
	"github.com/dkeepersun/BotGuard/internal/testdb"
	"github.com/zeromicro/go-zero/core/stores/redis"
)

// TestEndToEndWithDatabase drives the gateway against apps written by the
// management use case, so the read queries are checked against the real
// schema (SQLite, or MySQL with BOTGUARD_TEST_MYSQL_DSN).
func TestEndToEndWithDatabase(t *testing.T) {
	_, conn := testdb.Open(t)
	ctx := context.Background()
	clock := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }

	seal := testSealer(t)
	admin := &AppAdmin{Repo: repository.NewAppAdmin(conn), Sealer: seal, Now: now}
	app, err := admin.CreateApp(ctx, "e2e", "")
	if err != nil {
		t.Fatal(err)
	}
	const origin = "https://shop.example"
	if _, err := admin.AddOrigin(ctx, app, origin); err != nil {
		t.Fatal(err)
	}
	if err := admin.SetAction(ctx, app, "login", domain.ActionPolicy{Enabled: true, Mode: domain.ModeInvisible, Kinds: domain.InteractiveKinds, Puzzles: 3, Difficulty: 20}); err != nil {
		t.Fatal(err)
	}
	// Re-saving an identical policy changes nothing: no new policy version
	// (issued challenges stay valid) and no audit noise.
	before, _ := admin.AppDetail(ctx, app)
	if err := admin.SetAction(repository.WithActor(ctx, "cli:test", ""), app, "login", domain.ActionPolicy{Enabled: true, Mode: domain.ModeInvisible, Kinds: domain.InteractiveKinds, Puzzles: 3, Difficulty: 20}); err != nil {
		t.Fatal(err)
	}
	after, _ := admin.AppDetail(ctx, app)
	if before.PolicyVersion != after.PolicyVersion {
		t.Fatalf("unchanged policy bumped the version: %d -> %d", before.PolicyVersion, after.PolicyVersion)
	}
	if _, n, _ := admin.Repo.Audit(ctx, repository.AuditQuery{Operation: "action."}); n != 0 {
		t.Fatalf("unchanged policy audited %d times", n)
	}
	oldKey, _, err := admin.CreateSecret(ctx, app, 0)
	if err != nil {
		t.Fatal(err)
	}

	mr := miniredis.RunT(t)
	rds := redis.New(mr.Addr(), redis.WithIdentity())
	keys, _ := challenge.NewKeyring([]challenge.Key{{ID: "k1", Secret: bytes.Repeat([]byte("e"), 32), Active: true}})
	g := &Gateway{
		Apps: repository.NewSQLApps(conn, seal), State: repository.NewState(rds), Limiter: repository.NewRateLimiter(rds), Keys: keys,
		Settings: Settings{ChallengeTTL: 2 * time.Minute, TokenTTL: time.Minute, ResultTTL: 10 * time.Minute, ClockSkew: 5 * time.Second,
			MaxDifficulty: 1 << 20, SignatureWindow: 5 * time.Minute, ChallengePerIPPerMinute: 1000, SolvePerIPPerMinute: 1000, VerifyPerKeyPerMinute: 1000, AuthFailuresPerIPPerMinute: 60,
			RiskMedium: 30, RiskHigh: 70, HighDifficultyFactor: 4, FallbackDifficultyFactor: 8, FailureWindow: 10 * time.Minute, SliderTolerance: 4},
		Now: now,
	}
	ip := netip.MustParseAddr("198.51.100.4")
	token := func() string {
		t.Helper()
		c, _, err := g.Issue(ctx, IssueInput{AppKey: app, Action: "login", Origin: origin, ClientIP: ip})
		if err != nil {
			t.Fatalf("issue: %v", err)
		}
		if c.Params.Puzzles != 3 {
			t.Fatalf("policy not read from the database: %+v", c.Params)
		}
		nonces, _ := pow.Solve(c.Algorithm, c.ChallengeID, c.Params)
		out, _, err := g.Solve(ctx, SolveInput{AppKey: app, Challenge: c.Challenge, Nonces: nonces, Origin: origin, ClientIP: ip})
		if err != nil {
			t.Fatalf("solve: %v", err)
		}
		return out.Token
	}
	verify := func(p credential.SecretPair, tok, rid string) (*VerifyOutput, error) {
		return g.Verify(ctx, signed(p.SecretID, p.SecretKey, clock, app, tok, "login", rid))
	}

	if out, err := verify(oldKey, token(), "r1"); err != nil || !out.Success {
		t.Fatalf("first verify: %+v %v", out, err)
	}

	// Rotation: both keys work during the grace period, only the new one after.
	newKey, _, err := admin.RotateSecret(ctx, oldKey.SecretID, time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := verify(oldKey, token(), "r2"); err != nil || !out.Success {
		t.Fatalf("old key in grace: %+v %v", out, err)
	}
	clock = clock.Add(time.Hour)
	if _, err := verify(oldKey, token(), "r3"); code(err) != "UNAUTHORIZED" {
		t.Fatalf("old key after grace: %v", err)
	}
	if out, err := verify(newKey, token(), "r4"); err != nil || !out.Success {
		t.Fatalf("new key: %+v %v", out, err)
	}

	// Removing the origin rejects tokens already issued for it.
	pending := token()
	if _, err := admin.RemoveOrigin(ctx, app, origin); err != nil {
		t.Fatal(err)
	}
	if out, err := verify(newKey, pending, "r5"); err != nil || out.Reason != "ORIGIN_NOT_ALLOWED" {
		t.Fatalf("removed origin: %+v %v", out, err)
	}
	if _, err := admin.AddOrigin(ctx, app, origin); err != nil {
		t.Fatal(err)
	}

	if err := admin.SetAppEnabled(ctx, app, false); err != nil {
		t.Fatal(err)
	}
	if _, err := verify(newKey, pending, "r6"); code(err) != "APP_FORBIDDEN" {
		t.Fatalf("disabled app: %v", err)
	}
	if err := admin.SetAppEnabled(ctx, app, true); err != nil {
		t.Fatal(err)
	}

	if err := admin.RevokeSecret(ctx, newKey.SecretID); err != nil {
		t.Fatal(err)
	}
	if _, err := verify(newKey, pending, "r7"); code(err) != "UNAUTHORIZED" {
		t.Fatalf("revoked key: %v", err)
	}
}

// TestCachedRevocationTakesEffectWithinTTL checks the staleness bound the docs promise.
func TestCachedRevocationTakesEffectWithinTTL(t *testing.T) {
	_, conn := testdb.Open(t)
	ctx := context.Background()
	seal := testSealer(t)
	admin := &AppAdmin{Repo: repository.NewAppAdmin(conn), Sealer: seal, Now: time.Now}
	app, _ := admin.CreateApp(ctx, "cache", "")
	key, _, err := admin.CreateSecret(ctx, app, 0)
	if err != nil {
		t.Fatal(err)
	}
	cached, err := repository.NewCachedApps(repository.NewSQLApps(conn, seal), time.Second, 100)
	if err != nil {
		t.Fatal(err)
	}
	if k, err := cached.SecretPair(ctx, key.SecretID); err != nil || !k.Usable(time.Now()) || string(k.SecretKey) != key.SecretKey {
		t.Fatalf("before revoke: %v", err)
	}
	if err := admin.RevokeSecret(ctx, key.SecretID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if k, err := cached.SecretPair(ctx, key.SecretID); err != nil || k.Usable(time.Now()) {
		t.Fatalf("after TTL the key must be unusable: %+v %v", k, err)
	}
}

// TestKnownKeysListsAppsAndSecrets checks the query behind CachedApps' index,
// revoked secrets included.
func TestKnownKeysListsAppsAndSecrets(t *testing.T) {
	_, conn := testdb.Open(t)
	ctx := context.Background()
	seal := testSealer(t)
	admin := &AppAdmin{Repo: repository.NewAppAdmin(conn), Sealer: seal, Now: time.Now}
	app, _ := admin.CreateApp(ctx, "index", "")
	key, _, err := admin.CreateSecret(ctx, app, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.RevokeSecret(ctx, key.SecretID); err != nil {
		t.Fatal(err)
	}
	appKeys, secretIDs, err := repository.NewSQLApps(conn, seal).KnownKeys(ctx)
	if err != nil || !slices.Contains(appKeys, app) || !slices.Contains(secretIDs, key.SecretID) {
		t.Fatalf("known keys: %v %v %v", appKeys, secretIDs, err)
	}
}

func testSealer(t *testing.T) *sealer.Sealer {
	t.Helper()
	s, err := sealer.New("t1:" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestWrongKEKFailsClosed: a secret sealed under a KEK the gateway does not
// have must surface as 503, not as an authentication failure.
func TestWrongKEKFailsClosed(t *testing.T) {
	_, conn := testdb.Open(t)
	ctx := context.Background()
	admin := &AppAdmin{Repo: repository.NewAppAdmin(conn), Sealer: testSealer(t), Now: time.Now}
	app, _ := admin.CreateApp(ctx, "kek", "")
	key, _, err := admin.CreateSecret(ctx, app, 0)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := sealer.New("t2:" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32)))
	if _, err := repository.NewSQLApps(conn, other).SecretPair(ctx, key.SecretID); !errors.Is(err, sealer.ErrUnknownKEK) {
		t.Fatalf("unknown KEK: %v", err)
	}
	db, err := conn.RawDB()
	if err != nil {
		t.Fatal(err)
	}
	var stored []byte
	if err := db.QueryRowContext(ctx, "SELECT secret_ciphertext FROM botguard_app_secret WHERE secret_id = ?", key.SecretID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte(key.SecretKey)) {
		t.Fatal("secret key stored in plaintext")
	}
}

// App names end up in console markup (tabs, the app switcher); markup in
// them is refused outright, on top of the escaping at render time.
func TestAppTextRejectsMarkup(t *testing.T) {
	_, conn := testdb.Open(t)
	ctx := context.Background()
	admin := &AppAdmin{Repo: repository.NewAppAdmin(conn), Sealer: testSealer(t), Now: time.Now}
	for _, name := range []string{"Shop<script>x</script>", "a > b"} {
		if _, err := admin.CreateApp(ctx, name, ""); !errors.Is(err, ErrInvalid) {
			t.Errorf("name %q: %v", name, err)
		}
	}
	if _, err := admin.CreateApp(ctx, "Shop", "<img src=x>"); !errors.Is(err, ErrInvalid) {
		t.Errorf("remark: %v", err)
	}
	key, err := admin.CreateApp(ctx, "商城 & 积分", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.UpdateApp(ctx, key, "x<y", ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("update: %v", err)
	}
}

// The page-selection switch is stored per action and read back by the
// gateway's app loader; saving it again is a no-op.
func TestActionClientSelectRoundTrip(t *testing.T) {
	_, conn := testdb.Open(t)
	ctx := context.Background()
	seal := testSealer(t)
	admin := &AppAdmin{Repo: repository.NewAppAdmin(conn), Sealer: seal, Now: time.Now}
	key, err := admin.CreateApp(ctx, "select", "")
	if err != nil {
		t.Fatal(err)
	}
	p := domain.ActionPolicy{Enabled: true, Mode: domain.ModeAdaptive, Kinds: []string{domain.KindSlider, domain.KindSpatial}, Puzzles: 2, Difficulty: 10}
	if err := admin.SetAction(ctx, key, "login", p); err != nil {
		t.Fatal(err)
	}
	apps := repository.NewSQLApps(conn, seal)
	app, err := apps.AppByKey(ctx, key)
	if err != nil || app.Actions["login"].ClientSelect {
		t.Fatalf("default must be off: %+v %v", app, err)
	}
	p.ClientSelect = true
	if err := admin.SetAction(ctx, key, "login", p); err != nil {
		t.Fatal(err)
	}
	app, _ = apps.AppByKey(ctx, key)
	if !app.Actions["login"].ClientSelect || app.PolicyVersion < 3 {
		t.Fatalf("switch not stored: %+v", app)
	}
	d, _ := admin.AppDetail(ctx, key)
	if len(d.Actions) != 1 || !d.Actions[0].Client {
		t.Fatalf("detail: %+v", d.Actions)
	}
}
