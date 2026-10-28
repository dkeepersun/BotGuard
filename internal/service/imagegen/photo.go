package imagegen

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"math"
	"math/rand/v2"
	"sync"

	_ "image/png" // Normalize accepts PNG uploads

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // and WebP
)

// Photo libraries are stored at this size: large enough that a 320-pixel
// crop can come from anywhere between a third and most of the picture.
const (
	PhotoWidth  = 720
	PhotoHeight = 450
)

// Library is a set of photos, kept as the JPEG bytes they were stored as
// and decoded on first use.
type Library struct {
	entries []*entry
}

type entry struct {
	jpeg []byte
	once sync.Once
	img  image.Image
	err  error
}

// NewLibrary wraps JPEG images. Nothing is decoded until a photo is drawn.
func NewLibrary(jpegs [][]byte) *Library {
	l := &Library{entries: make([]*entry, len(jpegs))}
	for i, j := range jpegs {
		l.entries[i] = &entry{jpeg: j}
	}
	return l
}

// Join is a library of all the photos in libs. It shares their decoded
// photos, so joining costs nothing per photo.
func Join(libs ...*Library) *Library {
	out := &Library{}
	for _, l := range libs {
		if l != nil {
			out.entries = append(out.entries, l.entries...)
		}
	}
	return out
}

// Len is the number of photos; a nil library has none.
func (l *Library) Len() int {
	if l == nil {
		return 0
	}
	return len(l.entries)
}

// photo decodes photo i once and keeps it: a 720×450 JPEG decodes to about
// half a megabyte, against several milliseconds to decode it per challenge.
func (l *Library) photo(i int) (image.Image, error) {
	e := l.entries[i]
	e.once.Do(func() { e.img, e.err = jpeg.Decode(bytes.NewReader(e.jpeg)) })
	return e.img, e.err
}

// Source chooses each challenge's background: a treated photo from Photos
// with probability Share, otherwise a procedural picture. The zero Source
// draws procedural pictures only.
type Source struct {
	Photos *Library
	Share  float64
	// trace, set by tests, receives each photo picture's crop before any
	// treatment: what an attacker who holds the library and finds the crop
	// exactly would have.
	trace func(plain *image.RGBA)
}

// Background draws a background of the given size; photo reports whether
// it came from a photo, so callers can leave out marks that only the plain
// procedural pictures need.
func (s Source) Background(r *rand.Rand, w, h int) (img *image.RGBA, photo bool) {
	if n := s.Photos.Len(); n > 0 && r.Float64() < s.Share {
		i := r.IntN(n)
		if src, err := s.Photos.photo(i); err == nil {
			var second image.Image
			// A faint second photo on top on some pictures: a copy of the
			// library then explains the picture only up to that layer.
			if n > 1 && r.Float64() < 0.4 {
				j := (i + 1 + r.IntN(n-1)) % n
				second, _ = s.Photos.photo(j)
			}
			v := pickView(r, src.Bounds(), w, h)
			if s.trace != nil {
				plain := image.NewRGBA(image.Rect(0, 0, w, h))
				render(plain, src, v, field{}, field{}, 0)
				s.trace(plain)
			}
			return treat(r, src, v, second, w, h), true
		}
	}
	return Background(r, w, h), false
}

// treat turns a stored photo into a challenge background. Anyone can collect
// a library (the built-in one is published with the source), crop it and
// subtract it from a challenge to find what was drawn on top. Each picture
// therefore differs from its photo in ways that do not undo by a global
// adjustment:
//
//  1. a random crop covering 45–90% of the photo's width, possibly mirrored
//     and turned by up to 3°, so the crop has to be searched for;
//  2. a smooth random warp of a few pixels, so no single transform lines the
//     photo up with the picture everywhere;
//  3. a colour change: brightness, contrast, saturation, a hue turn and a
//     cast per channel;
//  4. sometimes a second photo blended in at 8–18%;
//  5. faint value-noise shading and grain.
//
// What survives is the photo's own content, which is what a person reads.
func treat(r *rand.Rand, src image.Image, v view, second image.Image, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	amp := 1.5 + r.Float64()*2.5
	cell := 40 + r.Float64()*50
	dx := sampleField(w, h, 4, newNoise(r, w, h, cell).at)
	dy := sampleField(w, h, 4, newNoise(r, w, h, cell).at)
	render(img, src, v, dx, dy, amp)
	colour(r, img)
	if second != nil {
		over := image.NewRGBA(img.Rect)
		render(over, second, pickView(r, second.Bounds(), w, h), field{}, field{}, 0)
		a := 0.08 + r.Float64()*0.1
		for i := 0; i < len(img.Pix); i += 4 {
			for k := 0; k < 3; k++ {
				img.Pix[i+k] = clamp8(float64(img.Pix[i+k])*(1-a) + float64(over.Pix[i+k])*a)
			}
		}
	}
	shade(r, img)
	grain(r, img)
	return img
}

