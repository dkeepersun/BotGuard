// Command glyphatlas renders clicktext.Chars from a CJK font into the
// grayscale atlas embedded by internal/service/clicktext. The font stays out
// of the repository; only the generated bitmap and the font licence are kept.
//
//	go run ./tools/glyphatlas -font third_party/fonts/NotoSansSC-Regular.otf
package main

import (
	"flag"
	"fmt"
	"image"
	"image/png"
	"log"
	"os"
	"unicode/utf8"

	"github.com/dkeepersun/BotGuard/internal/service/clicktext"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

func main() {
	fontPath := flag.String("font", "third_party/fonts/NotoSansSC-Regular.otf", "OpenType font file")
	out := flag.String("out", "internal/service/clicktext/assets/glyphs.png", "atlas PNG to write")
	flag.Parse()

	raw, err := os.ReadFile(*fontPath)
	if err != nil {
		log.Fatal(err)
	}
	f, err := opentype.Parse(raw)
	if err != nil {
		log.Fatal(err)
	}
	cell := clicktext.Cell
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: float64(cell) * 0.86, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		log.Fatal(err)
	}
	defer face.Close()

	chars := []rune(clicktext.Chars)
	rows := (len(chars) + clicktext.Columns - 1) / clicktext.Columns
	atlas := image.NewGray(image.Rect(0, 0, clicktext.Columns*cell, rows*cell))
	for i, ch := range chars {
		if _, ok := face.GlyphAdvance(ch); !ok {
			log.Fatalf("font has no glyph for %q", ch)
		}
		// Centre the glyph's ink box in its cell.
		bounds, _ := font.BoundString(face, string(ch))
		w := (bounds.Max.X - bounds.Min.X).Ceil()
		h := (bounds.Max.Y - bounds.Min.Y).Ceil()
		ox := (i%clicktext.Columns)*cell + (cell-w)/2 - bounds.Min.X.Floor()
		oy := (i/clicktext.Columns)*cell + (cell-h)/2 - bounds.Min.Y.Floor()
		d := &font.Drawer{Dst: atlas, Src: image.White, Face: face, Dot: fixed.P(ox, oy)}
		d.DrawString(string(ch))
	}
	file, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, atlas); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %s: %d glyphs, %dx%d\n", *out, utf8.RuneCountInString(clicktext.Chars), atlas.Bounds().Dx(), atlas.Bounds().Dy())
}
