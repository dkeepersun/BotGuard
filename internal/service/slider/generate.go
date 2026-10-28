// Package slider generates jigsaw slider puzzles and checks answers.
//
// Backgrounds come from an imagegen.Source: procedural pictures, photos, or
// both.
//
// Each background has a decoy slot on the piece's row, inside the answer
// range: the same shape, as deep, and filled with the same picture as the
// real one. Matching the piece's pixels, its correlation or its outline
// against the row then finds two equal candidates; only whether the slot
// continues its surroundings tells them apart, which is also what a person
// checks while dragging. To blunt that check for programs, a 5-pixel band
// around every slot and inside the piece's edge is softened and noised, so
// fine detail does not run across the edge while the larger picture still
// does. Slots are carved (darkened) or frosted (blurred and lightened) at
// random, so "the darkest place" is not a shortcut. The piece is cut from a
// colour-shifted, noisy copy, and every slot's depth varies pixel by pixel,
// so neither equals the background times a constant. Measured solvers still
// find the slot about two times in three (docs/challenge-catalog.md §9): the
// slider is a speed bump, which is why answers also need a human-looking
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
	"sync"

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

// rimPad is how far outside the shape mask.dist reaches.
const rimPad = 6

// mask precomputes inside and border pixels for a shape, and each pixel's
// distance to the shape's edge: dist[y+rimPad][x+rimPad] is the Chebyshev
// distance from (x, y) to the nearest pixel on the other side of the edge,
// capped at rimPad+1, for pixels up to rimPad outside the piece box.
type mask struct {
	inside, border [PieceSize][PieceSize]bool
	dist           [PieceSize + 2*rimPad][PieceSize + 2*rimPad]int
}

// masks caches newMask per shape (at most 5⁴); the distance table costs
// more than the rest of a puzzle.
var masks sync.Map

func maskFor(s shape) *mask {
	if m, ok := masks.Load(s); ok {
		return m.(*mask)
	}
	m, _ := masks.LoadOrStore(s, newMask(s))
	return m.(*mask)
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
	for y := -rimPad; y < PieceSize+rimPad; y++ {
		for x := -rimPad; x < PieceSize+rimPad; x++ {
			in := at(x, y)
			d := rimPad + 1
			for dy := -rimPad; dy <= rimPad; dy++ {
				for dx := -rimPad; dx <= rimPad; dx++ {
					if at(x+dx, y+dy) != in {
						d = min(d, max(abs(dx), abs(dy)))
					}
				}
			}
			m.dist[y+rimPad][x+rimPad] = d
		}
	}
	return m
}

func abs(v int) int { return max(v, -v) }

// gapStyle is how every slot in one puzzle is drawn: carved (darkened) or
// frosted (blurred and lightened, like the glass slots of the big vendors).
// Mixing the two means a solver cannot look for the darkest place.
type gapStyle struct {
	frosted bool
	depth   float64
}

func randomStyle(r *rand.Rand) gapStyle {
	if r.IntN(2) == 0 {
		return gapStyle{frosted: true, depth: 0.35 + r.Float64()*0.15}
	}
	return gapStyle{depth: 0.5 + r.Float64()*0.1}
}

// cutGap draws a slot: the inside darkened, or blurred (from soft, a blurred
// copy of img) and lightened, with a light outline. The depth varies by up
// to ±0.06 per pixel, so the slot is not the background times one constant.
func cutGap(r *rand.Rand, img, soft *image.RGBA, m *mask, ox, oy int, st gapStyle) {
	white := color.RGBA{255, 255, 255, 255}
	black := color.RGBA{0, 0, 0, 255}
	outline := 0.4
	if st.frosted {
		outline = 0.6
	}
	for y := 0; y < PieceSize; y++ {
		for x := 0; x < PieceSize; x++ {
			if !m.inside[y][x] {
				continue
			}
			px, py := ox+x, oy+y
			depth := st.depth + (r.Float64()*2-1)*0.06
			switch {
			case m.border[y][x]:
				imagegen.Blend(img, px, py, white, outline)
			case st.frosted:
				i := img.PixOffset(px, py)
				copy(img.Pix[i:i+3], soft.Pix[i:i+3])
				imagegen.Blend(img, px, py, white, depth)
			default:
				imagegen.Blend(img, px, py, black, depth)
			}
		}
	}
}

