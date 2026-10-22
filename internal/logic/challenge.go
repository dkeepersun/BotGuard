package logic

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net/netip"
	"runtime"
	"strconv"
	"time"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/errx"
	"github.com/dkeepersun/BotGuard/internal/repository"
	"github.com/dkeepersun/BotGuard/internal/service/challenge"
	"github.com/dkeepersun/BotGuard/internal/service/click"
	"github.com/dkeepersun/BotGuard/internal/service/clickicon"
	"github.com/dkeepersun/BotGuard/internal/service/clickshape"
	"github.com/dkeepersun/BotGuard/internal/service/clicktext"
	"github.com/dkeepersun/BotGuard/internal/service/clientip"
	"github.com/dkeepersun/BotGuard/internal/service/jigsaw"
	"github.com/dkeepersun/BotGuard/internal/service/pow"
	"github.com/dkeepersun/BotGuard/internal/service/risk"
	"github.com/dkeepersun/BotGuard/internal/service/rotate"
	"github.com/dkeepersun/BotGuard/internal/service/slider"
	"github.com/dkeepersun/BotGuard/internal/service/spatial"
	"github.com/zeromicro/go-zero/core/logx"
)

const (
	dimFallback = "fallback"
	// dimAppInteractive counts interactive challenges per app.
	dimAppInteractive = "appinteractive"
)

// sealedState travels inside the signed challenge, encrypted: the browser
// carries it back on solve but can neither read nor change it.
type sealedState struct {
	X       int            `json:"x,omitempty"`
	Targets []click.Target `json:"t,omitempty"`
	// Swap is the jigsaw answer: the two swapped tiles.
	Swap    []int    `json:"w,omitempty"`
	Score   int      `json:"s"`
	Reasons []string `json:"r,omitempty"`
	// Ladder is set when the page asked for a kind ladder: the solve then
	// moves the client's rung.
	Ladder bool `json:"l,omitempty"`
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
	// Select is the page's requested kinds or ladder; zero leaves the
	// choice to the action policy.
	Select Selection
}

type SliderRender struct {
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Background string `json:"background"`
	Piece      string `json:"piece"`
	PieceY     int    `json:"piece_y"`
	PieceSize  int    `json:"piece_size"`
}

type RotateRender struct {
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Background string `json:"background"`
	Disc       string `json:"disc"`
	CX         int    `json:"cx"`
	CY         int    `json:"cy"`
	Radius     int    `json:"radius"`
	Track      int    `json:"track"`
}

type ClickRender struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Image  string `json:"image"`
	Prompt string `json:"prompt"`
	// PromptImage shows the targets as pictures (click_icon).
	PromptImage string `json:"prompt_image,omitempty"`
	Targets     int    `json:"targets"`
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
	Rotate      *RotateRender `json:"rotate,omitempty"`
	Jigsaw      *JigsawRender `json:"jigsaw,omitempty"`
}

// JigsawRender is the swapped picture and its tile grid.
type JigsawRender struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Image  string `json:"image"`
	Rows   int    `json:"rows"`
	Cols   int    `json:"cols"`
}

// chooseKind applies the action policy to the risk level. Interactive mode
// and medium risk pick at random from the action's kinds, so an attacker
// cannot tune for one style; high risk gets the hardest kind offered. Every
// kind also carries proof of work; high risk and the fallback path make it
// costlier. requested is the page's selection, already resolved against the
// action (Selection.candidates): below high risk the pick comes from it.
func (g *Gateway) chooseKind(policy domain.ActionPolicy, level string, fallback bool, requested []string, pick func(int) int) (kind string, factor uint64) {
	factor = 1
	if level == risk.LevelHigh {
		factor = max(1, g.Settings.HighDifficultyFactor)
	}
	kinds := actionKinds(policy)
	switch {
	case policy.Mode == domain.ModeInvisible:
		return domain.KindInvisible, factor
	case fallback:
		// The factors compound: choosing fallback must never make a
		// high-risk client's work cheaper than its hardest interactive kind.
		return domain.KindFallback, factor * max(1, g.Settings.FallbackDifficultyFactor)
	case level == risk.LevelHigh:
		// The page's selection cannot lower the floor for a suspicious client.
		return domain.Hardest(kinds), factor
	case len(requested) > 0:
		return requested[pick(len(requested))], factor
	case policy.Mode == domain.ModeInteractive || level == risk.LevelMedium:
		return kinds[pick(len(kinds))], factor
	default:
		return domain.KindInvisible, factor
	}
}

