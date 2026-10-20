package logic

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"net/netip"
	"runtime"
	"time"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/errx"
	"github.com/dkeepersun/BotGuard/internal/repository"
	"github.com/dkeepersun/BotGuard/internal/service/challenge"
	"github.com/dkeepersun/BotGuard/internal/service/clientip"
	"github.com/dkeepersun/BotGuard/internal/service/pow"
	"github.com/dkeepersun/BotGuard/internal/service/risk"
	"github.com/dkeepersun/BotGuard/internal/service/slider"
	"github.com/zeromicro/go-zero/core/logx"
)

const dimFallback = "fallback"

// sealedState travels inside the signed challenge, encrypted: the browser
// carries it back on solve but can neither read nor change it.
type sealedState struct {
	X       int      `json:"x,omitempty"`
	Score   int      `json:"s"`
	Reasons []string `json:"r,omitempty"`
}

type IssueInput struct {
	AppKey   string
	Action   string
	Origin   string
	ClientIP netip.Addr
	HeaderUA string
	// Signals is nil when the client sent none.
	Signals  *risk.Signals
	Fallback bool
}

type SliderRender struct {
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Background string `json:"background"`
	Piece      string `json:"piece"`
	PieceY     int    `json:"piece_y"`
	PieceSize  int    `json:"piece_size"`
}

type IssueOutput struct {
	Kind        string        `json:"kind"`
	Challenge   string        `json:"challenge"`
	ChallengeID string        `json:"challenge_id"`
	Algorithm   string        `json:"algorithm"`
	Params      pow.Params    `json:"params"`
	ExpiresAt   int64         `json:"expires_at"`
	Slider      *SliderRender `json:"slider,omitempty"`
}

// chooseKind applies the action mode to the risk level. Every kind also
// carries proof of work; high risk and the fallback path make it costlier.
func (g *Gateway) chooseKind(mode, level string, fallback bool) (kind string, factor uint64) {
	factor = 1
	if level == risk.LevelHigh {
		factor = max(1, g.Settings.HighDifficultyFactor)
	}
	switch {
	case mode == domain.ModeInvisible:
		return domain.KindInvisible, factor
	case fallback:
		return domain.KindFallback, max(factor, g.Settings.FallbackDifficultyFactor)
	case mode == domain.ModeSlider || level != risk.LevelLow:
		return domain.KindSlider, factor
	default:
		return domain.KindInvisible, factor
	}
}

func (g *Gateway) newPuzzle() (slider.Puzzle, error) {
	g.slotsOnce.Do(func() { g.puzzleSlots = make(chan struct{}, runtime.NumCPU()) })
	select {
	case g.puzzleSlots <- struct{}{}:
		defer func() { <-g.puzzleSlots }()
	default:
		// Never queue image generation behind a flood; shed instead.
		return slider.Puzzle{}, errors.New("slider generation saturated")
	}
	if g.Puzzles != nil {
		return g.Puzzles()
	}
	var seed [32]byte
	if _, err := cryptorand.Read(seed[:]); err != nil {
		return slider.Puzzle{}, err
	}
	return slider.Generate(rand.New(rand.NewChaCha8(seed)))
}

