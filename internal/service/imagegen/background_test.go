package imagegen

import (
	"bytes"
	"image"
	"math/rand/v2"
	"testing"
)

func seeded(n uint64) *rand.Rand { return rand.New(rand.NewPCG(n, 0x9e3779b97f4a7c15)) }

// stats summarises an image for the tests below.
func stats(img *image.RGBA) (mean, lo, hi float64) {
	lo, hi = 255, 0
	var sum float64
	b := img.Bounds()
	// 8×8 blocks: block means show the large-scale structure.
	for y := 0; y+8 <= b.Dy(); y += 8 {
		for x := 0; x+8 <= b.Dx(); x += 8 {
			m := MeanLuma(img, image.Rect(x, y, x+8, y+8))
			sum += m
			lo, hi = min(lo, m), max(hi, m)
		}
	}
	n := float64((b.Dx() / 8) * (b.Dy() / 8))
	return sum / n, lo, hi
}

func TestBackgroundDeterministic(t *testing.T) {
	a, b := Background(seeded(1), 320, 200), Background(seeded(1), 320, 200)
	if !bytes.Equal(a.Pix, b.Pix) {
		t.Fatal("same seed must give the same background")
	}
	if c := Background(seeded(2), 320, 200); bytes.Equal(a.Pix, c.Pix) {
		t.Fatal("different seeds gave the same background")
	}
}

// Backgrounds must vary in structure, not only in colour: large-scale
// brightness differs inside each picture, and the brightest region moves
// around between pictures (the old gradient always ran top-left to
// bottom-right).
func TestBackgroundVaries(t *testing.T) {
	quadrant := map[int]int{}
	for i := uint64(0); i < 200; i++ {
		img := Background(seeded(i), 320, 200)
		mean, lo, hi := stats(img)
		if hi-lo < 20 {
			t.Fatalf("seed %d: nearly flat background (block luma %.0f–%.0f)", i, lo, hi)
		}
		if mean < 40 || mean > 230 {
			t.Fatalf("seed %d: mean luma %.0f out of range", i, mean)
		}
		best, q := -1.0, 0
		for k, rc := range []image.Rectangle{image.Rect(0, 0, 160, 100), image.Rect(160, 0, 320, 100), image.Rect(0, 100, 160, 200), image.Rect(160, 100, 320, 200)} {
			if m := MeanLuma(img, rc); m > best {
				best, q = m, k
			}
		}
		quadrant[q]++
	}
	for q := 0; q < 4; q++ {
		if quadrant[q] < 20 {
			t.Fatalf("brightest quadrant distribution %v: structure is not varying", quadrant)
		}
	}
}

// Finish must move colours off any fixed value but only slightly.
func TestFinishShiftsColoursSlightly(t *testing.T) {
	changed, worst := 0, 0
	for i := uint64(0); i < 50; i++ {
		img := image.NewRGBA(image.Rect(0, 0, 64, 64))
		for p := 0; p < len(img.Pix); p += 4 {
			img.Pix[p], img.Pix[p+1], img.Pix[p+2], img.Pix[p+3] = 220, 50, 55, 255
		}
		Finish(seeded(i), img)
		for p := 0; p < len(img.Pix); p += 4 {
			d := max(abs(int(img.Pix[p])-220), abs(int(img.Pix[p+1])-50), abs(int(img.Pix[p+2])-55))
			if d > 0 {
				changed++
			}
			worst = max(worst, d)
		}
	}
	if total := 50 * 64 * 64; changed < total*3/4 {
		t.Fatalf("only %d of %d pixels moved", changed, total)
	}
	// Hairlines blend up to half towards another colour; the cast and grain
	// alone stay within about 25 levels.
	if worst > 140 {
		t.Fatalf("a pixel moved by %d levels", worst)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func BenchmarkBackground(b *testing.B) {
	r := seeded(1)
	for b.Loop() {
		Background(r, 320, 200)
	}
}
