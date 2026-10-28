// Package clickshape generates "click the shapes in order" puzzles that mix
// precise localisation with counting: dots on dice, sides of polygons. Those
// are the abilities multimodal LLMs are weakest at (COGNITION, 2025), so
// near-miss decoys are deliberate: 4 versus 5 dots, pentagon versus hexagon.
// Everything is drawn procedurally; there are no image assets.
package clickshape

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"math/rand/v2"
	"strings"

	"github.com/dkeepersun/BotGuard/internal/service/click"
	"github.com/dkeepersun/BotGuard/internal/service/imagegen"
)

const (
	Width  = 320
	Height = 200
)

type kind int

const (
	triangle kind = iota
	pentagon
	hexagon
	circle
	star
	dice
)

var kindNames = map[kind]string{triangle: "三角形", pentagon: "五边形", hexagon: "六边形", circle: "圆", star: "五角星"}

type namedColor struct {
	name string
	c    color.RGBA
}

// Five colours people can name without hesitation.
var palette = []namedColor{
	{"红色", color.RGBA{220, 50, 55, 255}},
	{"蓝色", color.RGBA{45, 95, 215, 255}},
	{"绿色", color.RGBA{35, 150, 85, 255}},
	{"黄色", color.RGBA{240, 190, 0, 255}},
	{"紫色", color.RGBA{135, 70, 190, 255}},
}

type object struct {
	kind  kind
	dots  int // dice only
	color namedColor
	big   bool
	x, y  float64
	r     float64
	rot   float64
}

func (o object) desc() []string {
	if o.kind == dice {
		return []string{fmt.Sprintf("有 %d 个点的方块", o.dots)}
	}
	size := "小"
	if o.big {
		size = "大"
	}
	return []string{kindNames[o.kind], o.color.name + "的" + kindNames[o.kind], size + "的" + kindNames[o.kind]}
}

// unique returns the descriptions of objs[i] that match no other object.
func unique(objs []object, i int) []string {
	var out []string
	for _, d := range objs[i].desc() {
		clash := false
		for j, o := range objs {
			if j == i {
				continue
			}
			for _, e := range o.desc() {
				if e == d {
					clash = true
				}
			}
		}
		if !clash {
			out = append(out, d)
		}
	}
	return out
}

func pick[T any](r *rand.Rand, s []T) T { return s[r.IntN(len(s))] }

func plan(r *rand.Rand) []object {
	var kinds []kind
	// Near-miss pairs force counting: two dice one dot apart, and two
	// polygons one side apart.
	d := 1 + r.IntN(5)
	kinds = append(kinds, dice, dice)
	if r.IntN(2) == 0 {
		kinds = append(kinds, pentagon, hexagon)
	} else {
		kinds = append(kinds, triangle, pick(r, []kind{pentagon, hexagon}))
	}
	for n := 3 + r.IntN(3); n > 0; n-- {
		kinds = append(kinds, pick(r, []kind{triangle, pentagon, hexagon, circle, star, dice}))
	}
	objs := make([]object, len(kinds))
	for i, k := range kinds {
		objs[i] = object{kind: k, color: pick(r, palette), big: r.IntN(2) == 0, rot: r.Float64() * 2 * math.Pi}
		if k == dice {
			objs[i].dots = 1 + r.IntN(6)
		}
	}
	objs[0].dots, objs[1].dots = d, d+1
	return objs
}