// Issue scores the request, picks the challenge kind and signs a challenge
// without writing any per-challenge state. allowedOrigin is set once the
// origin is known to be allowed for the app, so the handler can attach CORS
// headers even to later errors.
func (g *Gateway) Issue(ctx context.Context, in IssueInput) (out *IssueOutput, allowedOrigin string, err error) {
	origin, ok := domain.NormalizeOrigin(in.Origin)
	if !ok {
		return nil, "", errx.ErrOriginNotAllowed
	}
	ipBucket := clientip.Bucket(in.ClientIP)
	count, retry, err := g.Limiter.Take(ctx, dimChallenge, ipBucket, rateWindow)
	if err != nil {
		logx.WithContext(ctx).Errorf("rate limiter: %v", err)
		return nil, "", errx.ErrUnavailable
	}
	if count > int64(g.Settings.ChallengePerIPPerMinute) {
		return nil, "", errx.RateLimited(retry)
	}
	if in.Fallback {
		if err := g.limit(ctx, dimFallback, ipBucket, g.Settings.FallbackPerIPPerMinute); err != nil {
			return nil, "", err
		}
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

	failures, err := g.State.Failures(ctx, ipBucket)
	if err != nil {
		logx.WithContext(ctx).Errorf("read failures: %v", err)
		return nil, origin, errx.ErrUnavailable
	}
	assessment := risk.Assess(in.Signals, risk.Facts{
		RecentFailures: failures,
		NearRateLimit:  count*5 >= int64(g.Settings.ChallengePerIPPerMinute)*4,
		HeaderUA:       risk.CleanUA(in.HeaderUA),
		Fallback:       in.Fallback && policy.Mode != domain.ModeInvisible,
	})
	level := risk.Level(assessment.Score, g.Settings.RiskMedium, g.Settings.RiskHigh)
	kind, factor := g.chooseKind(policy.Mode, level, in.Fallback)

	difficulty := min(policy.Difficulty*factor, g.Settings.MaxDifficulty)
	params, err := pow.NewParams(policy.Puzzles, difficulty)
	if err != nil {
		logx.WithContext(ctx).Errorf("app %d action %s has invalid pow policy", app.ID, in.Action)
		return nil, origin, errx.ErrUnavailable
	}
	id, err := challenge.NewID()
	if err != nil {
		return nil, origin, errx.ErrUnavailable
	}
	state := sealedState{Score: assessment.Score, Reasons: assessment.Reasons}
	var render *SliderRender
	if kind == domain.KindSlider {
		puzzle, err := g.newPuzzle()
		if err != nil {
			logx.WithContext(ctx).Errorf("slider: %v", err)
			return nil, origin, errx.ErrUnavailable
		}
		state.X = puzzle.X
		render = &SliderRender{Width: slider.Width, Height: slider.Height, Background: puzzle.Background,
			Piece: puzzle.Piece, PieceY: puzzle.Y, PieceSize: slider.PieceSize}
	}
	private, err := json.Marshal(state)
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
		Kind:          kind,
	}
	signed, err := g.Keys.Sign(p, private)
	if err != nil {
		logx.WithContext(ctx).Errorf("sign challenge: %v", err)
		return nil, origin, errx.ErrUnavailable
	}
	return &IssueOutput{Kind: kind, Challenge: signed, ChallengeID: id, Algorithm: p.Algorithm, Params: params,
		ExpiresAt: p.ExpiresAt, Slider: render}, origin, nil
}

// SliderAnswer is the final piece position and the drag that produced it,
// each trajectory point being [ms since pointer down, x, y].
type SliderAnswer struct {
	X          float64      `json:"x"`
	Trajectory [][3]float64 `json:"trajectory"`
}

type SolveInput struct {
	AppKey    string
	Challenge string
	Nonces    []string
	Origin    string
	ClientIP  netip.Addr
	Answer    *SliderAnswer
	Signals   *risk.Signals
}

type SolveOutput struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
}

// fail records a failed attempt for the IP, raising its risk next time.
func (g *Gateway) fail(ctx context.Context, ipBucket string, e *errx.Error) error {
	if err := g.State.RecordFailure(ctx, ipBucket, g.Settings.FailureWindow); err != nil {
		logx.WithContext(ctx).Errorf("record failure: %v", err)
	}
	return e
}

