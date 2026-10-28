package imagegen

import (
	"image"
	"image/color"
	"math"
	"math/rand/v2"
)

// wrap is the signed distance from v to the nearest multiple of period,
// like math.Remainder but several times faster.
func wrap(v, period float64) float64 { return v - period*math.Floor(v/period+0.5) }

// sinTable holds one period of sine; soft patterns look it up per pixel.
var sinTable = func() (t [256]float64) {
	for i := range t {
		t[i] = math.Sin(2 * math.Pi * float64(i) / 256)
	}
	return
}()

// wave is sin(2π·v/period) from the table.
func wave(v, period float64) float64 {
	return sinTable[int(math.Floor(v/period*256))&255]
}

// Background draws a fresh procedural picture. Every call picks its own mix
// of layers, so no two backgrounds share a structure a solver could learn and
// subtract:
//
//  1. a base gradient: linear at any angle with 2–3 stops, radial, or four
//     corner colours blended;
//  2. value-noise texture (fBm) that varies brightness and tints towards a
//     third colour, so there is no flat region to threshold;
//  3. one or two faint periodic patterns (stripes, ripples, waves, grid)
//     whose edges compete with the puzzle's own edges;
//  4. 8–14 translucent shapes (circles, rotated boxes, triangles, polygons,
//     rings), some fading across their extent;
//  5. 3–6 thin curves of clutter;
//  6. a colour grade (per-channel gain and gamma) and grain, luma-only or
//     per channel.
//
// Callers wash the result towards white before drawing their targets, which
// keeps every layer well below the contrast of what the user has to find.
func Background(r *rand.Rand, width, height int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	baseGradient(r, img)
	texture(r, img)
	for n := 1 + r.IntN(2); n > 0; n-- {
		pattern(r, img)
	}
	for n := 8 + r.IntN(7); n > 0; n-- {
		shape(r, img)
	}
	for n := 3 + r.IntN(4); n > 0; n-- {
		curve(r, img)
	}
	grade(r, img)
	grain(r, img)
	return img
}

func lerp(a, b color.RGBA, t float64) [3]float64 {
	return [3]float64{
		float64(a.R) + (float64(b.R)-float64(a.R))*t,
		float64(a.G) + (float64(b.G)-float64(a.G))*t,
		float64(a.B) + (float64(b.B)-float64(a.B))*t,
	}
}

func set(img *image.RGBA, x, y int, c [3]float64) {
	i := img.PixOffset(x, y)
	img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = clamp8(c[0]), clamp8(c[1]), clamp8(c[2]), 255
}

func clamp8(v float64) uint8 { return uint8(max(0, min(255, v+0.5))) }

func baseGradient(r *rand.Rand, img *image.RGBA) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	fw, fh := float64(w), float64(h)
	switch r.IntN(3) {
	case 0: // linear, 2–3 stops
		stops := []color.RGBA{RandomColor(r), RandomColor(r)}
		if r.IntN(2) == 0 {
			stops = append(stops, RandomColor(r))
		}
		sin, cos := math.Sincos(r.Float64() * 2 * math.Pi)
		span := math.Abs(cos)*fw + math.Abs(sin)*fh
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				t := ((float64(x)-fw/2)*cos+(float64(y)-fh/2)*sin)/span + 0.5
				t = max(0, min(1, t)) * float64(len(stops)-1)
				k := min(int(t), len(stops)-2)
				set(img, x, y, lerp(stops[k], stops[k+1], t-float64(k)))
			}
		}
	case 1: // radial from a random centre
		in, out := RandomColor(r), RandomColor(r)
		cx, cy := r.Float64()*fw, r.Float64()*fh
		reach := math.Hypot(max(cx, fw-cx), max(cy, fh-cy))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				set(img, x, y, lerp(in, out, math.Hypot(float64(x)-cx, float64(y)-cy)/reach))
			}
		}
	default: // four corners, bilinear
		c := [4]color.RGBA{RandomColor(r), RandomColor(r), RandomColor(r), RandomColor(r)}
		for y := 0; y < h; y++ {
			v := float64(y) / fh
			for x := 0; x < w; x++ {
				u := float64(x) / fw
				top, bottom := lerp(c[0], c[1], u), lerp(c[2], c[3], u)
				set(img, x, y, [3]float64{top[0] + (bottom[0]-top[0])*v, top[1] + (bottom[1]-top[1])*v, top[2] + (bottom[2]-top[2])*v})
			}
		}
	}
}

