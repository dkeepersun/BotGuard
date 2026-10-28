package slider

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
	seed[0] = byte(n)
	return rand.New(rand.NewChaCha8(seed))
}

func decode(t *testing.T, url, prefix string) []byte {
	t.Helper()
	if !strings.HasPrefix(url, prefix) {
		t.Fatalf("data url prefix: %.30s", url)
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, prefix))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestGenerate(t *testing.T) {
	a, err := Generate(seeded(1), imagegen.Source{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Generate(seeded(1), imagegen.Source{})
	if a != b {
		t.Fatal("same seed must give the same puzzle")
	}
	for i := uint64(0); i < 50; i++ {
		p, err := Generate(seeded(i), imagegen.Source{})
		if err != nil {
			t.Fatal(err)
		}
		if p.X < PieceSize+20 || p.X > Width-PieceSize || p.Y < 4 || p.Y > Height-PieceSize-4 {
			t.Fatalf("seed %d: answer out of range %d,%d", i, p.X, p.Y)
		}
		bg, err := jpeg.Decode(bytes.NewReader(decode(t, p.Background, "data:image/jpeg;base64,")))
		if err != nil || bg.Bounds().Dx() != Width || bg.Bounds().Dy() != Height {
			t.Fatalf("background: %v", err)
		}
		pc, err := png.Decode(bytes.NewReader(decode(t, p.Piece, "data:image/png;base64,")))
		if err != nil || pc.Bounds().Dx() != PieceSize {
			t.Fatalf("piece: %v", err)
		}
	}
}

func TestShapesAreJigsaws(t *testing.T) {
	for i := uint64(0); i < 20; i++ {
		m := newMask(randomShape(seeded(i)))
		n := 0
		for y := range m.inside {
			for x := range m.inside[y] {
				if m.inside[y][x] {
					n++
				}
			}
		}
		// Between a bare square minus knobs and a square plus four knobs.
		if n < square*square-4*160 || n > square*square+4*160 {
			t.Fatalf("seed %d: area %d", i, n)
		}
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

// humanDrag approximates a recorded mouse drag: ease-in-out progress, frame
// timing with jitter, small vertical wobble and a final overshoot correction.
func humanDrag(target float64) []Point {
	r := seeded(42)
	var pts []Point
	t := 0.0
	for i := 0; i <= 40; i++ {
		p := float64(i) / 40
		ease := p * p * (3 - 2*p)
		x := ease * (target + 4)
		pts = append(pts, Point{T: t, X: x, Y: math.Sin(p*3)*2 + r.Float64()})
		t += 14 + r.Float64()*6
	}
	pts = append(pts, Point{T: t + 80, X: target + 1, Y: 1.5}, Point{T: t + 140, X: target, Y: 1.2})
	return pts
}

func linear(target float64, n int, dt float64) []Point {
	var pts []Point
	for i := 0; i <= n; i++ {
		pts = append(pts, Point{T: float64(i) * dt, X: target * float64(i) / float64(n), Y: 0})
	}
	return pts
}

func TestCheck(t *testing.T) {
	const ans = 180.0
	cases := map[string]struct {
		x     float64
		pts   []Point
		input string
		want  string
	}{
		"human":            {ans + 2, humanDrag(ans + 2), "mouse", ""},
		"wrong position":   {ans + 9, humanDrag(ans + 9), "mouse", ReasonPosition},
		"linear script":    {ans, linear(ans, 30, 16), "mouse", ReasonEqualIntervals},
		"too fast":         {ans, linear(ans, 10, 5), "mouse", ReasonDuration},
		"teleport":         {ans, append(humanDrag(40)[:20], Point{T: 900, X: ans, Y: 1}), "mouse", ReasonTeleport},
		"few points":       {ans, []Point{{0, 0, 0}, {500, ans, 1}}, "mouse", ReasonTooFewPoints},
		"end mismatch":     {ans, humanDrag(ans - 30), "mouse", ReasonEndMismatch},
		"keyboard steps":   {ans, linear(ans, 45, 33), "keyboard", ""},
		"keyboard instant": {ans, []Point{{0, 0, 0}, {10, ans, 0}}, "keyboard", ReasonDuration},
	}
	for name, c := range cases {
		if got := Check(ans, c.x, 4, c.pts, c.input); got != c.want {
			t.Errorf("%s: got %q want %q", name, got, c.want)
		}
	}

	// Jittered timing but constant speed: still not human.
	r := seeded(7)
	var pts []Point
	tm := 0.0
	for i := 0; i <= 30; i++ {
		pts = append(pts, Point{T: tm, X: float64(i) * 6, Y: r.Float64() * 2})
		tm += 16
		if i%2 == 0 {
			tm += 0.5
		}
	}
	for i := range pts {
		pts[i].X = pts[i].T * 180 / pts[len(pts)-1].T
	}
	if got := Check(ans, ans, 4, pts, "mouse"); got != ReasonConstantVelocity {
		t.Errorf("constant velocity: %q", got)
	}

	straight := humanDrag(ans)
	for i := range straight {
		straight[i].Y = 0
	}
	if got := Check(ans, ans, 4, straight, "mouse"); got != ReasonStraightLine {
		t.Errorf("straight line: %q", got)
	}
	if got := Check(ans, ans, 4, straight, "touch"); got != "" {
		t.Errorf("straight touch is allowed: %q", got)
	}
}

// One decoy always fits on the row, inside the answer range and clear of the
// real slot, wherever that is.
func TestDecoyFits(t *testing.T) {
	r := seeded(3)
	for x := minX; x < maxX; x++ {
		d := decoyX(r, []int{x})
		if d < minX || d >= maxX || overlaps(d, x) {
			t.Fatalf("slot at %d: decoy at %d", x, d)
		}
	}
}
