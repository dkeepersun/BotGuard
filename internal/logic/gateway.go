// Package logic implements the challenge, solve and verify use cases.
package logic

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/errx"
	"github.com/dkeepersun/BotGuard/internal/repository"
	"github.com/dkeepersun/BotGuard/internal/service/challenge"
	"github.com/dkeepersun/BotGuard/internal/service/click"
	"github.com/dkeepersun/BotGuard/internal/service/clientip"
	"github.com/dkeepersun/BotGuard/internal/service/signature"
	"github.com/dkeepersun/BotGuard/internal/service/slider"
	"github.com/zeromicro/go-zero/core/logx"
)

const (
	tokenPrefix  = "bgt1"
	maxTokenLen  = 256
	rateWindow   = time.Minute
	dimChallenge = "challenge"
	dimSolve     = "solve"
	dimVerify    = "verify"
	dimAuthFail  = "authfail"
	signedPath   = "/v1/verify"
)

type Settings struct {
	ChallengeTTL  time.Duration
	TokenTTL      time.Duration
	ResultTTL     time.Duration
	ClockSkew     time.Duration
	MaxDifficulty uint64
	// SignatureWindow is how far a signed request's timestamp may be from now.
	SignatureWindow time.Duration

	ChallengePerIPPerMinute int
	SolvePerIPPerMinute     int
	VerifyPerKeyPerMinute   int
	// AuthFailuresPerIPPerMinute bounds failed verify signatures per IP;
	// beyond it the IP gets 429 before any secret lookup.
	AuthFailuresPerIPPerMinute int
	FallbackPerIPPerMinute     int

	// Risk thresholds and the PoW multipliers they trigger.
	RiskMedium               int
	RiskHigh                 int
	HighDifficultyFactor     uint64
	FallbackDifficultyFactor uint64
	FailureWindow            time.Duration

	SliderTolerance int
}

type Gateway struct {
	Apps     repository.AppStore
	State    *repository.State
	Limiter  *repository.RateLimiter
	Keys     *challenge.Keyring
	Settings Settings
	Now      func() time.Time
	// Puzzles and Clicks generate challenges; nil uses crypto-seeded generators.
	Puzzles func() (slider.Puzzle, error)
	Clicks  func(kind string) (click.Puzzle, error)
	// puzzleSlots bounds concurrent image generation; created lazily.
	puzzleSlots chan struct{}
	slotsOnce   sync.Once
}

func (g *Gateway) limit(ctx context.Context, dim, subject string, quota int) error {
	ok, retry, err := g.Limiter.Allow(ctx, dim, subject, quota, rateWindow)
	if err != nil {
		logx.WithContext(ctx).Errorf("rate limiter %s: %v", dim, err)
		return errx.ErrUnavailable
	}
	if !ok {
		return errx.RateLimited(retry)
	}
	return nil
}

// app returns the enabled app or a contract error; storage failures are 503.
func (g *Gateway) app(ctx context.Context, appKey string) (*domain.App, error) {
	app, err := g.Apps.AppByKey(ctx, appKey)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, errx.ErrAppForbidden
	}
	if err != nil {
		logx.WithContext(ctx).Errorf("app lookup: %v", err)
		return nil, errx.ErrUnavailable
	}
	if !app.Enabled {
		return nil, errx.ErrAppForbidden
	}
	return app, nil
}

// appPeeker is implemented by stores that can answer from cache alone.
type appPeeker interface {
	PeekApp(appKey string) (*domain.App, bool)
}

// cachedOrigin returns origin when an already-cached app allows it, so errors
// raised before the app lookup (rate limits, broken challenges) still carry
// CORS headers. It never queries storage, keeping those paths cheap.
func (g *Gateway) cachedOrigin(appKey, rawOrigin string) string {
	peeker, ok := g.Apps.(appPeeker)
	if !ok {
		return ""
	}
	origin, ok := domain.NormalizeOrigin(rawOrigin)
	if !ok {
		return ""
	}
	if app, ok := peeker.PeekApp(appKey); ok && app.Enabled && app.AllowsOrigin(origin) {
		return origin
	}
	return ""
}

