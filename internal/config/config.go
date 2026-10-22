package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/dkeepersun/BotGuard/internal/service/challenge"
	"github.com/dkeepersun/BotGuard/internal/service/sealer"
	"github.com/dkeepersun/BotGuard/internal/store"
	"github.com/zeromicro/go-zero/core/stores/redis"
	"github.com/zeromicro/go-zero/rest"
)

// AdminConfig configures the admin console (docs/admin.md). Secrets and the
// DSN come from the environment, never the YAML file.
type AdminConfig struct {
	rest.RestConf
	Redis redis.RedisConf
	// Database defaults to a SQLite file; see store.Config.
	Database store.Config `json:",optional"`
	// MySQL is the setting from before Database, still honoured.
	MySQL LegacyMySQL `json:",optional"`
	// SecretKEK seals new secret pairs and TOTP secrets; same value as the
	// gateway's.
	SecretKEK         string   `json:",optional"`
	SecretKEKPrevious []string `json:",optional"`
	// PublicEndpoint is the gateway URL browsers and SDKs use; it appears in
	// the integration snippets.
	PublicEndpoint string `json:",optional"`
	// GatewayURL is where the console checks the gateway's /readyz.
	GatewayURL string `json:",optional"`
	// CookieSecure marks the session cookie HTTPS-only. Turn it off only for
	// plain-HTTP local use.
	CookieSecure bool `json:",default=true"`
	Session      struct {
		IdleMinutes   int `json:",default=30"`
		AbsoluteHours int `json:",default=8"`
	}
	// TrustedProxies are CIDRs whose X-Forwarded-For is believed.
	TrustedProxies []string `json:",optional"`
	// Cleanup deletes old database rows (docs/admin.md). Only the console
	// runs it: the gateway may hold a read-only database account.
	Cleanup struct {
		Enabled         bool `json:",default=true"`
		IntervalMinutes int  `json:",default=60"`
		// 0 keeps rows forever.
		AuditLogDays      int `json:",default=180"`
		RetiredSecretDays int `json:",default=30"`
	}
}

// LegacyMySQL is the pre-Database configuration block.
type LegacyMySQL struct {
	DataSource string `json:",optional"`
}

func (c AdminConfig) DB() (store.Config, error) { return c.Database.Resolve(c.MySQL.DataSource) }

func (c AdminConfig) Sealer() (*sealer.Sealer, error) {
	if strings.TrimSpace(c.SecretKEK) == "" {
		return nil, nil
	}
	var previous []string
	for _, k := range c.SecretKEKPrevious {
		if k = strings.TrimSpace(k); k != "" {
			previous = append(previous, k)
		}
	}
	return sealer.New(c.SecretKEK, previous...)
}

func (c AdminConfig) Validate() error {
	if _, err := c.DB(); err != nil {
		return fmt.Errorf("Database: %w", err)
	}
	if c.Redis.Host == "" {
		return errors.New("Redis.Host is required")
	}
	if _, err := c.Sealer(); err != nil {
		return fmt.Errorf("SecretKEK: %w", err)
	}
	if c.Cleanup.IntervalMinutes < 5 || c.Cleanup.IntervalMinutes > 1440 {
		return errors.New("Cleanup.IntervalMinutes must be within [5, 1440]")
	}
	if c.Cleanup.AuditLogDays < 0 || c.Cleanup.RetiredSecretDays < 0 {
		return errors.New("Cleanup days must not be negative (0 keeps rows forever)")
	}
	if ip := net.ParseIP(c.Host); !c.CookieSecure && (ip == nil || !ip.IsLoopback()) {
		// A non-loopback listener without HTTPS cookies would send sessions
		// in clear text; refuse unless the operator opts in explicitly.
		if os.Getenv("BOTGUARD_ADMIN_INSECURE_COOKIE") != "1" {
			return errors.New("CookieSecure=false needs a loopback Host (or BOTGUARD_ADMIN_INSECURE_COOKIE=1 behind a TLS-less private network)")
		}
	}
	if c.Session.IdleMinutes < 5 || c.Session.IdleMinutes > 720 {
		return errors.New("Session.IdleMinutes must be within [5, 720]")
	}
	if c.Session.AbsoluteHours < 1 || c.Session.AbsoluteHours > 72 {
		return errors.New("Session.AbsoluteHours must be within [1, 72]")
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
	// Database defaults to a SQLite file; see store.Config.
	Database store.Config `json:",optional"`
	// MySQL is the setting from before Database, still honoured.
	MySQL     LegacyMySQL `json:",optional"`
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
		FallbackPerIPPerMinute  int `json:",default=5"`
		// Failed verify signatures per IP before 429 (no secret lookups).
		AuthFailuresPerIPPerMinute int `json:",default=60"`
		// Image challenges per app across all IPs; beyond it they degrade to
		// harder PoW. 0 disables.
		InteractivePerAppPerMinute int `json:",default=6000"`
	}
	Risk struct {
		MediumThreshold          int `json:",default=30"`
		HighThreshold            int `json:",default=70"`
		HighDifficultyFactor     int `json:",default=4"`
		FallbackDifficultyFactor int `json:",default=8"`
		DegradedDifficultyFactor int `json:",default=8"`
		FailureWindowSeconds     int `json:",default=600"`
		// Kind ladder (the page's levels): passes in a row per step down,
		// and how long a client's rung lasts after its last solve.
		LadderDowngradePasses int `json:",default=3"`
		LadderWindowSeconds   int `json:",default=1800"`
	}
	// PuzzlePool pre-generates PerKind puzzles of every interactive kind.
	// Workers 0 means half the CPUs (at least one); PerKind 0 disables it.
	PuzzlePool struct {
		PerKind int `json:",default=64"`
		Workers int `json:",default=0"`
	}
	Slider struct {
		Tolerance int `json:",default=4"`
	}
	Rotate struct {
		ToleranceDegrees int `json:",default=7"`
	}
}

