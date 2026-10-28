// Package mirror generates mirror-image grids: eight turned copies of one
// asymmetric shape, two or three of them flipped; the user selects the
// flipped ones. Telling a mirror image from a rotation needs mental
// rotation, which people do reliably and multimodal models do not (Mind's
// Eye: people about 80%, models under 50%; MirrorQA: the best model 65% on
// left/right reasoning), and the 2×4 multi-select answer leaves a blind
// guess about one chance in 255 (docs/challenge-catalog.md §7.3).
//
// The shape is a bar with a foot or a bump (an L or Y of five or six
// squares), which differs from its own mirror image under every rotation, so
// each copy is either a turn of the reference or a flip, never both.
package mirror

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"math/rand/v2"
	"slices"

	"github.com/dkeepersun/BotGuard/internal/service/grid"
	"github.com/dkeepersun/BotGuard/internal/service/imagegen"
)

const (
	Width  = 320
	Height = 200
	Rows   = 2
	Cols   = 4
	// PromptSize is the reference picture's side in pixels.
	PromptSize = 56
	// maxTurn bounds every copy's rotation either way: mental rotation
	// slows down with the angle, and past about a right angle people start
	// to make mistakes too.
	maxTurn = 75.0
)

type square [2]int

// shape is a polyomino translated so its smallest coordinates are 0.
type shape []square

func normalize(s shape) shape {
	minX, minY := s[0][0], s[0][1]
	for _, q := range s {
		minX, minY = min(minX, q[0]), min(minY, q[1])
	}
	out := make(shape, len(s))
	for i, q := range s {
		out[i] = square{q[0] - minX, q[1] - minY}
	}
	slices.SortFunc(out, func(a, b square) int {
		if a[1] != b[1] {
			return a[1] - b[1]
		}
		return a[0] - b[0]
	})
	return out
}

func (s shape) turned() shape {
	out := make(shape, len(s))
	for i, q := range s {
		out[i] = square{-q[1], q[0]}
	}
	return normalize(out)
}

func (s shape) flipped() shape {
	out := make(shape, len(s))
	for i, q := range s {
		out[i] = square{-q[0], q[1]}
	}
	return normalize(out)
}

// chiral reports whether no rotation of s equals its mirror image.
func (s shape) chiral() bool {
	m := s.flipped()
	t := normalize(s)
	for range 4 {
		if slices.Equal(t, m) {
			return false
		}
		t = t.turned()
	}
	return true
}

// bases are chiral shapes made of a long bar plus a foot or a bump, whose
// handedness a person reads at a glance: follow the bar to the foot and see
// which way it turns. Shapes without a long bar (S, N, F, P, random
// hexominoes) look like their own mirror image once turned, and a blind
// test showed people misjudging them.
var bases = []shape{
	{{0, 0}, {0, 1}, {0, 2}, {0, 3}, {1, 3}},         // L pentomino
	{{1, 0}, {0, 1}, {1, 1}, {1, 2}, {1, 3}},         // Y pentomino
	{{0, 0}, {0, 1}, {0, 2}, {0, 3}, {0, 4}, {1, 4}}, // long L
	// No L with a long foot: with arms of nearly equal length, which one is
	// the bar is no longer obvious.
}

// randomShape picks a base shape in a random orientation and handedness.
func randomShape(r *rand.Rand) shape {
	s := normalize(bases[r.IntN(len(bases))])
	for n := r.IntN(4); n > 0; n-- {
		s = s.turned()
	}
	if r.IntN(2) == 0 {
		s = s.flipped()
	}
	return s
}

// copyOf is one cell's picture of the shape.
type copyOf struct {
	turn    float64 // degrees
	flipped bool
	fill    color.RGBA
}

// centre and reach of a shape in square units: the centroid and the
// farthest square corner from it.
func extent(s shape) (cx, cy, reach float64) {
	for _, q := range s {
		cx += float64(q[0]) + 0.5
		cy += float64(q[1]) + 0.5
	}
	cx /= float64(len(s))
	cy /= float64(len(s))
	for _, q := range s {
		for _, c := range [][2]float64{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
			reach = math.Max(reach, math.Hypot(float64(q[0])+c[0]-cx, float64(q[1])+c[1]-cy))
		}
	}
	return
}

