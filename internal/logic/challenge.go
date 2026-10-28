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
	"slices"
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
	"github.com/dkeepersun/BotGuard/internal/service/clock"
	"github.com/dkeepersun/BotGuard/internal/service/grid"
	"github.com/dkeepersun/BotGuard/internal/service/gridcount"
	"github.com/dkeepersun/BotGuard/internal/service/imagegen"
	"github.com/dkeepersun/BotGuard/internal/service/jigsaw"
	"github.com/dkeepersun/BotGuard/internal/service/mirror"
	"github.com/dkeepersun/BotGuard/internal/service/pow"
	"github.com/dkeepersun/BotGuard/internal/service/prompt"
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
	// dimPrefixInteractive counts interactive challenges per app and client
	// network prefix (clientip.Prefix).
	dimPrefixInteractive = "prefixinteractive"
	// dimChallengePrefix counts challenges per client network prefix, across
	// apps: many addresses from one network cost like one.
	dimChallengePrefix = "challengeprefix"

	// minHumanSolve is the least time between issuing an image challenge and
	// its answer that a person needs to see the picture and act on it.
	minHumanSolve = 800 * time.Millisecond
)

// failSubject is the failure-counter subject for a whole network prefix; the
// bare IP bucket is the other one.
func failSubject(prefix string) string { return "net:" + prefix }

// sealedState travels inside the signed challenge, encrypted: the browser
// carries it back on solve but can neither read nor change it.
type sealedState struct {
	X       int            `json:"x,omitempty"`
	Targets []click.Target `json:"t,omitempty"`
	// Swap is the jigsaw answer: the two swapped tiles.
	Swap []int `json:"w,omitempty"`
	// Grid is the grid kinds' geometry and the cells to select.
	Grid    *gridState `json:"g,omitempty"`
	Score   int        `json:"s"`
	Reasons []string   `json:"r,omitempty"`
	// Ladder is set when the page asked for a kind ladder: the solve then
	// moves the client's rung.
	Ladder bool `json:"l,omitempty"`
	// What the gateway saw at issue, compared on solve: the pointer the
	// widget was activated with, the client's network prefix and the issue
	// time in unix milliseconds. Challenges from older gateways lack them.
	Pointer  string `json:"p,omitempty"`
	Prefix   string `json:"n,omitempty"`
	IssuedMs int64  `json:"i,omitempty"`
}

type gridState struct {
	Width  int   `json:"w"`
	Height int   `json:"h"`
	Rows   int   `json:"r"`
	Cols   int   `json:"c"`
	Cells  []int `json:"a"`
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
	// Lang is the page's language tag; prompts come back in the nearest
	// supported language (package prompt), Simplified Chinese when empty.
	Lang string
	// Widget is the version the browser widget reports, empty before 0.3.0.
	// It only feeds the console's count of outdated widgets: a script can
	// leave it out as easily as an old widget, so it never picks the kind.
	Widget string
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
	Grid        *GridRender   `json:"grid,omitempty"`
}

// GridRender is a select-the-cells picture: the cells split the image
// evenly into Rows × Cols.
type GridRender struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Image  string `json:"image"`
	Prompt string `json:"prompt"`
	// PromptImage is a reference picture (mirror).
	PromptImage string `json:"prompt_image,omitempty"`
	Rows        int    `json:"rows"`
	Cols        int    `json:"cols"`
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
// costlier. A high-risk client gets no fallback: the fallback is PoW alone,
// which a bot computes natively in milliseconds, so offering it there would
// let any suspicious client skip the picture by asking. requested is the page's selection, already resolved against the
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
	case level == risk.LevelHigh:
		// Neither the fallback nor the page's selection can lower the floor
		// for a suspicious client.
		return domain.Hardest(kinds), factor
	case fallback:
		return domain.KindFallback, factor * max(1, g.Settings.FallbackDifficultyFactor)
	case len(requested) > 0:
		return requested[pick(len(requested))], factor
	case policy.Mode == domain.ModeInteractive || level == risk.LevelMedium:
		return kinds[pick(len(kinds))], factor
	default:
		return domain.KindInvisible, factor
	}
}

