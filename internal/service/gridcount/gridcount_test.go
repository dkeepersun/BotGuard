package gridcount

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"github.com/dkeepersun/BotGuard/internal/service/imagegen"
	"image/jpeg"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/dkeepersun/BotGuard/internal/service/click"
	"github.com/dkeepersun/BotGuard/internal/service/grid"
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
	nearMiss := 0
	for i := uint64(0); i < 300; i++ {
		p, cells, err := generate(seeded(i), imagegen.Source{})
		if err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
		if p.Rows != Rows || p.Cols != Cols || len(cells) != Rows*Cols {
			t.Fatalf("seed %d: grid %d×%d", i, p.Rows, p.Cols)
		}
		if k := len(p.Answer); k < 2 || k > 4 {
			t.Fatalf("seed %d: %d answer cells", i, k)
		}
		var n, target int
		var name string
		for s := range shapes {
			if strings.HasSuffix(p.Prompt, "个"+shapes[s].name+"的格子") {
				target, name = s, shapes[s].name
			}
		}
		if name == "" {
			t.Fatalf("seed %d: prompt %q names no shape", i, p.Prompt)
		}
		if _, err := fmtSscan(p.Prompt, &n); err != nil {
			t.Fatalf("seed %d: prompt %q: %v", i, p.Prompt, err)
		}
		// The answer is exactly the cells holding n of the target shape.
		var want []int
		for c, items := range cells {
			got := 0
			for _, it := range items {
				if it.shape == target {
					got++
				}
			}
			if got == n {
				want = append(want, c)
			} else if got == n-1 || got == n+1 {
				nearMiss++
			}
			x0, y0, x1, y1 := grid.Cell(Width, Height, Rows, Cols, c)
			var placed []click.Target
			for _, it := range items {
				if it.x-it.r < x0 || it.y-it.r < y0 || it.x+it.r > x1 || it.y+it.r > y1 {
					t.Fatalf("seed %d: shape leaves cell %d", i, c)
				}
				if click.Overlaps(placed, it.x, it.y, it.r, 0) {
					t.Fatalf("seed %d: overlapping shapes in cell %d", i, c)
				}
				placed = append(placed, click.Target{X: it.x, Y: it.y, R: it.r})
			}
		}
		if !grid.Equal(want, p.Answer) {
			t.Fatalf("seed %d: answer %v, counted %v", i, p.Answer, want)
		}
	}
	// Most wrong cells are one off, so estimating is not enough.
	if nearMiss < 300*3 {
		t.Fatalf("only %d near-miss cells in 300 puzzles", nearMiss)
	}
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(a.Image, "data:image/jpeg;base64,"))
	if img, err := jpeg.Decode(bytes.NewReader(raw)); err != nil || img.Bounds().Dx() != Width {
		t.Fatalf("image: %v", err)
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

// fmtSscan reads the count from "请选出所有恰好有 4 个…".
func fmtSscan(prompt string, n *int) (int, error) {
	_, rest, _ := strings.Cut(prompt, "恰好有 ")
	return fmt.Sscan(rest, n)
}
