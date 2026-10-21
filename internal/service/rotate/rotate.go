// Package rotate generates "turn the disc until it lines up" puzzles.
//
// A disc in the middle of a procedural picture is turned by a random angle.
// The user drags a slider that turns the disc back; it is solved when the
// bands crossing the disc's edge line up with the picture around it. The
// picture is abstract, so there is no "upright" for a model to recognise:
// only edge continuity tells the answer. That can still be found with image
// processing along the circle, so this is one kind in the pool, not a wall
// on its own (docs/rotate-challenge.md).
package rotate

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"math/rand/v2"

	"github.com/dkeepersun/BotGuard/internal/service/imagegen"
)

const (
	Width  = 320
	Height = 200
	// Track is the drag distance, in image pixels, for a full turn.
	Track = 280
)

// Puzzle is one generated challenge. Angle is the answer and must never
// reach the browser except sealed inside the signed challenge.
type Puzzle struct {
	Background string // data URL, JPEG: the picture with the disc area blanked
	Disc       string // data URL, PNG with alpha: the turned disc
	CX, CY     int
	Radius     int
	// Angle in degrees, in [30, 330]: turning the disc clockwise by Angle
	// restores the picture.
	Angle float64
}

// AnswerX is the drag distance that turns the disc by the answer angle.
func (p Puzzle) AnswerX() float64 { return p.Angle / 360 * Track }

// ToleranceX converts an angle tolerance into drag pixels.
func ToleranceX(degrees float64) int { return int(math.Round(degrees / 360 * Track)) }

// picture draws a background whose bands cross the disc boundary, so every
// wrong angle visibly breaks at least one of them.
func picture(r *rand.Rand, cx, cy, radius float64) *image.RGBA {
	img := imagegen.Background(r, Width, Height)
	white := color.RGBA{255, 255, 255, 255}
	for y := 0; y < Height; y++ {
		for x := 0; x < Width; x++ {
			imagegen.Blend(img, x, y, white, 0.25)
		}
	}
	// Straight bands through the disc, at distinct angles so no two are
	// parallel enough to be confused after a turn.
	used := []float64{}
	for n := 3 + r.IntN(2); n > 0; n-- {
		var ang float64
		for try := 0; try < 50; try++ {
			ang = r.Float64() * math.Pi
			ok := true
			for _, u := range used {
				d := math.Abs(math.Mod(ang-u+math.Pi*1.5, math.Pi) - math.Pi/2)
				if d < 0.35 {
					ok = false
				}
			}
			if ok {
				break
			}
		}
		used = append(used, ang)
		off := (r.Float64()*2 - 1) * radius * 0.6
		nx, ny := -math.Sin(ang), math.Cos(ang)
		width := 5 + r.Float64()*6
		c := imagegen.RandomColor(r)
		imagegen.FillShape(img, 0, 0, Width, Height, c, 0.85, func(x, y float64) bool {
			return math.Abs((x-cx)*nx+(y-cy)*ny-off) <= width/2
		})
	}
	// Arcs from circles centred outside the disc that cut through its rim.
	for n := 1 + r.IntN(2); n > 0; n-- {
		a := r.Float64() * 2 * math.Pi
		dist := radius * (1.2 + r.Float64()*0.5)
		ax, ay := cx+math.Cos(a)*dist, cy+math.Sin(a)*dist
		ar := dist - radius*(0.3+r.Float64()*0.4)
		width := 4 + r.Float64()*4
		c := imagegen.RandomColor(r)
		imagegen.FillShape(img, 0, 0, Width, Height, c, 0.8, func(x, y float64) bool {
			return math.Abs(math.Hypot(x-ax, y-ay)-ar) <= width/2
		})
	}
	// A few solid dots straddling the rim.
	for n := 2 + r.IntN(3); n > 0; n-- {
		a := r.Float64() * 2 * math.Pi
		px, py := cx+math.Cos(a)*radius, cy+math.Sin(a)*radius
		size := 6 + r.Float64()*7
		c := imagegen.RandomColor(r)
		imagegen.FillShape(img, int(px-size)-1, int(py-size)-1, int(px+size)+2, int(py+size)+2, c, 0.9, func(x, y float64) bool {
			return math.Hypot(x-px, y-py) <= size
		})
	}
	return img
}