// textKinds ask the user to read Chinese characters.
var textKinds = []string{domain.KindClickText, domain.KindClickOrder}

// readable drops the text kinds from the action for pages in a language
// other than Chinese; with nothing left it offers the slider.
func readable(policy domain.ActionPolicy, lang string) domain.ActionPolicy {
	if prompt.Chinese(lang) {
		return policy
	}
	kinds := slices.DeleteFunc(slices.Clone(actionKinds(policy)), func(k string) bool { return slices.Contains(textKinds, k) })
	if len(kinds) == 0 {
		kinds = []string{domain.KindSlider}
	}
	policy.Kinds = kinds
	return policy
}

// promptIn picks the prompt in lang; zh is the Simplified Chinese one, for
// puzzles made without translations.
func promptIn(t prompt.Text, zh, lang string) string {
	if s := t[lang]; s != "" {
		return s
	}
	return zh
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
//
// The pool holds pictures from the default background source only, so an
// app with its own setting always gets a fresh one.
func (g *Gateway) puzzle(ctx context.Context, app *domain.App, kind string) (any, error) {
	pooled := app.Background.Default()
	if pooled {
		if v, ok := g.pool.take(kind); ok {
			return v, nil
		}
	}
	slots := g.slots()
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	default:
		return nil, errSaturated
	}
	src := g.Backgrounds.Default()
	if !pooled {
		src = g.Backgrounds.Source(ctx, app)
	}
	return g.generate(kind, src)
}

func cryptoRand() (*rand.Rand, error) {
	var seed [32]byte
	if _, err := cryptorand.Read(seed[:]); err != nil {
		return nil, err
	}
	return rand.New(rand.NewChaCha8(seed)), nil
}

// generate makes one puzzle of kind: a slider.Puzzle, rotate.Puzzle,
// jigsaw.Puzzle, click.Puzzle or grid.Puzzle. It takes no slot; callers bound it.
func (g *Gateway) generate(kind string, src imagegen.Source) (any, error) {
	switch {
	case kind == domain.KindSlider && g.Puzzles != nil:
		return g.Puzzles()
	case kind == domain.KindRotate && g.Rotates != nil:
		return g.Rotates()
	case kind == domain.KindJigsaw && g.Jigsaws != nil:
		return g.Jigsaws()
	case domain.ClickKind(kind) && g.Clicks != nil:
		return g.Clicks(kind)
	case domain.GridKind(kind) && g.Grids != nil:
		return g.Grids(kind)
	}
	r, err := cryptoRand()
	if err != nil {
		return nil, err
	}
	switch kind {
	case domain.KindSlider:
		return slider.Generate(r, src)
	case domain.KindRotate:
		return rotate.Generate(r, src)
	case domain.KindJigsaw:
		return jigsaw.Generate(r, src)
	case domain.KindClickText:
		return clicktext.Generate(r, src)
	case domain.KindClickOrder:
		return clicktext.GenerateOrder(r, src)
	case domain.KindClickIcon:
		return clickicon.Generate(r, src)
	case domain.KindSpatial:
		return spatial.Generate(r, src)
	case domain.KindClickShape:
		return clickshape.Generate(r, src)
	case domain.KindClock:
		return clock.Generate(r, src)
	case domain.KindGridCount:
		return gridcount.Generate(r, src)
	case domain.KindMirror:
		return mirror.Generate(r, src)
	default:
		return nil, fmt.Errorf("no generator for kind %q", kind)
	}
}

