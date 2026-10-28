// Package clicktext generates "click these characters in order" puzzles.
//
// Glyphs come from a pre-rendered atlas (assets/glyphs.png, made by
// tools/glyphatlas from Noto Sans SC under the SIL OFL 1.1), so the gateway
// needs no font at run time. Each character is scaled, rotated, sheared and
// coloured independently, and decoy characters are mixed in.
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

type glyph struct {
	idx         int
	x, y, size  float64
	rot, shear  float64
	color       color.RGBA
	outlineOpac float64
}

// draw renders the glyph by inverse-mapping each destination pixel into the
// atlas cell: rotate, shear, then scale.
func draw(img *image.RGBA, a *image.Gray, g glyph) {
	scale := float64(Cell) / g.size
	sin, cos := math.Sincos(-g.rot)
	half := g.size * 0.8
	x0, x1 := max(0, int(g.x-half)), min(Width, int(g.x+half)+1)
	y0, y1 := max(0, int(g.y-half)), min(Height, int(g.y+half)+1)
	light := color.RGBA{255, 255, 255, 255}
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			dx, dy := float64(x)+0.5-g.x, float64(y)+0.5-g.y
			rx, ry := dx*cos-dy*sin, dx*sin+dy*cos
			rx -= ry * g.shear
			u, v := rx*scale+Cell/2, ry*scale+Cell/2
			cov := sample(a, g.idx, u, v)
			// A soft light halo keeps dark glyphs readable on busy backgrounds.
			halo := math.Max(math.Max(sample(a, g.idx, u-3, v), sample(a, g.idx, u+3, v)),
				math.Max(sample(a, g.idx, u, v-3), sample(a, g.idx, u, v+3)))
			if halo > cov {
				imagegen.Blend(img, x, y, light, (halo-cov)*g.outlineOpac)
			}
			if cov > 0 {
				imagegen.Blend(img, x, y, g.color, cov)
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

// Generate draws 3 targets (4 a quarter of the time) and 1–2 decoys.
func Generate(r *rand.Rand, src imagegen.Source) (click.Puzzle, error) {
	n := 3
	if r.IntN(4) == 0 {
		n = 4
	}
	total := n + 1 + r.IntN(2)
	perm := r.Perm(len(runes))[:total]
	return compose(r, src, perm, n, func(words []string) string { return "请依次点击：" + strings.Join(words, "、") })
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
	return compose(r, src, glyphs, len(idiom), func([]string) string { return "请按成语的正确顺序，依次点击图中的 4 个字" })
}

// compose places and draws glyphs; the first n are the targets, in order.
func compose(r *rand.Rand, src imagegen.Source, perm []int, n int, prompt func(words []string) string) (click.Puzzle, error) {
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
				glyphs = append(glyphs, glyph{idx: perm[i], x: x, y: y, size: size,
					rot:   (r.Float64()*70 - 35) * math.Pi / 180,
					shear: r.Float64()*0.3 - 0.15, color: darkColor(r), outlineOpac: 0.5 + r.Float64()*0.3})
				ok = true
				break
			}
		}
		if !ok {
			return click.Puzzle{}, fmt.Errorf("clicktext: could not place %d glyphs", total)
		}
	}

	bg, _ := src.Background(r, Width, Height)
	white := color.RGBA{255, 255, 255, 255}
	for y := 0; y < Height; y++ {
		for x := 0; x < Width; x++ {
			imagegen.Blend(bg, x, y, white, 0.35)
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
	return click.Puzzle{Image: url, Prompt: prompt(words), Width: Width, Height: Height, Targets: targets}, nil
}

func segmentDistance(px, py, x0, y0, x1, y1 float64) float64 {
	dx, dy := x1-x0, y1-y0
	t := ((px-x0)*dx + (py-y0)*dy) / (dx*dx + dy*dy)
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(px-(x0+t*dx), py-(y0+t*dy))
}
