package mirror

import (
	"bytes"
	"encoding/base64"
	"github.com/dkeepersun/BotGuard/internal/service/imagegen"
	"image/jpeg"
	"image/png"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
)

func seeded(n uint64) *rand.Rand {
	var seed [32]byte
	seed[0], seed[1] = byte(n), byte(n>>8)
	return rand.New(rand.NewChaCha8(seed))
}

func TestChirality(t *testing.T) {
	for name, c := range map[string]struct {
		s      shape
		chiral bool
	}{
		"P": {shape{{0, 0}, {1, 0}, {0, 1}, {1, 1}, {0, 2}}, true},
		"L": {shape{{0, 0}, {0, 1}, {0, 2}, {0, 3}, {1, 3}}, true},
		"F": {shape{{1, 0}, {2, 0}, {0, 1}, {1, 1}, {1, 2}}, true},
		"T": {shape{{0, 0}, {1, 0}, {2, 0}, {1, 1}, {1, 2}}, false},
		"U": {shape{{0, 0}, {2, 0}, {0, 1}, {1, 1}, {2, 1}}, false},
		"W": {shape{{0, 0}, {0, 1}, {1, 1}, {1, 2}, {2, 2}}, false},
	} {
		if got := normalize(c.s).chiral(); got != c.chiral {
			t.Errorf("%s: chiral %v, want %v", name, got, c.chiral)
		}
	}
	for i, b := range bases {
		if !normalize(b).chiral() {
			t.Errorf("base %d is not chiral", i)
		}
	}
}

func TestGenerate(t *testing.T) {
	a, err := Generate(seeded(1), imagegen.Source{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Generate(seeded(1), imagegen.Source{})
	if a.Image != b.Image || a.PromptImage != b.PromptImage {
		t.Fatal("same seed must give the same puzzle")
	}
	for i := uint64(0); i < 300; i++ {
		p, s, copies, err := generate(seeded(i), imagegen.Source{})
		if err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
		if !s.chiral() || len(s) < 5 || len(s) > 6 {
			t.Fatalf("seed %d: shape %v", i, s)
		}
		if k := len(p.Answer); k < 2 || k > 3 || len(copies) != Rows*Cols {
			t.Fatalf("seed %d: %d flipped of %d", i, k, len(copies))
		}
		var flipped []int
		for j, c := range copies {
			if math.Abs(c.turn) > maxTurn {
				t.Fatalf("seed %d: copy %d turned %.0f°", i, j, c.turn)
			}
			if c.flipped {
				flipped = append(flipped, j)
			}
		}
		if len(flipped) != len(p.Answer) {
			t.Fatalf("seed %d: answer %v, flipped %v", i, p.Answer, flipped)
		}
		for j := range flipped {
			if flipped[j] != p.Answer[j] {
				t.Fatalf("seed %d: answer %v, flipped %v", i, p.Answer, flipped)
			}
		}
	}
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(a.Image, "data:image/jpeg;base64,"))
	if img, err := jpeg.Decode(bytes.NewReader(raw)); err != nil || img.Bounds().Dx() != Width {
		t.Fatalf("image: %v", err)
	}
	raw, _ = base64.StdEncoding.DecodeString(strings.TrimPrefix(a.PromptImage, "data:image/png;base64,"))
	if img, err := png.Decode(bytes.NewReader(raw)); err != nil || img.Bounds().Dx() != PromptSize {
		t.Fatalf("reference: %v", err)
	}
}

func BenchmarkGenerate(b *testing.B) {
	r := seeded(1)
	for b.Loop() {
		if _, err := Generate(r, imagegen.Source{}); err != nil {
			b.Fatal(err)
		}
	}
}