// interactivePuzzle returns a puzzle of kind, or nil and the degrade cause
// when the app is over its interactive quota or generation is saturated; the
// caller then degrades.
// The quota counts across addresses and replicas, so a flood spread over many
// IPs against one app cannot take the image capacity every app shares.
//
// Issuing costs the client nothing, so the app quota alone would let anyone
// spend it from a few networks and push the app onto the PoW-only path. A
// network prefix over its own share is therefore refused (429) before it
// touches the app quota: degrading it would hand it exactly that cheap path.
func (g *Gateway) interactivePuzzle(ctx context.Context, app *domain.App, prefix, kind string) (puzzle any, cause string, err error) {
	appID := app.ID
	if quota := g.Settings.InteractivePerPrefixPerMinute; quota > 0 {
		ok, retry, err := g.Limiter.Allow(ctx, dimPrefixInteractive, strconv.FormatInt(appID, 10)+":"+prefix, quota, rateWindow)
		if err != nil {
			logx.WithContext(ctx).Errorf("rate limiter %s: %v", dimPrefixInteractive, err)
			return nil, "", errx.ErrUnavailable
		}
		if !ok {
			return nil, "", errx.RateLimited(retry)
		}
	}
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
	v, err := g.puzzle(ctx, app, kind)
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
		// Only requests with signals came through a widget; bare API calls
		// would count as outdated widgets forever.
		if in.Signals != nil {
			if widgetCurrent(in.Widget) {
				fields = append(fields, FieldWidgetCurrent)
			} else {
				fields = append(fields, FieldWidgetOutdated)
			}
		}
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
	prefix := clientip.Prefix(in.ClientIP)
	near := count*5 >= int64(g.Settings.ChallengePerIPPerMinute)*4
	if quota := g.Settings.ChallengePerPrefixPerMinute; quota > 0 {
		n, retry, err := g.Limiter.Take(ctx, dimChallengePrefix, prefix, quota, rateWindow)
		if err != nil {
			logx.WithContext(ctx).Errorf("rate limiter %s: %v", dimChallengePrefix, err)
			return nil, "", errx.ErrUnavailable
		}
		if n > int64(quota) {
			return nil, g.cachedOrigin(in.AppKey, origin), errx.RateLimited(retry)
		}
		near = near || n*5 >= int64(quota)*4
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
	lang := prompt.Lang(in.Lang)
	policy = readable(policy, lang)
	// The page's kinds / levels count only where the action opts in: anyone
	// can write them, so left on by default they would let a bot always ask
	// for the easiest kind. Elsewhere they are ignored, not refused, so a
	// page configured for them keeps working when the switch is off.
	if !policy.ClientSelect {
		in.Select = Selection{}
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
	prefixFailures, err := g.State.Failures(ctx, failSubject(prefix))
	if err != nil {
		logx.WithContext(ctx).Errorf("read failures: %v", err)
		return nil, origin, errx.ErrUnavailable
	}
	// Past this many failures a new challenge is only another guess: answer
	// 429 until the failures age out of the window.
	if limit := g.Settings.FailureBlockThreshold; limit > 0 && failures >= limit {
		return nil, origin, errx.RateLimited(int(rateWindow / time.Second))
	}
	assessment := risk.Assess(in.Signals, risk.Facts{
		RecentFailures: failures,
		PrefixFailures: prefixFailures,
		NearRateLimit:  near,
		HeaderUA:       risk.CleanUA(in.HeaderUA),
		Fallback:       in.Fallback && policy.Mode != domain.ModeInvisible,
	})
	level := risk.Level(assessment.Score, g.Settings.RiskMedium, g.Settings.RiskHigh)
	// Under attack every client is treated one level worse: low risk sees a
	// puzzle, medium the hardest kind with harder PoW.
	if g.underAttack(ctx, app.ID) {
		level = risk.Raise(level)
		assessment = assessment.Add(risk.Assessment{Reasons: []string{risk.ReasonAppUnderAttack}})
	}
	// Invisible mode never shows a picture, whatever the page asks.
	ladder := in.Select.ladder() && policy.Mode != domain.ModeInvisible
	rung := 0
	if ladder {
		rung = g.ladderRung(ctx, app.ID, ipBucket)
	}
	// A widget that started on its own (inline mode) showed no human click,
	// so it never passes invisibly: low risk picks a kind as medium would.
	// The recorded level stays.
	pick := level
	if in.Signals.AutoStart() && level == risk.LevelLow {
		pick = risk.LevelMedium
	}
	kind, factor := g.chooseKind(policy, pick, in.Fallback, in.Select.candidates(policy, pick, rung), rand.IntN)
	var puzzle any
	degraded := false
	if domain.ValidInteractiveKind(kind) {
		var cause string
		if puzzle, cause, err = g.interactivePuzzle(ctx, app, prefix, kind); err != nil {
			return nil, origin, err
		}
		if puzzle == nil {
			g.Monitor.Degraded(cause)
			// No image capacity for this app right now. Refusing would hand
			// a flood its goal, so the client gets harder PoW instead and the
			// business server sees why in risk_reasons.
			kind, factor = domain.KindInvisible, factor*max(1, g.Settings.DegradedDifficultyFactor)
			degraded = true
			assessment = assessment.Add(risk.Assessment{Reasons: []string{risk.ReasonCapacityDegraded}})
		}
	}
	st.kind, st.level, st.reasons = kind, level, assessment.Reasons

	difficulty := min(policy.Difficulty*factor, g.Settings.MaxDifficulty)
	if degraded {
		// A multiple of a small action difficulty is still nearly free for
		// native code; the degraded path gets an absolute floor as well.
		difficulty = min(max(difficulty, g.Settings.DegradedMinDifficulty), g.Settings.MaxDifficulty)
	}
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
	state := sealedState{Score: assessment.Score, Reasons: assessment.Reasons, Ladder: ladder,
		Pointer: risk.PointerMouse, Prefix: prefix, IssuedMs: now.UnixMilli()}
	if in.Signals != nil {
		state.Pointer = in.Signals.PointerType
	}
	var (
		render       *SliderRender
		rotateRender *RotateRender
		jigsawRender *JigsawRender
		clickRender  *ClickRender
		gridRender   *GridRender
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
			Prompt: promptIn(pz.Prompts, pz.Prompt, lang), PromptImage: pz.PromptImage, Targets: len(pz.Targets)}
	case grid.Puzzle:
		state.Grid = &gridState{Width: pz.Width, Height: pz.Height, Rows: pz.Rows, Cols: pz.Cols, Cells: pz.Answer}
		gridRender = &GridRender{Width: pz.Width, Height: pz.Height, Image: pz.Image, Prompt: promptIn(pz.Prompts, pz.Prompt, lang),
			PromptImage: pz.PromptImage, Rows: pz.Rows, Cols: pz.Cols}
	}
	private, err := json.Marshal(state)
	if err != nil {
		return nil, origin, errx.ErrUnavailable
	}
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
		ExpiresAt: p.ExpiresAt, Slider: render, Click: clickRender, Rotate: rotateRender, Jigsaw: jigsawRender,
		Grid: gridRender}, origin, nil
}

