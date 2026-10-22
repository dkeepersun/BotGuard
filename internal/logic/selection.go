package logic

import (
	"context"
	"slices"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/errx"
	"github.com/dkeepersun/BotGuard/internal/service/risk"
	"github.com/zeromicro/go-zero/core/logx"
)

// Selection is the page's say in the challenge kind: one kind, a set to pick
// from at random (KindAll for every kind the action offers), or a ladder of
// tiers that the client's behaviour walks up and down. It is a request, not
// an order: kinds the action does not offer are dropped, and high risk still
// gets the action's hardest kind, so a bot cannot ask its way to the easiest.
type Selection struct {
	Kinds  []string
	Levels [][]string
}

const (
	// KindAll in a selection stands for every kind the action offers.
	KindAll = "*"
	// MaxSelectKinds bounds the kinds in a selection or in one ladder tier.
	MaxSelectKinds = 16
	// MaxLevels bounds the ladder; the stored rung never exceeds it.
	MaxLevels = 8
)

// Validate checks the shape only; unknown kind names are dropped later, so a
// newer widget still works against this gateway.
func (s Selection) Validate() error {
	if len(s.Kinds) > 0 && len(s.Levels) > 0 {
		return errx.InvalidRequest("kinds 和 levels 只能二选一")
	}
	if len(s.Kinds) > MaxSelectKinds || len(s.Levels) > MaxLevels {
		return errx.InvalidRequest("kinds 最多 16 个，levels 最多 8 档")
	}
	for _, tier := range s.Levels {
		if len(tier) > MaxSelectKinds {
			return errx.InvalidRequest("levels 每档最多 16 个题型")
		}
	}
	return nil
}

func (s Selection) ladder() bool { return len(s.Levels) > 0 }

// actionKinds is the action's kind list, never empty.
func actionKinds(policy domain.ActionPolicy) []string {
	if len(policy.Kinds) == 0 {
		return []string{domain.KindSlider}
	}
	return policy.Kinds
}

// allowedKinds keeps the requested kinds the action offers, in order and
// without repeats. Invisible counts only where the gateway would give it
// anyway: adaptive mode at low risk.
func allowedKinds(policy domain.ActionPolicy, level string, requested []string) []string {
	offered := actionKinds(policy)
	var out []string
	add := func(k string) {
		if !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	for _, k := range requested {
		switch {
		case k == KindAll:
			for _, v := range offered {
				add(v)
			}
		case k == domain.KindInvisible:
			if policy.Mode == domain.ModeAdaptive && level == risk.LevelLow {
				add(k)
			}
		case slices.Contains(offered, k):
			add(k)
		}
	}
	return out
}

// candidates resolves the selection to the kinds to pick from, or nil to
// leave the choice to the action policy. rung is the client's ladder level,
// counted in the page's tiers; medium risk starts at the second tier at
// least. A tier the filter leaves empty hands over to the next harder one
// (the next easier one only at the top), so a ladder never gets easier for
// it.
func (s Selection) candidates(policy domain.ActionPolicy, level string, rung int) []string {
	if !s.ladder() {
		return allowedKinds(policy, level, s.Kinds)
	}
	if level == risk.LevelMedium {
		rung = max(rung, 1)
	}
	rung = min(max(rung, 0), len(s.Levels)-1)
	for i := rung; i < len(s.Levels); i++ {
		if kinds := allowedKinds(policy, level, s.Levels[i]); len(kinds) > 0 {
			return kinds
		}
	}
	for i := rung - 1; i >= 0; i-- {
		if kinds := allowedKinds(policy, level, s.Levels[i]); len(kinds) > 0 {
			return kinds
		}
	}
	return nil
}

// ladderRung reads the client's ladder level. A Redis error starts it at the
// bottom rather than failing the request: risk still applies on top.
func (g *Gateway) ladderRung(ctx context.Context, appID int64, ipBucket string) int {
	rung, err := g.State.LadderLevel(ctx, appID, ipBucket)
	if err != nil {
		logx.WithContext(ctx).Errorf("read ladder: %v", err)
		return 0
	}
	return rung
}

// climb moves the client's ladder after a solve: up one rung on a failure,
// down one after Settings.LadderDowngradePasses passes in a row.
func (g *Gateway) climb(ctx context.Context, appID int64, ipBucket string, passed bool) {
	if err := g.State.RecordLadder(ctx, appID, ipBucket, passed, g.Settings.LadderDowngradePasses, MaxLevels-1, g.Settings.LadderWindow); err != nil {
		logx.WithContext(ctx).Errorf("record ladder: %v", err)
	}
}
