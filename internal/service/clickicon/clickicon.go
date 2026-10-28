// Package clickicon generates "click these icons in order" puzzles. The
// prompt is a strip of small upright icons; in the picture the same icons
// appear turned, scaled and recoloured among decoys, so plain template
// matching against the prompt does not find them. Every icon is drawn from
// geometry: no image assets (docs/challenge-catalog.md §3.2).
package clickicon

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"math/rand/v2"

	"github.com/dkeepersun/BotGuard/internal/service/click"
	"github.com/dkeepersun/BotGuard/internal/service/imagegen"
)

const (
	Width  = 320
	Height = 200
	// PromptCell is the size of one icon in the prompt strip.
	PromptCell = 36
)

// shape reports whether a point in icon space ([-1, 1]², y down) is inside.
type shape func(x, y float64) bool

func inPoly(pts ...[2]float64) shape {
	return func(x, y float64) bool { return imagegen.InPolygon(x, y, pts) }
}

func disc(cx, cy, r float64) shape {
	return func(x, y float64) bool { return math.Hypot(x-cx, y-cy) <= r }
}

func union(parts ...shape) shape {
	return func(x, y float64) bool {
		for _, p := range parts {
			if p(x, y) {
				return true
			}
		}
		return false
	}
}

func minus(a, b shape) shape { return func(x, y float64) bool { return a(x, y) && !b(x, y) } }

func starShape() shape {
	var pts [][2]float64
	for i := 0; i < 10; i++ {
		r := 0.95
		if i%2 == 1 {
			r = 0.4
		}
		a := -math.Pi/2 + math.Pi*float64(i)/5
		pts = append(pts, [2]float64{math.Cos(a) * r, math.Sin(a) * r})
	}
	return func(x, y float64) bool { return imagegen.InPolygon(x, y, pts) }
}

// Icons, each recognisable in silhouette. Names are for tests and logs only:
// the prompt is a picture.
var icons = []struct {
	name  string
	shape shape
}{
	{"heart", func(x, y float64) bool {
		x, y = x*1.25, -(y*1.25 - 0.15)
		return math.Pow(x*x+y*y-1, 3)-x*x*y*y*y <= 0
	}},
	{"star", starShape()},
	{"moon", minus(disc(0, 0, 0.9), disc(0.42, -0.25, 0.75))},
	{"bolt", inPoly([2]float64{0.15, -0.95}, [2]float64{-0.55, 0.1}, [2]float64{-0.05, 0.1}, [2]float64{-0.25, 0.95},
		[2]float64{0.55, -0.15}, [2]float64{0.05, -0.15}, [2]float64{0.35, -0.95})},
	{"cloud", union(disc(-0.45, 0.2, 0.4), disc(0.05, -0.12, 0.52), disc(0.5, 0.2, 0.38),
		inPoly([2]float64{-0.45, 0.2}, [2]float64{0.5, 0.2}, [2]float64{0.5, 0.58}, [2]float64{-0.45, 0.6}))},
	{"house", union(inPoly([2]float64{0, -0.95}, [2]float64{0.95, -0.05}, [2]float64{-0.95, -0.05}),
		minus(inPoly([2]float64{-0.65, -0.1}, [2]float64{0.65, -0.1}, [2]float64{0.65, 0.9}, [2]float64{-0.65, 0.9}),
			inPoly([2]float64{-0.15, 0.35}, [2]float64{0.15, 0.35}, [2]float64{0.15, 0.9}, [2]float64{-0.15, 0.9})))},
	{"drop", union(disc(0, 0.32, 0.6), inPoly([2]float64{0, -0.95}, [2]float64{0.52, 0.05}, [2]float64{-0.52, 0.05}))},
	{"arrow", inPoly([2]float64{0.95, 0}, [2]float64{0.1, -0.75}, [2]float64{0.1, -0.3}, [2]float64{-0.9, -0.3},
		[2]float64{-0.9, 0.3}, [2]float64{0.1, 0.3}, [2]float64{0.1, 0.75})},
	{"cross", union(inPoly([2]float64{-0.28, -0.9}, [2]float64{0.28, -0.9}, [2]float64{0.28, 0.9}, [2]float64{-0.28, 0.9}),
		inPoly([2]float64{-0.9, -0.28}, [2]float64{0.9, -0.28}, [2]float64{0.9, 0.28}, [2]float64{-0.9, 0.28}))},
	{"key", union(minus(disc(-0.45, 0, 0.45), disc(-0.45, 0, 0.18)),
		inPoly([2]float64{-0.05, -0.12}, [2]float64{0.95, -0.12}, [2]float64{0.95, 0.12}, [2]float64{-0.05, 0.12}),
		inPoly([2]float64{0.55, 0.1}, [2]float64{0.72, 0.1}, [2]float64{0.72, 0.45}, [2]float64{0.55, 0.45}),
		inPoly([2]float64{0.8, 0.1}, [2]float64{0.95, 0.1}, [2]float64{0.95, 0.35}, [2]float64{0.8, 0.35}))},
	{"umbrella", union(func(x, y float64) bool { return y <= 0 && math.Hypot(x, y+0.05) <= 0.95 },
		inPoly([2]float64{-0.07, -0.05}, [2]float64{0.07, -0.05}, [2]float64{0.07, 0.8}, [2]float64{-0.07, 0.8}),
		minus(disc(0.22, 0.8, 0.22), disc(0.22, 0.8, 0.09)))},
	{"flag", union(inPoly([2]float64{-0.75, -0.95}, [2]float64{-0.58, -0.95}, [2]float64{-0.58, 0.95}, [2]float64{-0.75, 0.95}),
		inPoly([2]float64{-0.58, -0.9}, [2]float64{0.9, -0.62}, [2]float64{-0.58, -0.1}))},
	{"leaf", func(x, y float64) bool {
		u, v := (x+y)/math.Sqrt2, (y-x)/math.Sqrt2
		return math.Hypot(u, v-0.62) <= 1.0 && math.Hypot(u, v+0.62) <= 1.0
	}},
	{"ring", minus(disc(0, 0, 0.92), disc(0, 0, 0.5))},
}

