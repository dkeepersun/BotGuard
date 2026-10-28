package clickshape

import (
	"bytes"
	"encoding/base64"
	"github.com/dkeepersun/BotGuard/internal/service/imagegen"
	"image/jpeg"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/dkeepersun/BotGuard/internal/service/click"
)

func seeded(n uint64) *rand.Rand {
	var seed [32]byte
	seed[0], seed[1] = byte(n), byte(n>>8)
	return rand.New(rand.NewChaCha8(seed))
}

func TestGenerate(t *testing.T) {
	a, err := Generate(seeded(1), imagegen.Source{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Generate(seeded(1), imagegen.Source{})
	if a.Prompt != b.Prompt || a.Image != b.Image {
		t.Fatal("same seed must give the same puzzle")
	}
	for i := uint64(0); i < 300; i++ {
		p, err := Generate(seeded(i), imagegen.Source{})
		if err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
		if n := len(p.Targets); n < 3 || n > 4 || strings.Count(p.Prompt, "、") != n-1 {
			t.Fatalf("seed %d: %d targets, prompt %q", i, n, p.Prompt)
		}
		for j, tg := range p.Targets {
			if tg.X-tg.R < 0 || tg.Y-tg.R < 0 || tg.X+tg.R > Width || tg.Y+tg.R > Height {
				t.Fatalf("seed %d: target outside image", i)
			}
			if click.Overlaps(append(append([]click.Target{}, p.Targets[:j]...), p.Targets[j+1:]...), tg.X, tg.Y, tg.R, 0) {
				t.Fatalf("seed %d: overlapping targets", i)
			}
		}
	}
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(a.Image, "data:image/jpeg;base64,"))
	if img, err := jpeg.Decode(bytes.NewReader(raw)); err != nil || img.Bounds().Dx() != Width {
		t.Fatalf("image: %v", err)
	}
}

// Every description must point at exactly one object, or a human cannot
// answer either.
func TestDescriptionsAreUnique(t *testing.T) {
	for i := uint64(0); i < 300; i++ {
		r := seeded(i)
		objs := plan(r)
		if !place(r, objs) {
			continue
		}
		for j := range objs {
			for _, d := range unique(objs, j) {
				matches := 0
				for _, o := range objs {
					for _, e := range o.desc() {
						if e == d {
							matches++
						}
					}
				}
				if matches != 1 {
					t.Fatalf("seed %d: %q matches %d objects", i, d, matches)
				}
			}
		}
	}
}

func TestNearMissDecoys(t *testing.T) {
	objs := plan(seeded(3))
	if objs[0].kind != dice || objs[1].kind != dice || objs[1].dots != objs[0].dots+1 {
		t.Fatalf("dice pair: %+v %+v", objs[0], objs[1])
	}
}

func BenchmarkGenerate(b *testing.B) {
	r := seeded(9)
	for b.Loop() {
		if _, err := Generate(r, imagegen.Source{}); err != nil {
			b.Fatal(err)
		}
	}
}