// slots is the semaphore bounding concurrent image generation, one slot per
// CPU, shared by requests and pool workers.
func (g *Gateway) slots() chan struct{} {
	g.slotsOnce.Do(func() { g.puzzleSlots = make(chan struct{}, runtime.NumCPU()) })
	return g.puzzleSlots
}

// errSaturated means every generation slot is busy and the pool is empty.
var errSaturated = errors.New("puzzle generation saturated")

// puzzle returns a fresh puzzle of an interactive kind: from the pool when it
// has one, otherwise generated now if a slot is free. It never queues behind
// a flood; it answers errSaturated instead.
func (g *Gateway) puzzle(kind string) (any, error) {
	if v, ok := g.pool.take(kind); ok {
		return v, nil
	}
	slots := g.slots()
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	default:
		return nil, errSaturated
	}
	return g.generate(kind)
}

func cryptoRand() (*rand.Rand, error) {
	var seed [32]byte
	if _, err := cryptorand.Read(seed[:]); err != nil {
		return nil, err
	}
	return rand.New(rand.NewChaCha8(seed)), nil
}

// generate makes one puzzle of kind: a slider.Puzzle, rotate.Puzzle,
// jigsaw.Puzzle or click.Puzzle. It takes no slot; callers bound it.
func (g *Gateway) generate(kind string) (any, error) {
	switch {
	case kind == domain.KindSlider && g.Puzzles != nil:
		return g.Puzzles()
	case kind == domain.KindRotate && g.Rotates != nil:
		return g.Rotates()
	case kind == domain.KindJigsaw && g.Jigsaws != nil:
		return g.Jigsaws()
	case domain.ClickKind(kind) && g.Clicks != nil:
		return g.Clicks(kind)
	}
	r, err := cryptoRand()
	if err != nil {
		return nil, err
	}
	switch kind {
	case domain.KindSlider:
		return slider.Generate(r)
	case domain.KindRotate:
		return rotate.Generate(r)
	case domain.KindJigsaw:
		return jigsaw.Generate(r)
	case domain.KindClickText:
		return clicktext.Generate(r)
	case domain.KindClickOrder:
		return clicktext.GenerateOrder(r)
	case domain.KindClickIcon:
		return clickicon.Generate(r)
	case domain.KindSpatial:
		return spatial.Generate(r)
	case domain.KindClickShape:
		return clickshape.Generate(r)
	default:
		return nil, fmt.Errorf("no generator for kind %q", kind)
	}
}

// interactivePuzzle returns a puzzle of kind, or nil and the degrade cause
// when the app is over its interactive quota or generation is saturated; the
// caller then degrades.
// The quota counts across addresses and replicas, so a flood spread over many
// IPs against one app cannot take the image capacity every app shares.
func (g *Gateway) interactivePuzzle(ctx context.Context, appID int64, kind string) (puzzle any, cause string, err error) {
	if quota := g.Settings.InteractivePerAppPerMinute; quota > 0 {
		ok, _, err := g.Limiter.Allow(ctx, dimAppInteractive, strconv.FormatInt(appID, 10), quota, rateWindow)
		if err != nil {
			logx.WithContext(ctx).Errorf("rate limiter %s: %v", dimAppInteractive, err)
			return nil, "", errx.ErrUnavailable
		}
		if !ok {
			return nil, DegradeQuota, nil
		}
	}
	v, err := g.puzzle(kind)
	if errors.Is(err, errSaturated) {
		return nil, DegradeSaturated, nil
	}
	if err != nil {
		logx.WithContext(ctx).Errorf("%s: %v", kind, err)
		return nil, "", errx.ErrUnavailable
	}
	return v, "", nil
}

