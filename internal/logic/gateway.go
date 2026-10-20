// Package logic implements the challenge, solve and verify use cases.
package logic

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
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
	"github.com/zeromicro/go-zero/core/logx"
)

const (
	tokenPrefix  = "bgt1"
	maxTokenLen  = 256
	rateWindow   = time.Minute
	dimChallenge = "challenge"
	dimSolve     = "solve"
	dimVerify    = "verify"
)

type Settings struct {
	ChallengeTTL  time.Duration
	TokenTTL      time.Duration
	ResultTTL     time.Duration
	ClockSkew     time.Duration
	MaxDifficulty uint64

	ChallengePerIPPerMinute int
	SolvePerIPPerMinute     int
	VerifyPerKeyPerMinute   int
}

type Gateway struct {
	Sites    repository.SiteStore
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

// site returns the enabled site or a contract error; storage failures are 503.
func (g *Gateway) site(ctx context.Context, siteKey string) (*domain.Site, error) {
	site, err := g.Sites.SiteByKey(ctx, siteKey)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, errx.ErrSiteForbidden
	}
	if err != nil {
		logx.WithContext(ctx).Errorf("site lookup: %v", err)
		return nil, errx.ErrUnavailable
	}
	if !site.Enabled {
		return nil, errx.ErrSiteForbidden
	}
	return site, nil
}

