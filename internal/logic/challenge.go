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
	"github.com/dkeepersun/BotGuard/internal/service/click"
	"github.com/dkeepersun/BotGuard/internal/service/clickshape"
	"github.com/dkeepersun/BotGuard/internal/service/clicktext"
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
	X       int            `json:"x,omitempty"`
	Targets []click.Target `json:"t,omitempty"`
	Score   int            `json:"s"`
	Reasons []string       `json:"r,omitempty"`
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

type ClickRender struct {
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Image   string `json:"image"`
	Prompt  string `json:"prompt"`
	Targets int    `json:"targets"`
}

type IssueOutput struct {
	Kind        string        `json:"kind"`
	Challenge   string        `json:"challenge"`
	ChallengeID string        `json:"challenge_id"`
	Algorithm   string        `json:"algorithm"`
	Params      pow.Params    `json:"params"`
	ExpiresAt   int64         `json:"expires_at"`
	Slider      *SliderRender `json:"slider,omitempty"`
	Click       *ClickRender  `json:"click,omitempty"`
}

// chooseKind applies the action policy to the risk level. Interactive mode
// and medium risk pick at random from the action's kinds, so an attacker
// cannot tune for one style; high risk gets the hardest kind offered. Every
// kind also carries proof of work; high risk and the fallback path make it
// costlier.
func (g *Gateway) chooseKind(policy domain.ActionPolicy, level string, fallback bool, pick func(int) int) (kind string, factor uint64) {
	factor = 1
	if level == risk.LevelHigh {
		factor = max(1, g.Settings.HighDifficultyFactor)
	}
	kinds := policy.Kinds
	if len(kinds) == 0 {
		kinds = []string{domain.KindSlider}
	}
	switch {
	case policy.Mode == domain.ModeInvisible:
		return domain.KindInvisible, factor
	case fallback:
		// The factors compound: choosing fallback must never make a
		// high-risk client's work cheaper than its hardest interactive kind.
		return domain.KindFallback, factor * max(1, g.Settings.FallbackDifficultyFactor)
	case level == risk.LevelHigh:
		return domain.Hardest(kinds), factor
	case policy.Mode == domain.ModeInteractive || level == risk.LevelMedium:
		return kinds[pick(len(kinds))], factor
	default:
		return domain.KindInvisible, factor
	}
}

// reserveSlot bounds concurrent image generation; release must be called.
func (g *Gateway) reserveSlot() (release func(), ok bool) {
	g.slotsOnce.Do(func() { g.puzzleSlots = make(chan struct{}, runtime.NumCPU()) })
	select {
	case g.puzzleSlots <- struct{}{}:
		return func() { <-g.puzzleSlots }, true
	default:
		// Never queue image generation behind a flood; shed instead.
		return nil, false
	}
}

func cryptoRand() (*rand.Rand, error) {
	var seed [32]byte
	if _, err := cryptorand.Read(seed[:]); err != nil {
		return nil, err
	}
	return rand.New(rand.NewChaCha8(seed)), nil
}

func (g *Gateway) newClickPuzzle(kind string) (click.Puzzle, error) {
	release, ok := g.reserveSlot()
	if !ok {
		return click.Puzzle{}, errors.New("click generation saturated")
	}
	defer release()
	if g.Clicks != nil {
		return g.Clicks(kind)
	}
	r, err := cryptoRand()
	if err != nil {
		return click.Puzzle{}, err
	}
	if kind == domain.KindClickText {
		return clicktext.Generate(r)
	}
	return clickshape.Generate(r)
}