func (c GatewayConfig) DB() (store.Config, error) { return c.Database.Resolve(c.MySQL.DataSource) }

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
	// Empty entries come from an unset ${BOTGUARD_SECRET_KEK_PREVIOUS}.
	var previous []string
	for _, k := range c.SecretKEKPrevious {
		if k = strings.TrimSpace(k); k != "" {
			previous = append(previous, k)
		}
	}
	return sealer.New(c.SecretKEK, previous...)
}

func (c GatewayConfig) Validate() error {
	if _, err := c.Keyring(); err != nil {
		return err
	}
	if _, err := c.Sealer(); err != nil {
		return fmt.Errorf("SecretKEK: %w", err)
	}
	if _, err := c.DB(); err != nil {
		return fmt.Errorf("Database: %w", err)
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
	var maxDifficulty error
	if d := c.Challenge.MaxDifficulty; d < 1 || d > 1<<32 {
		// 0 would make every challenge fail with 503 and blame the app policy.
		maxDifficulty = fmt.Errorf("Challenge.MaxDifficulty must be within [1, %d]", uint64(1)<<32)
	}
	return errors.Join(
		maxDifficulty,
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
		inRange("RateLimit.FallbackPerIPPerMinute", c.RateLimit.FallbackPerIPPerMinute, 1, 1000),
		inRange("RateLimit.AuthFailuresPerIPPerMinute", c.RateLimit.AuthFailuresPerIPPerMinute, 1, 100_000),
		inRange("RateLimit.InteractivePerAppPerMinute", c.RateLimit.InteractivePerAppPerMinute, 0, 10_000_000),
		inRange("Risk.MediumThreshold", c.Risk.MediumThreshold, 1, 100),
		inRange("Risk.HighThreshold", c.Risk.HighThreshold, c.Risk.MediumThreshold+1, 101),
		inRange("Risk.HighDifficultyFactor", c.Risk.HighDifficultyFactor, 1, 64),
		inRange("Risk.FallbackDifficultyFactor", c.Risk.FallbackDifficultyFactor, 1, 256),
		inRange("Risk.DegradedDifficultyFactor", c.Risk.DegradedDifficultyFactor, 1, 256),
		inRange("PuzzlePool.PerKind", c.PuzzlePool.PerKind, 0, 1024),
		inRange("PuzzlePool.Workers", c.PuzzlePool.Workers, 0, 256),
		inRange("Risk.FailureWindowSeconds", c.Risk.FailureWindowSeconds, 60, 86400),
		inRange("Risk.LadderDowngradePasses", c.Risk.LadderDowngradePasses, 1, 100),
		inRange("Risk.LadderWindowSeconds", c.Risk.LadderWindowSeconds, 60, 86400),
		inRange("Slider.Tolerance", c.Slider.Tolerance, 1, 20),
		inRange("Rotate.ToleranceDegrees", c.Rotate.ToleranceDegrees, 2, 20),
	)
}

func Seconds(n int) time.Duration { return time.Duration(n) * time.Second }
