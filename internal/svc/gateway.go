// Package svc wires configuration into the gateway's dependencies.
package svc

import (
	"context"
	"time"

	"github.com/dkeepersun/BotGuard/internal/config"
	"github.com/dkeepersun/BotGuard/internal/logic"
	"github.com/dkeepersun/BotGuard/internal/repository"
	"github.com/dkeepersun/BotGuard/internal/service/clientip"
	"github.com/zeromicro/go-zero/core/stores/redis"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type Gateway struct {
	Logic *logic.Gateway
	IPs   *clientip.Resolver
	rds   *redis.Redis
	db    sqlx.SqlConn
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
	dsn, err := config.NormalizeMySQLDSN(c.MySQL.DataSource)
	if err != nil {
		return nil, err
	}
	db := sqlx.NewMysql(dsn)
	seal, err := c.Sealer()
	if err != nil {
		return nil, err
	}
	apps, err := repository.NewCachedApps(repository.NewMySQLApps(db, seal), config.Seconds(c.AppCache.TTLSeconds), c.AppCache.Limit)
	if err != nil {
		return nil, err
	}
	return &Gateway{
		Logic: &logic.Gateway{
			Apps:    apps,
			State:   repository.NewState(rds),
			Limiter: repository.NewRateLimiter(rds),
			Keys:    keys,
			Settings: logic.Settings{
				ChallengeTTL:            config.Seconds(c.Challenge.TTLSeconds),
				TokenTTL:                config.Seconds(c.TokenTTLSeconds),
				ResultTTL:               config.Seconds(c.VerifyResultTTLSeconds),
				ClockSkew:               config.Seconds(c.ClockSkewSeconds),
				MaxDifficulty:           c.Challenge.MaxDifficulty,
				SignatureWindow:         config.Seconds(c.SignatureWindowSeconds),
				ChallengePerIPPerMinute: c.RateLimit.ChallengePerIPPerMinute,
				SolvePerIPPerMinute:     c.RateLimit.SolvePerIPPerMinute,
				VerifyPerKeyPerMinute:   c.RateLimit.VerifyPerKeyPerMinute,
				FallbackPerIPPerMinute:  c.RateLimit.FallbackPerIPPerMinute,

				RiskMedium:               c.Risk.MediumThreshold,
				RiskHigh:                 c.Risk.HighThreshold,
				HighDifficultyFactor:     uint64(c.Risk.HighDifficultyFactor),
				FallbackDifficultyFactor: uint64(c.Risk.FallbackDifficultyFactor),
				FailureWindow:            config.Seconds(c.Risk.FailureWindowSeconds),
				SliderTolerance:          c.Slider.Tolerance,
			},
			Now: time.Now,
		},
		IPs: ips,
		rds: rds,
		db:  db,
	}, nil
}

// Ready reports whether both stores answer. The gateway fails closed without
// either one, so readiness must not pass on a partial outage.
func (g *Gateway) Ready(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if !g.rds.PingCtx(ctx) {
		return false
	}
	raw, err := g.db.RawDB()
	return err == nil && raw.PingContext(ctx) == nil
}