func (g *Gateway) newPuzzle() (slider.Puzzle, error) {
	release, ok := g.reserveSlot()
	if !ok {
		return slider.Puzzle{}, errors.New("slider generation saturated")
	}
	defer release()
	if g.Puzzles != nil {
		return g.Puzzles()
	}
	r, err := cryptoRand()
	if err != nil {
		return slider.Puzzle{}, err
	}
	return slider.Generate(r)
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
		return nil, g.cachedOrigin(in.AppKey, origin), errx.RateLimited(retry)
	}
	if in.Fallback {
		if err := g.limit(ctx, dimFallback, ipBucket, g.Settings.FallbackPerIPPerMinute); err != nil {
			return nil, g.cachedOrigin(in.AppKey, origin), err
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

	if in.Signals != nil {
		s := *in.Signals
		s.PointerType = risk.NormalizePointer(s.PointerType)
		in.Signals = &s
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
	kind, factor := g.chooseKind(policy, level, in.Fallback, rand.IntN)

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
	var clickRender *ClickRender
	if kind == domain.KindClickText || kind == domain.KindClickShape {
		puzzle, err := g.newClickPuzzle(kind)
		if err != nil {
			logx.WithContext(ctx).Errorf("%s: %v", kind, err)
			return nil, origin, errx.ErrUnavailable
		}
		state.Targets = puzzle.Targets
		clickRender = &ClickRender{Width: puzzle.Width, Height: puzzle.Height, Image: puzzle.Image,
			Prompt: puzzle.Prompt, Targets: len(puzzle.Targets)}
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
		ExpiresAt: p.ExpiresAt, Slider: render, Click: clickRender}, origin, nil
}

// SliderAnswer carries the interactive answer. For the slider: the final
// piece position and the drag that produced it ([ms since pointer down, x, y]).
// For click kinds: each click as [ms since the panel appeared, x, y] and the
// pointer moves seen before each click.
type SliderAnswer struct {
	X            float64      `json:"x"`
	Trajectory   [][3]float64 `json:"trajectory"`
	Clicks       [][3]float64 `json:"clicks"`
	MovesBetween []int        `json:"moves_between"`
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
	// ExpiresIn is relative, so a browser with a wrong clock still knows
	// how long the token lives.
	ExpiresIn int64 `json:"expires_in"`
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
		// No app is known yet; answer with CORS only if the cache already
		// knows this origin, so the widget can read the error code.
		return nil, g.cachedOrigin(in.AppKey, in.Origin), errx.ErrChallengeInvalid
	}
	var state sealedState
	if len(private) > 0 && json.Unmarshal(private, &state) != nil {
		return nil, "", errx.ErrChallengeInvalid
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
	// Time checks come after the origin is known, so the widget can read
	// CHALLENGE_EXPIRED and fetch a new challenge.
	now := g.Now()
	skew := int64(g.Settings.ClockSkew / time.Second)
	if p.IssuedAt > now.Unix()+skew {
		return nil, origin, errx.ErrChallengeInvalid
	}
	if now.Unix() > p.ExpiresAt+skew {
		return nil, origin, errx.ErrChallengeExpired
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
	isClick := p.Kind == domain.KindClickText || p.Kind == domain.KindClickShape
	if isClick && (in.Answer == nil || len(in.Answer.Clicks) > click.MaxClicks || len(in.Answer.MovesBetween) > click.MaxClicks) {
		return nil, origin, errx.InvalidRequest("点选题需要 answer.clicks，且不超过 8 次点击")
	}
	if p.Kind == domain.KindSlider {
		if in.Answer == nil || len(in.Answer.Trajectory) > slider.MaxPoints {
			return nil, origin, errx.InvalidRequest("滑块题需要 answer，且轨迹不超过 400 个点")
		}
		for _, t := range in.Answer.Trajectory {
			points = append(points, slider.Point{T: t[0], X: t[1], Y: t[2]})
		}
	}
	// Another replica's clock may run up to skew behind ours and still accept
	// the challenge until its own exp+skew, so keep the mark that much longer.
	remaining := time.Unix(p.ExpiresAt+2*skew, 0).Sub(now)
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
	input := risk.PointerMouse
	if in.Signals != nil {
		input = risk.NormalizePointer(in.Signals.PointerType)
	}
	if p.Kind == domain.KindSlider {
		if reason := slider.Check(float64(state.X), in.Answer.X, g.Settings.SliderTolerance, points, input); reason != "" {
			// One public code for every failure; the specific reason stays in our logs.
			logx.WithContext(ctx).Infof("slider rejected: %s (app %d, %s)", reason, app.ID, input)
			return nil, origin, g.fail(ctx, ipBucket, errx.ErrAnswerInvalid)
		}
	}

	if isClick {
		// Click kinds cannot be answered from the keyboard; hold such a
		// claim to the mouse rules.
		if input == risk.PointerKeyboard {
			input = risk.PointerMouse
		}
		if reason := click.Check(state.Targets, in.Answer.Clicks, in.Answer.MovesBetween, input); reason != "" {
			logx.WithContext(ctx).Infof("%s rejected: %s (app %d, %s)", p.Kind, reason, app.ID, input)
			return nil, origin, g.fail(ctx, ipBucket, errx.ErrAnswerInvalid)
		}
	}

	// Behaviour during the interaction can only add risk, never remove it.
	final := risk.Assessment{Score: state.Score, Reasons: state.Reasons}
	if in.Signals != nil && in.Signals.UntrustedEvent {
		final = final.Add(risk.Assessment{Score: risk.Weights[risk.ReasonUntrustedEvent], Reasons: []string{risk.ReasonUntrustedEvent}})
	}
	interactive := p.Kind == domain.KindSlider || isClick
	if p.Kind == domain.KindSlider && input == risk.PointerKeyboard {
		final = final.Add(risk.Assessment{Score: risk.Weights[risk.ReasonKeyboardInput], Reasons: []string{risk.ReasonKeyboardInput}})
	}
	if interactive && input == risk.PointerTouch {
		final = final.Add(risk.Assessment{Score: risk.Weights[risk.ReasonTouchInput], Reasons: []string{risk.ReasonTouchInput}})
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
	return &SolveOutput{Token: token, ExpiresAt: exp, ExpiresIn: int64(g.Settings.TokenTTL / time.Second)}, origin, nil
}
