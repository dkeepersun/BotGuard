// Package clicktext generates "click these characters in order" puzzles.
//
// Glyphs come from a pre-rendered atlas (assets/glyphs.png, made by
// tools/glyphatlas from Noto Sans SC under the SIL OFL 1.1), so the gateway
// needs no font at run time. Each character is scaled, rotated, sheared and
// coloured independently, drawn in one of three styles (dark with a halo,
// 3D extruded, hollow) and sometimes broken by thin cuts, and decoy
// characters are mixed in.
//
// This style is familiar to users and hard for general AI agents to localise
// precisely, but dedicated text-click solvers exist; treat it as one option
// among several, not as a barrier on its own.
package clicktext

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"

	"github.com/dkeepersun/BotGuard/internal/service/click"
	"github.com/dkeepersun/BotGuard/internal/service/imagegen"
	"github.com/dkeepersun/BotGuard/internal/service/prompt"
)

const (
	Width   = 320
	Height  = 200
	Cell    = 64 // atlas cell size in pixels
	Columns = 24 // atlas cells per row
)

//go:embed assets/glyphs.png
var atlasPNG []byte

var (
	atlasOnce sync.Once
	atlas     *image.Gray
	atlasErr  error
	runes     = []rune(Chars)
)

func loadAtlas() (*image.Gray, error) {
	atlasOnce.Do(func() {
		img, err := png.Decode(bytes.NewReader(atlasPNG))
		if err != nil {
			atlasErr = err
			return
		}
		g, ok := img.(*image.Gray)
		if !ok {
			atlasErr = fmt.Errorf("clicktext: atlas is %T, want *image.Gray", img)
			return
		}
		if rows := (len(runes) + Columns - 1) / Columns; g.Bounds().Dx() != Columns*Cell || g.Bounds().Dy() != rows*Cell {
			atlasErr = fmt.Errorf("clicktext: atlas size %v does not match %d chars; regenerate with tools/glyphatlas", g.Bounds(), len(runes))
			return
		}
		atlas = g
	})
	return atlas, atlasErr
}

// sample returns the glyph coverage at (u, v) in cell pixels, bilinear.
func sample(a *image.Gray, idx int, u, v float64) float64 {
	if u < 0 || v < 0 || u >= Cell-1 || v >= Cell-1 {
		return 0
	}
	ox, oy := (idx%Columns)*Cell, (idx/Columns)*Cell
	x0, y0 := int(u), int(v)
	fx, fy := u-float64(x0), v-float64(y0)
	at := func(x, y int) float64 { return float64(a.Pix[a.PixOffset(ox+x, oy+y)]) / 255 }
	top := at(x0, y0)*(1-fx) + at(x0+1, y0)*fx
	bottom := at(x0, y0+1)*(1-fx) + at(x0+1, y0+1)*fx
	return top*(1-fy) + bottom*fy
}

// Glyph styles. Each character gets one at random, so no single colour
// or brightness threshold separates all of them from the background.
const (
	stylePlain   = iota // dark fill, light halo
	styleExtrude        // light or vivid face over a darker 3D extrusion
	styleHollow         // light fill inside a dark outline
	styleCount
)

type glyph struct {
	idx        int
	x, y, size float64
	rot, shear float64
	style      int
	face, edge color.RGBA // fill, and halo / extrusion / outline
	haloOpac   float64
	ex, ey     float64 // extrusion step in image pixels
	depth      int
	cuts       [][3]float64 // breaks: unit normal and offset, in glyph pixels
}

// local maps image point (x, y) into the glyph's atlas cell, and returns the
// glyph-space point before scaling (for cuts).
func (g glyph) local(x, y float64) (u, v, rx, ry float64) {
	sin, cos := math.Sincos(-g.rot)
	dx, dy := x-g.x, y-g.y
	rx, ry = dx*cos-dy*sin, dx*sin+dy*cos
	rx -= ry * g.shear
	scale := float64(Cell) / g.size
	return rx*scale + Cell/2, ry*scale + Cell/2, rx, ry
}

// coverage is the glyph's ink at image point (x, y), with its breaks cut
// out: thin gaps across a stroke that a reader bridges without noticing.
func (g glyph) coverage(a *image.Gray, x, y float64) float64 {
	u, v, rx, ry := g.local(x, y)
	c := sample(a, g.idx, u, v)
	for _, cut := range g.cuts {
		if math.Abs(rx*cut[0]+ry*cut[1]-cut[2]) < 0.9 {
			c *= 0.12
		}
	}
	return c
}

