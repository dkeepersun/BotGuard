package logic

import (
	"bytes"
	"context"
	"errors"
	"image/jpeg"
	"math/rand/v2"
	"testing"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/service/imagegen"
)

type fakePhotos struct {
	jpegs [][]byte
	err   error
	calls int
}

func (f *fakePhotos) AppPhotos(context.Context, int64) ([][]byte, error) {
	f.calls++
	return f.jpegs, f.err
}

func testJPEGs(t *testing.T, n int) [][]byte {
	t.Helper()
	var out [][]byte
	for i := range n {
		var b bytes.Buffer
		img := imagegen.Background(rand.New(rand.NewPCG(uint64(i), 3)), imagegen.PhotoWidth, imagegen.PhotoHeight)
		if err := jpeg.Encode(&b, img, nil); err != nil {
			t.Fatal(err)
		}
		out = append(out, b.Bytes())
	}
	return out
}

// photoShareOf draws 100 backgrounds and counts the photos.
func photoShareOf(s imagegen.Source) int {
	n := 0
	for i := range 100 {
		if _, photo := s.Background(rand.New(rand.NewPCG(uint64(i), 9)), 64, 40); photo {
			n++
		}
	}
	return n
}

func TestBackgroundSources(t *testing.T) {
	ctx := context.Background()
	builtin := imagegen.NewLibrary(testJPEGs(t, 3))
	store := &fakePhotos{jpegs: testJPEGs(t, 2)}
	b := &Backgrounds{Builtin: builtin, Store: store}
	app := func(bg domain.Background) *domain.App { return &domain.App{ID: 7, Background: bg} }

	if s := (*Backgrounds)(nil).Source(ctx, app(domain.Background{Mode: domain.BackgroundPhoto})); s.Photos.Len() != 0 {
		t.Fatal("nil backgrounds drew photos")
	}
	def := b.Source(ctx, app(domain.Background{}))
	if def.Photos != builtin || def.Share != 0.5 {
		t.Fatalf("default: %+v", def)
	}
	if n := photoShareOf(b.Source(ctx, app(domain.Background{Mode: domain.BackgroundProcedural, Uploads: 2, Version: 1}))); n != 0 {
		t.Fatalf("procedural mode drew %d photos", n)
	}
	if store.calls != 0 {
		t.Fatal("procedural mode loaded uploads")
	}

	// Uploads only: loaded once per version.
	own := domain.Background{Mode: domain.BackgroundPhoto, NoBuiltin: true, Uploads: 2, Version: 1}
	s := b.Source(ctx, app(own))
	if s.Photos.Len() != 2 || photoShareOf(s) != 100 {
		t.Fatalf("uploads only: %d photos", s.Photos.Len())
	}
	b.Source(ctx, app(own))
	if store.calls != 1 {
		t.Fatalf("reloaded unchanged uploads: %d calls", store.calls)
	}
	own.Version = 2
	if s := b.Source(ctx, app(own)); store.calls != 2 || s.Photos.Len() != 2 {
		t.Fatalf("version change: %d calls", store.calls)
	}
	// Both libraries.
	if s := b.Source(ctx, app(domain.Background{Mode: domain.BackgroundMixed, Uploads: 2, Version: 2})); s.Photos.Len() != 5 {
		t.Fatalf("built-in and uploads: %d photos", s.Photos.Len())
	}

	// A failed load leaves the uploads out; with no built-in photos either,
	// every picture is procedural.
	broken := &Backgrounds{Builtin: builtin, Store: &fakePhotos{err: errors.New("db down")}}
	if n := photoShareOf(broken.Source(ctx, app(domain.Background{Mode: domain.BackgroundPhoto, NoBuiltin: true, Uploads: 1, Version: 1}))); n != 0 {
		t.Fatalf("failed load drew %d photos", n)
	}
}