type IssueInput struct {
	SiteKey  string
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
// allowedOrigin is set once the origin is known to be allowed for the site,
// so the handler can attach CORS headers even to later errors.
func (g *Gateway) Issue(ctx context.Context, in IssueInput) (out *IssueOutput, allowedOrigin string, err error) {
	origin, ok := domain.NormalizeOrigin(in.Origin)
	if !ok {
		return nil, "", errx.ErrOriginNotAllowed
	}
	if err := g.limit(ctx, dimChallenge, clientip.Bucket(in.ClientIP), g.Settings.ChallengePerIPPerMinute); err != nil {
		return nil, "", err
	}
	if !domain.ValidSiteKey(in.SiteKey) || !domain.ValidAction(in.Action) {
		return nil, "", errx.InvalidRequest("site_key 或 action 格式错误")
	}
	site, err := g.site(ctx, in.SiteKey)
	if err != nil {
		return nil, "", err
	}
	if !site.AllowsOrigin(origin) {
		return nil, "", errx.ErrOriginNotAllowed
	}
	policy, ok := site.Actions[in.Action]
	if !ok || !policy.Enabled {
		return nil, origin, errx.ErrActionForbidden
	}
	difficulty := min(policy.Difficulty, g.Settings.MaxDifficulty)
	params, err := pow.NewParams(policy.Puzzles, difficulty)
	if err != nil {
		logx.WithContext(ctx).Errorf("site %d action %s has invalid pow policy", site.ID, in.Action)
		return nil, origin, errx.ErrUnavailable
	}
	id, err := challenge.NewID()
	if err != nil {
		return nil, origin, errx.ErrUnavailable
	}
	now := g.Now()
	p := challenge.Payload{
		ChallengeID:   id,
		SiteKey:       site.PublicKey,
		Action:        in.Action,
		Origin:        origin,
		Algorithm:     pow.AlgSHA256Target,
		Params:        params,
		PolicyVersion: site.PolicyVersion,
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
	SiteKey   string
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
	if !domain.ValidSiteKey(in.SiteKey) || in.Challenge == "" {
		return nil, "", errx.InvalidRequest("site_key 或 challenge 格式错误")
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
	if p.SiteKey != in.SiteKey {
		return nil, "", errx.ErrSiteForbidden
	}
	site, err := g.site(ctx, in.SiteKey)
	if err != nil {
		return nil, "", err
	}
	origin, ok := domain.NormalizeOrigin(in.Origin)
	if !ok || !site.AllowsOrigin(origin) {
		return nil, "", errx.ErrOriginNotAllowed
	}
	if origin != p.Origin {
		return nil, origin, errx.ErrOriginNotAllowed
	}
	if policy, ok := site.Actions[p.Action]; !ok || !policy.Enabled {
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
	token, err := newToken(site.PublicKey)
	if err != nil {
		return nil, origin, errx.ErrUnavailable
	}
	exp := now.Add(g.Settings.TokenTTL).Unix()
	// Keep the record past expiry so verify can say EXPIRED/CONSUMED, not INVALID.
	keyTTL := g.Settings.TokenTTL + g.Settings.ResultTTL
	rec := repository.TokenRecord{Action: p.Action, Origin: p.Origin, ExpiresAt: exp}
	if err := g.State.PutToken(ctx, site.ID, hashToken(token), rec, keyTTL); err != nil {
		// The challenge is already marked; failing here issues fewer tokens, never more.
		logx.WithContext(ctx).Errorf("store token: %v", err)
		return nil, origin, errx.ErrUnavailable
	}
	return &SolveOutput{Token: token, ExpiresAt: exp}, origin, nil
}

// Tokens embed the public site key so a token from another site is rejected
// as SITE_MISMATCH without a Redis lookup.
func newToken(siteKey string) (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return tokenPrefix + "." + siteKey + "." + base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// tokenSite returns the site key embedded in a well-formed token.
func tokenSite(token string) (string, bool) {
	if len(token) > maxTokenLen {
		return "", false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != tokenPrefix || !domain.ValidSiteKey(parts[1]) {
		return "", false
	}
	if b, err := base64.RawURLEncoding.DecodeString(parts[2]); err != nil || len(b) != 32 {
		return "", false
	}
	return parts[1], true
}

type VerifyInput struct {
	Authorization string
	SiteKey       string
	Token         string
	Action        string
	RequestID     string
}

type VerifyOutput struct {
	Success    bool   `json:"success"`
	Reason     string `json:"reason,omitempty"`
	SiteKey    string `json:"site_key,omitempty"`
	Action     string `json:"action,omitempty"`
	VerifiedAt int64  `json:"verified_at,omitempty"`
}

func (g *Gateway) authenticate(ctx context.Context, header string) (*domain.APIKey, error) {
	cred, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		return nil, errx.ErrUnauthorized
	}
	keyID, secret, ok := strings.Cut(cred, ".")
	if !ok || !domain.ValidKeyID(keyID) || secret == "" || len(secret) > 128 {
		return nil, errx.ErrUnauthorized
	}
	key, err := g.Sites.APIKey(ctx, keyID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, errx.ErrUnauthorized
	}
	if err != nil {
		logx.WithContext(ctx).Errorf("api key lookup: %v", err)
		return nil, errx.ErrUnavailable
	}
	sum := sha256.Sum256([]byte(secret))
	if subtle.ConstantTimeCompare(sum[:], key.SecretHash) != 1 || !key.Usable(g.Now()) {
		return nil, errx.ErrUnauthorized
	}
	return key, nil
}

// Verify authenticates the caller and checks the site before reading any
// idempotency record, so a revoked key or disabled site is never masked by a
// cached pass. Token problems are 200 rejections; caller problems are 4xx.
func (g *Gateway) Verify(ctx context.Context, in VerifyInput) (*VerifyOutput, error) {
	if !domain.ValidSiteKey(in.SiteKey) || !domain.ValidAction(in.Action) || !domain.ValidRequestID(in.RequestID) {
		return nil, errx.InvalidRequest("site_key、action 或 request_id 格式错误")
	}
	key, err := g.authenticate(ctx, in.Authorization)
	if err != nil {
		return nil, err
	}
	site, err := g.site(ctx, in.SiteKey)
	if err != nil {
		return nil, err
	}
	if site.ID != key.SiteID {
		return nil, errx.ErrSiteForbidden
	}
	if err := g.limit(ctx, dimVerify, key.KeyID, g.Settings.VerifyPerKeyPerMinute); err != nil {
		return nil, err
	}

	tokenHash := hashToken(in.Token)
	forced := ""
	if owner, ok := tokenSite(in.Token); !ok {
		forced = repository.ReasonInvalid
	} else if owner != site.PublicKey {
		forced = repository.ReasonSite
	}
	digest := sha256.Sum256([]byte(strconv.FormatInt(site.ID, 10) + "|" + in.Action + "|" + tokenHash))
	outcome, _, err := g.State.Consume(ctx, repository.ConsumeInput{
		SiteID:         site.ID,
		RequestID:      in.RequestID,
		RequestDigest:  hex.EncodeToString(digest[:]),
		TokenHash:      tokenHash,
		Action:         in.Action,
		AllowedOrigins: site.Origins,
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
		return &VerifyOutput{Success: true, SiteKey: site.PublicKey, Action: in.Action, VerifiedAt: at}, nil
	}
	return &VerifyOutput{Success: false, Reason: outcome}, nil
}
