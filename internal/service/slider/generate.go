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
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"math/rand/v2"
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

func randomColor(r *rand.Rand) color.RGBA {
	h := r.Float64() * 360
	s := 0.3 + r.Float64()*0.45
	v := 0.45 + r.Float64()*0.45
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	var rr, gg, bb float64
	switch int(h / 60) {
	case 0:
		rr, gg = c, x
	case 1:
		rr, gg = x, c
	case 2:
		gg, bb = c, x
	case 3:
		gg, bb = x, c
	case 4:
		rr, bb = x, c
	default:
		rr, bb = c, x
	}
	m := v - c
	return color.RGBA{uint8((rr + m) * 255), uint8((gg + m) * 255), uint8((bb + m) * 255), 255}
}

func blend(dst *image.RGBA, x, y int, c color.RGBA, a float64) {
	i := dst.PixOffset(x, y)
	p := dst.Pix[i : i+3 : i+3]
	p[0] = uint8(float64(p[0])*(1-a) + float64(c.R)*a)
	p[1] = uint8(float64(p[1])*(1-a) + float64(c.G)*a)
	p[2] = uint8(float64(p[2])*(1-a) + float64(c.B)*a)
}

func background(r *rand.Rand) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, Width, Height))
	a, b := randomColor(r), randomColor(r)
	for y := 0; y < Height; y++ {
		for x := 0; x < Width; x++ {
			t := (float64(x)/Width + float64(y)/Height) / 2
			i := img.PixOffset(x, y)
			img.Pix[i] = uint8(float64(a.R)*(1-t) + float64(b.R)*t)
			img.Pix[i+1] = uint8(float64(a.G)*(1-t) + float64(b.G)*t)
			img.Pix[i+2] = uint8(float64(a.B)*(1-t) + float64(b.B)*t)
			img.Pix[i+3] = 255
		}
	}
	for n := 10 + r.IntN(7); n > 0; n-- {
		c, alpha := randomColor(r), 0.25+r.Float64()*0.45
		cx, cy := r.Float64()*Width, r.Float64()*Height
		size := 15 + r.Float64()*60
		var inside func(x, y float64) bool
		switch r.IntN(3) {
		case 0:
			inside = func(x, y float64) bool { return math.Hypot(x-cx, y-cy) <= size }
		case 1:
			w, h, rot := size, size*(0.4+r.Float64()), r.Float64()*math.Pi
			sin, cos := math.Sincos(rot)
			inside = func(x, y float64) bool {
				dx, dy := x-cx, y-cy
				u, v := dx*cos+dy*sin, -dx*sin+dy*cos
				return math.Abs(u) <= w && math.Abs(v) <= h
			}
		default:
			var pts [3][2]float64
			for i := range pts {
				ang := r.Float64() * 2 * math.Pi
				pts[i] = [2]float64{cx + math.Cos(ang)*size, cy + math.Sin(ang)*size}
			}
			inside = func(x, y float64) bool { return inTriangle(x, y, pts) }
		}
		x0, x1 := max(0, int(cx-size*1.5)), min(Width, int(cx+size*1.5)+1)
		y0, y1 := max(0, int(cy-size*1.5)), min(Height, int(cy+size*1.5)+1)
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				if inside(float64(x)+0.5, float64(y)+0.5) {
					blend(img, x, y, c, alpha)
				}
			}
		}
	}
	for i := 0; i < len(img.Pix); i += 4 {
		d := r.IntN(25) - 12
		for k := 0; k < 3; k++ {
			img.Pix[i+k] = uint8(max(0, min(255, int(img.Pix[i+k])+d)))
		}
	}
	return img
}

func inTriangle(x, y float64, p [3][2]float64) bool {
	sign := func(a, b [2]float64) float64 { return (x-b[0])*(a[1]-b[1]) - (a[0]-b[0])*(y-b[1]) }
	d1, d2, d3 := sign(p[0], p[1]), sign(p[1], p[2]), sign(p[2], p[0])
	neg := d1 < 0 || d2 < 0 || d3 < 0
	pos := d1 > 0 || d2 > 0 || d3 > 0
	return !(neg && pos)
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
				blend(img, ox+x, oy+y, white, outline)
			} else {
				blend(img, ox+x, oy+y, black, darken)
			}
		}
	}
}

func dataURL(mime string, b []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)
}

// Generate builds a puzzle from r. Production callers seed r from crypto/rand;
// tests use a fixed seed for reproducible images.
func Generate(r *rand.Rand) (Puzzle, error) {
	bg := background(r)
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
				blend(piece, px, py, color.RGBA{255, 255, 255, 255}, 0.7)
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
	return Puzzle{Background: dataURL("image/jpeg", jb.Bytes()), Piece: dataURL("image/png", pb.Bytes()), X: x, Y: y}, nil
}