// view is where a picture sits in its photo: the crop's centre, the photo
// pixels per picture pixel, a turn and a mirror.
type view struct {
	cx, cy, scale, sin, cos float64
	flip                    bool
}

// pickView picks a random crop of a photo with bounds sb for a w×h picture.
func pickView(r *rand.Rand, sb image.Rectangle, w, h int) view {
	sw, sh := float64(sb.Dx()), float64(sb.Dy())
	aspect := float64(w) / float64(h)
	// Largest crop with the output's aspect, then a random share of it.
	cw := math.Min(sw, sh*aspect) * (0.45 + r.Float64()*0.45)
	cw = math.Max(cw, math.Min(float64(w), math.Min(sw, sh*aspect)))
	ch := cw / aspect
	sin, cos := math.Sincos((r.Float64()*2 - 1) * 3 * math.Pi / 180)
	// Room the turn needs so the crop stays inside the photo.
	padX := (math.Abs(sin)*ch + math.Abs(cos)*cw - cw) / 2
	padY := (math.Abs(sin)*cw + math.Abs(cos)*ch - ch) / 2
	return view{
		cx:    float64(sb.Min.X) + cw/2 + padX + r.Float64()*math.Max(0, sw-cw-2*padX),
		cy:    float64(sb.Min.Y) + ch/2 + padY + r.Float64()*math.Max(0, sh-ch-2*padY),
		scale: cw / float64(w),
		sin:   sin, cos: cos,
		flip: r.IntN(2) == 0,
	}
}

// render fills dst from src through v, bilinearly interpolated, displacing
// each pixel by up to amp along the noise fields dx and dy (amp 0: none).
func render(dst *image.RGBA, src image.Image, v view, dx, dy field, amp float64) {
	w, h := dst.Rect.Dx(), dst.Rect.Dy()
	rgba, _ := src.(*image.RGBA)
	ycc, _ := src.(*image.YCbCr)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			ox, oy := float64(x)+0.5, float64(y)+0.5
			if amp > 0 {
				ox += (dx.at(x, y)*2 - 1) * amp
				oy += (dy.at(x, y)*2 - 1) * amp
			}
			u, t := (ox-float64(w)/2)*v.scale, (oy-float64(h)/2)*v.scale
			if v.flip {
				u = -u
			}
			c := bilinear(src, rgba, ycc, v.cx+u*v.cos-t*v.sin-0.5, v.cy+u*v.sin+t*v.cos-0.5)
			i := dst.PixOffset(x, y)
			dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = clamp8(c[0]), clamp8(c[1]), clamp8(c[2]), 255
		}
	}
}

// bilinear samples src at (x, y), clamped to its bounds. The YCbCr and RGBA
// paths avoid the interface call per pixel that the generic path makes.
func bilinear(src image.Image, rgba *image.RGBA, ycc *image.YCbCr, x, y float64) [3]float64 {
	b := src.Bounds()
	x = max(float64(b.Min.X), min(float64(b.Max.X-1), x))
	y = max(float64(b.Min.Y), min(float64(b.Max.Y-1), y))
	x0, y0 := int(x), int(y)
	x1, y1 := min(x0+1, b.Max.X-1), min(y0+1, b.Max.Y-1)
	tx, ty := x-float64(x0), y-float64(y0)
	at := func(px, py int) [3]float64 {
		switch {
		case ycc != nil:
			yi, ci := ycc.YOffset(px, py), ycc.COffset(px, py)
			yy, cb, cr := float64(ycc.Y[yi]), float64(ycc.Cb[ci])-128, float64(ycc.Cr[ci])-128
			return [3]float64{yy + 1.402*cr, yy - 0.344136*cb - 0.714136*cr, yy + 1.772*cb}
		case rgba != nil:
			i := rgba.PixOffset(px, py)
			return [3]float64{float64(rgba.Pix[i]), float64(rgba.Pix[i+1]), float64(rgba.Pix[i+2])}
		default:
			r, g, b, _ := src.At(px, py).RGBA()
			return [3]float64{float64(r >> 8), float64(g >> 8), float64(b >> 8)}
		}
	}
	a, bb, c, d := at(x0, y0), at(x1, y0), at(x0, y1), at(x1, y1)
	var out [3]float64
	for k := range out {
		top := a[k] + (bb[k]-a[k])*tx
		bottom := c[k] + (d[k]-c[k])*tx
		out[k] = top + (bottom-top)*ty
	}
	return out
}

