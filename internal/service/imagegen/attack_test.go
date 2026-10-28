package imagegen_test

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"math/rand/v2"
	"os"
	"strings"
	"testing"

	"github.com/dkeepersun/BotGuard/internal/service/imagegen"
	"github.com/dkeepersun/BotGuard/internal/service/imagegen/photos"
	"github.com/dkeepersun/BotGuard/internal/service/slider"
)

// TestSliderAttacks measures two automated slider solvers on procedural and
// photo backgrounds; docs/challenge-catalog.md reports the numbers. It only
// logs: the rates are measurements, not requirements. Run with
//
//	BOTGUARD_ATTACK=1 go test -run SliderAttacks -v ./internal/service/imagegen
//
// Both solvers see only what a browser gets (background, piece, piece_y):
//
//   - continuity: take the two darkest slot-shaped dips on the piece's row
//     (the slot and its decoy) and keep the one whose surroundings continue
//     the piece's own edge pixels best;
//   - library: additionally holds the photo library and is told the exact
//     crop each picture came from, before warp and colour changes (an upper
//     bound: a real attacker has to search for it). It subtracts the crop to
//     find the two dips, then keeps the one where the picture is the photo
//     darkened evenly: the decoy's inside is a copy from elsewhere.
func TestSliderAttacks(t *testing.T) {
	if os.Getenv("BOTGUARD_ATTACK") == "" {
		t.Skip("set BOTGUARD_ATTACK=1 to run the slider attack measurement")
	}
	const n = 300
	lib := photos.Library()
	for _, c := range []struct {
		name string
		src  imagegen.Source
	}{
		{"procedural", imagegen.Source{}},
		{"photo", imagegen.Source{Photos: lib, Share: 1}},
	} {
		var cont, library, dips int
		for i := range n {
			var ref *image.RGBA
			src := imagegen.Traced(c.src, func(plain *image.RGBA) { ref = plain })
			p, err := slider.Generate(rand.New(rand.NewPCG(uint64(i), 77)), src)
			if err != nil {
				t.Fatal(err)
			}
			bg := toRGBA(decodeURL(t, p.Background))
			piece := toRGBA(decodeURL(t, p.Piece))
			m := pieceMask(piece)
			cands := darkest(bg, m, p.Y, func(x, y int) float64 { return luma(bg, x, y) })
			if near(cands, p.X) {
				dips++
			}
			if pick := bestContinuity(bg, piece, m, p.Y, cands); abs(pick-p.X) <= 4 {
				cont++
			}
			if ref != nil {
				res := residual(bg, ref)
				lc := darkest(bg, m, p.Y, func(x, y int) float64 { return res[y*bg.Rect.Dx()+x] })
				if pick := bestMatch(bg.Rect.Dx(), res, m, p.Y, lc); abs(pick-p.X) <= 4 {
					library++
				}
			}
		}
		libRate := "—"
		if c.src.Photos != nil {
			libRate = fmt.Sprintf("%.1f%%", pct(library, n))
		}
		t.Logf("%-10s slot among the two darkest dips %5.1f%%  continuity solver %5.1f%%  library solver %s",
			c.name, pct(dips, n), pct(cont, n), libRate)
	}
}

func pct(a, n int) float64 { return float64(a) * 100 / float64(n) }

func abs(v int) int { return max(v, -v) }

func near(xs []int, x int) bool {
	for _, c := range xs {
		if abs(c-x) <= 4 {
			return true
		}
	}
	return false
}

