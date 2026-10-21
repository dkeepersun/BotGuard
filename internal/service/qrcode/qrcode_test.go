package qrcode

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"
)

func TestVersionsAndShape(t *testing.T) {
	for n, ver := range map[int]int{10: 1, 14: 1, 15: 2, 100: 6, 106: 6, 107: 7, 213: 10} {
		c, err := Encode([]byte(strings.Repeat("a", n)))
		if err != nil {
			t.Fatal(n, err)
		}
		if c.Size != ver*4+17 {
			t.Errorf("%d bytes: size %d, want version %d", n, c.Size, ver)
		}
	}
	if _, err := Encode(make([]byte, 214)); err != ErrTooLong {
		t.Fatal(err)
	}
	c, _ := Encode([]byte("x"))
	// Finder centre is dark, its inner ring light; the dark module is set.
	if !c.Modules[3][3] || c.Modules[1][3] || !c.Modules[c.Size-8][8] {
		t.Fatal("function patterns")
	}
	if svg := c.SVG(160); !strings.HasPrefix(svg, "<svg") || !strings.Contains(svg, "M4 4h1v1h-1z") {
		t.Fatal(svg[:80])
	}
}

// QR_DUMP_DIR writes PNGs for an external decoder (macOS CoreImage in the
// validation run; see docs/validation.md).
func TestDumpForExternalDecoder(t *testing.T) {
	dir := os.Getenv("QR_DUMP_DIR")
	if dir == "" {
		t.Skip("QR_DUMP_DIR not set")
	}
	inputs := []string{"hi", "https://example.com/botguard", strings.Repeat("0123456789", 7),
		"otpauth://totp/BotGuard:admin?algorithm=SHA1&digits=6&issuer=BotGuard&period=30&secret=JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP",
		strings.Repeat("Z", 150), strings.Repeat("q", 213)}
	for i, in := range inputs {
		c, err := Encode([]byte(in))
		if err != nil {
			t.Fatal(err)
		}
		scale := 8
		n := (c.Size + 8) * scale
		img := image.NewGray(image.Rect(0, 0, n, n))
		for y := 0; y < n; y++ {
			for x := 0; x < n; x++ {
				mx, my := x/scale-4, y/scale-4
				dark := mx >= 0 && my >= 0 && mx < c.Size && my < c.Size && c.Modules[my][mx]
				if dark {
					img.SetGray(x, y, color.Gray{0})
				} else {
					img.SetGray(x, y, color.Gray{255})
				}
			}
		}
		f, _ := os.Create(fmt.Sprintf("%s/%d.png", dir, i))
		_ = png.Encode(f, img)
		f.Close()
		_ = os.WriteFile(fmt.Sprintf("%s/%d.txt", dir, i), []byte(in), 0o644)
	}
}