// colour changes brightness, contrast, saturation and hue, then casts each
// channel; one lookup table per channel after the 3×3 colour matrix.
func colour(r *rand.Rand, img *image.RGBA) {
	sat := 0.75 + r.Float64()*0.5
	hue := (r.Float64()*2 - 1) * 12 * math.Pi / 180
	// Hue turn and saturation in YIQ, written as one RGB matrix.
	hs, hc := math.Sincos(hue)
	toYIQ := [3][3]float64{{0.299, 0.587, 0.114}, {0.596, -0.274, -0.322}, {0.211, -0.523, 0.312}}
	fromYIQ := [3][3]float64{{1, 0.956, 0.621}, {1, -0.272, -0.647}, {1, -1.106, 1.703}}
	rot := [3][3]float64{{1, 0, 0}, {0, sat * hc, -sat * hs}, {0, sat * hs, sat * hc}}
	m := mul(fromYIQ, mul(rot, toYIQ))

	contrast, bright := 0.85+r.Float64()*0.3, 0.9+r.Float64()*0.2
	var lut [3][256]uint8
	for k := range lut {
		gain, gamma := 0.94+r.Float64()*0.12, 0.9+r.Float64()*0.2
		for v := range lut[k] {
			f := (float64(v)-128)*contrast + 128
			f = 255 * math.Pow(max(0, min(255, f*bright))/255, gamma) * gain
			lut[k][v] = clamp8(f)
		}
	}
	for i := 0; i < len(img.Pix); i += 4 {
		p := [3]float64{float64(img.Pix[i]), float64(img.Pix[i+1]), float64(img.Pix[i+2])}
		for k := 0; k < 3; k++ {
			img.Pix[i+k] = lut[k][clamp8(m[k][0]*p[0]+m[k][1]*p[1]+m[k][2]*p[2])]
		}
	}
}

func mul(a, b [3][3]float64) (out [3][3]float64) {
	for i := range 3 {
		for j := range 3 {
			for k := range 3 {
				out[i][j] += a[i][k] * b[k][j]
			}
		}
	}
	return
}

// shade varies brightness by up to ±8% with smooth value noise.
func shade(r *rand.Rand, img *image.RGBA) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	f := sampleField(w, h, 4, newFBM(r, w, h, 30+r.Float64()*50, 2).at)
	depth := 0.04 + r.Float64()*0.04
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			g := 1 + depth*(f.at(x, y)*2-1)
			i := img.PixOffset(x, y)
			for k := 0; k < 3; k++ {
				img.Pix[i+k] = clamp8(float64(img.Pix[i+k]) * g)
			}
		}
	}
}

// Upload limits for Normalize.
const (
	MinUploadWidth  = 640
	MinUploadHeight = 400
	MaxUploadPixels = 40_000_000
)

var ErrUploadTooSmall = fmt.Errorf("图片至少要 %d×%d 像素", MinUploadWidth, MinUploadHeight)

// Normalize turns an uploaded JPEG, PNG or WebP into a library photo: the
// largest centred crop with the library's aspect, scaled to PhotoWidth ×
// PhotoHeight and re-encoded at quality 85, which also drops EXIF and other
// metadata.
func Normalize(data []byte) ([]byte, error) { return NormalizeAt(data, 85) }

// NormalizeAt is Normalize at another JPEG quality; the built-in library
// uses a lower one to keep the binaries small.
func NormalizeAt(data []byte, quality int) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("无法识别的图片，请上传 JPEG、PNG 或 WebP")
	}
	if cfg.Width < MinUploadWidth || cfg.Height < MinUploadHeight {
		return nil, ErrUploadTooSmall
	}
	if cfg.Width*cfg.Height > MaxUploadPixels {
		return nil, errors.New("图片太大，请先缩小到 4000 万像素以内")
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("图片已损坏，无法解码")
	}
	return encodePhoto(src, quality)
}

func encodePhoto(src image.Image, quality int) ([]byte, error) {
	b := src.Bounds()
	cw, ch := b.Dx(), b.Dx()*PhotoHeight/PhotoWidth
	if ch > b.Dy() {
		cw, ch = b.Dy()*PhotoWidth/PhotoHeight, b.Dy()
	}
	crop := image.Rect(0, 0, cw, ch).Add(b.Min).Add(image.Pt((b.Dx()-cw)/2, (b.Dy()-ch)/2))
	dst := image.NewRGBA(image.Rect(0, 0, PhotoWidth, PhotoHeight))
	xdraw.CatmullRom.Scale(dst, dst.Rect, src, crop, xdraw.Src, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