// draw renders the glyph by inverse-mapping each destination pixel into the
// atlas cell: rotate, shear, then scale.
func draw(img *image.RGBA, a *image.Gray, g glyph) {
	half := g.size*0.8 + float64(g.depth)*math.Hypot(g.ex, g.ey)
	x0, x1 := max(0, int(g.x-half)), min(Width, int(g.x+half)+1)
	y0, y1 := max(0, int(g.y-half)), min(Height, int(g.y+half)+1)
	light := color.RGBA{255, 255, 255, 255}
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			cov := g.coverage(a, px, py)
			switch g.style {
			case styleExtrude:
				// Nearer layers of the extrusion are darker, as if lit from
				// the front.
				for k := g.depth; k >= 1; k-- {
					if side := g.coverage(a, px-float64(k)*g.ex, py-float64(k)*g.ey); side > 0 {
						shade := 1 - 0.5*float64(k)/float64(g.depth)
						c := color.RGBA{uint8(float64(g.edge.R) * shade), uint8(float64(g.edge.G) * shade), uint8(float64(g.edge.B) * shade), 255}
						imagegen.Blend(img, x, y, c, side)
						break
					}
				}
			default:
				u, v, _, _ := g.local(px, py)
				d := 3.0
				if g.style == styleHollow {
					d = 1.6
				}
				halo := math.Max(math.Max(sample(a, g.idx, u-d, v), sample(a, g.idx, u+d, v)),
					math.Max(sample(a, g.idx, u, v-d), sample(a, g.idx, u, v+d)))
				if halo > cov {
					if g.style == styleHollow {
						imagegen.Blend(img, x, y, g.edge, (halo-cov)*0.95)
					} else {
						imagegen.Blend(img, x, y, light, (halo-cov)*g.haloOpac)
					}
				}
			}
			if cov > 0 {
				imagegen.Blend(img, x, y, g.face, cov)
			}
		}
	}
}

func darkColor(r *rand.Rand) color.RGBA {
	for {
		c := imagegen.RandomColor(r)
		c.R, c.G, c.B = c.R/2, c.G/2, c.B/2
		if imagegen.Luma(c) < 90 {
			return c
		}
	}
}

// styled fills in a glyph's style, colours and breaks.
func styled(r *rand.Rand, g glyph) glyph {
	g.style = r.IntN(styleCount)
	switch g.style {
	case styleExtrude:
		// A light or vivid face; the extrusion is a dark version of a hue.
		if r.IntN(2) == 0 {
			g.face = imagegen.HSV(r.Float64()*360, 0.05+r.Float64()*0.15, 0.95)
		} else {
			g.face = imagegen.HSV(r.Float64()*360, 0.6+r.Float64()*0.3, 0.8+r.Float64()*0.2)
		}
		g.edge = imagegen.HSV(r.Float64()*360, 0.3+r.Float64()*0.4, 0.25+r.Float64()*0.2)
		g.depth = 3 + r.IntN(3)
		dir := r.Float64() * 2 * math.Pi
		g.ex, g.ey = math.Cos(dir)*0.9, math.Sin(dir)*0.9
	case styleHollow:
		g.face = imagegen.HSV(r.Float64()*360, 0.1+r.Float64()*0.3, 0.92+r.Float64()*0.08)
		g.edge = darkColor(r)
	default:
		g.face = darkColor(r)
		g.haloOpac = 0.5 + r.Float64()*0.3
	}
	if r.IntN(5) < 2 {
		for n := 1 + r.IntN(2); n > 0; n-- {
			a := r.Float64() * math.Pi
			g.cuts = append(g.cuts, [3]float64{math.Cos(a), math.Sin(a), (r.Float64()*2 - 1) * g.size * 0.25})
		}
	}
	return g
}

// Generate draws 3 targets (4 a quarter of the time) and 1–2 decoys.
func Generate(r *rand.Rand, src imagegen.Source) (click.Puzzle, error) {
	n := 3
	if r.IntN(4) == 0 {
		n = 4
	}
	total := n + 1 + r.IntN(2)
	perm := r.Perm(len(runes))[:total]
	return compose(r, src, perm, n, func(words []string) prompt.Text {
		return prompt.Text{
			prompt.ZhCN: "请依次点击：" + prompt.Join(prompt.ZhCN, words),
			prompt.ZhTW: "請依次點擊：" + prompt.Join(prompt.ZhTW, words),
			prompt.En:   "Click in order: " + prompt.Join(prompt.En, words),
		}
	})
}

