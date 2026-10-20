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
	"time"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/errx"
	"github.com/dkeepersun/BotGuard/internal/repository"
	"github.com/dkeepersun/BotGuard/internal/service/challenge"
	"github.com/dkeepersun/BotGuard/internal/service/clientip"
	"github.com/dkeepersun/BotGuard/internal/service/pow"
	"github.com/dkeepersun/BotGuard/internal/service/signature"
	"github.com/zeromicro/go-zero/core/logx"
)

const (
	tokenPrefix  = "bgt1"
	maxTokenLen  = 256
	rateWindow   = time.Minute
	dimChallenge = "challenge"
	dimSolve     = "solve"
	dimVerify    = "verify"
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
}

type Gateway struct {
	Apps     repository.AppStore
	State    *repository.State
	Limiter  *repository.RateLimiter
	Keys     *challenge.Keyring
	Settings Settings
	Now      func() time.Time
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

type IssueInput struct {
	AppKey   string
	Action   string
	Origin   string
	ClientIP netip.Addr
}

type IssueOutput struct {
	Challenge   string     `json:"challenge"`
	ChallengeID string     `json:"challenge_id"`
	Algorithm   string     `json:"algorithm"`
	Params      pow.Params `json:"params"`
	ExpiresAt   int64      `json:"expires_at"`
}

// Issue signs a challenge without writing any per-challenge state.
// allowedOrigin is set once the origin is known to be allowed for the app,
// so the handler can attach CORS headers even to later errors.
func (g *Gateway) Issue(ctx context.Context, in IssueInput) (out *IssueOutput, allowedOrigin string, err error) {
	origin, ok := domain.NormalizeOrigin(in.Origin)
	if !ok {
		return nil, "", errx.ErrOriginNotAllowed
	}
	if err := g.limit(ctx, dimChallenge, clientip.Bucket(in.ClientIP), g.Settings.ChallengePerIPPerMinute); err != nil {
		return nil, "", err
	}
	if !domain.ValidAppKey(in.AppKey) || !domain.ValidAction(in.Action) {
		return nil, "", errx.InvalidRequest("app_key 或 action 格式错误")
	}
	app, err := g.app(ctx, in.AppKey)
	if err != nil {
		return nil, "", err
	}
	if !app.AllowsOrigin(origin) {
		return nil, "", errx.ErrOriginNotAllowed
	}
	policy, ok := app.Actions[in.Action]
	if !ok || !policy.Enabled {
		return nil, origin, errx.ErrActionForbidden
	}
	difficulty := min(policy.Difficulty, g.Settings.MaxDifficulty)
	params, err := pow.NewParams(policy.Puzzles, difficulty)
	if err != nil {
		logx.WithContext(ctx).Errorf("app %d action %s has invalid pow policy", app.ID, in.Action)
		return nil, origin, errx.ErrUnavailable
	}
	id, err := challenge.NewID()
	if err != nil {
		return nil, origin, errx.ErrUnavailable
	}
	now := g.Now()
	p := challenge.Payload{
		ChallengeID:   id,
		AppKey:        app.AppKey,
		Action:        in.Action,
		Origin:        origin,
		Algorithm:     pow.AlgSHA256Target,
		Params:        params,
		PolicyVersion: app.PolicyVersion,
		IssuedAt:      now.Unix(),
		ExpiresAt:     now.Add(g.Settings.ChallengeTTL).Unix(),
	}
	signed, err := g.Keys.Sign(p)
	if err != nil {
		logx.WithContext(ctx).Errorf("sign challenge: %v", err)
		return nil, origin, errx.ErrUnavailable
	}
	return &IssueOutput{Challenge: signed, ChallengeID: id, Algorithm: p.Algorithm, Params: params, ExpiresAt: p.ExpiresAt}, origin, nil
}

type SolveInput struct {
	AppKey    string
	Challenge string
	Nonces    []string
	Origin    string
	ClientIP  netip.Addr
}

type SolveOutput struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
}

