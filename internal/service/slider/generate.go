// Package slider generates jigsaw slider puzzles and checks answers.
//
// Backgrounds are procedural (layered random shapes and noise), so there are
// no image licences to manage and no two puzzles repeat.
//
// Each background has a decoy slot on the piece's row, inside the answer
// range: the same shape, as deep, and filled with the same picture as the
// real one. Matching the piece's pixels, its correlation or its outline
// against the row then finds two equal candidates; only whether the slot
// continues its surroundings tells them apart, which is also what a person
// checks while dragging. The piece is cut from a colour-shifted, noisy copy,
// and every slot's depth varies pixel by pixel, so neither equals the
// background times a constant. This halves what cheap solvers get, no more:
// the slider is a speed bump, which is why answers also need a human-looking
// drag trajectory, every challenge carries proof of work, and high risk gets
// a harder kind.
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

	// Decoys is how many extra slots each background gets. One costs people
	// a second look; more would cost them failed attempts.
	Decoys = 1
	// minX and maxX bound the answer: the piece starts at x=0, so the answer
	// is always a real drag away.
	minX = PieceSize + 20
	maxX = Width - PieceSize - 4
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
// The depth varies by up to ±jitter per pixel, so the slot is not the
// background times one constant.
func cutGap(r *rand.Rand, img *image.RGBA, m *mask, ox, oy int, darken, outline, jitter float64) {
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
				imagegen.Blend(img, ox+x, oy+y, black, darken+(r.Float64()*2-1)*jitter)
			}
		}
	}
}

// shift returns a channel value moved by gain, offset and noise, clamped.
func shift(v uint8, gain, offset float64, noise int) uint8 {
	return uint8(min(255, max(0, float64(v)*gain+offset+float64(noise))))
}

// overlaps reports whether slots starting at a and b come closer than 4 px.
func overlaps(a, b int) bool { return a < b+PieceSize+4 && b < a+PieceSize+4 }

// decoyX picks a left edge for a decoy inside the answer range, clear of the
// slots already placed, or -1 when the row has no room left. One decoy always
// fits beside the real slot.
func decoyX(r *rand.Rand, taken []int) int {
	var free []int
	for x := minX; x < maxX; x++ {
		clear := true
		for _, t := range taken {
			clear = clear && !overlaps(x, t)
		}
		if clear {
			free = append(free, x)
		}
	}
	if len(free) == 0 {
		return -1
	}
	return free[r.IntN(len(free))]
}

// Generate builds a puzzle from r. Production callers seed r from crypto/rand;
// tests use a fixed seed for reproducible images.
func Generate(r *rand.Rand, src imagegen.Source) (Puzzle, error) {
	bg, _ := src.Background(r, Width, Height)
	m := newMask(randomShape(r))

	x := minX + r.IntN(maxX-minX)
	y := 4 + r.IntN(Height-PieceSize-8)

	// The piece is the background under the slot, shifted in colour and
	// noised, so its pixels never equal the background's.
	var gain, offset [3]float64
	for c := range gain {
		gain[c], offset[c] = 0.88+r.Float64()*0.24, r.Float64()*28-14
	}
	piece := image.NewRGBA(image.Rect(0, 0, PieceSize, PieceSize))
	for py := 0; py < PieceSize; py++ {
		for px := 0; px < PieceSize; px++ {
			if !m.inside[py][px] {
				continue
			}
			i, j := bg.PixOffset(x+px, y+py), piece.PixOffset(px, py)
			for c := range 3 {
				piece.Pix[j+c] = shift(bg.Pix[i+c], gain[c], offset[c], r.IntN(17)-8)
			}
			piece.Pix[j+3] = 255
			if m.border[py][px] {
				imagegen.Blend(piece, px, py, color.RGBA{255, 255, 255, 255}, 0.7)
			}
		}
	}

	// Decoys: the real slot's picture copied onto the same row, then carved
	// the same way.
	taken := []int{x}
	for range Decoys {
		dx := decoyX(r, taken)
		if dx < 0 {
			break
		}
		taken = append(taken, dx)
		for py := 0; py < PieceSize; py++ {
			for px := 0; px < PieceSize; px++ {
				if m.inside[py][px] {
					i, j := bg.PixOffset(x+px, y+py), bg.PixOffset(dx+px, y+py)
					copy(bg.Pix[j:j+3], bg.Pix[i:i+3])
				}
			}
		}
		cutGap(r, bg, m, dx, y, 0.5+r.Float64()*0.1, 0.4, 0.06)
	}
	cutGap(r, bg, m, x, y, 0.5+r.Float64()*0.1, 0.4, 0.06)

	var jb, pb bytes.Buffer
	if err := jpeg.Encode(&jb, bg, &jpeg.Options{Quality: 60 + r.IntN(21)}); err != nil {
		return Puzzle{}, err
	}
	if err := png.Encode(&pb, piece); err != nil {
		return Puzzle{}, err
	}
	return Puzzle{Background: imagegen.DataURL("image/jpeg", jb.Bytes()), Piece: imagegen.DataURL("image/png", pb.Bytes()), X: x, Y: y}, nil
}
