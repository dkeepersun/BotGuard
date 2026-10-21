package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/service/sealer"
	"github.com/zeromicro/go-zero/core/collection"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const (
	statusEnabled = 1
	// legacyModeSlider is the pre-0004 name of interactive + [slider].
	legacyModeSlider = "slider"
)

// AppStore is what the gateway needs from app configuration.
type AppStore interface {
	AppByKey(ctx context.Context, appKey string) (*domain.App, error)
	SecretPair(ctx context.Context, secretID string) (*domain.SecretPair, error)
}

// KeyLister lists every app key and secret id, whatever their status, for
// CachedApps' index of known keys.
type KeyLister interface {
	KnownKeys(ctx context.Context) (appKeys, secretIDs []string, err error)
}

// MySQLApps reads app configuration. It is read-only; writes belong to admin.
type MySQLApps struct {
	conn   sqlx.SqlConn
	sealer *sealer.Sealer
}

func NewMySQLApps(conn sqlx.SqlConn, s *sealer.Sealer) *MySQLApps {
	return &MySQLApps{conn: conn, sealer: s}
}

type appRow struct {
	ID            int64  `db:"id"`
	AppKey        string `db:"app_key"`
	Status        int    `db:"status"`
	PolicyVersion int64  `db:"active_policy_version"`
}

type actionRow struct {
	Action     string `db:"action"`
	Enabled    bool   `db:"enabled"`
	Mode       string `db:"challenge_mode"`
	Kinds      string `db:"challenge_kinds"`
	Puzzles    int    `db:"pow_puzzles"`
	Difficulty uint64 `db:"pow_difficulty"`
}

type secretRow struct {
	SecretID   string         `db:"secret_id"`
	AppID      int64          `db:"app_id"`
	Ciphertext []byte         `db:"secret_ciphertext"`
	KEKID      sql.NullString `db:"kek_id"`
	Status     int            `db:"status"`
	ExpiresAt  sql.NullTime   `db:"expires_at"`
}

const secretColumns = "secret_id, app_id, secret_ciphertext, kek_id, status, expires_at"

func (r *MySQLApps) AppByKey(ctx context.Context, appKey string) (*domain.App, error) {
	var row appRow
	err := r.conn.QueryRowCtx(ctx, &row,
		"SELECT id, app_key, status, active_policy_version FROM botguard_app WHERE app_key = ?", appKey)
	if errors.Is(err, sqlx.ErrNotFound) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var origins []string
	if err := r.conn.QueryRowsCtx(ctx, &origins, "SELECT origin FROM botguard_app_origin WHERE app_id = ?", row.ID); err != nil {
		return nil, err
	}
	var actions []actionRow
	if err := r.conn.QueryRowsCtx(ctx, &actions,
		"SELECT action, enabled, challenge_mode, challenge_kinds, pow_puzzles, pow_difficulty FROM botguard_app_action WHERE app_id = ?", row.ID); err != nil {
		return nil, err
	}
	app := &domain.App{
		ID:            row.ID,
		AppKey:        row.AppKey,
		Enabled:       row.Status == statusEnabled,
		PolicyVersion: row.PolicyVersion,
		Actions:       make(map[string]domain.ActionPolicy, len(actions)),
	}
	for _, o := range origins {
		n, ok := domain.NormalizeOrigin(o)
		if !ok || n != o {
			// Admin writes normalized origins; anything else is ignored, not guessed at.
			logx.WithContext(ctx).Errorf("app %d has non-normalized origin, ignored", row.ID)
			continue
		}
		app.Origins = append(app.Origins, n)
	}
	for _, a := range actions {
		mode, kinds, known := storedMode(a.Mode, a.Kinds)
		if !known {
			logx.WithContext(ctx).Errorf("app %d action %s has unknown mode %q, using adaptive", row.ID, a.Action, a.Mode)
		}
		app.Actions[a.Action] = domain.ActionPolicy{Enabled: a.Enabled, Mode: mode, Kinds: kinds,
			Puzzles: a.Puzzles, Difficulty: a.Difficulty}
	}
	return app, nil
}