// noise is smooth value noise: random values on a lattice, interpolated
// with smoothstep.
type noise struct {
	cell     float64
	cols     int
	lattice  []float64
	offX     float64
	offY     float64
	inverted bool
}

func newNoise(r *rand.Rand, w, h int, cell float64) noise {
	// Room for the random offset and for fields sampled a step past the edge.
	cols, rows := int(float64(w+8)/cell)+3, int(float64(h+8)/cell)+3
	n := noise{cell: cell, cols: cols, lattice: make([]float64, cols*rows), offX: r.Float64() * cell, offY: r.Float64() * cell}
	for i := range n.lattice {
		n.lattice[i] = r.Float64()
	}
	return n
}

func (n noise) at(x, y float64) float64 {
	fx, fy := (x+n.offX)/n.cell, (y+n.offY)/n.cell
	x0, y0 := int(fx), int(fy)
	tx, ty := fx-float64(x0), fy-float64(y0)
	tx, ty = tx*tx*(3-2*tx), ty*ty*(3-2*ty)
	i := y0*n.cols + x0
	a, b := n.lattice[i], n.lattice[i+1]
	c, d := n.lattice[i+n.cols], n.lattice[i+n.cols+1]
	return (a+(b-a)*tx)*(1-ty) + (c+(d-c)*tx)*ty
}

// fbm sums octaves of value noise, halving the cell each time; the result
// is in [0, 1].
type fbm []noise

func newFBM(r *rand.Rand, w, h int, cell float64, octaves int) fbm {
	f := make(fbm, octaves)
	for i := range f {
		f[i] = newNoise(r, w, h, cell)
		cell = max(2, cell/2)
	}
	return f
}

func (f fbm) at(x, y float64) float64 {
	sum, amp, norm := 0.0, 1.0, 0.0
	for _, n := range f {
		sum += n.at(x, y) * amp
		norm += amp
		amp *= 0.5
	}
	return sum / norm
}

// field samples f every step pixels; at interpolates between samples. The
// noise layers are smooth, so this costs nothing visible and saves most of
// their time.
type field struct {
	step float64
	cols int
	v    []float64
}

func sampleField(w, h, step int, f func(x, y float64) float64) field {
	cols, rows := w/step+2, h/step+2
	out := field{step: float64(step), cols: cols, v: make([]float64, cols*rows)}
	for j := 0; j < rows; j++ {
		for i := 0; i < cols; i++ {
			out.v[j*cols+i] = f(float64(i*step), float64(j*step))
		}
	}
	return out
}

func (f field) at(x, y int) float64 {
	fx, fy := float64(x)/f.step, float64(y)/f.step
	x0, y0 := int(fx), int(fy)
	tx, ty := fx-float64(x0), fy-float64(y0)
	i := y0*f.cols + x0
	a, b, c, d := f.v[i], f.v[i+1], f.v[i+f.cols], f.v[i+f.cols+1]
	return (a+(b-a)*tx)*(1-ty) + (c+(d-c)*tx)*ty
}

// texture modulates brightness with one noise field and tints towards a
// third colour with another, at a random scale.
func texture(r *rand.Rand, img *image.RGBA) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	light := sampleField(w, h, 2, newFBM(r, w, h, 18+r.Float64()*60, 2+r.IntN(3)).at)
	tint := sampleField(w, h, 3, newFBM(r, w, h, 25+r.Float64()*70, 2).at)
	depth := 0.2 + r.Float64()*0.3
	tc, tAlpha := RandomColor(r), 0.25+r.Float64()*0.35
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			gain := 1 + depth*(light.at(x, y)-0.5)*2
			a := tAlpha * max(0, tint.at(x, y)*2-0.6)
			i := img.PixOffset(x, y)
			p := img.Pix[i : i+3 : i+3]
			p[0] = clamp8((float64(p[0])*(1-a) + float64(tc.R)*a) * gain)
			p[1] = clamp8((float64(p[1])*(1-a) + float64(tc.G)*a) * gain)
			p[2] = clamp8((float64(p[2])*(1-a) + float64(tc.B)*a) * gain)
		}
	}
}

