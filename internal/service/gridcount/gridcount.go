// Package gridcount generates counting grids: "select every cell with
// exactly 4 triangles". Counting is a weak spot of multimodal models (no
// model in Open CaptchaWorld solved its dice-counting task, and COGNITION
// lists counting among the hardest), and the 3×3 multi-select answer leaves
// a blind guess about one chance in 511 (docs/challenge-catalog.md §7.2).
//
// Every cell holds a few small shapes of mixed kinds and random colours.
// Cells that are not part of the answer prefer counts one off the asked
// number, so a rough estimate is not enough.
package gridcount

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"math/rand/v2"

	"github.com/dkeepersun/BotGuard/internal/service/click"
	"github.com/dkeepersun/BotGuard/internal/service/grid"
	"github.com/dkeepersun/BotGuard/internal/service/imagegen"
	"github.com/dkeepersun/BotGuard/internal/service/prompt"
)

const (
	Width  = 320
	Height = 200
	Rows   = 3
	Cols   = 3
	// maxItems is the most shapes one cell holds; with radius ≤ 10 and the
	// gaps between them, more would crowd a 106×66 cell.
	maxItems = 7
)

// shapes are the kinds a prompt can count; each stays recognisable when
// small and turned (squares are only tilted a little, or they would pass for
// diamonds).
// names translates the shape names (Traditional Chinese, English singular).
var names = map[string]struct{ tw, en string }{
	"圆形": {"圓形", "circle"}, "三角形": {"三角形", "triangle"}, "正方形": {"正方形", "square"},
	"五角星": {"五角星", "star"}, "十字": {"十字", "cross"},
}

var shapes = []struct {
	name   string                  // Simplified Chinese; names has the others
	inside func(x, y float64) bool // unit coordinates, centred
	spin   float64                 // largest rotation, radians
}{
	{"圆形", func(x, y float64) bool { return x*x+y*y <= 1 }, 0},
	{"三角形", func(x, y float64) bool {
		return imagegen.InPolygon(x, y, [][2]float64{{0, -1}, {0.95, 0.72}, {-0.95, 0.72}})
	}, math.Pi},
	{"正方形", func(x, y float64) bool { return math.Abs(x) <= 0.8 && math.Abs(y) <= 0.8 }, 0.25},
	{"五角星", star, math.Pi},
	{"十字", func(x, y float64) bool {
		return math.Abs(x) <= 0.32 && math.Abs(y) <= 1 || math.Abs(y) <= 0.32 && math.Abs(x) <= 1
	}, math.Pi / 4},
}

func star(x, y float64) bool {
	var pts [][2]float64
	for i := 0; i < 10; i++ {
		rr := 1.0
		if i%2 == 1 {
			rr = 0.45
		}
		a := -math.Pi/2 + math.Pi*float64(i)/5
		pts = append(pts, [2]float64{math.Cos(a) * rr, math.Sin(a) * rr})
	}
	return imagegen.InPolygon(x, y, pts)
}

type item struct {
	shape   int
	x, y, r float64
	rot     float64
	fill    color.RGBA
}

// counts plans how many target shapes each cell holds: k cells with n, the
// rest preferring n±1.
func counts(r *rand.Rand, n, k int) []int {
	out := make([]int, Rows*Cols)
	cells := r.Perm(Rows * Cols)
	for i, c := range cells {
		if i < k {
			out[c] = n
			continue
		}
		var near []int
		for _, v := range []int{n - 1, n + 1} {
			if v >= 0 && v <= 6 {
				near = append(near, v)
			}
		}
		if r.IntN(3) > 0 {
			out[c] = near[r.IntN(len(near))]
			continue
		}
		for {
			v := r.IntN(7)
			if v != n {
				out[c] = v
				break
			}
		}
	}
	return out
}