// Solve follows the cheap-to-expensive order in the contract. The challenge
// is marked used before any proof is checked, so each challenge gets exactly
// one attempt.
func (g *Gateway) Solve(ctx context.Context, in SolveInput) (out *SolveOutput, allowedOrigin string, err error) {
	if !domain.ValidAppKey(in.AppKey) || in.Challenge == "" {
		return nil, "", errx.InvalidRequest("app_key 或 challenge 格式错误")
	}
	if len(in.Challenge) > challenge.MaxLen {
		return nil, "", errx.InvalidRequest("challenge 过长")
	}
	p, private, err := g.Keys.Open(in.Challenge)
	if err != nil {
		return nil, "", errx.ErrChallengeInvalid
	}
	var state sealedState
	if len(private) > 0 && json.Unmarshal(private, &state) != nil {
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
	ipBucket := clientip.Bucket(in.ClientIP)
	if err := g.limit(ctx, dimSolve, ipBucket, g.Settings.SolvePerIPPerMinute); err != nil {
		return nil, origin, err
	}
	if p.Algorithm != pow.AlgSHA256Target {
		return nil, origin, errx.ErrChallengeInvalid
	}
	if err := pow.CheckNonces(p.Params, in.Nonces); err != nil {
		return nil, origin, errx.InvalidRequest("nonces 数量或格式错误")
	}
	var points []slider.Point
	if p.Kind == domain.KindSlider {
		if in.Answer == nil || len(in.Answer.Trajectory) > slider.MaxPoints {
			return nil, origin, errx.InvalidRequest("滑块题需要 answer，且轨迹不超过 400 个点")
		}
		for _, t := range in.Answer.Trajectory {
			points = append(points, slider.Point{T: t[0], X: t[1], Y: t[2]})
		}
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
			return nil, origin, g.fail(ctx, ipBucket, errx.ErrProofInvalid)
		}
		return nil, origin, errx.ErrChallengeInvalid
	}
	if p.Kind == domain.KindSlider {
		input := "mouse"
		if in.Signals != nil && in.Signals.PointerType != "" {
			input = in.Signals.PointerType
		}
		if reason := slider.Check(float64(state.X), in.Answer.X, g.Settings.SliderTolerance, points, input); reason != "" {
			// One public code for every failure; the specific reason stays in our logs.
			logx.WithContext(ctx).Infof("slider rejected: %s (app %d, %s)", reason, app.ID, input)
			return nil, origin, g.fail(ctx, ipBucket, errx.ErrAnswerInvalid)
		}
	}

	// Behaviour during the interaction can only add risk, never remove it.
	final := risk.Assessment{Score: state.Score, Reasons: state.Reasons}
	if in.Signals != nil && in.Signals.UntrustedEvent {
		final = final.Add(risk.Assessment{Score: risk.Weights[risk.ReasonUntrustedEvent], Reasons: []string{risk.ReasonUntrustedEvent}})
	}
	if p.Kind == domain.KindSlider && in.Signals != nil && in.Signals.PointerType == "keyboard" {
		final = final.Add(risk.Assessment{Score: risk.Weights[risk.ReasonKeyboardInput], Reasons: []string{risk.ReasonKeyboardInput}})
	}

	token, err := newToken(app.AppKey)
	if err != nil {
		return nil, origin, errx.ErrUnavailable
	}
	exp := now.Add(g.Settings.TokenTTL).Unix()
	// Keep the record past expiry so verify can say EXPIRED/CONSUMED, not INVALID.
	keyTTL := g.Settings.TokenTTL + g.Settings.ResultTTL
	rec := repository.TokenRecord{Action: p.Action, Origin: p.Origin, ExpiresAt: exp, Kind: p.Kind,
		RiskLevel: risk.Level(final.Score, g.Settings.RiskMedium, g.Settings.RiskHigh), Reasons: final.Reasons}
	if err := g.State.PutToken(ctx, app.ID, hashToken(token), rec, keyTTL); err != nil {
		// The challenge is already marked; failing here issues fewer tokens, never more.
		logx.WithContext(ctx).Errorf("store token: %v", err)
		return nil, origin, errx.ErrUnavailable
	}
	return &SolveOutput{Token: token, ExpiresAt: exp}, origin, nil
}
