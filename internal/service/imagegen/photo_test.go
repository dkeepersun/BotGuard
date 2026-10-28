package imagegen

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"testing"
)

// testLibrary is n procedural pictures stored as library photos.
func testLibrary(t *testing.T, n int) *Library {
	t.Helper()
	var jpegs [][]byte
	for i := range n {
		var b bytes.Buffer
		if err := jpeg.Encode(&b, Background(seeded(uint64(100+i)), PhotoWidth, PhotoHeight), nil); err != nil {
			t.Fatal(err)
		}
		jpegs = append(jpegs, b.Bytes())
	}
	return NewLibrary(jpegs)
}

func TestSourceShare(t *testing.T) {
	lib := testLibrary(t, 3)
	count := func(s Source) int {
		photos := 0
		for i := range 200 {
			if _, photo := s.Background(seeded(uint64(i)), 320, 200); photo {
				photos++
			}
		}
		return photos
	}
	if n := count(Source{}); n != 0 {
		t.Errorf("zero source drew %d photos", n)
	}
	if n := count(Source{Share: 1}); n != 0 {
		t.Errorf("source without photos drew %d photos", n)
	}
	if n := count(Source{Photos: lib, Share: 1}); n != 200 {
		t.Errorf("photo-only source drew %d/200 photos", n)
	}
	if n := count(Source{Photos: lib, Share: 0.5}); n < 70 || n > 130 {
		t.Errorf("mixed source drew %d/200 photos", n)
	}
}

// The same photo never gives the same picture twice, and a picture is
// neither the photo's plain crop nor equal to it up to one gain and offset.
func TestPhotoTreatment(t *testing.T) {
	lib := testLibrary(t, 1)
	var plain *image.RGBA
	s := Source{Photos: lib, Share: 1, trace: func(p *image.RGBA) { plain = p }}
	a, _ := s.Background(seeded(1), 320, 200)
	b, _ := s.Background(seeded(2), 320, 200)
	if bytes.Equal(a.Pix, b.Pix) {
		t.Fatal("two seeds gave the same picture")
	}
	again, _ := s.Background(seeded(2), 320, 200)
	if !bytes.Equal(b.Pix, again.Pix) {
		t.Fatal("same seed gave different pictures")
	}
	// Mean absolute difference from the plain crop after the best gain and
	// offset on luma: the warp, colour change and grain must leave a clear
	// residual everywhere.
	var sx, sy, sxx, sxy float64
	px := func(img *image.RGBA, i int) float64 {
		return 0.299*float64(img.Pix[i]) + 0.587*float64(img.Pix[i+1]) + 0.114*float64(img.Pix[i+2])
	}
	n := float64(len(b.Pix) / 4)
	for i := 0; i < len(b.Pix); i += 4 {
		u, v := px(plain, i), px(b, i)
		sx, sy, sxx, sxy = sx+u, sy+v, sxx+u*u, sxy+u*v
	}
	k := (n*sxy - sx*sy) / (n*sxx - sx*sx)
	c := (sy - k*sx) / n
	res := 0.0
	for i := 0; i < len(b.Pix); i += 4 {
		d := px(b, i) - (k*px(plain, i) + c)
		res += max(d, -d)
	}
	if res/n < 4 {
		t.Errorf("picture is the plain crop up to gain and offset (residual %.1f)", res/n)
	}
}

func TestJoin(t *testing.T) {
	a, b := testLibrary(t, 2), testLibrary(t, 3)
	if n := Join(a, nil, b).Len(); n != 5 {
		t.Fatalf("joined %d photos, want 5", n)
	}
	if Join().Len() != 0 || (*Library)(nil).Len() != 0 {
		t.Fatal("empty libraries")
	}
	// Joined libraries share decoded photos.
	j := Join(a, b)
	pa, _ := a.photo(1)
	pj, _ := j.photo(1)
	if pa != pj {
		t.Fatal("join decoded a photo again")
	}
}

func TestNormalize(t *testing.T) {
	encode := func(w, h int) []byte {
		var b bytes.Buffer
		if err := png.Encode(&b, Background(seeded(9), w, h)); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	for _, size := range [][2]int{{1200, 800}, {640, 400}, {800, 1200}} {
		out, err := Normalize(encode(size[0], size[1]))
		if err != nil {
			t.Fatalf("%v: %v", size, err)
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(out))
		if err != nil || format != "jpeg" || cfg.Width != PhotoWidth || cfg.Height != PhotoHeight {
			t.Fatalf("%v: got %s %dx%d, %v", size, format, cfg.Width, cfg.Height, err)
		}
	}
	if _, err := Normalize(encode(600, 400)); err != ErrUploadTooSmall {
		t.Errorf("small image: %v", err)
	}
	if _, err := Normalize([]byte("<svg></svg>")); err == nil {
		t.Error("accepted a non-image")
	}
}

func BenchmarkPhotoBackground(b *testing.B) {
	var jpegs [][]byte
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, Background(seeded(1), PhotoWidth, PhotoHeight), &jpeg.Options{Quality: 80}); err != nil {
		b.Fatal(err)
	}
	jpegs = append(jpegs, buf.Bytes(), buf.Bytes())
	s := Source{Photos: NewLibrary(jpegs), Share: 1}
	r := seeded(2)
	for b.Loop() {
		s.Background(r, 320, 200)
	}
}