// roughen softens a band of rimWidth pixels just outside a slot: half
// blurred, plus noise. Fine detail across the slot's edge is what a solver
// compares with the piece's edge; a person judges the larger picture, which
// the band keeps.
func roughen(r *rand.Rand, img, soft *image.RGBA, m *mask, ox, oy int) {
	b := img.Rect
	for y := -rimPad; y < PieceSize+rimPad; y++ {
		for x := -rimPad; x < PieceSize+rimPad; x++ {
			px, py := ox+x, oy+y
			if d := m.dist[y+rimPad][x+rimPad]; d > rimWidth || !image.Pt(px, py).In(b) ||
				(x >= 0 && y >= 0 && x < PieceSize && y < PieceSize && m.inside[y][x]) {
				continue
			}
			i := img.PixOffset(px, py)
			for c := range 3 {
				v := 0.4*float64(img.Pix[i+c]) + 0.6*float64(soft.Pix[i+c]) + float64(r.IntN(21)-10)
				img.Pix[i+c] = uint8(max(0, min(255, v)))
			}
		}
	}
}

// rimWidth is the band roughen softens outside each slot, and the band of
// the piece softened inside its edge.
const rimWidth = 5

// blur returns img box-blurred with radius rad.
func blur(img *image.RGBA, rad int) *image.RGBA {
	b := img.Rect
	tmp, out := image.NewRGBA(b), image.NewRGBA(b)
	pass := func(dst, src *image.RGBA, dx, dy int) {
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				var sum [3]int
				n := 0
				for k := -rad; k <= rad; k++ {
					q := image.Pt(x+k*dx, y+k*dy)
					if !q.In(b) {
						continue
					}
					j := src.PixOffset(q.X, q.Y)
					sum[0], sum[1], sum[2] = sum[0]+int(src.Pix[j]), sum[1]+int(src.Pix[j+1]), sum[2]+int(src.Pix[j+2])
					n++
				}
				i := dst.PixOffset(x, y)
				dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = uint8(sum[0]/n), uint8(sum[1]/n), uint8(sum[2]/n), 255
			}
		}
	}
	pass(tmp, img, 1, 0)
	pass(out, tmp, 0, 1)
	return out
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
	m := maskFor(randomShape(r))

	x := minX + r.IntN(maxX-minX)
	y := 4 + r.IntN(Height-PieceSize-8)

	// Decoys: the real slot's picture copied onto the same row. Copies stay
	// 4 px clear of the slot, so the blur below sees the slot as it was.
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
	}
	soft := blur(bg, 2)

	// The piece is the background under the slot, shifted in colour and
	// noised, so its pixels never equal the background's.
	var gain, offset [3]float64
	for c := range gain {
		gain[c], offset[c] = 0.88+r.Float64()*0.24, r.Float64()*28-14
	}
	// Its own edge band is softened like the band around each slot, and
	// lightened like glass.
	piece := image.NewRGBA(image.Rect(0, 0, PieceSize, PieceSize))
	for py := 0; py < PieceSize; py++ {
		for px := 0; px < PieceSize; px++ {
			if !m.inside[py][px] {
				continue
			}
			i, j := bg.PixOffset(x+px, y+py), piece.PixOffset(px, py)
			edge := m.dist[py+rimPad][px+rimPad] <= rimWidth
			for c := range 3 {
				v := bg.Pix[i+c]
				if edge {
					v = uint8((int(v)*2 + int(soft.Pix[i+c])*3) / 5)
				}
				piece.Pix[j+c] = shift(v, gain[c], offset[c], r.IntN(17)-8)
			}
			piece.Pix[j+3] = 255
			switch {
			case m.border[py][px]:
				imagegen.Blend(piece, px, py, color.RGBA{255, 255, 255, 255}, 0.7)
			case edge:
				imagegen.Blend(piece, px, py, color.RGBA{255, 255, 255, 255}, 0.2)
			}
		}
	}

	// Every slot gets the same softened band around it and is cut the
	// same way.
	st := randomStyle(r)
	for _, sx := range taken {
		roughen(r, bg, soft, m, sx, y)
	}
	for _, sx := range taken {
		cutGap(r, bg, soft, m, sx, y, st)
	}

	var jb, pb bytes.Buffer
	if err := jpeg.Encode(&jb, bg, &jpeg.Options{Quality: 60 + r.IntN(21)}); err != nil {
		return Puzzle{}, err
	}
	if err := png.Encode(&pb, piece); err != nil {
		return Puzzle{}, err
	}
	return Puzzle{Background: imagegen.DataURL("image/jpeg", jb.Bytes()), Piece: imagegen.DataURL("image/png", pb.Bytes()), X: x, Y: y}, nil
}
