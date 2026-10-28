// Package imagegen draws the procedural backgrounds and primitives shared by
// the slider and click challenges. Standard library only: no image assets,
// so there are no licences to track and no two images repeat.
package imagegen

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"math/rand/v2"
)

func RandomColor(r *rand.Rand) color.RGBA {
	return HSV(r.Float64()*360, 0.3+r.Float64()*0.45, 0.45+r.Float64()*0.45)
}

// HSV converts hue in degrees, saturation and value in [0, 1].
func HSV(h, s, v float64) color.RGBA {
	h = math.Mod(h, 360)
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

func Blend(dst *image.RGBA, x, y int, c color.RGBA, a float64) {
	i := dst.PixOffset(x, y)
	p := dst.Pix[i : i+3 : i+3]
	p[0] = uint8(float64(p[0])*(1-a) + float64(c.R)*a)
	p[1] = uint8(float64(p[1])*(1-a) + float64(c.G)*a)
	p[2] = uint8(float64(p[2])*(1-a) + float64(c.B)*a)
}

func InTriangle(x, y float64, p [3][2]float64) bool {
	sign := func(a, b [2]float64) float64 { return (x-b[0])*(a[1]-b[1]) - (a[0]-b[0])*(y-b[1]) }
	d1, d2, d3 := sign(p[0], p[1]), sign(p[1], p[2]), sign(p[2], p[0])
	neg := d1 < 0 || d2 < 0 || d3 < 0
	pos := d1 > 0 || d2 > 0 || d3 > 0
	return !(neg && pos)
}

func DataURL(mime string, b []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)
}

// JPEG encodes img at a random quality in [60, 80] as a data URL; the
// compression noise varies per image.
func JPEG(r *rand.Rand, img image.Image) (string, error) {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 60 + r.IntN(21)}); err != nil {
		return "", err
	}
	return DataURL("image/jpeg", b.Bytes()), nil
}

// FillShape alpha-blends c into every pixel whose centre satisfies inside,
// within the bounding box [x0,x1)×[y0,y1).
func FillShape(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA, alpha float64, inside func(x, y float64) bool) {
	b := img.Bounds()
	x0, y0 = max(x0, b.Min.X), max(y0, b.Min.Y)
	x1, y1 = min(x1, b.Max.X), min(y1, b.Max.Y)
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			if inside(float64(x)+0.5, float64(y)+0.5) {
				Blend(img, x, y, c, alpha)
			}
		}
	}
}

// InPolygon reports whether (x, y) is inside the polygon (even-odd rule).
func InPolygon(x, y float64, pts [][2]float64) bool {
	in := false
	for i, j := 0, len(pts)-1; i < len(pts); j, i = i, i+1 {
		a, b := pts[i], pts[j]
		if (a[1] > y) != (b[1] > y) && x < (b[0]-a[0])*(y-a[1])/(b[1]-a[1])+a[0] {
			in = !in
		}
	}
	return in
}

// Luma is the perceived brightness of c in [0, 255].
func Luma(c color.RGBA) float64 {
	return 0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)
}

// MeanLuma is the average brightness of img inside the rectangle.
func MeanLuma(img *image.RGBA, rect image.Rectangle) float64 {
	rect = rect.Intersect(img.Bounds())
	sum, n := 0.0, 0
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			i := img.PixOffset(x, y)
			sum += 0.299*float64(img.Pix[i]) + 0.587*float64(img.Pix[i+1]) + 0.114*float64(img.Pix[i+2])
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}
