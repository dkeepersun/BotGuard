package clicktext

import (
	"bytes"
	"encoding/base64"
	"image/jpeg"
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/dkeepersun/BotGuard/internal/service/click"
)

func seeded(n uint64) *rand.Rand {
	var seed [32]byte
	seed[0], seed[1] = byte(n), byte(n>>8)
	return rand.New(rand.NewChaCha8(seed))
}

func TestCharset(t *testing.T) {
	seen := map[rune]bool{}
	for _, c := range Chars {
		if seen[c] {
			t.Fatalf("duplicate %q", c)
		}
		seen[c] = true
	}
	for _, c := range "己已巳土士未末人入日曰大太犬天王玉午牛一十口" {
		if seen[c] {
			t.Fatalf("look-alike %q must not be in the charset", c)
		}
	}
	if _, err := loadAtlas(); err != nil {
		t.Fatal(err)
	}
}

func TestGenerate(t *testing.T) {
	a, err := Generate(seeded(1))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Generate(seeded(1))
	if a.Prompt != b.Prompt || a.Image != b.Image {
		t.Fatal("same seed must give the same puzzle")
	}
	for i := uint64(0); i < 200; i++ {
		p, err := Generate(seeded(i))
		if err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
		words := strings.Split(strings.TrimPrefix(p.Prompt, "请依次点击："), "、")
		if len(words) != len(p.Targets) || len(words) < 3 || len(words) > 4 {
			t.Fatalf("seed %d: prompt %q targets %d", i, p.Prompt, len(p.Targets))
		}
		for j, w := range words {
			if utf8.RuneCountInString(w) != 1 || !strings.Contains(Chars, w) {
				t.Fatalf("seed %d: word %q", i, w)
			}
			tg := p.Targets[j]
			if tg.X-tg.R < 0 || tg.Y-tg.R < 0 || tg.X+tg.R > Width || tg.Y+tg.R > Height {
				t.Fatalf("seed %d: target outside image", i)
			}
			others := append(append([]click.Target{}, p.Targets[:j]...), p.Targets[j+1:]...)
			if click.Overlaps(others, tg.X, tg.Y, tg.R, 0) {
				t.Fatalf("seed %d: overlapping targets", i)
			}
		}
	}
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(a.Image, "data:image/jpeg;base64,"))
	if img, err := jpeg.Decode(bytes.NewReader(raw)); err != nil || img.Bounds().Dx() != Width {
		t.Fatalf("image: %v", err)
	}
}

func BenchmarkGenerate(b *testing.B) {
	r := seeded(9)
	for b.Loop() {
		if _, err := Generate(r); err != nil {
			b.Fatal(err)
		}
	}
}