// pattern lays a faint periodic layer over the whole image. phase maps a
// pixel to a position along the period; sharp patterns draw thin lines,
// soft ones a sinusoid.
func pattern(r *rand.Rand, img *image.RGBA) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	period := 7 + r.Float64()*22
	sin, cos := math.Sincos(r.Float64() * math.Pi)
	cx, cy := r.Float64()*float64(w), r.Float64()*float64(h)
	amp, freq := 3+r.Float64()*9, 0.02+r.Float64()*0.06
	var phase func(x, y float64) float64
	sharp := r.IntN(2) == 0
	kind := r.IntN(4)
	switch kind {
	case 0: // stripes
		phase = func(x, y float64) float64 { return x*cos + y*sin }
	case 1: // ripples
		phase = func(x, y float64) float64 { return math.Sqrt((x-cx)*(x-cx) + (y-cy)*(y-cy)) }
	case 2: // waves
		phase = func(x, y float64) float64 {
			u, v := x*cos+y*sin, -x*sin+y*cos
			return u + amp*wave(v*freq, 1)
		}
	default: // grid: lines in two directions
		sharp = true
		phase = func(x, y float64) float64 {
			u, v := x*cos+y*sin, -x*sin+y*cos
			du, dv := math.Abs(wrap(u, period)), math.Abs(wrap(v, period))
			return min(du, dv)
		}
	}
	c := RandomColor(r)
	alpha := 0.06 + r.Float64()*0.12
	line := 0.7 + r.Float64()*1.3
	if sharp {
		alpha *= 2.5
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			p := phase(float64(x)+0.5, float64(y)+0.5)
			var a float64
			switch {
			case kind == 3:
				a = alpha * max(0, 1-p/line)
			case sharp:
				a = alpha * max(0, 1-math.Abs(wrap(p, period))/line)
			default:
				a = alpha * (0.5 + 0.5*wave(p, period))
			}
			if a > 0.002 {
				Blend(img, x, y, c, a)
			}
		}
	}
}

// shape blends one translucent figure, optionally fading along an axis.
func shape(r *rand.Rand, img *image.RGBA) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	c, alpha := RandomColor(r), 0.2+r.Float64()*0.45
	cx, cy := r.Float64()*float64(w), r.Float64()*float64(h)
	size := 12 + r.Float64()*58
	rot := r.Float64() * 2 * math.Pi
	sin, cos := math.Sincos(rot)
	var inside func(x, y float64) bool
	switch r.IntN(5) {
	case 0:
		inside = func(x, y float64) bool { return math.Hypot(x-cx, y-cy) <= size }
	case 1:
		bw, bh := size, size*(0.3+r.Float64())
		inside = func(x, y float64) bool {
			u, v := (x-cx)*cos+(y-cy)*sin, -(x-cx)*sin+(y-cy)*cos
			return math.Abs(u) <= bw && math.Abs(v) <= bh
		}
	case 2:
		var pts [3][2]float64
		for i := range pts {
			a := r.Float64() * 2 * math.Pi
			pts[i] = [2]float64{cx + math.Cos(a)*size, cy + math.Sin(a)*size}
		}
		inside = func(x, y float64) bool { return InTriangle(x, y, pts) }
	case 3:
		n := 4 + r.IntN(4)
		pts := make([][2]float64, n)
		for i := range pts {
			a := rot + 2*math.Pi*float64(i)/float64(n)
			rr := size * (0.6 + r.Float64()*0.4)
			pts[i] = [2]float64{cx + math.Cos(a)*rr, cy + math.Sin(a)*rr}
		}
		inside = func(x, y float64) bool { return InPolygon(x, y, pts) }
	default:
		thick := 2 + r.Float64()*size*0.25
		inside = func(x, y float64) bool { return math.Abs(math.Hypot(x-cx, y-cy)-size+thick) <= thick }
	}
	fade := r.IntN(3) == 0
	x0, x1 := max(0, int(cx-size*1.5)), min(w, int(cx+size*1.5)+1)
	y0, y1 := max(0, int(cy-size*1.5)), min(h, int(cy+size*1.5)+1)
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			if !inside(fx, fy) {
				continue
			}
			a := alpha
			if fade {
				a *= max(0, min(1, 0.5+((fx-cx)*cos+(fy-cy)*sin)/(2*size)))
			}
			Blend(img, x, y, c, a)
		}
	}
}

