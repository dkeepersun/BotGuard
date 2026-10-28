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
	"github.com/dkeepersun/BotGuard/internal/service/grid"
	"github.com/dkeepersun/BotGuard/internal/service/jigsaw"
	"github.com/dkeepersun/BotGuard/internal/service/rotate"
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
	// InteractivePerAppPerMinute bounds image challenges per app across all
	// addresses; beyond it the app's challenges degrade to harder PoW. Zero
	// disables it.
	InteractivePerAppPerMinute int
	// InteractivePerPrefixPerMinute bounds image challenges per app and
	// client network prefix (IPv4 /24, IPv6 /48); beyond it issuing an image
	// challenge answers 429. Zero disables it.
	InteractivePerPrefixPerMinute int
	// ChallengePerPrefixPerMinute bounds challenges per client network prefix
	// (IPv4 /24, IPv6 /48) across apps; beyond it issuing answers 429. Zero
	// disables it.
	ChallengePerPrefixPerMinute int

	// Risk thresholds and the PoW multipliers they trigger.
	RiskMedium               int
	RiskHigh                 int
	HighDifficultyFactor     uint64
	FallbackDifficultyFactor uint64
	// DegradedDifficultyFactor multiplies PoW work when an interactive
	// challenge degrades for lack of image capacity.
	DegradedDifficultyFactor uint64
	// DegradedMinDifficulty is the least per-puzzle difficulty of a degraded
	// challenge, whatever the action's own.
	DegradedMinDifficulty uint64
	FailureWindow         time.Duration
	// FailureBlockThreshold failed solves from one IP within FailureWindow
	// make issuing answer 429 until they age out. Zero disables it.
	FailureBlockThreshold int
	// LadderDowngradePasses passes in a row move a client one rung down the
	// kind ladder a page asked for; LadderWindow is how long the rung lasts
	// after the client's last solve.
	LadderDowngradePasses int
	LadderWindow          time.Duration
	// Attack mode: AttackFailures failed solves for one app within two
	// minutes, at least AttackRatioPercent of its solves, raise every risk
	// level of that app one step for AttackHold. Zero failures disables it.
	AttackFailures     int
	AttackRatioPercent int
	AttackHold         time.Duration

	SliderTolerance int
	// IPHashKey salts the IP tags in console events; empty disables them.
	IPHashKey []byte
	// RotateToleranceDegrees is how far off the rotate answer may be.
	RotateToleranceDegrees int
}

type Gateway struct {
	Apps  repository.AppStore
	State *repository.State
	// Stats feeds the admin console; nil disables it.
	Stats    *repository.Stats
	Limiter  *repository.RateLimiter
	Keys     *challenge.Keyring
	Settings Settings
	Now      func() time.Time
	// Puzzles and Clicks generate challenges; nil uses crypto-seeded generators.
	Puzzles func() (slider.Puzzle, error)
	Clicks  func(kind string) (click.Puzzle, error)
	Rotates func() (rotate.Puzzle, error)
	Jigsaws func() (jigsaw.Puzzle, error)
	Grids   func(kind string) (grid.Puzzle, error)
	// puzzleSlots bounds concurrent image generation; created lazily.
	puzzleSlots chan struct{}
	slotsOnce   sync.Once
	// pool holds pre-generated puzzles; nil generates every one on demand.
	pool *puzzlePool
	// Monitor counts degraded challenges for the health page; nil is off.
	Monitor *Monitor
	// Backgrounds picks each app's picture source; nil draws procedural
	// pictures only.
	Backgrounds *Backgrounds
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
	// Origin is the page origin the token was earned on. Apps allowing
	// wildcard or any origin can check it against their own list.
	Origin string `json:"origin,omitempty"`
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
func (g *Gateway) Verify(ctx context.Context, in VerifyInput) (out *VerifyOutput, err error) {
	var appID int64
	defer func() {
		if appID == 0 {
			return
		}
		if err != nil {
			g.record(ctx, appID, []string{"verify_error", "verify_error:" + errCode(err)}, nil)
			return
		}
		ev := &repository.Event{Type: repository.EventVerify, Action: in.Action, RequestID: in.RequestID, IP: g.ipTag(in.ClientIP)}
		fields := []string{"verify"}
		if out.Success {
			ev.Result, ev.Kind, ev.Level, ev.Reasons = "pass", out.ChallengeKind, out.RiskLevel, out.RiskReasons
			fields = append(fields, "verify_pass", "verify_pass:"+out.ChallengeKind)
		} else {
			ev.Result = out.Reason
			fields = append(fields, "verify_reject", "verify_reject:"+out.Reason)
		}
		g.record(ctx, appID, fields, ev)
	}()
	if !domain.ValidAppKey(in.AppKey) || !domain.ValidAction(in.Action) || !domain.ValidRequestID(in.RequestID) {
		return nil, errx.InvalidRequest("app_key、action 或 request_id 格式错误")
	}
	// Unknown secret ids miss every cache, so an IP that keeps failing
	// authentication is cut off before it can turn into MySQL queries.
	// Zero disables the limit (tests and embedded use; config requires >= 1).
	ipBucket, authLimit := clientip.Bucket(in.ClientIP), g.Settings.AuthFailuresPerIPPerMinute
	if authLimit > 0 {
		fails, retry, err := g.Limiter.Count(ctx, dimAuthFail, ipBucket, authLimit, rateWindow)
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
		if _, _, lerr := g.Limiter.Take(ctx, dimAuthFail, ipBucket, authLimit, rateWindow); lerr != nil {
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
	appID = app.ID
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
		// verified_at:kind:level:reasons:origin. Older stored results stop
		// after reasons or verified_at, and kind and later may be empty.
		parts := strings.SplitN(rest, ":", 5)
		at, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, errx.ErrUnavailable
		}
		out := &VerifyOutput{Success: true, AppKey: app.AppKey, Action: in.Action, VerifiedAt: at}
		if len(parts) >= 4 {
			out.ChallengeKind, out.RiskLevel = parts[1], parts[2]
			if parts[3] != "" {
				out.RiskReasons = strings.Split(parts[3], ",")
			}
		}
		if len(parts) == 5 {
			out.Origin = parts[4]
		}
		return out, nil
	}
	return &VerifyOutput{Success: false, Reason: outcome}, nil
}