// SliderAnswer carries the interactive answer. For the slider: the final
// piece position and the drag that produced it ([ms since pointer down, x, y]).
// For click kinds: each click as [ms since the panel appeared, x, y] and the
// pointer moves seen before each click. Grid kinds send their cell toggles
// the same way; the selection is replayed from them.
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

func trajectoryPoints(t [][3]float64) []slider.Point {
	out := make([]slider.Point, len(t))
	for i, p := range t {
		out[i] = slider.Point{T: p[0], X: p[1], Y: p[2]}
	}
	return out
}

// countSolve feeds the app's attack detector (Settings.AttackFailures).
func (g *Gateway) countSolve(ctx context.Context, appID int64, failed bool) {
	if g.Settings.AttackFailures <= 0 {
		return
	}
	started, err := g.State.CountSolve(ctx, appID, failed, g.Now(), g.Settings.AttackFailures, g.Settings.AttackRatioPercent, g.Settings.AttackHold)
	if err != nil {
		logx.WithContext(ctx).Errorf("attack detector: %v", err)
		return
	}
	if started {
		logx.WithContext(ctx).Infof("app %d under attack: failures crossed %d in two minutes", appID, g.Settings.AttackFailures)
	}
}

// underAttack reports whether the app's attack flag is up; a Redis error
// counts as no.
func (g *Gateway) underAttack(ctx context.Context, appID int64) bool {
	if g.Settings.AttackFailures <= 0 {
		return false
	}
	on, err := g.State.UnderAttack(ctx, appID)
	if err != nil {
		logx.WithContext(ctx).Errorf("attack flag: %v", err)
	}
	return on
}