// Solve follows the cheap-to-expensive order in the contract. The challenge
// is marked used before hashing, so each challenge gets exactly one attempt.
func (g *Gateway) Solve(ctx context.Context, in SolveInput) (out *SolveOutput, allowedOrigin string, err error) {
	if !domain.ValidAppKey(in.AppKey) || in.Challenge == "" {
		return nil, "", errx.InvalidRequest("app_key 或 challenge 格式错误")
	}
	if len(in.Challenge) > challenge.MaxLen {
		return nil, "", errx.InvalidRequest("challenge 过长")
	}
	p, err := g.Keys.Open(in.Challenge)
	if err != nil {
		return nil, "", errx.ErrChallengeInvalid
	}
	now := g.Now()
	skew := int64(g.Settings.ClockSkew / time.Second)
	if p.IssuedAt > now.Unix()+skew {
		return nil, "", errx.ErrChallengeInvalid
	}
	if now.Unix() > p.ExpiresAt+skew {
		return nil, "", errx.ErrChallengeExpired
	}
	if p.AppKey != in.AppKey {
		return nil, "", errx.ErrAppForbidden
	}
	app, err := g.app(ctx, in.AppKey)
	if err != nil {
		return nil, "", err
	}
	origin, ok := domain.NormalizeOrigin(in.Origin)
	if !ok || !app.AllowsOrigin(origin) {
		return nil, "", errx.ErrOriginNotAllowed
	}
	if origin != p.Origin {
		return nil, origin, errx.ErrOriginNotAllowed
	}
	if policy, ok := app.Actions[p.Action]; !ok || !policy.Enabled {
		return nil, origin, errx.ErrActionForbidden
	}
	if err := g.limit(ctx, dimSolve, clientip.Bucket(in.ClientIP), g.Settings.SolvePerIPPerMinute); err != nil {
		return nil, origin, err
	}
	if p.Algorithm != pow.AlgSHA256Target {
		return nil, origin, errx.ErrChallengeInvalid
	}
	if err := pow.CheckNonces(p.Params, in.Nonces); err != nil {
		return nil, origin, errx.InvalidRequest("nonces 数量或格式错误")
	}
	remaining := time.Unix(p.ExpiresAt+skew, 0).Sub(now)
	first, err := g.State.MarkChallengeUsed(ctx, p.ChallengeID, remaining)
	if err != nil {
		logx.WithContext(ctx).Errorf("mark challenge: %v", err)
		return nil, origin, errx.ErrUnavailable
	}
	if !first {
		return nil, origin, errx.ErrChallengeUsed
	}
	if err := pow.Verify(p.Algorithm, p.ChallengeID, p.Params, in.Nonces); err != nil {
		if errors.Is(err, pow.ErrProofInvalid) {
			return nil, origin, errx.ErrProofInvalid
		}
		return nil, origin, errx.ErrChallengeInvalid
	}
	token, err := newToken(app.AppKey)
	if err != nil {
		return nil, origin, errx.ErrUnavailable
	}
	exp := now.Add(g.Settings.TokenTTL).Unix()
	// Keep the record past expiry so verify can say EXPIRED/CONSUMED, not INVALID.
	keyTTL := g.Settings.TokenTTL + g.Settings.ResultTTL
	rec := repository.TokenRecord{Action: p.Action, Origin: p.Origin, ExpiresAt: exp}
	if err := g.State.PutToken(ctx, app.ID, hashToken(token), rec, keyTTL); err != nil {
		// The challenge is already marked; failing here issues fewer tokens, never more.
		logx.WithContext(ctx).Errorf("store token: %v", err)
		return nil, origin, errx.ErrUnavailable
	}
	return &SolveOutput{Token: token, ExpiresAt: exp}, origin, nil
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
	// Keep nonces for the whole window on both sides of now.
	fresh, err := g.State.MarkNonce(ctx, in.SecretID, in.Nonce, 2*g.Settings.SignatureWindow)
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
	pair, err := g.authenticate(ctx, in)
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
	if ts, ok := strings.CutPrefix(outcome, repository.PassPrefix); ok {
		at, err := strconv.ParseInt(ts, 10, 64)
		if err != nil {
			return nil, errx.ErrUnavailable
		}
		return &VerifyOutput{Success: true, AppKey: app.AppKey, Action: in.Action, VerifiedAt: at}, nil
	}
	return &VerifyOutput{Success: false, Reason: outcome}, nil
}