// place assigns positions without overlap; false when the layout is too tight.
func place(r *rand.Rand, objs []object) bool {
	var placed []click.Target
	for i := range objs {
		radius := 13 + r.Float64()*3
		if objs[i].big {
			radius = 21 + r.Float64()*4
		}
		if objs[i].kind == dice {
			// Dice carry no size description, and their pips must stay
			// countable on a phone: always draw them large.
			radius = 20 + r.Float64()*4
		}
		ok := false
		for try := 0; try < 200; try++ {
			x := radius + 6 + r.Float64()*(Width-2*radius-12)
			y := radius + 6 + r.Float64()*(Height-2*radius-12)
			if !click.Overlaps(placed, x, y, radius, 6) {
				objs[i].x, objs[i].y, objs[i].r = x, y, radius
				placed = append(placed, click.Target{X: x, Y: y, R: radius})
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func regular(o object, n int, scale float64) [][2]float64 {
	pts := make([][2]float64, n)
	for i := range pts {
		a := o.rot + 2*math.Pi*float64(i)/float64(n)
		pts[i] = [2]float64{o.x + math.Cos(a)*o.r*scale, o.y + math.Sin(a)*o.r*scale}
	}
	return pts
}

func starPoints(o object, scale float64) [][2]float64 {
	pts := make([][2]float64, 10)
	for i := range pts {
		rad := o.r * scale
		if i%2 == 1 {
			rad *= 0.45
		}
		a := o.rot + math.Pi*float64(i)/5
		pts[i] = [2]float64{o.x + math.Cos(a)*rad, o.y + math.Sin(a)*rad}
	}
	return pts
}

// Standard dice pip layouts on a [-1, 1] square.
var pips = map[int][][2]float64{
	1: {{0, 0}},
	2: {{-0.5, -0.5}, {0.5, 0.5}},
	3: {{-0.5, -0.5}, {0, 0}, {0.5, 0.5}},
	4: {{-0.5, -0.5}, {0.5, -0.5}, {-0.5, 0.5}, {0.5, 0.5}},
	5: {{-0.5, -0.5}, {0.5, -0.5}, {0, 0}, {-0.5, 0.5}, {0.5, 0.5}},
	6: {{-0.5, -0.55}, {0.5, -0.55}, {-0.5, 0}, {0.5, 0}, {-0.5, 0.55}, {0.5, 0.55}},
}

func draw(img *image.RGBA, o object) {
	outline := color.RGBA{30, 30, 30, 255}
	box := func(scale float64) (int, int, int, int) {
		e := o.r*scale + 2
		return int(o.x - e), int(o.y - e), int(o.x+e) + 1, int(o.y+e) + 1
	}
	poly := func(pts func(float64) [][2]float64) {
		outer, inner := pts(1+2.5/o.r), pts(1)
		x0, y0, x1, y1 := box(1.2)
		imagegen.FillShape(img, x0, y0, x1, y1, outline, 0.9, func(x, y float64) bool { return imagegen.InPolygon(x, y, outer) })
		imagegen.FillShape(img, x0, y0, x1, y1, o.color.c, 1, func(x, y float64) bool { return imagegen.InPolygon(x, y, inner) })
	}
	switch o.kind {
	case triangle:
		poly(func(s float64) [][2]float64 { return regular(o, 3, s) })
	case pentagon:
		poly(func(s float64) [][2]float64 { return regular(o, 5, s) })
	case hexagon:
		poly(func(s float64) [][2]float64 { return regular(o, 6, s) })
	case star:
		poly(func(s float64) [][2]float64 { return starPoints(o, s) })
	case circle:
		x0, y0, x1, y1 := box(1.2)
		imagegen.FillShape(img, x0, y0, x1, y1, outline, 0.9, func(x, y float64) bool { return math.Hypot(x-o.x, y-o.y) <= o.r+2.5 })
		imagegen.FillShape(img, x0, y0, x1, y1, o.color.c, 1, func(x, y float64) bool { return math.Hypot(x-o.x, y-o.y) <= o.r })
	case dice:
		// A square inscribed in the bounding circle, with white pips.
		sq := o
		sq.rot = o.rot + math.Pi/4
		poly(func(s float64) [][2]float64 { return regular(sq, 4, s) })
		half := o.r / math.Sqrt2
		sin, cos := math.Sincos(o.rot)
		pipR := math.Max(3, half*0.22)
		for _, p := range pips[o.dots] {
			px, py := p[0]*half, p[1]*half
			cx, cy := o.x+px*cos-py*sin, o.y+px*sin+py*cos
			imagegen.FillShape(img, int(cx-pipR-1), int(cy-pipR-1), int(cx+pipR)+2, int(cy+pipR)+2, color.RGBA{255, 255, 255, 255}, 1,
				func(x, y float64) bool { return math.Hypot(x-cx, y-cy) <= pipR })
		}
	}
}

// Generate draws a puzzle with 3 targets (4 a quarter of the time).
func Generate(r *rand.Rand) (click.Puzzle, error) {
	for attempt := 0; attempt < 50; attempt++ {
		objs := plan(r)
		if !place(r, objs) {
			continue
		}
		var candidates []int
		descs := map[int][]string{}
		for i := range objs {
			if u := unique(objs, i); len(u) > 0 {
				candidates = append(candidates, i)
				descs[i] = u
			}
		}
		n := 3
		if r.IntN(4) == 0 {
			n = 4
		}
		if len(candidates) < n {
			continue
		}
		r.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })
		var targets []click.Target
		var words []string
		for _, i := range candidates[:n] {
			o := objs[i]
			targets = append(targets, click.Target{X: o.x, Y: o.y, R: o.r})
			// Prefer the shortest description: a bare count ("五边形",
			// "有 4 个点的方块") makes the user, and a model, count.
			d := descs[i][0]
			words = append(words, d)
		}

		bg := imagegen.Background(r, Width, Height)
		// Wash the background towards white so saturated shapes stand out.
		white := color.RGBA{255, 255, 255, 255}
		for y := 0; y < Height; y++ {
			for x := 0; x < Width; x++ {
				imagegen.Blend(bg, x, y, white, 0.45)
			}
		}
		imagegen.Clutter(r, bg, false)
		for _, o := range objs {
			draw(bg, o)
		}
		imagegen.Finish(r, bg)
		url, err := imagegen.JPEG(r, bg)
		if err != nil {
			return click.Puzzle{}, err
		}
		return click.Puzzle{Image: url, Prompt: "请依次点击：" + strings.Join(words, "、"), Width: Width, Height: Height, Targets: targets}, nil
	}
	return click.Puzzle{}, fmt.Errorf("clickshape: no valid layout after 50 attempts")
}
