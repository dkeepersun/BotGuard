package logic

import (
	"context"
	"sync"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/service/imagegen"
	"github.com/zeromicro/go-zero/core/logx"
)

// PhotoStore loads an app's uploaded background photos.
type PhotoStore interface {
	AppPhotos(ctx context.Context, appID int64) ([][]byte, error)
}

// Backgrounds picks each app's picture source from its background setting:
// the built-in photo library, the app's uploads, both, or procedural only.
type Backgrounds struct {
	Builtin *imagegen.Library
	// Store loads uploads; nil ignores them.
	Store PhotoStore

	mu      sync.Mutex
	uploads map[int64]appPhotos
}

type appPhotos struct {
	version int64
	lib     *imagegen.Library
}

// photoShare is the chance of a photo in each mode.
func photoShare(mode string) float64 {
	switch mode {
	case domain.BackgroundPhoto:
		return 1
	case domain.BackgroundProcedural:
		return 0
	default:
		return 0.5
	}
}

// Default is the source for the default setting, which the shared pool is
// filled from.
func (b *Backgrounds) Default() imagegen.Source {
	if b == nil {
		return imagegen.Source{}
	}
	return imagegen.Source{Photos: b.Builtin, Share: photoShare(domain.BackgroundMixed)}
}

// Source returns the app's source. Uploads that fail to load are logged and
// left out rather than failing the challenge; with no photos at all every
// picture is procedural.
func (b *Backgrounds) Source(ctx context.Context, app *domain.App) imagegen.Source {
	if b == nil {
		return imagegen.Source{}
	}
	bg := app.Background
	if bg.Default() {
		return b.Default()
	}
	share := photoShare(bg.Mode)
	if share == 0 {
		return imagegen.Source{}
	}
	var libs []*imagegen.Library
	if !bg.NoBuiltin {
		libs = append(libs, b.Builtin)
	}
	if bg.Uploads > 0 {
		libs = append(libs, b.appUploads(ctx, app))
	}
	return imagegen.Source{Photos: imagegen.Join(libs...), Share: share}
}

// appUploads returns the app's photos, reloading them when the app's
// background version moves.
func (b *Backgrounds) appUploads(ctx context.Context, app *domain.App) *imagegen.Library {
	if b.Store == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if e, ok := b.uploads[app.ID]; ok && e.version == app.Background.Version {
		return e.lib
	}
	jpegs, err := b.Store.AppPhotos(ctx, app.ID)
	if err != nil {
		logx.WithContext(ctx).Errorf("app %d backgrounds: %v", app.ID, err)
		return nil
	}
	if b.uploads == nil {
		b.uploads = map[int64]appPhotos{}
	}
	lib := imagegen.NewLibrary(jpegs)
	b.uploads[app.ID] = appPhotos{version: app.Background.Version, lib: lib}
	return lib
}