// Issue scores the request, picks the challenge kind and signs a challenge
// without writing any per-challenge state. allowedOrigin is set once the
// origin is known to be allowed for the app, so the handler can attach CORS
// headers even to later errors.
func (g *Gateway) Issue(ctx context.Context, in IssueInput) (out *IssueOutput, allowedOrigin string, err error) {
	var st struct {
		appID       int64
		kind, level string
		reasons     []string
	}
	defer func() {
		if st.appID == 0 {
			return
		}
		if err != nil {
			g.record(ctx, st.appID, []string{"issue_fail", "issue_fail:" + errCode(err)}, nil)
			return
		}
		fields := append([]string{"issue", "issue:" + st.kind, "risk:" + st.level}, reasonFields("reason:", st.reasons)...)
		g.record(ctx, st.appID, fields, nil)
	}()
	origin, ok := domain.NormalizeOrigin(in.Origin)
	if !ok {
		return nil, "", errx.ErrOriginNotAllowed
	}
	ipBucket := clientip.Bucket(in.ClientIP)
	count, retry, err := g.Limiter.Take(ctx, dimChallenge, ipBucket, g.Settings.ChallengePerIPPerMinute, rateWindow)
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
	st.appID = app.ID
	if !app.AllowsOrigin(origin) {
		return nil, "", errx.ErrOriginNotAllowed
	}
	if err := in.Select.Validate(); err != nil {
		return nil, origin, err
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
	// Invisible mode never shows a picture, whatever the page asks.
	ladder := in.Select.ladder() && policy.Mode != domain.ModeInvisible
	rung := 0
	if ladder {
		rung = g.ladderRung(ctx, app.ID, ipBucket)
	}
	kind, factor := g.chooseKind(policy, level, in.Fallback, in.Select.candidates(policy, level, rung), rand.IntN)
	var puzzle any
	if domain.ValidInteractiveKind(kind) {
		var cause string
		if puzzle, cause, err = g.interactivePuzzle(ctx, app.ID, kind); err != nil {
			return nil, origin, err
		}
		if puzzle == nil {
			g.Monitor.Degraded(cause)
			// No image capacity for this app right now. Refusing would hand
			// a flood its goal, so the client gets harder PoW instead and the
			// business server sees why in risk_reasons.
			kind, factor = domain.KindInvisible, factor*max(1, g.Settings.DegradedDifficultyFactor)
			assessment = assessment.Add(risk.Assessment{Reasons: []string{risk.ReasonCapacityDegraded}})
		}
	}
	st.kind, st.level, st.reasons = kind, level, assessment.Reasons

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
	state := sealedState{Score: assessment.Score, Reasons: assessment.Reasons, Ladder: ladder}
	var (
		render       *SliderRender
		rotateRender *RotateRender
		jigsawRender *JigsawRender
		clickRender  *ClickRender
	)
	switch pz := puzzle.(type) {
	case slider.Puzzle:
		state.X = pz.X
		render = &SliderRender{Width: slider.Width, Height: slider.Height, Background: pz.Background,
			Piece: pz.Piece, PieceY: pz.Y, PieceSize: slider.PieceSize}
	case rotate.Puzzle:
		state.X = int(math.Round(pz.AnswerX()))
		rotateRender = &RotateRender{Width: rotate.Width, Height: rotate.Height, Background: pz.Background,
			Disc: pz.Disc, CX: pz.CX, CY: pz.CY, Radius: pz.Radius, Track: rotate.Track}
	case jigsaw.Puzzle:
		state.Swap = []int{pz.A, pz.B}
		jigsawRender = &JigsawRender{Width: jigsaw.Width, Height: jigsaw.Height, Image: pz.Image, Rows: jigsaw.Rows, Cols: jigsaw.Cols}
	case click.Puzzle:
		state.Targets = pz.Targets
		clickRender = &ClickRender{Width: pz.Width, Height: pz.Height, Image: pz.Image,
			Prompt: pz.Prompt, PromptImage: pz.PromptImage, Targets: len(pz.Targets)}
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
		ExpiresAt: p.ExpiresAt, Slider: render, Click: clickRender, Rotate: rotateRender, Jigsaw: jigsawRender}, origin, nil
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
	// Swap is the jigsaw answer: the dragged tile and the one it was dropped
	// on; Trajectory then holds the drag in image coordinates.
	Swap []int `json:"swap"`
}

type SolveInput struct {
	// HeaderUA is the request's User-Agent header, for the event list only.
	HeaderUA  string
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
	var st struct {
		appID                 int64
		action, kind, pointer string
		level                 string
	}
	defer func() {
		if st.appID == 0 {
			return
		}
		ev := &repository.Event{Type: repository.EventSolve, Action: st.action, Kind: st.kind, Level: st.level,
			Pointer: st.pointer, IP: g.ipTag(in.ClientIP), Result: "pass", Device: risk.Device(risk.CleanUA(in.HeaderUA))}
		fields := []string{"solve"}
		if err != nil {
			ev.Result = errCode(err)
			fields = append(fields, "solve_fail", "solve_fail:"+ev.Result, "solve_fail_kind:"+st.kind)
		} else {
			fields = append(fields, "solve_ok", "solve_ok:"+st.kind)
		}
		g.record(ctx, st.appID, fields, ev)
	}()
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
	st.appID, st.action, st.kind = app.ID, p.Action, p.Kind
	st.level = risk.Level(state.Score, g.Settings.RiskMedium, g.Settings.RiskHigh)
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
	isClick := domain.ClickKind(p.Kind)
	isJigsaw := p.Kind == domain.KindJigsaw
	if isJigsaw && (in.Answer == nil || len(in.Answer.Swap) != 2 || len(in.Answer.Trajectory) > jigsaw.MaxPoints) {
		return nil, origin, errx.InvalidRequest("拼图题需要 answer.swap，且轨迹不超过 400 个点")
	}
	if isClick && (in.Answer == nil || len(in.Answer.Clicks) > click.MaxClicks || len(in.Answer.MovesBetween) > click.MaxClicks) {
		return nil, origin, errx.InvalidRequest("点选题需要 answer.clicks，且不超过 8 次点击")
	}
	if domain.DragKind(p.Kind) {
		if in.Answer == nil || len(in.Answer.Trajectory) > slider.MaxPoints {
			return nil, origin, errx.InvalidRequest("拖动题需要 answer，且轨迹不超过 400 个点")
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
	// A wrong answer moves the client up its ladder as well as raising its
	// risk; only proof and answer failures count.
	fail := func(e *errx.Error) error {
		if state.Ladder {
			g.climb(ctx, app.ID, ipBucket, false)
		}
		return g.fail(ctx, ipBucket, e)
	}
	if err := pow.Verify(p.Algorithm, p.ChallengeID, p.Params, in.Nonces); err != nil {
		if errors.Is(err, pow.ErrProofInvalid) {
			return nil, origin, fail(errx.ErrProofInvalid)
		}
		return nil, origin, errx.ErrChallengeInvalid
	}
	input := risk.PointerMouse
	if in.Signals != nil {
		input = risk.NormalizePointer(in.Signals.PointerType)
	}
	if domain.DragKind(p.Kind) || isClick || isJigsaw {
		st.pointer = input
	}
	if isJigsaw {
		// No keyboard path: such a claim gets the mouse rules.
		if input == risk.PointerKeyboard {
			input = risk.PointerMouse
		}
		if len(state.Swap) != 2 {
			return nil, origin, errx.ErrChallengeInvalid
		}
		if reason := jigsaw.Check(state.Swap[0], state.Swap[1], in.Answer.Swap, in.Answer.Trajectory, input); reason != "" {
			logx.WithContext(ctx).Infof("%s rejected: %s (app %d, %s)", p.Kind, reason, app.ID, input)
			return nil, origin, fail(errx.ErrAnswerInvalid)
		}
	}
	if domain.DragKind(p.Kind) {
		// Rotate reuses the slider drag: the drag distance maps to the angle.
		tolerance := g.Settings.SliderTolerance
		if p.Kind == domain.KindRotate {
			tolerance = rotate.ToleranceX(float64(g.Settings.RotateToleranceDegrees))
		}
		if reason := slider.Check(float64(state.X), in.Answer.X, tolerance, points, input); reason != "" {
			// One public code for every failure; the specific reason stays in our logs.
			logx.WithContext(ctx).Infof("%s rejected: %s (app %d, %s)", p.Kind, reason, app.ID, input)
			return nil, origin, fail(errx.ErrAnswerInvalid)
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
			return nil, origin, fail(errx.ErrAnswerInvalid)
		}
	}

	// Behaviour during the interaction can only add risk, never remove it.
	final := risk.Assessment{Score: state.Score, Reasons: state.Reasons}
	if in.Signals != nil && in.Signals.UntrustedEvent {
		final = final.Add(risk.Assessment{Score: risk.Weights[risk.ReasonUntrustedEvent], Reasons: []string{risk.ReasonUntrustedEvent}})
	}
	interactive := domain.DragKind(p.Kind) || isClick || isJigsaw
	if domain.DragKind(p.Kind) && input == risk.PointerKeyboard {
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
	st.level = risk.Level(final.Score, g.Settings.RiskMedium, g.Settings.RiskHigh)
	// Keep the record past expiry so verify can say EXPIRED/CONSUMED, not INVALID.
	keyTTL := g.Settings.TokenTTL + g.Settings.ResultTTL
	rec := repository.TokenRecord{Action: p.Action, Origin: p.Origin, ExpiresAt: exp, Kind: p.Kind,
		RiskLevel: risk.Level(final.Score, g.Settings.RiskMedium, g.Settings.RiskHigh), Reasons: final.Reasons}
	if err := g.State.PutToken(ctx, app.ID, hashToken(token), rec, keyTTL); err != nil {
		// The challenge is already marked; failing here issues fewer tokens, never more.
		logx.WithContext(ctx).Errorf("store token: %v", err)
		return nil, origin, errx.ErrUnavailable
	}
	if state.Ladder {
		g.climb(ctx, app.ID, ipBucket, true)
	}
	return &SolveOutput{Token: token, ExpiresAt: exp, ExpiresIn: int64(g.Settings.TokenTTL / time.Second)}, origin, nil
}
