package jigsaw

import (
	"encoding/base64"
	"math"
	"math/rand/v2"
	"os"
	"strings"
	"testing"
)

// humanPath is an eased, wobbly drag from the centre of one tile to another.
func humanPath(from, to int) [][3]float64 {
	x0, y0 := float64(from%Cols*TileW+TileW/2), float64(from/Cols*TileH+TileH/2)
	x1, y1 := float64(to%Cols*TileW+TileW/2), float64(to/Cols*TileH+TileH/2)
	var out [][3]float64
	t := 0.0
	for i := 0; i <= 30; i++ {
		p := float64(i) / 30
		e := p * p * (3 - 2*p)
		out = append(out, [3]float64{t, x0 + (x1-x0)*e + math.Sin(p*5)*3, y0 + (y1-y0)*e + math.Cos(p*4)*2})
		t += 12 + float64(i%4)*3
	}
	return out
}

func TestCheck(t *testing.T) {
	good := humanPath(1, 6)
	if r := Check(1, 6, []int{6, 1}, good, "mouse"); r != "" {
		t.Fatalf("human drag: %s", r)
	}
	if r := Check(1, 6, []int{1, 5}, good, "mouse"); r != ReasonPair {
		t.Fatalf("wrong pair: %s", r)
	}
	if r := Check(1, 6, []int{1, 6}, humanPath(2, 6), "mouse"); r != ReasonEnds {
		t.Fatalf("drag from another tile: %s", r)
	}
	var straight [][3]float64
	for i := 0; i <= 30; i++ {
		p := float64(i) / 30
		straight = append(straight, [3]float64{float64(i)*16 + float64(i%3)*4, 120 + 160*p*p*(3-2*p), 50})
	}
	if r := Check(1, 3, []int{1, 3}, straight, "mouse"); r != ReasonStraightLine {
		t.Fatalf("ruler-straight mouse drag: %s", r)
	}
	if r := Check(1, 3, []int{1, 3}, straight, "touch"); r != "" {
		t.Fatalf("touch drag: %s", r)
	}
	jump := [][3]float64{{0, 120, 50}, {50, 121, 50}, {100, 122, 51}, {150, 123, 50}, {200, 124, 51}, {300, 280, 50}}
	if r := Check(1, 3, []int{1, 3}, jump, "touch"); r != ReasonTeleport {
		t.Fatalf("teleport: %s", r)
	}
}

func TestGenerate(t *testing.T) {
	for seed := uint64(1); seed <= 20; seed++ {
		p, err := Generate(rand.New(rand.NewPCG(seed, 2)))
		if err != nil {
			t.Fatal(err)
		}
		if p.A == p.B || p.A < 0 || p.B >= Rows*Cols || p.A > p.B {
			t.Fatalf("%d %d", p.A, p.B)
		}
	}
	if TileAt(0, 0) != 0 || TileAt(319, 199) != 7 || TileAt(-1, 5) != -1 {
		t.Fatal("TileAt")
	}
	if dir := os.Getenv("KIND_SAMPLE_DIR"); dir != "" {
		p, _ := Generate(rand.New(rand.NewPCG(4, 4)))
		b, _ := base64.StdEncoding.DecodeString(p.Image[strings.Index(p.Image, ",")+1:])
		_ = os.WriteFile(dir+"/jigsaw.jpg", b, 0o644)
		_ = os.WriteFile(dir+"/jigsaw.txt", []byte(string(rune('0'+p.A))+","+string(rune('0'+p.B))), 0o644)
	}
}
