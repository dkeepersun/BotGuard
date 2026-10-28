package clock

import (
	"bytes"
	"encoding/base64"
	"image/jpeg"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/dkeepersun/BotGuard/internal/service/click"
)

func seeded(n uint64) *rand.Rand {
	var seed [32]byte
	seed[0], seed[1] = byte(n), byte(n>>8)
	return rand.New(rand.NewChaCha8(seed))
}

func TestTimeReading(t *testing.T) {
	cases := []struct {
		t                 Time
		text              string
		swapped, mirrored Time
	}{
		// 3:40: hour hand at 110°, minute hand at 240°.
		{3*60 + 40, "3:40", 8*60 + 20, 8*60 + 20},
		// 12:10: hour at 5°, minute at 60°.
		{10, "12:10", 2*60 + 0, 11*60 + 50},
		// 9:15: hour at 277.5°, minute at 90°.
		{9*60 + 15, "9:15", 3*60 + 45, 2*60 + 45},
	}
	for _, c := range cases {
		if got := c.t.String(); got != c.text {
			t.Errorf("%d: String %q, want %q", c.t, got, c.text)
		}
		if got := c.t.swapped(); got != c.swapped {
			t.Errorf("%s: swapped %s, want %s", c.text, got, c.swapped)
		}
		if got := c.t.mirrored(); got != c.mirrored {
			t.Errorf("%s: mirrored %s, want %s", c.text, got, c.mirrored)
		}
	}
	// 12:00 and 6:30 (hands 15° apart) are unreadable; 3:00 is fine.
	if Time(0).readable() || Time(6*60+30).readable() {
		t.Error("12:00 and 6:30 have overlapping hands")
	}
	if !Time(3 * 60).readable() {
		t.Error("3:00 should be readable")
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
	for i := uint64(0); i < 300; i++ {
		p, faces, err := generate(seeded(i))
		if err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
		if n := len(faces); n < 4 || n > 5 {
			t.Fatalf("seed %d: %d clocks", i, n)
		}
		if len(p.Targets) != Targets || strings.Count(p.Prompt, "的时钟") != Targets {
			t.Fatalf("seed %d: prompt %q, %d targets", i, p.Prompt, len(p.Targets))
		}
		for j, f := range faces {
			if !f.t.readable() {
				t.Fatalf("seed %d: %s has overlapping hands", i, f.t)
			}
			if f.x-f.r < 0 || f.y-f.r < 0 || f.x+f.r > Width || f.y+f.r > Height {
				t.Fatalf("seed %d: clock outside the image", i)
			}
			for k, g := range faces {
				if k != j && apart(f.t, g.t) < 5 {
					t.Fatalf("seed %d: two clocks show %s and %s", i, f.t, g.t)
				}
			}
		}
		for j, tg := range p.Targets {
			f := faces[j]
			if tg.X != f.x || tg.Y != f.y || !strings.Contains(p.Prompt, "显示 "+f.t.String()+" 的时钟") {
				t.Fatalf("seed %d: target %d does not match %s", i, j, f.t)
			}
			if click.Overlaps(p.Targets[:j], tg.X, tg.Y, tg.R, 0) {
				t.Fatalf("seed %d: overlapping targets", i)
			}
		}
		if apart(faces[0].t, faces[1].t) < 30 {
			t.Fatalf("seed %d: targets %s and %s too close", i, faces[0].t, faces[1].t)
		}
	}
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(a.Image, "data:image/jpeg;base64,"))
	if img, err := jpeg.Decode(bytes.NewReader(raw)); err != nil || img.Bounds().Dx() != Width {
		t.Fatalf("image: %v", err)
	}
}

// Most puzzles should carry at least one decoy a model confuses with a
// target: its swapped or mirrored reading, or a time a few minutes off.
func TestConfusableDecoys(t *testing.T) {
	with := 0
	for i := uint64(0); i < 200; i++ {
		_, faces, err := generate(seeded(i))
		if err != nil {
			t.Fatal(err)
		}
	found:
		for _, d := range faces[Targets:] {
			for _, tg := range faces[:Targets] {
				if d.t == tg.t.swapped() || d.t == tg.t.mirrored() || apart(d.t, tg.t) <= 10 {
					with++
					break found
				}
			}
		}
	}
	if with < 180 {
		t.Fatalf("only %d of 200 puzzles have a confusable decoy", with)
	}
}

func BenchmarkGenerate(b *testing.B) {
	r := seeded(1)
	for b.Loop() {
		if _, err := Generate(r); err != nil {
			b.Fatal(err)
		}
	}
}
