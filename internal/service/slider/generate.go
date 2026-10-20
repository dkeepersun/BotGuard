// Package slider generates jigsaw slider puzzles and checks answers.
//
// Backgrounds are procedural (layered random shapes and noise), so there are
// no image licences to manage and no two puzzles repeat. A fainter decoy gap
// and JPEG noise make simple template matching less reliable; none of this
// stops a determined solver, which is why answers also need a human-looking
// drag trajectory and every challenge still carries proof of work.
package slider

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"math/rand/v2"

	"github.com/dkeepersun/BotGuard/internal/service/imagegen"
)

const (
	Width     = 320
	Height    = 160
	PieceSize = 56

	square = 40 // body of the piece inside the PieceSize box
	margin = (PieceSize - square) / 2
	knob   = 7.0
)

// Puzzle is one generated challenge. X is the answer and must never reach
// the browser except sealed inside the signed challenge.
type Puzzle struct {
	Background string // data URL, JPEG
	Piece      string // data URL, PNG with alpha
	X, Y       int
}

// shape is a jigsaw mask: +1 knob out, -1 knob in, 0 flat, per side
// (top, right, bottom, left).
type shape [4]int

func randomShape(r *rand.Rand) shape {
	var s shape
	for i := range s {
		s[i] = []int{1, -1, 1, -1, 0}[r.IntN(5)]
	}
	if s == (shape{}) {
		s[r.IntN(4)] = 1
	}
	return s
}

func (s shape) contains(px, py int) bool {
	x, y := float64(px)+0.5, float64(py)+0.5
	lo, hi := float64(margin), float64(margin+square)
	in := x >= lo && x < hi && y >= lo && y < hi
	mid := float64(PieceSize) / 2
	centers := [4][2]float64{{mid, lo}, {hi, mid}, {mid, hi}, {lo, mid}}
	for i, c := range centers {
		if s[i] == 0 {
			continue
		}
		d := math.Hypot(x-c[0], y-c[1]) <= knob
		if s[i] > 0 && d {
			in = true
		} else if s[i] < 0 && d {
			in = false
		}
	}
	return in
}

// mask precomputes inside and border pixels for a shape.
type mask struct {
	inside, border [PieceSize][PieceSize]bool
}

func newMask(s shape) *mask {
	m := &mask{}
	for y := 0; y < PieceSize; y++ {
		for x := 0; x < PieceSize; x++ {
			m.inside[y][x] = s.contains(x, y)
		}
	}
	at := func(x, y int) bool { return x >= 0 && y >= 0 && x < PieceSize && y < PieceSize && m.inside[y][x] }
	for y := 0; y < PieceSize; y++ {
		for x := 0; x < PieceSize; x++ {
			if m.inside[y][x] && (!at(x-1, y) || !at(x+1, y) || !at(x, y-1) || !at(x, y+1)) {
				m.border[y][x] = true
			}
		}
	}
	return m
}

// cutGap darkens the masked area and lightens its outline, like a carved slot.
func cutGap(img *image.RGBA, m *mask, ox, oy int, darken, outline float64) {
	white := color.RGBA{255, 255, 255, 255}
	black := color.RGBA{0, 0, 0, 255}
	for y := 0; y < PieceSize; y++ {
		for x := 0; x < PieceSize; x++ {
			if !m.inside[y][x] {
				continue
			}
			if m.border[y][x] {
				imagegen.Blend(img, ox+x, oy+y, white, outline)
			} else {
				imagegen.Blend(img, ox+x, oy+y, black, darken)
			}
		}
	}
}

// Generate builds a puzzle from r. Production callers seed r from crypto/rand;
// tests use a fixed seed for reproducible images.
func Generate(r *rand.Rand) (Puzzle, error) {
	bg := imagegen.Background(r, Width, Height)
	m := newMask(randomShape(r))

	// The piece starts at x=0, so the answer is always a real drag away.
	x := PieceSize + 20 + r.IntN(Width-2*PieceSize-24)
	y := 4 + r.IntN(Height-PieceSize-8)

	piece := image.NewRGBA(image.Rect(0, 0, PieceSize, PieceSize))
	for py := 0; py < PieceSize; py++ {
		for px := 0; px < PieceSize; px++ {
			if !m.inside[py][px] {
				continue
			}
			i, j := bg.PixOffset(x+px, y+py), piece.PixOffset(px, py)
			copy(piece.Pix[j:j+3], bg.Pix[i:i+3])
			piece.Pix[j+3] = 255
			if m.border[py][px] {
				imagegen.Blend(piece, px, py, color.RGBA{255, 255, 255, 255}, 0.7)
			}
		}
	}

	// A fainter decoy slot that does not overlap the real one.
	decoy := newMask(randomShape(r))
	var dx int
	for {
		dx = r.IntN(Width - PieceSize)
		if dx+PieceSize < x-4 || dx > x+PieceSize+4 {
			break
		}
	}
	dy := 4 + r.IntN(Height-PieceSize-8)
	cutGap(bg, decoy, dx, dy, 0.28+r.Float64()*0.1, 0.2)
	cutGap(bg, m, x, y, 0.5+r.Float64()*0.1, 0.4)

	var jb, pb bytes.Buffer
	if err := jpeg.Encode(&jb, bg, &jpeg.Options{Quality: 60 + r.IntN(21)}); err != nil {
		return Puzzle{}, err
	}
	if err := png.Encode(&pb, piece); err != nil {
		return Puzzle{}, err
	}
	return Puzzle{Background: imagegen.DataURL("image/jpeg", jb.Bytes()), Piece: imagegen.DataURL("image/png", pb.Bytes()), X: x, Y: y}, nil
}