// curve strokes a thin quadratic Bézier from edge to edge.
func curve(r *rand.Rand, img *image.RGBA) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	fw, fh := float64(w), float64(h)
	p0 := [2]float64{r.Float64() * fw, r.Float64() * fh}
	p1 := [2]float64{(r.Float64()*1.4 - 0.2) * fw, (r.Float64()*1.4 - 0.2) * fh}
	p2 := [2]float64{r.Float64() * fw, r.Float64() * fh}
	Stroke(img, bezier(p0, p1, p2), 0.6+r.Float64()*1.2, RandomColor(r), 0.3+r.Float64()*0.35)
}

func bezier(p0, p1, p2 [2]float64) [][2]float64 {
	steps := int(math.Hypot(p2[0]-p0[0], p2[1]-p0[1])*1.5) + 20
	pts := make([][2]float64, 0, steps+1)
	for s := 0; s <= steps; s++ {
		t := float64(s) / float64(steps)
		u := 1 - t
		pts = append(pts, [2]float64{u*u*p0[0] + 2*u*t*p1[0] + t*t*p2[0], u*u*p0[1] + 2*u*t*p1[1] + t*t*p2[1]})
	}
	return pts
}

// Stroke blends a line of the given half-width through densely sampled
// points (see Polyline). Pixels are marked first and blended once, so the
// stroke has no darker joints.
func Stroke(img *image.RGBA, pts [][2]float64, width float64, c color.RGBA, alpha float64) {
	if len(pts) == 0 {
		return
	}
	b := img.Bounds()
	x0, y0, x1, y1 := pts[0][0], pts[0][1], pts[0][0], pts[0][1]
	for _, p := range pts {
		x0, y0, x1, y1 = min(x0, p[0]), min(y0, p[1]), max(x1, p[0]), max(y1, p[1])
	}
	// The mark covers only the stroke's bounding box.
	bx0, by0 := max(b.Min.X, int(x0-width-1)), max(b.Min.Y, int(y0-width-1))
	bx1, by1 := min(b.Max.X, int(x1+width+2)), min(b.Max.Y, int(y1+width+2))
	if bx1 <= bx0 || by1 <= by0 {
		return
	}
	w := bx1 - bx0
	mark := make([]bool, w*(by1-by0))
	for _, p := range pts {
		for y := max(by0, int(p[1]-width-1)); y < min(by1, int(p[1]+width+2)); y++ {
			for x := max(bx0, int(p[0]-width-1)); x < min(bx1, int(p[0]+width+2)); x++ {
				if math.Hypot(float64(x)+0.5-p[0], float64(y)+0.5-p[1]) <= width {
					mark[(y-by0)*w+x-bx0] = true
				}
			}
		}
	}
	for i, m := range mark {
		if m {
			Blend(img, bx0+i%w, by0+i/w, c, alpha)
		}
	}
}

// Polyline samples the segments through pts every half pixel, for Stroke.
func Polyline(pts ...[2]float64) [][2]float64 {
	if len(pts) < 2 {
		return pts
	}
	var out [][2]float64
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		n := int(math.Hypot(b[0]-a[0], b[1]-a[1])*2) + 1
		for s := 0; s < n; s++ {
			t := float64(s) / float64(n)
			out = append(out, [2]float64{a[0] + (b[0]-a[0])*t, a[1] + (b[1]-a[1])*t})
		}
	}
	return append(out, pts[len(pts)-1])
}

// Clutter draws decoys after the caller's wash and before its targets: short
// arcs, hooks and a few long curves coloured like the targets (dark for
// glyphs, vivid for shapes and icons). Without it the washed background is
// pale and every target is the only dark or saturated thing in the picture,
// so one brightness or saturation threshold cuts the targets out for an OCR
// or matching step. The decoys are open strokes, never closed figures or
// characters, so a person still tells them from the targets at a glance.
func Clutter(r *rand.Rand, img *image.RGBA, dark bool) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	pick := func() color.RGBA {
		if dark {
			return HSV(r.Float64()*360, 0.3+r.Float64()*0.5, 0.18+r.Float64()*0.3)
		}
		return HSV(r.Float64()*360, 0.55+r.Float64()*0.35, 0.6+r.Float64()*0.3)
	}
	for n := 6 + r.IntN(6); n > 0; n-- {
		cx, cy := r.Float64()*float64(w), r.Float64()*float64(h)
		rad := 5 + r.Float64()*12
		a0, span := r.Float64()*2*math.Pi, (0.35+r.Float64()*0.6)*math.Pi
		var pts [][2]float64
		for t := 0.0; t <= 1; t += 0.5 / (rad * span) {
			a := a0 + span*t
			// A spiral drift turns some arcs into hooks.
			rr := rad * (1 + 0.25*t*float64(n%2))
			pts = append(pts, [2]float64{cx + math.Cos(a)*rr, cy + math.Sin(a)*rr})
		}
		Stroke(img, pts, 0.8+r.Float64()*0.7, pick(), 0.45+r.Float64()*0.3)
	}
	for n := 1 + r.IntN(2); n > 0; n-- {
		p0 := [2]float64{r.Float64() * float64(w), r.Float64() * float64(h)}
		p1 := [2]float64{r.Float64() * float64(w), r.Float64() * float64(h)}
		p2 := [2]float64{r.Float64() * float64(w), r.Float64() * float64(h)}
		Stroke(img, bezier(p0, p1, p2), 0.7+r.Float64()*0.6, pick(), 0.4+r.Float64()*0.25)
	}
}

