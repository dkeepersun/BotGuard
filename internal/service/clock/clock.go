// Package clock generates analog-clock puzzles: "click the clock showing
// 3:40, then the one showing 8:15". Reading hands is where multimodal models
// fail (ClockBench, 2025: people 89%, the best model 13%), while the
// arithmetic around it is easy for them, so the puzzle asks for reading and
// nothing else (docs/challenge-catalog.md §7.1).
//
// Four or five clocks of varied face styles share the picture. Decoys show
// the times models confuse a target with: hour and minute hands swapped, the
// face mirrored, a few minutes off. Times are whole multiples of five
// minutes, hands are never close to overlapping, and the hour hand is much
// shorter and thicker than the minute hand, so a person reads every clock
// unambiguously.
package clock

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"math/rand/v2"

	"github.com/dkeepersun/BotGuard/internal/service/click"
	"github.com/dkeepersun/BotGuard/internal/service/imagegen"
	"github.com/dkeepersun/BotGuard/internal/service/prompt"
)

const (
	Width  = 320
	Height = 200
	// Targets is the number of clocks to click.
	Targets = 2
	// minHandGap keeps the hands visibly apart, in degrees.
	minHandGap = 24.0
)

// Time is minutes past 12:00, in [0, 720), a multiple of 5.
type Time int

func (t Time) String() string {
	h := int(t) / 60
	if h == 0 {
		h = 12
	}
	return fmt.Sprintf("%d:%02d", h, int(t)%60)
}

// hands returns the hour and minute hand angles in degrees, clockwise from 12.
func (t Time) hands() (hour, minute float64) {
	return float64(t) / 2, float64(int(t)%60) * 6
}

// readable reports whether the hands are far enough apart to tell.
func (t Time) readable() bool {
	h, m := t.hands()
	d := math.Mod(math.Abs(h-m), 360)
	return math.Min(d, 360-d) >= minHandGap
}

func round5(minutes float64) Time {
	return Time((int(math.Round(minutes/5))*5%720 + 720) % 720)
}

// fromHands is the time whose hour hand points at hour° and minute hand at
// minute°, rounded to five minutes; hour picks the hour, minute the minutes.
func fromHands(hour, minute float64) Time {
	h := int(math.Floor(math.Mod(hour+360, 360)/30)) % 12
	m := int(math.Round(math.Mod(minute+360, 360)/30)) * 5 % 60
	return Time(h*60 + m)
}

// swapped reads t with the hands taken for each other.
func (t Time) swapped() Time { h, m := t.hands(); return fromHands(m, h) }

// mirrored is t seen in a mirror (left and right exchanged).
func (t Time) mirrored() Time { h, m := t.hands(); return fromHands(360-h, 360-m) }

// apart is the distance between two times in minutes, around the dial.
func apart(a, b Time) int {
	d := int(a) - int(b)
	if d < 0 {
		d = -d
	}
	return min(d, 720-d)
}