func decodeURL(t *testing.T, url string) image.Image {
	t.Helper()
	i := strings.IndexByte(url, ',')
	raw, err := base64.StdEncoding.DecodeString(url[i+1:])
	if err != nil {
		t.Fatal(err)
	}
	var img image.Image
	if strings.HasPrefix(url, "data:image/png") {
		img, err = png.Decode(bytes.NewReader(raw))
	} else {
		img, err = jpeg.Decode(bytes.NewReader(raw))
	}
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func toRGBA(img image.Image) *image.RGBA {
	out := image.NewRGBA(img.Bounds())
	draw.Draw(out, out.Rect, img, img.Bounds().Min, draw.Src)
	return out
}

func luma(img *image.RGBA, x, y int) float64 {
	i := img.PixOffset(x, y)
	return 0.299*float64(img.Pix[i]) + 0.587*float64(img.Pix[i+1]) + 0.114*float64(img.Pix[i+2])
}

// pieceMask lists the piece's opaque pixels.
func pieceMask(piece *image.RGBA) [][2]int {
	var m [][2]int
	for y := 0; y < piece.Rect.Dy(); y++ {
		for x := 0; x < piece.Rect.Dx(); x++ {
			if piece.Pix[piece.PixOffset(x, y)+3] > 128 {
				m = append(m, [2]int{x, y})
			}
		}
	}
	return m
}

// darkest returns the two slot positions on row y where v is lowest inside
// the piece's shape, at least a piece apart.
func darkest(bg *image.RGBA, m [][2]int, y int, v func(x, y int) float64) []int {
	w := bg.Rect.Dx()
	score := make([]float64, w-slider.PieceSize)
	for x := range score {
		s := 0.0
		for _, p := range m {
			s += v(x+p[0], y+p[1])
		}
		score[x] = s / float64(len(m))
	}
	var out []int
	for range 2 {
		best := -1
		for x, s := range score {
			ok := true
			for _, o := range out {
				if abs(x-o) < slider.PieceSize {
					ok = false
				}
			}
			if ok && (best < 0 || s < score[best]) {
				best = x
			}
		}
		out = append(out, best)
	}
	return out
}

// bestContinuity keeps the candidate whose surroundings best continue the
// piece: each edge pixel of the piece, two pixels in, against the
// background one pixel outside the slot in the same direction.
func bestContinuity(bg, piece *image.RGBA, m [][2]int, y int, cands []int) int {
	in := map[[2]int]bool{}
	for _, p := range m {
		in[p] = true
	}
	best, bestScore := cands[0], math.Inf(1)
	for _, x := range cands {
		sum, n := 0.0, 0
		for _, p := range m {
			for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				out := [2]int{p[0] + d[0], p[1] + d[1]}
				inner := [2]int{p[0] - 2*d[0], p[1] - 2*d[1]}
				if in[out] || !in[inner] {
					continue
				}
				bx, by := x+out[0], y+out[1]
				if bx < 0 || by < 0 || bx >= bg.Rect.Dx() || by >= bg.Rect.Dy() {
					continue
				}
				sum += math.Abs(luma(piece, inner[0], inner[1]) - luma(bg, bx, by))
				n++
			}
		}
		if n > 0 && sum/float64(n) < bestScore {
			best, bestScore = x, sum/float64(n)
		}
	}
	return best
}

// residual fits img ≈ a·ref + b by least squares on luma and returns, per
// pixel, the picture over the fit.
func residual(img, ref *image.RGBA) []float64 {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	var sx, sy, sxx, sxy float64
	n := float64(w * h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a, b := luma(ref, x, y), luma(img, x, y)
			sx, sy, sxx, sxy = sx+a, sy+b, sxx+a*a, sxy+a*b
		}
	}
	k := (n*sxy - sx*sy) / (n*sxx - sx*sx)
	c := (sy - k*sx) / n
	out := make([]float64, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// A ratio: the slot darkens by a factor, whatever the photo's
			// brightness there.
			out[y*w+x] = (luma(img, x, y) + 8) / (max(0, k*luma(ref, x, y)+c) + 8)
		}
	}
	return out
}

// bestMatch keeps the candidate where the picture over the library crop is
// most even: the slot darkens the photo by one factor, while the decoy's
// inside is a copy that does not follow the photo at that place. Only the
// core of the shape counts (the rim is lightened), at the best of a few
// offsets around each candidate.
func bestMatch(w int, res []float64, m [][2]int, y int, cands []int) int {
	core := erode(m, 3)
	best, bestSD := cands[0], math.Inf(1)
	for _, c := range cands {
		for x := c - 2; x <= c+2; x++ {
			if x < 0 || x+slider.PieceSize > w {
				continue
			}
			var s, ss float64
			for _, p := range core {
				v := res[(y+p[1])*w+x+p[0]]
				s, ss = s+v, ss+v*v
			}
			n := float64(len(core))
			if sd := math.Sqrt(max(0, ss/n-(s/n)*(s/n))); sd < bestSD {
				best, bestSD = x, sd
			}
		}
	}
	return best
}

// erode keeps the pixels of m at least d pixels inside it.
func erode(m [][2]int, d int) [][2]int {
	in := map[[2]int]bool{}
	for _, p := range m {
		in[p] = true
	}
	var out [][2]int
	for _, p := range m {
		ok := true
		for dy := -d; dy <= d && ok; dy++ {
			for dx := -d; dx <= d; dx++ {
				if !in[[2]int{p[0] + dx, p[1] + dy}] {
					ok = false
					break
				}
			}
		}
		if ok {
			out = append(out, p)
		}
	}
	return out
}
