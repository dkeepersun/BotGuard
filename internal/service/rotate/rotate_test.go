package rotate

import (
	"bytes"
	"encoding/base64"
	"github.com/dkeepersun/BotGuard/internal/service/imagegen"
	"image"
	"image/png"
	"math"
	"math/rand/v2"
	"os"
	"strings"
	"testing"
	"time"
)

func decodePNG(t *testing.T, url string) image.Image {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, "data:image/png;base64,"))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// mismatch is the mean colour difference between the disc shown turned by
// turn degrees and the original picture under it.
func mismatch(disc image.Image, full *image.RGBA, p Puzzle, turn float64) float64 {
	sin, cos := math.Sincos(-turn * math.Pi / 180)
	half := float64(disc.Bounds().Dx()) / 2
	sum, n := 0.0, 0
	for y := -p.Radius + 4; y < p.Radius-4; y += 2 {
		for x := -p.Radius + 4; x < p.Radius-4; x += 2 {
			if math.Hypot(float64(x), float64(y)) > float64(p.Radius)-4 {
				continue
			}
			// Screen point p shows disc(Rot(-turn)·p).
			qx, qy := float64(x)*cos-float64(y)*sin, float64(x)*sin+float64(y)*cos
			dr, dg, db, _ := disc.At(int(qx+half), int(qy+half)).RGBA()
			o := full.RGBAAt(p.CX+x, p.CY+y)
			sum += math.Abs(float64(dr>>8)-float64(o.R)) + math.Abs(float64(dg>>8)-float64(o.G)) + math.Abs(float64(db>>8)-float64(o.B))
			n++
		}
	}
	return sum / float64(n) / 3
}

func TestTurningByTheAnswerRestoresThePicture(t *testing.T) {
	for seed := uint64(1); seed <= 20; seed++ {
		p, full, err := generate(rand.New(rand.NewPCG(seed, 7)), imagegen.Source{})
		if err != nil {
			t.Fatal(err)
		}
		if p.Angle < 30 || p.Angle > 330 {
			t.Fatalf("angle %f", p.Angle)
		}
		disc := decodePNG(t, p.Disc)
		right := mismatch(disc, full, p, p.Angle)
		wrong := mismatch(disc, full, p, p.Angle+25)
		if right > 8 || wrong < right*3 {
			t.Errorf("seed %d: right %.1f, 25° off %.1f", seed, right, wrong)
		}
	}
}

func TestBackgroundHidesTheDiscArea(t *testing.T) {
	p, _, err := generate(rand.New(rand.NewPCG(3, 3)), imagegen.Source{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.Background, "data:image/jpeg;base64,") || p.AnswerX() <= 0 || p.AnswerX() >= Track {
		t.Fatalf("%v %v", p.Background[:30], p.AnswerX())
	}
	if ToleranceX(7) != 5 {
		t.Fatal(ToleranceX(7))
	}
}

func TestGenerateIsFastEnough(t *testing.T) {
	r := rand.New(rand.NewPCG(9, 9))
	// The fastest of several batches: go test runs packages in parallel, and
	// a CI runner busy with them once failed a single 20-puzzle average.
	per := time.Duration(math.MaxInt64)
	for range 5 {
		start := time.Now()
		for i := 0; i < 4; i++ {
			if _, err := Generate(r, imagegen.Source{}); err != nil {
				t.Fatal(err)
			}
		}
		per = min(per, time.Since(start)/4)
	}
	t.Logf("%v per puzzle", per)
	if per > 40*time.Millisecond && !raceEnabled {
		t.Fatalf("too slow: %v", per)
	}
	if dir := os.Getenv("ROTATE_SAMPLE_DIR"); dir != "" {
		p, _ := Generate(r, imagegen.Source{})
		for name, url := range map[string]string{"bg.jpg": p.Background, "disc.png": p.Disc} {
			b, _ := base64.StdEncoding.DecodeString(url[strings.Index(url, ",")+1:])
			_ = os.WriteFile(dir+"/"+name, b, 0o644)
		}
	}
}