// Finish runs over the whole finished picture, targets included: a faint
// hairline or two across everything, a slight colour cast and grain. Target
// colours then never equal a fixed palette entry and no filled area is flat,
// so exact-colour lookup and flat-region segmentation stop working; the
// shift is small enough that a red shape still reads as red.
func Finish(r *rand.Rand, img *image.RGBA) {
	Hairlines(r, img)
	Perturb(r, img)
}

// Hairlines draws Finish's one or two faint lines across the whole picture.
// Kinds whose targets contain thin strokes of their own (clock hands) draw
// them under the targets instead.
func Hairlines(r *rand.Rand, img *image.RGBA) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	for n := 1 + r.IntN(2); n > 0; n-- {
		p0 := [2]float64{0, r.Float64() * float64(h)}
		p1 := [2]float64{r.Float64() * float64(w), (r.Float64()*1.6 - 0.3) * float64(h)}
		p2 := [2]float64{float64(w), r.Float64() * float64(h)}
		Stroke(img, bezier(p0, p1, p2), 0.55+r.Float64()*0.3, HSV(r.Float64()*360, 0.4, 0.35+r.Float64()*0.4), 0.3+r.Float64()*0.2)
	}
}

// Perturb is Finish's colour cast and grain.
func Perturb(r *rand.Rand, img *image.RGBA) {
	var lut [3][256]uint8
	for k := range lut {
		gain, lift := 0.93+r.Float64()*0.1, r.Float64()*10-5
		for v := range lut[k] {
			lut[k][v] = clamp8(float64(v)*gain + lift)
		}
	}
	n := noiseBytes{r: r}
	for i := 0; i < len(img.Pix); i += 4 {
		d := n.next(4)
		for k := 0; k < 3; k++ {
			img.Pix[i+k] = uint8(max(0, min(255, int(lut[k][img.Pix[i+k]])+d)))
		}
	}
}

// grade applies a random colour cast: per-channel gain and gamma.
func grade(r *rand.Rand, img *image.RGBA) {
	var lut [3][256]uint8
	for k := range lut {
		gain, gamma := 0.9+r.Float64()*0.2, 0.85+r.Float64()*0.3
		for v := range lut[k] {
			lut[k][v] = clamp8(255 * math.Pow(float64(v)/255, gamma) * gain)
		}
	}
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2] = lut[0][img.Pix[i]], lut[1][img.Pix[i+1]], lut[2][img.Pix[i+2]]
	}
}

// grain adds per-pixel noise of random strength, either the same on every
// channel or independent per channel (chroma noise).
func grain(r *rand.Rand, img *image.RGBA) {
	amp := 5 + r.IntN(8)
	chroma := r.IntN(3) == 0
	n := noiseBytes{r: r}
	for i := 0; i < len(img.Pix); i += 4 {
		d := n.next(amp)
		for k := 0; k < 3; k++ {
			if chroma && k > 0 {
				d = n.next(amp)
			}
			img.Pix[i+k] = uint8(max(0, min(255, int(img.Pix[i+k])+d)))
		}
	}
}

// noiseBytes deals grain values in [-amp, amp] eight to a random word;
// calling r per pixel was a third of the background's time.
type noiseBytes struct {
	r    *rand.Rand
	word uint64
	left int
}

func (n *noiseBytes) next(amp int) int {
	if n.left == 0 {
		n.word, n.left = n.r.Uint64(), 8
	}
	b := int(n.word & 0xff)
	n.word >>= 8
	n.left--
	return b*(2*amp+1)>>8 - amp
}