// decoys proposes times a careless reader confuses with the targets.
func decoys(r *rand.Rand, targets []Time) []Time {
	var out []Time
	for _, t := range targets {
		off := []int{-10, -5, 5, 10}
		out = append(out, t.swapped(), t.mirrored(), round5(float64(int(t)+off[r.IntN(len(off))])))
	}
	r.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// pickTimes chooses the targets and decoys: all readable and pairwise
// distinct, decoys preferring the confusable times.
func pickTimes(r *rand.Rand, n int) ([]Time, bool) {
	var times []Time
	// gap is the least distance, in minutes, to every time already chosen.
	ok := func(t Time, gap int) bool {
		if !t.readable() {
			return false
		}
		for _, u := range times {
			if apart(t, u) < gap {
				return false
			}
		}
		return true
	}
	// The targets are well apart and not each other's swap or mirror, so the
	// prompt never names two confusable times.
	for len(times) < Targets {
		t := Time(r.IntN(144) * 5)
		if ok(t, 30) && (len(times) == 0 || apart(t.swapped(), times[0]) >= 30 && apart(t.mirrored(), times[0]) >= 30) {
			times = append(times, t)
		}
	}
	for _, d := range decoys(r, times) {
		if len(times) < n && ok(d, 5) {
			times = append(times, d)
		}
	}
	for tries := 0; len(times) < n && tries < 100; tries++ {
		if t := Time(r.IntN(144) * 5); ok(t, 5) {
			times = append(times, t)
		}
	}
	return times, len(times) == n
}

// face is one drawn clock.
type face struct {
	x, y, r float64
	t       Time
	style   int
	// rot turns the whole face, hands included; only faces whose 12 is
	// marked are turned, so "up" stays readable.
	rot       float64
	fill, rim color.RGBA
	hand      color.RGBA
}

const (
	styleArabic   = iota // all twelve numerals
	styleQuarters        // 12, 3, 6, 9 and ticks
	styleRoman           // XII, III, VI, IX and ticks
	styleTicks           // ticks only, a triangle at 12
	styles
)

// layout places n clocks without overlap.
func layout(r *rand.Rand, n int) ([]face, bool) {
	var placed []click.Target
	var faces []face
	lo, hi := 38.0, 44.0
	if n == 5 {
		lo, hi = 33, 37
	}
	for i := 0; i < n; i++ {
		rad := lo + r.Float64()*(hi-lo)
		found := false
		for try := 0; try < 400; try++ {
			x := rad + 3 + r.Float64()*(Width-2*rad-6)
			y := rad + 3 + r.Float64()*(Height-2*rad-6)
			if !click.Overlaps(placed, x, y, rad, 6) {
				placed = append(placed, click.Target{X: x, Y: y, R: rad})
				faces = append(faces, face{x: x, y: y, r: rad})
				found = true
				break
			}
		}
		if !found {
			return nil, false
		}
	}
	return faces, true
}

// Generate builds a puzzle from r. Production callers seed r from crypto/rand.
func Generate(r *rand.Rand, src imagegen.Source) (click.Puzzle, error) {
	p, _, err := generate(r, src)
	return p, err
}

// generate also returns the faces, for tests; the targets are the first
// Targets of them.
func generate(r *rand.Rand, src imagegen.Source) (click.Puzzle, []face, error) {
	for attempt := 0; attempt < 50; attempt++ {
		n := 4
		if r.IntN(3) == 0 {
			n = 5
		}
		faces, ok := layout(r, n)
		if !ok {
			continue
		}
		times, ok := pickTimes(r, n)
		if !ok {
			continue
		}
		// Targets are the first times; the faces are already in random
		// positions, so assigning in order leaks nothing.
		for i := range faces {
			f := &faces[i]
			f.t = times[i]
			f.style = r.IntN(styles)
			if f.style != styleArabic || r.IntN(2) == 0 {
				f.rot = (r.Float64()*2 - 1) * 12
			}
			f.fill = imagegen.HSV(r.Float64()*360, 0.05+r.Float64()*0.18, 0.93+r.Float64()*0.07)
			f.rim = imagegen.HSV(r.Float64()*360, 0.3+r.Float64()*0.5, 0.25+r.Float64()*0.4)
			f.hand = imagegen.HSV(r.Float64()*360, 0.2+r.Float64()*0.6, 0.08+r.Float64()*0.22)
		}

		bg, _ := src.Background(r, Width, Height)
		white := color.RGBA{255, 255, 255, 255}
		for y := 0; y < Height; y++ {
			for x := 0; x < Width; x++ {
				imagegen.Blend(bg, x, y, white, 0.3)
			}
		}
		imagegen.Clutter(r, bg, true)
		// Hairlines stay under the faces, where they cannot pass for a hand.
		imagegen.Hairlines(r, bg)
		for _, f := range faces {
			drawFace(r, bg, f)
		}
		imagegen.Perturb(r, bg)
		url, err := imagegen.JPEG(r, bg)
		if err != nil {
			return click.Puzzle{}, nil, err
		}
		var targets []click.Target
		var cn, tw, en []string
		for _, f := range faces[:Targets] {
			targets = append(targets, click.Target{X: f.x, Y: f.y, R: f.r * 0.85})
			cn = append(cn, "显示 "+f.t.String()+" 的时钟")
			tw = append(tw, "顯示 "+f.t.String()+" 的時鐘")
			en = append(en, "the clock showing "+f.t.String())
		}
		t := prompt.Text{
			prompt.ZhCN: "请依次点击：" + prompt.Join(prompt.ZhCN, cn),
			prompt.ZhTW: "請依次點擊：" + prompt.Join(prompt.ZhTW, tw),
			prompt.En:   "Click in order: " + prompt.Join(prompt.En, en),
		}
		return click.Puzzle{Image: url, Prompt: t[prompt.ZhCN], Prompts: t, Width: Width, Height: Height, Targets: targets}, faces, nil
	}
	return click.Puzzle{}, nil, fmt.Errorf("clock: no layout after 50 attempts")
}

// polar is the point at angle deg (clockwise from 12) and distance d.
func polar(f face, deg, d float64) [2]float64 {
	s, c := math.Sincos((deg + f.rot) * math.Pi / 180)
	return [2]float64{f.x + s*d, f.y - c*d}
}

func drawFace(r *rand.Rand, img *image.RGBA, f face) {
	shadow := color.RGBA{0, 0, 0, 255}
	imagegen.FillShape(img, int(f.x-f.r-4), int(f.y-f.r-4), int(f.x+f.r+6), int(f.y+f.r+6), shadow, 0.18, func(x, y float64) bool {
		return math.Hypot(x-f.x-1.5, y-f.y-2) <= f.r+1
	})
	rimW := 2 + r.Float64()*2.5
	imagegen.FillShape(img, int(f.x-f.r-2), int(f.y-f.r-2), int(f.x+f.r+3), int(f.y+f.r+3), f.rim, 1, func(x, y float64) bool {
		return math.Hypot(x-f.x, y-f.y) <= f.r
	})
	imagegen.FillShape(img, int(f.x-f.r), int(f.y-f.r), int(f.x+f.r+1), int(f.y+f.r+1), f.fill, 0.96, func(x, y float64) bool {
		return math.Hypot(x-f.x, y-f.y) <= f.r-rimW
	})
	ink := f.hand
	inner := f.r - rimW
	// Ticks: minute ticks on some faces, hour ticks on most.
	if f.style != styleArabic || r.IntN(2) == 0 {
		for i := 0; i < 12; i++ {
			deg := float64(i) * 30
			long, w := 0.14, 0.9
			if i%3 == 0 {
				long, w = 0.2, 1.3
			}
			// Quarters carrying a numeral or the 12 triangle get no tick.
			if i%3 == 0 && (f.style == styleQuarters || f.style == styleRoman) || f.style == styleTicks && i == 0 {
				continue
			}
			imagegen.Stroke(img, imagegen.Polyline(polar(f, deg, inner*(0.95-long)), polar(f, deg, inner*0.95)), w, ink, 0.9)
		}
	}
	switch f.style {
	case styleArabic:
		for h := 1; h <= 12; h++ {
			drawText(img, f, float64(h)*30, inner*0.7, fmt.Sprint(h), inner*0.22, ink)
		}
	case styleQuarters:
		for _, h := range []int{12, 3, 6, 9} {
			drawText(img, f, float64(h)*30, inner*0.62, fmt.Sprint(h), inner*0.24, ink)
		}
	case styleRoman:
		for _, h := range []struct {
			n int
			s string
		}{{12, "XII"}, {3, "III"}, {6, "VI"}, {9, "IX"}} {
			drawText(img, f, float64(h.n)*30, inner*0.62, h.s, inner*0.22, ink)
		}
	case styleTicks:
		a, b, c := polar(f, 0, inner*0.93), polar(f, -6, inner*0.7), polar(f, 6, inner*0.7)
		imagegen.FillShape(img, int(f.x-f.r), int(f.y-f.r), int(f.x+f.r), int(f.y), ink, 0.95, func(x, y float64) bool {
			return imagegen.InTriangle(x, y, [3][2]float64{a, b, c})
		})
	}
	hour, minute := f.t.hands()
	// Hour hand short and thick, minute hand long and thin. No tails: with the
	// hands opposite, a tail would lie along the other hand.
	imagegen.Stroke(img, imagegen.Polyline([2]float64{f.x, f.y}, polar(f, hour, inner*0.5)), max(1.9, inner*0.065), ink, 1)
	imagegen.Stroke(img, imagegen.Polyline([2]float64{f.x, f.y}, polar(f, minute, inner*0.86)), max(1.1, inner*0.032), ink, 1)
	imagegen.FillShape(img, int(f.x-4), int(f.y-4), int(f.x+5), int(f.y+5), ink, 1, func(x, y float64) bool {
		return math.Hypot(x-f.x, y-f.y) <= 2.6
	})
}

// drawText strokes s centred at angle deg, distance d from the centre; the
// glyphs stay upright relative to the face.
func drawText(img *image.RGBA, f face, deg, d float64, s string, size float64, c color.RGBA) {
	centre := polar(f, deg, d)
	w := 0.0
	for _, ch := range s {
		w += glyphs[ch].width + 0.25
	}
	w -= 0.25
	sin, cos := math.Sincos(f.rot * math.Pi / 180)
	x := -w / 2
	for _, ch := range s {
		g := glyphs[ch]
		for _, line := range g.strokes {
			pts := make([][2]float64, len(line))
			for i, p := range line {
				// Glyph space: unit height, y down; centred, then turned with the face.
				u, v := (x+p[0])*size, (p[1]-0.5)*size
				pts[i] = [2]float64{centre[0] + u*cos - v*sin, centre[1] + u*sin + v*cos}
			}
			imagegen.Stroke(img, imagegen.Polyline(pts...), max(0.65, size*0.085), c, 0.95)
		}
		x += g.width + 0.25
	}
}

type glyph struct {
	width   float64
	strokes [][][2]float64
}

// glyphs is a tiny stroke font for clock numerals, in a box one unit high.
var glyphs = map[rune]glyph{
	'0': {0.6, [][][2]float64{{{0.3, 0}, {0.55, 0.12}, {0.6, 0.5}, {0.55, 0.88}, {0.3, 1}, {0.05, 0.88}, {0, 0.5}, {0.05, 0.12}, {0.3, 0}}}},
	'1': {0.4, [][][2]float64{{{0.05, 0.22}, {0.32, 0}, {0.32, 1}}}},
	'2': {0.6, [][][2]float64{{{0.02, 0.2}, {0.18, 0.03}, {0.42, 0.01}, {0.58, 0.18}, {0.56, 0.38}, {0, 1}, {0.6, 1}}}},
	'3': {0.6, [][][2]float64{{{0.02, 0.08}, {0.25, 0}, {0.5, 0.06}, {0.56, 0.26}, {0.3, 0.46}, {0.56, 0.6}, {0.58, 0.84}, {0.36, 1}, {0.12, 0.98}, {0, 0.88}}}},
	'4': {0.62, [][][2]float64{{{0.46, 1}, {0.46, 0}, {0, 0.7}, {0.62, 0.7}}}},
	'5': {0.6, [][][2]float64{{{0.56, 0}, {0.08, 0}, {0.03, 0.45}, {0.3, 0.38}, {0.55, 0.5}, {0.6, 0.75}, {0.45, 0.96}, {0.2, 1}, {0, 0.9}}}},
	'6': {0.6, [][][2]float64{{{0.52, 0.04}, {0.3, 0}, {0.08, 0.2}, {0, 0.6}, {0.08, 0.92}, {0.3, 1}, {0.52, 0.92}, {0.6, 0.7}, {0.5, 0.5}, {0.28, 0.44}, {0.06, 0.55}}}},
	'7': {0.6, [][][2]float64{{{0, 0}, {0.6, 0}, {0.22, 1}}}},
	'8': {0.6, [][][2]float64{{{0.3, 0.46}, {0.08, 0.36}, {0.06, 0.12}, {0.3, 0}, {0.54, 0.12}, {0.52, 0.36}, {0.3, 0.46}, {0.04, 0.6}, {0.02, 0.86}, {0.3, 1}, {0.58, 0.86}, {0.56, 0.6}, {0.3, 0.46}}}},
	'9': {0.6, [][][2]float64{{{0.54, 0.45}, {0.32, 0.56}, {0.08, 0.48}, {0, 0.26}, {0.12, 0.04}, {0.32, 0}, {0.52, 0.08}, {0.6, 0.4}, {0.52, 0.8}, {0.3, 1}, {0.08, 0.96}}}},
	'I': {0.12, [][][2]float64{{{0.06, 0}, {0.06, 1}}}},
	'V': {0.5, [][][2]float64{{{0, 0}, {0.25, 1}, {0.5, 0}}}},
	'X': {0.5, [][][2]float64{{{0, 0}, {0.5, 1}}, {{0.5, 0}, {0, 1}}}},
}