// bilinear samples img at (x, y), clamped to the image.
func bilinear(img *image.RGBA, x, y float64) (float64, float64, float64) {
	x = math.Max(0, math.Min(float64(Width)-1.001, x-0.5))
	y = math.Max(0, math.Min(float64(Height)-1.001, y-0.5))
	x0, y0 := int(x), int(y)
	fx, fy := x-float64(x0), y-float64(y0)
	var out [3]float64
	for k := 0; k < 3; k++ {
		at := func(px, py int) float64 { return float64(img.Pix[img.PixOffset(px, py)+k]) }
		top := at(x0, y0)*(1-fx) + at(x0+1, y0)*fx
		bottom := at(x0, y0+1)*(1-fx) + at(x0+1, y0+1)*fx
		out[k] = top*(1-fy) + bottom*fy
	}
	return out[0], out[1], out[2]
}

// Generate builds a puzzle from r. Production callers seed r from crypto/rand.
func Generate(r *rand.Rand) (Puzzle, error) {
	p, _, err := generate(r)
	return p, err
}

// generate also returns the unturned picture, for tests.
func generate(r *rand.Rand) (Puzzle, *image.RGBA, error) {
	radius := 64 + r.IntN(9)
	cx := Width/2 + r.IntN(25) - 12
	cy := Height/2 + r.IntN(17) - 8
	full := picture(r, float64(cx), float64(cy), float64(radius))
	// Away from 0° so the puzzle never starts solved or nearly so.
	angle := 30 + r.Float64()*300

	// The disc shows full(c + Rot(angle)·q): rotating it on screen by angle
	// (CSS rotate, clockwise in y-down coordinates) restores the picture.
	size := 2*radius + 2
	disc := image.NewNRGBA(image.Rect(0, 0, size, size))
	sin, cos := math.Sincos(angle * math.Pi / 180)
	half := float64(size) / 2
	R := float64(radius)
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			qx, qy := float64(px)+0.5-half, float64(py)+0.5-half
			d := math.Hypot(qx, qy)
			cover := math.Max(0, math.Min(1, R-d+0.5)) // anti-aliased rim
			if cover == 0 {
				continue
			}
			sx, sy := float64(cx)+qx*cos-qy*sin, float64(cy)+qx*sin+qy*cos
			cr, cg, cb := bilinear(full, sx, sy)
			// A thin light ring on the rim: rotation-invariant, and it hides
			// the seam so the outline itself gives nothing away.
			if d > R-2.5 {
				t := 0.55
				cr, cg, cb = cr*(1-t)+255*t, cg*(1-t)+255*t, cb*(1-t)+255*t
			}
			i := disc.PixOffset(px, py)
			disc.Pix[i], disc.Pix[i+1], disc.Pix[i+2] = uint8(cr), uint8(cg), uint8(cb)
			disc.Pix[i+3] = uint8(cover * 255)
		}
	}

	// Blank the disc area of the background with its average colour, so the
	// original picture under the disc never reaches the browser.
	bg := image.NewRGBA(full.Rect)
	copy(bg.Pix, full.Pix)
	rect := image.Rect(cx-radius-1, cy-radius-1, cx+radius+2, cy+radius+2)
	mean := imagegen.MeanLuma(full, rect)
	fill := color.RGBA{uint8(mean), uint8(mean), uint8(mean), 255}
	imagegen.FillShape(bg, rect.Min.X, rect.Min.Y, rect.Max.X, rect.Max.Y, fill, 1, func(x, y float64) bool {
		return math.Hypot(x-float64(cx), y-float64(cy)) <= R+0.5
	})

	bgURL, err := imagegen.JPEG(r, bg)
	if err != nil {
		return Puzzle{}, nil, err
	}
	var pb bytes.Buffer
	if err := png.Encode(&pb, disc); err != nil {
		return Puzzle{}, nil, err
	}
	return Puzzle{Background: bgURL, Disc: imagegen.DataURL("image/png", pb.Bytes()),
		CX: cx, CY: cy, Radius: radius, Angle: angle}, full, nil
}