// fail records a failed attempt for the IP and its network prefix, raising
// their risk next time.
func (g *Gateway) fail(ctx context.Context, ip netip.Addr, e *errx.Error) error {
	for _, subject := range []string{clientip.Bucket(ip), failSubject(clientip.Prefix(ip))} {
		if err := g.State.RecordFailure(ctx, subject, g.Settings.FailureWindow); err != nil {
			logx.WithContext(ctx).Errorf("record failure: %v", err)
		}
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
	isGrid := domain.GridKind(p.Kind)
	if isGrid && (in.Answer == nil || len(in.Answer.Clicks) > grid.MaxClicks || len(in.Answer.MovesBetween) > grid.MaxClicks) {
		return nil, origin, errx.InvalidRequest("选格题需要 answer.clicks，且不超过 24 次点击")
	}
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
		g.countSolve(ctx, app.ID, true)
		return g.fail(ctx, in.ClientIP, e)
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
	// Touch and pen relax the mouse-only checks, so the claim counts only when
	// the widget was activated with touch or pen as well.
	var observed []string
	if (input == risk.PointerTouch || input == risk.PointerPen) && state.Pointer != "" &&
		state.Pointer != risk.PointerTouch && state.Pointer != risk.PointerPen {
		input = risk.PointerMouse
		observed = append(observed, risk.ReasonPointerMismatch)
	}
	if domain.DragKind(p.Kind) || isClick || isJigsaw || isGrid {
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

	if isGrid {
		// Like click kinds, no keyboard path.
		if input == risk.PointerKeyboard {
			input = risk.PointerMouse
		}
		gs := state.Grid
		if gs == nil {
			return nil, origin, errx.ErrChallengeInvalid
		}
		pz := grid.Puzzle{Width: gs.Width, Height: gs.Height, Rows: gs.Rows, Cols: gs.Cols, Answer: gs.Cells}
		if reason := grid.Check(pz, in.Answer.Clicks, in.Answer.MovesBetween, input); reason != "" {
			logx.WithContext(ctx).Infof("%s rejected: %s (app %d, %s)", p.Kind, reason, app.ID, input)
			return nil, origin, fail(errx.ErrAnswerInvalid)
		}
	}

	// Behaviour during the interaction can only add risk, never remove it.
	final := risk.Assessment{Score: state.Score, Reasons: state.Reasons}
	if in.Signals != nil && in.Signals.UntrustedEvent {
		final = final.Add(risk.Assessment{Score: risk.Weights[risk.ReasonUntrustedEvent], Reasons: []string{risk.ReasonUntrustedEvent}})
	}
	interactive := domain.DragKind(p.Kind) || isClick || isJigsaw || isGrid
	if domain.DragKind(p.Kind) && input == risk.PointerKeyboard {
		final = final.Add(risk.Assessment{Score: risk.Weights[risk.ReasonKeyboardInput], Reasons: []string{risk.ReasonKeyboardInput}})
	}
	if interactive && input == risk.PointerTouch {
		observed = append(observed, risk.ReasonTouchInput)
	}
	// Keyboard drags move in fixed steps; only pointer paths are judged.
	if input != risk.PointerKeyboard {
		if domain.DragKind(p.Kind) && slider.Smooth(points) {
			observed = append(observed, risk.ReasonTrajectorySmooth)
		}
		if isJigsaw && slider.Smooth(trajectoryPoints(in.Answer.Trajectory)) {
			observed = append(observed, risk.ReasonTrajectorySmooth)
		}
	}
	if state.Prefix != "" && clientip.Prefix(in.ClientIP) != state.Prefix {
		observed = append(observed, risk.ReasonIPChanged)
	}
	// A negative gap is clock skew between replicas, not a fast answer.
	if elapsed := now.UnixMilli() - state.IssuedMs; interactive && state.IssuedMs > 0 && elapsed >= 0 && elapsed < minHumanSolve.Milliseconds() {
		observed = append(observed, risk.ReasonSolveTooFast)
	}
	for _, r := range observed {
		final = final.Add(risk.Assessment{Score: risk.Weights[r], Reasons: []string{r}})
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
	g.countSolve(ctx, app.ID, false)
	return &SolveOutput{Token: token, ExpiresAt: exp, ExpiresIn: int64(g.Settings.TokenTTL / time.Second)}, origin, nil
}
