package click

import "testing"

var targets = []Target{{X: 50, Y: 60, R: 20}, {X: 200, Y: 40, R: 18}, {X: 120, Y: 150, R: 22}}

func TestCheck(t *testing.T) {
	human := [][3]float64{{1800, 52, 58}, {2650, 205, 44}, {3320, 118, 156}}
	moves := []int{0, 14, 9}
	cases := map[string]struct {
		clicks [][3]float64
		moves  []int
		input  string
		want   string
	}{
		"human mouse":     {human, moves, "mouse", ""},
		"edge of target":  {[][3]float64{{1800, 50 + 20 + 3.9, 60}, {2650, 200, 40}, {3320, 120, 150}}, moves, "mouse", ""},
		"wrong order":     {[][3]float64{{1800, 200, 40}, {2650, 50, 60}, {3320, 120, 150}}, moves, "mouse", ReasonPosition},
		"just outside":    {[][3]float64{{1800, 50 + 20 + 4.1, 60}, {2650, 200, 40}, {3320, 120, 150}}, moves, "mouse", ReasonPosition},
		"too few":         {human[:2], moves[:2], "mouse", ReasonCount},
		"no time to read": {[][3]float64{{120, 52, 58}, {900, 205, 44}, {1700, 118, 156}}, moves, "mouse", ReasonTooSoon},
		"machine gun":     {[][3]float64{{1800, 52, 58}, {1850, 205, 44}, {2500, 118, 156}}, moves, "mouse", ReasonTooFast},
		"metronome":       {[][3]float64{{1800, 52, 58}, {2400, 205, 44}, {3005, 118, 156}}, moves, "mouse", ReasonEqualGaps},
		"teleport clicks": {human, []int{5, 0, 1}, "mouse", ReasonTeleport},
		"missing moves":   {human, nil, "mouse", ReasonTeleport},
		"touch taps":      {human, []int{0, 0, 0}, "touch", ""},
		"out of order ts": {[][3]float64{{1800, 52, 58}, {1500, 205, 44}, {3320, 118, 156}}, moves, "mouse", ReasonTimeOrder},
		"too slow":        {[][3]float64{{1800, 52, 58}, {2650, 205, 44}, {61000, 118, 156}}, moves, "mouse", ReasonTooSlow},
	}
	for name, c := range cases {
		if got := Check(targets, c.clicks, c.moves, c.input); got != c.want {
			t.Errorf("%s: got %q want %q", name, got, c.want)
		}
	}
}

func TestOverlaps(t *testing.T) {
	if !Overlaps(targets, 60, 60, 10, 0) || Overlaps(targets, 300, 190, 5, 2) {
		t.Fatal("overlap")
	}
}