// SecretPair decrypts the secret key. A row without ciphertext (a revoked
// legacy Bearer key) is returned unusable; a ciphertext that does not open is
// an error, so a missing or wrong KEK fails closed instead of rejecting
// callers as if their signature were wrong.
func (r *MySQLApps) SecretPair(ctx context.Context, secretID string) (*domain.SecretPair, error) {
	var row secretRow
	err := r.conn.QueryRowCtx(ctx, &row, "SELECT "+secretColumns+" FROM botguard_app_secret WHERE secret_id = ?", secretID)
	if errors.Is(err, sqlx.ErrNotFound) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	k := &domain.SecretPair{SecretID: row.SecretID, AppID: row.AppID, Enabled: row.Status == statusEnabled}
	if row.ExpiresAt.Valid {
		t := row.ExpiresAt.Time
		k.ExpiresAt = &t
	}
	if len(row.Ciphertext) == 0 || !row.KEKID.Valid {
		k.Enabled = false
		return k, nil
	}
	if k.SecretKey, err = r.sealer.Open(row.KEKID.String, row.SecretID, row.Ciphertext); err != nil {
		return nil, fmt.Errorf("secret %s: %w", row.SecretID, err)
	}
	return k, nil
}

func (r *MySQLApps) KnownKeys(ctx context.Context) (appKeys, secretIDs []string, err error) {
	if err := r.conn.QueryRowsCtx(ctx, &appKeys, "SELECT app_key FROM botguard_app"); err != nil {
		return nil, nil, err
	}
	if err := r.conn.QueryRowsCtx(ctx, &secretIDs, "SELECT secret_id FROM botguard_app_secret"); err != nil {
		return nil, nil, err
	}
	return appKeys, secretIDs, nil
}

// CachedApps bounds both staleness (ttl) and memory (limit). Misses are
// cached separately, so repeated unknown keys cost no query and a flood of
// random keys cannot evict the apps and secret pairs that are in use.
//
// When the source is a KeyLister, CachedApps also keeps an index of every
// existing app key and secret id, reloaded every ttl. A key missing from the
// index is most likely made up, so looking it up spends from a small shared
// budget (unknownQueriesPerSecond) instead of costing a query each: a flood
// of random keys from many addresses cannot turn into MySQL load. The budget
// still lets a key created since the last reload through in normal traffic.
type CachedApps struct {
	src    AppStore
	apps   *collection.Cache
	keys   *collection.Cache
	misses *collection.Cache

	lister     KeyLister
	ttl        time.Duration
	now        func() time.Time
	index      atomic.Pointer[keyIndex]
	refreshing atomic.Bool
	unknown    *tokenBucket
}

const (
	// unknownQueriesPerSecond bounds lookups of keys missing from the index,
	// across all callers.
	unknownQueriesPerSecond = 20
	// indexMaxAgeTTLs is how many ttls an index stays in use while reloads
	// fail; after that every key is looked up again, as without an index.
	indexMaxAgeTTLs  = 6
	indexLoadTimeout = 5 * time.Second
)

type keyIndex struct {
	keys     map[string]struct{}
	loadedAt time.Time
}

func NewCachedApps(src AppStore, ttl time.Duration, limit int) (*CachedApps, error) {
	apps, err := collection.NewCache(ttl, collection.WithLimit(limit), collection.WithName("bg-apps"))
	if err != nil {
		return nil, err
	}
	keys, err := collection.NewCache(ttl, collection.WithLimit(limit), collection.WithName("bg-secret-pairs"))
	if err != nil {
		return nil, err
	}
	misses, err := collection.NewCache(ttl, collection.WithLimit(limit), collection.WithName("bg-misses"))
	if err != nil {
		return nil, err
	}
	c := &CachedApps{src: src, apps: apps, keys: keys, misses: misses, ttl: ttl, now: time.Now,
		unknown: newTokenBucket(unknownQueriesPerSecond, time.Now)}
	c.lister, _ = src.(KeyLister)
	return c, nil
}

func take[T any](c *CachedApps, cache *collection.Cache, key string, fetch func() (*T, error)) (*T, error) {
	if v, ok := cache.Get(key); ok {
		return v.(*T), nil
	}
	if _, ok := c.misses.Get(key); ok {
		return nil, domain.ErrNotFound
	}
	if !c.mayExist(key) {
		return nil, domain.ErrNotFound
	}
	v, err := cache.Take(key, func() (any, error) { return fetch() })
	if errors.Is(err, domain.ErrNotFound) {
		c.misses.Set(key, struct{}{})
	}
	if err != nil {
		return nil, err
	}
	return v.(*T), nil
}

