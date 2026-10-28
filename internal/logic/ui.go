package logic

import (
	"context"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/errx"
)

// UI returns the app's widget appearance for GET /v1/ui. It is not secret,
// but like the challenge endpoints it answers only the app's own origins,
// so another site cannot read it through the visitor's browser either.
func (g *Gateway) UI(ctx context.Context, appKey, rawOrigin string) (domain.AppUI, string, error) {
	origin, ok := domain.NormalizeOrigin(rawOrigin)
	if !ok {
		return domain.AppUI{}, "", errx.ErrOriginNotAllowed
	}
	app, err := g.app(ctx, appKey)
	if err != nil {
		return domain.AppUI{}, "", err
	}
	if !app.AllowsOrigin(origin) {
		return domain.AppUI{}, "", errx.ErrOriginNotAllowed
	}
	return app.UI, origin, nil
}
