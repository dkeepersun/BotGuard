package grid

import (
	"testing"

	"github.com/dkeepersun/BotGuard/internal/service/click"
)

func TestCellAt(t *testing.T) {
	for _, c := range []struct {
		x, y float64
		want int
	}{{0, 0, 0}, {106, 10, 0}, {107, 10, 1}, {319.9, 199.9, 8}, {160, 100, 4}, {-1, 5, -1}, {320, 5, -1}} {
		if got := CellAt(320, 200, 3, 3, c.x, c.y); got != c.want {
			t.Errorf("(%v, %v): %d, want %d", c.x, c.y, got, c.want)
		}
	}
}

func TestCheck(t *testing.T) {
	p := Puzzle{Width: 320, Height: 200, Rows: 3, Cols: 3, Answer: []int{0, 4, 8}}
	human := [][3]float64{{1500, 50, 30}, {2350, 160, 100}, {3050, 270, 170}}
	moves := []int{5, 9, 7}
	cases := []struct {
		name   string
		clicks [][3]float64
		moves  []int
		input  string
		want   string
	}{
		{"correct", human, moves, "mouse", ""},
		// Selecting a wrong cell and taking it back still counts.
		{"second thoughts", [][3]float64{{1500, 50, 30}, {2100, 160, 30}, {2800, 160, 40}, {3500, 160, 100}, {4300, 270, 170}}, []int{4, 6, 5, 8, 9}, "mouse", ""},
		{"missing cell", human[:2], moves[:2], "mouse", ReasonSelection},
		{"extra cell", append(append([][3]float64{}, human...), [3]float64{3900, 50, 170}), []int{5, 9, 7, 6}, "mouse", ReasonSelection},
		{"outside", [][3]float64{{1500, 50, 30}, {2300, 400, 100}}, []int{5, 9}, "mouse", ReasonOutside},
		{"no clicks", nil, nil, "mouse", click.ReasonCount},
		{"too soon", [][3]float64{{100, 50, 30}, {2300, 160, 100}, {3100, 270, 170}}, moves, "mouse", click.ReasonTooSoon},
		{"teleport", human, []int{5, 0, 0}, "mouse", click.ReasonTeleport},
		{"touch skips moves", human, []int{0, 0, 0}, "touch", ""},
		{"equal gaps", [][3]float64{{1500, 50, 30}, {2300, 160, 100}, {3100, 270, 170}, {3900, 270, 30}, {4700, 270, 30}}, []int{4, 4, 4, 4, 4}, "mouse", click.ReasonEqualGaps},
	}
	for _, c := range cases {
		if got := Check(p, c.clicks, c.moves, c.input); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	long := make([][3]float64, MaxClicks+1)
	if got := Check(p, long, nil, "mouse"); got != click.ReasonCount {
		t.Errorf("too many clicks: %q", got)
	}
}