// mayExist reports whether key is worth a query. Without a usable index
// every key is; otherwise keys in the index are, and the rest share the
// unknown-key budget.
func (c *CachedApps) mayExist(key string) bool {
	if c.lister == nil {
		return true
	}
	now := c.now()
	idx := c.index.Load()
	if idx == nil || now.Sub(idx.loadedAt) >= c.ttl {
		c.reloadAsync()
	}
	if idx == nil || now.Sub(idx.loadedAt) >= indexMaxAgeTTLs*c.ttl {
		return true
	}
	if _, ok := idx.keys[key]; ok {
		return true
	}
	return c.unknown.take()
}

// reloadAsync starts one reload in the background; lookups never wait for it.
func (c *CachedApps) reloadAsync() {
	if !c.refreshing.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer c.refreshing.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), indexLoadTimeout)
		defer cancel()
		if err := c.reload(ctx); err != nil {
			logx.WithContext(ctx).Errorf("load known app keys: %v", err)
		}
	}()
}

func (c *CachedApps) reload(ctx context.Context) error {
	appKeys, secretIDs, err := c.lister.KnownKeys(ctx)
	if err != nil {
		return err
	}
	idx := &keyIndex{keys: make(map[string]struct{}, len(appKeys)+len(secretIDs)), loadedAt: c.now()}
	for _, k := range appKeys {
		idx.keys["app:"+k] = struct{}{}
	}
	for _, k := range secretIDs {
		idx.keys["secret:"+k] = struct{}{}
	}
	c.index.Store(idx)
	return nil
}

func (c *CachedApps) AppByKey(ctx context.Context, appKey string) (*domain.App, error) {
	return take(c, c.apps, "app:"+appKey, func() (*domain.App, error) { return c.src.AppByKey(ctx, appKey) })
}

func (c *CachedApps) SecretPair(ctx context.Context, secretID string) (*domain.SecretPair, error) {
	return take(c, c.keys, "secret:"+secretID, func() (*domain.SecretPair, error) { return c.src.SecretPair(ctx, secretID) })
}

// PeekApp returns an app only if it is already cached; it never queries.
func (c *CachedApps) PeekApp(appKey string) (*domain.App, bool) {
	v, ok := c.apps.Get("app:" + appKey)
	if !ok {
		return nil, false
	}
	app, ok := v.(*domain.App)
	return app, ok
}

// storedMode interprets a stored mode. The legacy "slider" was written by
// botguard-ctl before migration 0004 (or by an old ctl against a migrated
// database) and meant "always the slider". Anything unknown becomes adaptive,
// never invisible: a typo must not silently drop to the weakest mode.
func storedMode(mode, kinds string) (string, []string, bool) {
	switch {
	case mode == legacyModeSlider:
		return domain.ModeInteractive, []string{domain.KindSlider}, true
	case domain.ValidMode(mode):
		return mode, ParseKinds(kinds), true
	default:
		return domain.ModeAdaptive, ParseKinds(kinds), false
	}
}

// ParseKinds reads a stored kind list, dropping unknown kinds and falling
// back to the slider so an action can always show an interactive challenge.
func ParseKinds(stored string) []string {
	var out []string
	for _, k := range strings.Split(stored, ",") {
		k = strings.TrimSpace(k)
		if domain.ValidInteractiveKind(k) && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	if len(out) == 0 {
		return []string{domain.KindSlider}
	}
	return out
}

// tokenBucket allows rate events per second with a burst of one second's
// worth.
type tokenBucket struct {
	mu     sync.Mutex
	rate   float64
	tokens float64
	last   time.Time
	now    func() time.Time
}

func newTokenBucket(rate float64, now func() time.Time) *tokenBucket {
	return &tokenBucket{rate: rate, tokens: rate, last: now(), now: now}
}

func (b *tokenBucket) take() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if now := b.now(); now.After(b.last) {
		b.tokens = min(b.rate, b.tokens+now.Sub(b.last).Seconds()*b.rate)
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