type placed struct {
	icon       int
	x, y, r    float64
	rot, scale float64
	color      color.RGBA
}

// draw fills icon i into img around (x, y), radius r, turned by rot, with a
// dark outline so it stands out from the busy background.
func draw(img *image.RGBA, i int, x, y, r, rot float64, c color.RGBA) {
	sin, cos := math.Sincos(-rot)
	inside := func(px, py, grow float64) bool {
		dx, dy := (px-x)/r, (py-y)/r
		u, v := dx*cos-dy*sin, dx*sin+dy*cos
		s := 1 + grow
		return icons[i].shape(u/s, v/s)
	}
	x0, y0, x1, y1 := int(x-r*1.3), int(y-r*1.3), int(x+r*1.3)+1, int(y+r*1.3)+1
	outline := color.RGBA{30, 30, 30, 255}
	imagegen.FillShape(img, x0, y0, x1, y1, outline, 0.85, func(px, py float64) bool { return inside(px, py, 2.2/r) })
	imagegen.FillShape(img, x0, y0, x1, y1, c, 1, func(px, py float64) bool { return inside(px, py, 0) })
}

func vivid(r *rand.Rand) color.RGBA {
	palette := []color.RGBA{{220, 50, 55, 255}, {45, 95, 215, 255}, {35, 150, 85, 255}, {240, 175, 0, 255},
		{135, 70, 190, 255}, {230, 110, 30, 255}, {20, 150, 160, 255}}
	return palette[r.IntN(len(palette))]
}

// Generate draws 3 targets (4 a quarter of the time) among 3–4 decoys.
func Generate(r *rand.Rand) (click.Puzzle, error) {
	n := 3
	if r.IntN(4) == 0 {
		n = 4
	}
	total := n + 3 + r.IntN(2)
	kinds := r.Perm(len(icons))[:total]
	for attempt := 0; attempt < 50; attempt++ {
		var objs []placed
		var targets []click.Target
		ok := true
		for _, k := range kinds {
			rad := 16 + r.Float64()*7
			found := false
			for try := 0; try < 200; try++ {
				x := rad + 6 + r.Float64()*(Width-2*rad-12)
				y := rad + 6 + r.Float64()*(Height-2*rad-12)
				if !click.Overlaps(targets, x, y, rad, 8) {
					targets = append(targets, click.Target{X: x, Y: y, R: rad})
					objs = append(objs, placed{icon: k, x: x, y: y, r: rad, rot: (r.Float64()*2 - 1) * 0.8, color: vivid(r)})
					found = true
					break
				}
			}
			if !found {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		bg := imagegen.Background(r, Width, Height)
		white := color.RGBA{255, 255, 255, 255}
		for y := 0; y < Height; y++ {
			for x := 0; x < Width; x++ {
				imagegen.Blend(bg, x, y, white, 0.4)
			}
		}
		imagegen.Clutter(r, bg, false)
		for _, o := range objs {
			draw(bg, o.icon, o.x, o.y, o.r, o.rot, o.color)
		}
		imagegen.Finish(r, bg)
		url, err := imagegen.JPEG(r, bg)
		if err != nil {
			return click.Puzzle{}, err
		}
		prompt, err := strip(kinds[:n])
		if err != nil {
			return click.Puzzle{}, err
		}
		return click.Puzzle{Image: url, Prompt: "请依次点击下列图标", PromptImage: prompt, Width: Width, Height: Height,
			Targets: targets[:n]}, nil
	}
	return click.Puzzle{}, fmt.Errorf("clickicon: no layout after 50 attempts")
}

// strip renders the prompt: the target icons upright, dark, left to right.
func strip(kinds []int) (string, error) {
	img := image.NewRGBA(image.Rect(0, 0, PromptCell*len(kinds), PromptCell))
	// Opaque light ground: FillShape blends colour, not alpha.
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 246, 248, 250, 255
	}
	dark := color.RGBA{40, 44, 52, 255}
	for i, k := range kinds {
		cx := float64(i*PromptCell) + PromptCell/2.0
		r := PromptCell * 0.4
		imagegen.FillShape(img, i*PromptCell, 0, (i+1)*PromptCell, PromptCell, dark, 1, func(x, y float64) bool {
			return icons[k].shape((x-cx)/r, (y-PromptCell/2.0)/r)
		})
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return "", err
	}
	return imagegen.DataURL("image/png", b.Bytes()), nil
}
