package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/dkeepersun/BotGuard/internal/service/challenge"
	"github.com/dkeepersun/BotGuard/internal/service/sealer"
	"github.com/zeromicro/go-zero/core/stores/redis"
	"github.com/zeromicro/go-zero/rest"
)

// AdminConfig is the read-only admin preview. It stays loopback-only until
// admin authentication exists.
type AdminConfig struct{ rest.RestConf }

// ValidateBootstrap prevents the unauthenticated preview from being exposed.
func (c AdminConfig) ValidateBootstrap() error {
	ip := net.ParseIP(c.Host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("bootstrap requires a loopback Host")
	}
	return nil
}

type ChallengeKeyConf struct {
	ID string
	// Secret is standard base64 of at least 32 random bytes, injected via env.
	Secret string
	Active bool `json:",optional"`
}

type GatewayConfig struct {
	rest.RestConf
	Redis redis.RedisConf
	MySQL struct {
		DataSource string
	}
	Challenge struct {
		Keys          []ChallengeKeyConf
		TTLSeconds    int    `json:",default=120"`
		MaxDifficulty uint64 `json:",default=1000000"`
	}
	// SecretKEK seals app secret keys at rest: "id:base64(32 bytes)", from env.
	SecretKEK string
	// SecretKEKPrevious are decrypt-only KEKs kept during a KEK rotation.
	SecretKEKPrevious      []string `json:",optional"`
	SignatureWindowSeconds int      `json:",default=300"`
	TokenTTLSeconds        int      `json:",default=60"`
	VerifyResultTTLSeconds int      `json:",default=600"`
	ClockSkewSeconds       int      `json:",default=5"`
	// TrustedProxies are CIDRs whose X-Forwarded-For entries are believed.
	TrustedProxies []string `json:",optional"`
	AppCache       struct {
		TTLSeconds int `json:",default=10"`
		Limit      int `json:",default=10000"`
	}
	RateLimit struct {
		ChallengePerIPPerMinute int `json:",default=120"`
		SolvePerIPPerMinute     int `json:",default=120"`
		VerifyPerKeyPerMinute   int `json:",default=6000"`
	}
}

// Keyring decodes and validates the challenge signing keys.
func (c GatewayConfig) Keyring() (*challenge.Keyring, error) {
	keys := make([]challenge.Key, 0, len(c.Challenge.Keys))
	for _, k := range c.Challenge.Keys {
		secret, err := base64.StdEncoding.DecodeString(k.Secret)
		if err != nil {
			return nil, fmt.Errorf("challenge key %q: secret is not base64", k.ID)
		}
		keys = append(keys, challenge.Key{ID: k.ID, Secret: secret, Active: k.Active})
	}
	return challenge.NewKeyring(keys)
}

func (c GatewayConfig) Sealer() (*sealer.Sealer, error) {
	return sealer.New(c.SecretKEK, c.SecretKEKPrevious...)
}

func (c GatewayConfig) Validate() error {
	if _, err := c.Keyring(); err != nil {
		return err
	}
	if _, err := c.Sealer(); err != nil {
		return fmt.Errorf("SecretKEK: %w", err)
	}
	if _, err := NormalizeMySQLDSN(c.MySQL.DataSource); err != nil {
		return fmt.Errorf("MySQL.DataSource: %w", err)
	}
	if c.Redis.Host == "" {
		return errors.New("Redis.Host is required")
	}
	inRange := func(name string, v, lo, hi int) error {
		if v < lo || v > hi {
			return fmt.Errorf("%s must be within [%d, %d]", name, lo, hi)
		}
		return nil
	}
	return errors.Join(
		inRange("Challenge.TTLSeconds", c.Challenge.TTLSeconds, 10, 600),
		inRange("TokenTTLSeconds", c.TokenTTLSeconds, 10, 600),
		inRange("VerifyResultTTLSeconds", c.VerifyResultTTLSeconds, 60, 3600),
		inRange("ClockSkewSeconds", c.ClockSkewSeconds, 0, 30),
		inRange("SignatureWindowSeconds", c.SignatureWindowSeconds, 30, 900),
		inRange("AppCache.TTLSeconds", c.AppCache.TTLSeconds, 1, 300),
		inRange("AppCache.Limit", c.AppCache.Limit, 100, 1_000_000),
		inRange("RateLimit.ChallengePerIPPerMinute", c.RateLimit.ChallengePerIPPerMinute, 1, 1_000_000),
		inRange("RateLimit.SolvePerIPPerMinute", c.RateLimit.SolvePerIPPerMinute, 1, 1_000_000),
		inRange("RateLimit.VerifyPerKeyPerMinute", c.RateLimit.VerifyPerKeyPerMinute, 1, 10_000_000),
	)
}

func Seconds(n int) time.Duration { return time.Duration(n) * time.Second }
