// Package svc wires configuration into the gateway's dependencies.
package svc

import (
	"context"
	"crypto/sha256"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/dkeepersun/BotGuard/internal/config"
	"github.com/dkeepersun/BotGuard/internal/logic"
	"github.com/dkeepersun/BotGuard/internal/migration"
	"github.com/dkeepersun/BotGuard/internal/repository"
	"github.com/dkeepersun/BotGuard/internal/service/clientip"
	"github.com/dkeepersun/BotGuard/internal/store"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/redis"
)

type Gateway struct {
	Logic   *logic.Gateway
	IPs     *clientip.Resolver
	Monitor *logic.Monitor
	rds     *redis.Redis
	db      *store.DB
	// schema is the migration version this binary needs; schemaOK latches
	// once the database has it, since migrations only move forward.
	schema   int
	schemaOK atomic.Bool
}

func NewGateway(c config.GatewayConfig) (*Gateway, error) {
	keys, err := c.Keyring()
	if err != nil {
		return nil, err
	}
	ips, err := clientip.New(c.TrustedProxies)
	if err != nil {
		return nil, err
	}
	rds, err := redis.NewRedis(c.Redis)
	if err != nil {
		return nil, err
	}
	dbc, err := c.DB()
	if err != nil {
		return nil, err
	}
	db, err := store.Open(dbc)
	if err != nil {
		return nil, err
	}
	// SQLite is migrated here; a MySQL schema waits for botguard-ctl and
	// keeps /readyz at schema_outdated until then.
	if _, err := migration.Prepare(context.Background(), db); err != nil {
		return nil, err
	}
	logx.Infof("database: %s %s", db.Driver, db.Name)
	seal, err := c.Sealer()
	if err != nil {
		return nil, err
	}
	schema, err := migration.Latest(db.Driver)
	if err != nil {
		return nil, err
	}
	apps, err := repository.NewCachedApps(repository.NewSQLApps(db, seal), config.Seconds(c.AppCache.TTLSeconds), c.AppCache.Limit)
	if err != nil {
		return nil, err
	}
	gw := &Gateway{
		Logic: &logic.Gateway{
			Apps:    apps,
			State:   repository.NewState(rds),
			Stats:   repository.NewStats(rds),
			Limiter: repository.NewRateLimiter(rds),
			Keys:    keys,
			Settings: logic.Settings{
				ChallengeTTL:                  config.Seconds(c.Challenge.TTLSeconds),
				TokenTTL:                      config.Seconds(c.TokenTTLSeconds),
				ResultTTL:                     config.Seconds(c.VerifyResultTTLSeconds),
				ClockSkew:                     config.Seconds(c.ClockSkewSeconds),
				MaxDifficulty:                 c.Challenge.MaxDifficulty,
				SignatureWindow:               config.Seconds(c.SignatureWindowSeconds),
				ChallengePerIPPerMinute:       c.RateLimit.ChallengePerIPPerMinute,
				SolvePerIPPerMinute:           c.RateLimit.SolvePerIPPerMinute,
				VerifyPerKeyPerMinute:         c.RateLimit.VerifyPerKeyPerMinute,
				FallbackPerIPPerMinute:        c.RateLimit.FallbackPerIPPerMinute,
				AuthFailuresPerIPPerMinute:    c.RateLimit.AuthFailuresPerIPPerMinute,
				InteractivePerAppPerMinute:    c.RateLimit.InteractivePerAppPerMinute,
				InteractivePerPrefixPerMinute: c.RateLimit.InteractivePerPrefixPerMinute,
				ChallengePerPrefixPerMinute:   c.RateLimit.ChallengePerPrefixPerMinute,

				RiskMedium:               c.Risk.MediumThreshold,
				RiskHigh:                 c.Risk.HighThreshold,
				HighDifficultyFactor:     uint64(c.Risk.HighDifficultyFactor),
				FallbackDifficultyFactor: uint64(c.Risk.FallbackDifficultyFactor),
				DegradedDifficultyFactor: uint64(c.Risk.DegradedDifficultyFactor),
				DegradedMinDifficulty:    uint64(c.Risk.DegradedMinDifficulty),
				FailureWindow:            config.Seconds(c.Risk.FailureWindowSeconds),
				FailureBlockThreshold:    c.Risk.FailureBlockThreshold,
				LadderDowngradePasses:    c.Risk.LadderDowngradePasses,
				LadderWindow:             config.Seconds(c.Risk.LadderWindowSeconds),
				SliderTolerance:          c.Slider.Tolerance,
				RotateToleranceDegrees:   c.Rotate.ToleranceDegrees,
				IPHashKey:                ipHashKey(c),
			},
			Now: time.Now,
		},
		IPs:    ips,
		rds:    rds,
		db:     db,
		schema: schema,
	}
	workers := c.PuzzlePool.Workers
	if workers == 0 {
		// Leave the other half of the generation slots to requests that
		// find the pool empty.
		workers = max(1, runtime.NumCPU()/2)
	}
	gw.Logic.StartPuzzlePool(context.Background(), c.PuzzlePool.PerKind, workers)
	instance := c.Monitor.Instance
	if instance == "" {
		host, err := os.Hostname()
		if err != nil {
			host = c.Host
		}
		instance = host + ":" + strconv.Itoa(c.Port)
	}
	gw.Monitor = &logic.Monitor{Instance: instance, Store: repository.NewHealthStore(rds),
		RateLimitedPerMinute: int64(c.Monitor.RateLimitedPerMinute), Pool: gw.Logic.PoolLevels}
	gw.Logic.Monitor = gw.Monitor
	go gw.Monitor.Run(context.Background())
	return gw, nil
}

// ipHashKey salts the IP tags in console events. It is derived from the
// active challenge key, so it is secret, shared by all replicas, and rotates
// with that key.
func ipHashKey(c config.GatewayConfig) []byte {
	for _, k := range c.Challenge.Keys {
		if k.Active {
			sum := sha256.Sum256([]byte("botguard ip tag v1\x00" + k.Secret))
			return sum[:]
		}
	}
	return nil
}

// Ready reports whether both stores answer and the schema is migrated. The
// gateway fails closed without either store, so readiness must not pass on a
// partial outage; and a gateway newer than the schema would fail every
// challenge, so during an upgrade it stays out of rotation until
// "botguard-ctl migrate up" has run.
func (g *Gateway) Ready(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if !g.rds.PingCtx(ctx) {
		return "store_unavailable"
	}
	raw, err := g.db.Raw()
	if err != nil || raw.PingContext(ctx) != nil {
		return "store_unavailable"
	}
	if g.schemaOK.Load() {
		return ""
	}
	applied, err := migration.Applied(ctx, raw, g.db.Driver)
	if err != nil {
		return "store_unavailable"
	}
	if applied < g.schema {
		logx.WithContext(ctx).Errorf("database schema is at %d, this gateway needs %d: run botguard-ctl migrate up", applied, g.schema)
		return "schema_outdated"
	}
	g.schemaOK.Store(true)
	return ""
}