// Tokens embed the public app key so a token from another app is rejected
// as APP_MISMATCH without a Redis lookup.
func newToken(appKey string) (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return tokenPrefix + "." + appKey + "." + base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// tokenApp returns the app key embedded in a well-formed token.
func tokenApp(token string) (string, bool) {
	if len(token) > maxTokenLen {
		return "", false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != tokenPrefix || !domain.ValidAppKey(parts[1]) {
		return "", false
	}
	if b, err := base64.RawURLEncoding.DecodeString(parts[2]); err != nil || len(b) != 32 {
		return "", false
	}
	return parts[1], true
}

// VerifyInput carries the signature headers and the exact body bytes that
// were signed, alongside the decoded fields.
type VerifyInput struct {
	ClientIP  netip.Addr
	SecretID  string
	Timestamp string
	Nonce     string
	Signature string
	Body      []byte

	AppKey    string
	Token     string
	Action    string
	RequestID string
}

type VerifyOutput struct {
	Success    bool   `json:"success"`
	Reason     string `json:"reason,omitempty"`
	AppKey     string `json:"app_key,omitempty"`
	Action     string `json:"action,omitempty"`
	VerifiedAt int64  `json:"verified_at,omitempty"`
	// How the token was earned; additive fields, ignored by older SDKs.
	ChallengeKind string   `json:"challenge_kind,omitempty"`
	RiskLevel     string   `json:"risk_level,omitempty"`
	RiskReasons   []string `json:"risk_reasons,omitempty"`
}

// authenticate checks a BG1-HMAC-SHA256 signature. Cheap format and clock
// checks run first; the nonce is recorded only after the signature verifies,
// so unauthenticated callers cannot burn nonces. Unknown secret_id and bad
// signature share one error so secret ids cannot be probed.
func (g *Gateway) authenticate(ctx context.Context, in VerifyInput) (*domain.SecretPair, error) {
	ts, err := strconv.ParseInt(in.Timestamp, 10, 64)
	if !domain.ValidSecretID(in.SecretID) || err != nil || !signature.ValidNonce(in.Nonce) || !signature.ValidSignature(in.Signature) {
		return nil, errx.ErrUnauthorized
	}
	now := g.Now()
	window := int64(g.Settings.SignatureWindow / time.Second)
	if d := now.Unix() - ts; d > window || d < -window {
		return nil, errx.ErrTimestampExpired
	}
	pair, err := g.Apps.SecretPair(ctx, in.SecretID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, errx.ErrUnauthorized
	}
	if err != nil {
		logx.WithContext(ctx).Errorf("secret pair lookup: %v", err)
		return nil, errx.ErrUnavailable
	}
	if !pair.Usable(now) {
		return nil, errx.ErrUnauthorized
	}
	canonical := signature.Canonical("POST", signedPath, ts, in.Nonce, in.Body)
	if !signature.Equal(pair.SecretKey, canonical, in.Signature) {
		return nil, errx.ErrUnauthorized
	}
	// Keep nonces for the whole window on both sides of now, plus the clock
	// skew between replicas.
	fresh, err := g.State.MarkNonce(ctx, in.SecretID, in.Nonce, 2*g.Settings.SignatureWindow+g.Settings.ClockSkew)
	if err != nil {
		logx.WithContext(ctx).Errorf("mark nonce: %v", err)
		return nil, errx.ErrUnavailable
	}
	if !fresh {
		return nil, errx.ErrNonceReused
	}
	return pair, nil
}

// Verify authenticates the caller and checks the app before reading any
// idempotency record, so a revoked secret or disabled app is never masked by a
// cached pass. Token problems are 200 rejections; caller problems are 4xx.
func (g *Gateway) Verify(ctx context.Context, in VerifyInput) (*VerifyOutput, error) {
	if !domain.ValidAppKey(in.AppKey) || !domain.ValidAction(in.Action) || !domain.ValidRequestID(in.RequestID) {
		return nil, errx.InvalidRequest("app_key、action 或 request_id 格式错误")
	}
	// Unknown secret ids miss every cache, so an IP that keeps failing
	// authentication is cut off before it can turn into MySQL queries.
	// Zero disables the limit (tests and embedded use; config requires >= 1).
	ipBucket, authLimit := clientip.Bucket(in.ClientIP), g.Settings.AuthFailuresPerIPPerMinute
	if authLimit > 0 {
		fails, retry, err := g.Limiter.Count(ctx, dimAuthFail, ipBucket, rateWindow)
		if err != nil {
			logx.WithContext(ctx).Errorf("rate limiter %s: %v", dimAuthFail, err)
			return nil, errx.ErrUnavailable
		}
		if fails >= int64(authLimit) {
			return nil, errx.RateLimited(retry)
		}
	}
	pair, err := g.authenticate(ctx, in)
	// Clock errors are rejected before any lookup and must stay visible as
	// TIMESTAMP_EXPIRED, so only signature failures count.
	if authLimit > 0 && errors.Is(err, errx.ErrUnauthorized) {
		if _, _, lerr := g.Limiter.Take(ctx, dimAuthFail, ipBucket, rateWindow); lerr != nil {
			logx.WithContext(ctx).Errorf("rate limiter %s: %v", dimAuthFail, lerr)
		}
	}
	if err != nil {
		return nil, err
	}
	app, err := g.app(ctx, in.AppKey)
	if err != nil {
		return nil, err
	}
	if app.ID != pair.AppID {
		return nil, errx.ErrAppForbidden
	}
	if err := g.limit(ctx, dimVerify, pair.SecretID, g.Settings.VerifyPerKeyPerMinute); err != nil {
		return nil, err
	}

	tokenHash := hashToken(in.Token)
	forced := ""
	if owner, ok := tokenApp(in.Token); !ok {
		forced = repository.ReasonInvalid
	} else if owner != app.AppKey {
		forced = repository.ReasonApp
	}
	digest := sha256.Sum256([]byte(strconv.FormatInt(app.ID, 10) + "|" + in.Action + "|" + tokenHash))
	outcome, _, err := g.State.Consume(ctx, repository.ConsumeInput{
		AppID:          app.ID,
		RequestID:      in.RequestID,
		RequestDigest:  hex.EncodeToString(digest[:]),
		TokenHash:      tokenHash,
		Action:         in.Action,
		AllowedOrigins: app.Origins,
		Forced:         forced,
		Now:            g.Now().Unix(),
		ResultTTL:      g.Settings.ResultTTL,
	})
	if errors.Is(err, repository.ErrIdempotencyConflict) {
		return nil, errx.ErrIdempotency
	}
	if err != nil {
		logx.WithContext(ctx).Errorf("consume token: %v", err)
		return nil, errx.ErrUnavailable
	}
	if rest, ok := strings.CutPrefix(outcome, repository.PassPrefix); ok {
		// verified_at:kind:level:reasons (kind and later may be empty for old tokens).
		parts := strings.SplitN(rest, ":", 4)
		at, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, errx.ErrUnavailable
		}
		out := &VerifyOutput{Success: true, AppKey: app.AppKey, Action: in.Action, VerifiedAt: at}
		if len(parts) == 4 {
			out.ChallengeKind, out.RiskLevel = parts[1], parts[2]
			if parts[3] != "" {
				out.RiskReasons = strings.Split(parts[3], ",")
			}
		}
		return out, nil
	}
	return &VerifyOutput{Success: false, Reason: outcome}, nil
}
