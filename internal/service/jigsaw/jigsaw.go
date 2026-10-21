// Package jigsaw generates "swap the two misplaced tiles" puzzles. A
// procedural picture is cut into 2×4 tiles and two of them are swapped; the
// bands running across the picture break at the swapped tiles. The user drags
// one onto the other. Answers are the tile pair plus the drag path, checked
// like the slider's (docs/challenge-catalog.md §3.1).
package jigsaw

import (
	"image"
	"image/color"
	"math"
	"math/rand/v2"

	"github.com/dkeepersun/BotGuard/internal/service/imagegen"
)

const (
	Width  = 320
	Height = 200
	Rows   = 2
	Cols   = 4
	TileW  = Width / Cols
	TileH  = Height / Rows
	// MaxPoints matches the slider's cap on submitted path samples.
	MaxPoints = 400
)

// Puzzle is one generated challenge; A and B are the answer and must never
// reach the browser except sealed inside the signed challenge.
type Puzzle struct {
	Image string // data URL, JPEG, with tiles A and B swapped
	A, B  int
}

// picture draws long bands and arcs across the whole image, so every tile
// border cuts through several of them.
func picture(r *rand.Rand) *image.RGBA {
	img := imagegen.Background(r, Width, Height)
	white := color.RGBA{255, 255, 255, 255}
	for y := 0; y < Height; y++ {
		for x := 0; x < Width; x++ {
			imagegen.Blend(img, x, y, white, 0.2)
		}
	}
	for n := 4 + r.IntN(3); n > 0; n-- {
		ang := r.Float64() * math.Pi
		cx, cy := r.Float64()*Width, r.Float64()*Height
		nx, ny := -math.Sin(ang), math.Cos(ang)
		w := 6 + r.Float64()*8
		c := imagegen.RandomColor(r)
		imagegen.FillShape(img, 0, 0, Width, Height, c, 0.85, func(x, y float64) bool {
			return math.Abs((x-cx)*nx+(y-cy)*ny) <= w/2
		})
	}
	for n := 2 + r.IntN(2); n > 0; n-- {
		cx, cy := r.Float64()*Width, r.Float64()*Height
		rad := 40 + r.Float64()*70
		w := 5 + r.Float64()*6
		c := imagegen.RandomColor(r)
		imagegen.FillShape(img, 0, 0, Width, Height, c, 0.8, func(x, y float64) bool {
			return math.Abs(math.Hypot(x-cx, y-cy)-rad) <= w/2
		})
	}
	return img
}

func swapTiles(img *image.RGBA, a, b int) {
	ax, ay := (a%Cols)*TileW, (a/Cols)*TileH
	bx, by := (b%Cols)*TileW, (b/Cols)*TileH
	for y := 0; y < TileH; y++ {
		for x := 0; x < TileW; x++ {
			i, j := img.PixOffset(ax+x, ay+y), img.PixOffset(bx+x, by+y)
			for k := 0; k < 4; k++ {
				img.Pix[i+k], img.Pix[j+k] = img.Pix[j+k], img.Pix[i+k]
			}
		}
	}
}

// Generate builds a puzzle from r. Production callers seed r from crypto/rand.
func Generate(r *rand.Rand) (Puzzle, error) {
	img := picture(r)
	a := r.IntN(Rows * Cols)
	b := r.IntN(Rows*Cols - 1)
	if b >= a {
		b++
	}
	swapTiles(img, a, b)
	url, err := imagegen.JPEG(r, img)
	if err != nil {
		return Puzzle{}, err
	}
	return Puzzle{Image: url, A: min(a, b), B: max(a, b)}, nil
}

// TileAt returns the tile under image point (x, y), or -1 outside.
func TileAt(x, y float64) int {
	if x < 0 || y < 0 || x >= Width || y >= Height {
		return -1
	}
	return int(y)/TileH*Cols + int(x)/TileW
}

// Rejection reasons, logged server-side only.
const (
	ReasonPair         = "PAIR"
	ReasonPoints       = "TOO_FEW_POINTS"
	ReasonDuration     = "DURATION"
	ReasonTimeOrder    = "TIME_ORDER"
	ReasonEnds         = "ENDS"
	ReasonTeleport     = "TELEPORT"
	ReasonEqualSteps   = "EQUAL_INTERVALS"
	ReasonConstant     = "CONSTANT_VELOCITY"
	ReasonStraightLine = "STRAIGHT_LINE"
)

// Check validates the swapped pair and the drag that made it: path points
// are [ms since grab, x, y] in image coordinates. input is the pointer type.
func Check(a, b int, swap []int, path [][3]float64, input string) string {
	if len(swap) != 2 || swap[0] == swap[1] || min(swap[0], swap[1]) != a || max(swap[0], swap[1]) != b {
		return ReasonPair
	}
	if len(path) < 6 || len(path) > MaxPoints {
		return ReasonPoints
	}
	first, last := path[0], path[len(path)-1]
	if d := last[0] - first[0]; d < 250 || d > 30_000 {
		return ReasonDuration
	}
	from, to := TileAt(first[1], first[2]), TileAt(last[1], last[2])
	if !((from == swap[0] && to == swap[1]) || (from == swap[1] && to == swap[0])) {
		return ReasonEnds
	}
	total := 0.0
	var dts, speeds []float64
	for i := 1; i < len(path); i++ {
		if path[i][0] < path[i-1][0] {
			return ReasonTimeOrder
		}
		total += math.Hypot(path[i][1]-path[i-1][1], path[i][2]-path[i-1][2])
	}
	for i := 1; i < len(path); i++ {
		step := math.Hypot(path[i][1]-path[i-1][1], path[i][2]-path[i-1][2])
		dt := path[i][0] - path[i-1][0]
		if total > 30 && step > total*0.5 {
			return ReasonTeleport
		}
		dts = append(dts, dt)
		if dt > 0 && step > 0 {
			speeds = append(speeds, step/dt)
		}
	}
	if len(dts) >= 8 && spread(dts) < 0.05 {
		return ReasonEqualSteps
	}
	if len(speeds) >= 8 && cv(speeds) < 0.08 {
		return ReasonConstant
	}
	if input == "mouse" && deviation(path) < 0.5 && math.Hypot(last[1]-first[1], last[2]-first[2]) > 60 {
		return ReasonStraightLine
	}
	return ""
}

func spread(v []float64) float64 {
	mean := 0.0
	for _, x := range v {
		mean += x
	}
	mean /= float64(len(v))
	worst := 0.0
	for _, x := range v {
		worst = math.Max(worst, math.Abs(x-mean))
	}
	return worst
}

func cv(v []float64) float64 {
	mean, sq := 0.0, 0.0
	for _, x := range v {
		mean += x
	}
	mean /= float64(len(v))
	for _, x := range v {
		sq += (x - mean) * (x - mean)
	}
	if mean == 0 {
		return 0
	}
	return math.Sqrt(sq/float64(len(v))) / mean
}

// deviation is the largest distance of any point from the straight line
// between the first and last points.
func deviation(path [][3]float64) float64 {
	x0, y0, x1, y1 := path[0][1], path[0][2], path[len(path)-1][1], path[len(path)-1][2]
	l := math.Hypot(x1-x0, y1-y0)
	if l == 0 {
		return 0
	}
	worst := 0.0
	for _, p := range path {
		worst = math.Max(worst, math.Abs((x1-x0)*(y0-p[2])-(x0-p[1])*(y1-y0))/l)
	}
	return worst
}