// paint draws s centred at (x, y) with the given unit size, turned and
// optionally flipped, with a dark outline.
func paint(img *image.RGBA, s shape, x, y, unit, turn float64, flipped bool, fill color.RGBA) {
	cx, cy, reach := extent(s)
	has := map[square]bool{}
	for _, q := range s {
		has[q] = true
	}
	sin, cos := math.Sincos(-turn * math.Pi / 180)
	// local maps an image point to shape units, undoing the turn and flip.
	local := func(px, py float64) (float64, float64) {
		dx, dy := (px-x)/unit, (py-y)/unit
		u, v := dx*cos-dy*sin, dx*sin+dy*cos
		if flipped {
			u = -u
		}
		return u + cx, v + cy
	}
	in := func(u, v float64) bool { return has[square{int(math.Floor(u)), int(math.Floor(v))}] }
	edge := 1.7 / unit
	half := reach*unit + 3
	x0, y0, x1, y1 := int(x-half), int(y-half), int(x+half+1), int(y+half+1)
	imagegen.FillShape(img, x0, y0, x1, y1, color.RGBA{30, 30, 34, 255}, 0.9, func(px, py float64) bool {
		u, v := local(px, py)
		return in(u-edge, v-edge) || in(u+edge, v-edge) || in(u-edge, v+edge) || in(u+edge, v+edge)
	})
	imagegen.FillShape(img, x0, y0, x1, y1, fill, 1, func(px, py float64) bool { return in(local(px, py)) })
}

// Generate builds a puzzle from r. Production callers seed r from crypto/rand.
func Generate(r *rand.Rand) (grid.Puzzle, error) {
	p, _, _, err := generate(r)
	return p, err
}

// generate also returns the shape and the copies, for tests.
func generate(r *rand.Rand) (grid.Puzzle, shape, []copyOf, error) {
	s := randomShape(r)
	_, _, reach := extent(s)
	cellW, cellH := float64(Width)/Cols, float64(Height)/Rows
	unit := math.Min(cellW, cellH) * 0.4 / reach

	copies := make([]copyOf, Rows*Cols)
	flips := r.Perm(Rows * Cols)[:2+r.IntN(2)]
	var answer []int
	for i := range copies {
		copies[i] = copyOf{turn: (r.Float64()*2 - 1) * maxTurn, fill: imagegen.HSV(r.Float64()*360, 0.5+r.Float64()*0.35, 0.6+r.Float64()*0.3)}
		if slices.Contains(flips, i) {
			copies[i].flipped = true
			answer = append(answer, i)
		}
	}

	bg := imagegen.Background(r, Width, Height)
	white := color.RGBA{255, 255, 255, 255}
	for y := 0; y < Height; y++ {
		for x := 0; x < Width; x++ {
			imagegen.Blend(bg, x, y, white, 0.42)
		}
	}
	imagegen.Clutter(r, bg, false)
	for i, c := range copies {
		x0, y0, x1, y1 := grid.Cell(Width, Height, Rows, Cols, i)
		// A little jitter, so the copies do not sit on a fixed lattice.
		x := (x0+x1)/2 + (r.Float64()*2-1)*4
		y := (y0+y1)/2 + (r.Float64()*2-1)*5
		paint(bg, s, x, y, unit*(0.9+r.Float64()*0.15), c.turn, c.flipped, c.fill)
	}
	grid.Divide(bg, Rows, Cols)
	imagegen.Finish(r, bg)
	url, err := imagegen.JPEG(r, bg)
	if err != nil {
		return grid.Puzzle{}, nil, nil, err
	}
	ref, err := reference(s)
	if err != nil {
		return grid.Puzzle{}, nil, nil, err
	}
	return grid.Puzzle{Image: url, PromptImage: ref, Prompt: "请选出所有与示例互为镜像（翻转过）的图形，只是转动过的不要选",
		Width: Width, Height: Height, Rows: Rows, Cols: Cols, Answer: answer}, s, copies, nil
}

// reference draws the shape upright, dark on light, for the prompt.
func reference(s shape) (string, error) {
	img := image.NewRGBA(image.Rect(0, 0, PromptSize, PromptSize))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 246, 248, 250, 255
	}
	_, _, reach := extent(s)
	paint(img, s, PromptSize/2, PromptSize/2, PromptSize*0.42/reach, 0, false, color.RGBA{70, 80, 96, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return "", fmt.Errorf("mirror: %w", err)
	}
	return imagegen.DataURL("image/png", b.Bytes()), nil
}