// GenerateOrder draws a four-character idiom, shuffled, with 1–2 decoy
// characters. The prompt does not give the order: the user has to know the
// idiom, then find its characters.
func GenerateOrder(r *rand.Rand, src imagegen.Source) (click.Puzzle, error) {
	idiom := []rune(Idioms[r.IntN(len(Idioms))])
	index := make(map[rune]int, len(runes))
	for i, c := range runes {
		index[c] = i
	}
	glyphs := make([]int, 0, 6)
	for _, c := range idiom {
		i, ok := index[c]
		if !ok {
			return click.Puzzle{}, fmt.Errorf("clicktext: idiom character %q missing from the atlas", c)
		}
		glyphs = append(glyphs, i)
	}
	for decoys := 1 + r.IntN(2); decoys > 0; {
		i := r.IntN(len(runes))
		if !strings.ContainsRune(string(idiom), runes[i]) && !slices.Contains(glyphs, i) {
			glyphs = append(glyphs, i)
			decoys--
		}
	}
	return compose(r, src, glyphs, len(idiom), func([]string) prompt.Text {
		return prompt.Text{
			prompt.ZhCN: "请按成语的正确顺序，依次点击图中的 4 个字",
			prompt.ZhTW: "請按成語的正確順序，依次點擊圖中的 4 個字",
			prompt.En:   "Click the 4 characters in the order of the Chinese idiom they form",
		}
	})
}

// compose places and draws glyphs; the first n are the targets, in order.
func compose(r *rand.Rand, src imagegen.Source, perm []int, n int, text func(words []string) prompt.Text) (click.Puzzle, error) {
	a, err := loadAtlas()
	if err != nil {
		return click.Puzzle{}, err
	}
	total := len(perm)

	var placed []click.Target
	glyphs := make([]glyph, 0, total)
	for i := 0; i < total; i++ {
		size := 26 + r.Float64()*8
		radius := size * 0.6
		ok := false
		for try := 0; try < 300; try++ {
			x := radius + 4 + r.Float64()*(Width-2*radius-8)
			y := radius + 4 + r.Float64()*(Height-2*radius-8)
			if !click.Overlaps(placed, x, y, radius, 6) {
				placed = append(placed, click.Target{X: x, Y: y, R: radius})
				glyphs = append(glyphs, styled(r, glyph{idx: perm[i], x: x, y: y, size: size,
					rot:   (r.Float64()*70 - 35) * math.Pi / 180,
					shear: r.Float64()*0.3 - 0.15}))
				ok = true
				break
			}
		}
		if !ok {
			return click.Puzzle{}, fmt.Errorf("clicktext: could not place %d glyphs", total)
		}
	}

	bg, photo := src.Background(r, Width, Height)
	// Washed lighter so dark glyphs read; a photo less, since every style
	// carries its own contrast (halo, extrusion or outline).
	wash := 0.35
	if photo {
		wash = 0.2
	}
	white := color.RGBA{255, 255, 255, 255}
	for y := 0; y < Height; y++ {
		for x := 0; x < Width; x++ {
			imagegen.Blend(bg, x, y, white, wash)
		}
	}
	imagegen.Clutter(r, bg, true)
	// Interference lines under the glyphs.
	for k := 2 + r.IntN(3); k > 0; k-- {
		c := imagegen.RandomColor(r)
		x0, y0 := r.Float64()*Width, r.Float64()*Height
		x1, y1 := r.Float64()*Width, r.Float64()*Height
		w := 1 + r.Float64()*1.5
		imagegen.FillShape(bg, 0, 0, Width, Height, c, 0.55, func(x, y float64) bool {
			return segmentDistance(x, y, x0, y0, x1, y1) <= w
		})
	}
	for _, g := range glyphs {
		draw(bg, a, g)
	}
	imagegen.Finish(r, bg)
	url, err := imagegen.JPEG(r, bg)
	if err != nil {
		return click.Puzzle{}, err
	}

	targets := make([]click.Target, n)
	words := make([]string, n)
	for i := 0; i < n; i++ {
		g := glyphs[i]
		targets[i] = click.Target{X: g.x, Y: g.y, R: g.size * 0.55}
		words[i] = string(runes[g.idx])
	}
	t := text(words)
	return click.Puzzle{Image: url, Prompt: t[prompt.ZhCN], Prompts: t, Width: Width, Height: Height, Targets: targets}, nil
}

func segmentDistance(px, py, x0, y0, x1, y1 float64) float64 {
	dx, dy := x1-x0, y1-y0
	t := ((px-x0)*dx + (py-y0)*dy) / (dx*dx + dy*dy)
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(px-(x0+t*dx), py-(y0+t*dy))
}