// fill places want shapes of the target kind and some others in one cell
// without overlap; false when the cell is too crowded.
func fill(r *rand.Rand, cell int, target, want int) ([]item, bool) {
	x0, y0, x1, y1 := grid.Cell(Width, Height, Rows, Cols, cell)
	others := min(maxItems-want, 1+r.IntN(3))
	if want == 0 {
		others = 2 + r.IntN(3)
	}
	var placed []click.Target
	var items []item
	for i := 0; i < want+others; i++ {
		kind := target
		if i >= want {
			kind = r.IntN(len(shapes) - 1)
			if kind >= target {
				kind++
			}
		}
		rad := 7.5 + r.Float64()*2.5
		found := false
		for try := 0; try < 200; try++ {
			x := x0 + 3 + rad + r.Float64()*(x1-x0-6-2*rad)
			y := y0 + 3 + rad + r.Float64()*(y1-y0-6-2*rad)
			if !click.Overlaps(placed, x, y, rad, 3) {
				placed = append(placed, click.Target{X: x, Y: y, R: rad})
				items = append(items, item{shape: kind, x: x, y: y, r: rad, rot: (r.Float64()*2 - 1) * shapes[kind].spin,
					fill: imagegen.HSV(r.Float64()*360, 0.55+r.Float64()*0.35, 0.55+r.Float64()*0.35)})
				found = true
				break
			}
		}
		if !found {
			return nil, false
		}
	}
	return items, true
}

// Generate builds a puzzle from r. Production callers seed r from crypto/rand.
func Generate(r *rand.Rand, src imagegen.Source) (grid.Puzzle, error) {
	p, _, err := generate(r, src)
	return p, err
}

// generate also returns each cell's items, for tests.
func generate(r *rand.Rand, src imagegen.Source) (grid.Puzzle, [][]item, error) {
	for attempt := 0; attempt < 50; attempt++ {
		target := r.IntN(len(shapes))
		n := 2 + r.IntN(4)
		k := 2 + r.IntN(3)
		plan := counts(r, n, k)
		cells := make([][]item, len(plan))
		ok := true
		for c, want := range plan {
			if cells[c], ok = fill(r, c, target, want); !ok {
				break
			}
		}
		if !ok {
			continue
		}
		var answer []int
		for c, want := range plan {
			if want == n {
				answer = append(answer, c)
			}
		}

		bg, _ := src.Background(r, Width, Height)
		white := color.RGBA{255, 255, 255, 255}
		for y := 0; y < Height; y++ {
			for x := 0; x < Width; x++ {
				imagegen.Blend(bg, x, y, white, 0.42)
			}
		}
		imagegen.Clutter(r, bg, false)
		for _, cell := range cells {
			for _, it := range cell {
				draw(bg, it)
			}
		}
		grid.Divide(bg, Rows, Cols)
		imagegen.Finish(r, bg)
		url, err := imagegen.JPEG(r, bg)
		if err != nil {
			return grid.Puzzle{}, nil, err
		}
		name := names[shapes[target].name]
		plural := name.en + "s"
		if n == 1 {
			plural = name.en
		}
		t := prompt.Text{
			prompt.ZhCN: fmt.Sprintf("请选出所有恰好有 %d 个%s的格子", n, shapes[target].name),
			prompt.ZhTW: fmt.Sprintf("請選出所有恰好有 %d 個%s的格子", n, name.tw),
			prompt.En:   fmt.Sprintf("Select every cell with exactly %d %s", n, plural),
		}
		return grid.Puzzle{Image: url, Prompt: t[prompt.ZhCN], Prompts: t,
			Width: Width, Height: Height, Rows: Rows, Cols: Cols, Answer: answer}, cells, nil
	}
	return grid.Puzzle{}, nil, fmt.Errorf("gridcount: no layout after 50 attempts")
}

func draw(img *image.RGBA, it item) {
	sin, cos := math.Sincos(it.rot)
	unit := func(scale float64) func(x, y float64) bool {
		return func(x, y float64) bool {
			dx, dy := (x-it.x)/(it.r*scale), (y-it.y)/(it.r*scale)
			return shapes[it.shape].inside(dx*cos+dy*sin, -dx*sin+dy*cos)
		}
	}
	x0, y0, x1, y1 := int(it.x-it.r-3), int(it.y-it.r-3), int(it.x+it.r+4), int(it.y+it.r+4)
	// A dark outline on every shape keeps neighbours apart for counting.
	imagegen.FillShape(img, x0, y0, x1, y1, color.RGBA{30, 30, 34, 255}, 0.85, unit(1+1.6/it.r))
	imagegen.FillShape(img, x0, y0, x1, y1, it.fill, 1, unit(1))
}
