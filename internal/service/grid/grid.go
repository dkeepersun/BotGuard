// Package grid defines select-the-cells challenges and checks answers.
//
// The picture is split into a grid of cells and the prompt describes which
// to select ("every cell with exactly 4 triangles"); the user toggles cells
// and confirms. Unlike a multiple-choice question, every subset is a possible
// answer, so a blind guess on a 3×3 grid succeeds about once in 511 tries.
// The selection is derived on the server from the clicks themselves, and the
// clicks pass the same timing and movement rules as click kinds.
package grid

import (
	"image"
	"image/color"
	"math"
	"slices"

	"github.com/dkeepersun/BotGuard/internal/service/click"
	"github.com/dkeepersun/BotGuard/internal/service/imagegen"
)

// Puzzle is one generated challenge. Answer must never reach the browser
// except sealed inside the signed challenge.
type Puzzle struct {
	Image string // data URL
	// PromptImage, when set, shows a reference picture (mirror).
	PromptImage string
	Prompt      string
	Width       int
	Height      int
	Rows        int
	Cols        int
	// Answer lists the cells to select, row by row from 0, ascending.
	Answer []int
}

// MaxClicks bounds the clicks of one answer: enough to select and unselect
// every cell of a 3×3 grid with room for second thoughts.
const MaxClicks = 24

// Rejection reasons beyond the click package's, logged server-side only.
const (
	ReasonOutside   = "OUTSIDE"
	ReasonSelection = "SELECTION"
)

// CellAt returns the cell under image point (x, y), or -1 outside.
func CellAt(width, height, rows, cols int, x, y float64) int {
	if x < 0 || y < 0 || x >= float64(width) || y >= float64(height) {
		return -1
	}
	return int(y*float64(rows)/float64(height))*cols + int(x*float64(cols)/float64(width))
}

// Selected replays clicks ([ms, x, y]) as toggles and returns the cells left
// selected, ascending, or ok false when a click lands outside the image.
func Selected(width, height, rows, cols int, clicks [][3]float64) (cells []int, ok bool) {
	on := make([]bool, rows*cols)
	for _, c := range clicks {
		i := CellAt(width, height, rows, cols, c[1], c[2])
		if i < 0 {
			return nil, false
		}
		on[i] = !on[i]
	}
	for i, v := range on {
		if v {
			cells = append(cells, i)
		}
	}
	return cells, true
}

// Equal reports whether two ascending cell lists match.
func Equal(a, b []int) bool { return slices.Equal(a, b) }

// Check validates an answer: the clicks must leave exactly the answer cells
// selected, and pass click.Rhythm. input is the pointer type.
func Check(p Puzzle, clicks [][3]float64, movesBetween []int, input string) string {
	if len(clicks) == 0 || len(clicks) > MaxClicks {
		return click.ReasonCount
	}
	cells, ok := Selected(p.Width, p.Height, p.Rows, p.Cols, clicks)
	if !ok {
		return ReasonOutside
	}
	if !Equal(cells, p.Answer) {
		return ReasonSelection
	}
	return click.Rhythm(clicks, movesBetween, input)
}

// Cell returns cell i's rectangle in image pixels.
func Cell(width, height, rows, cols, i int) (x0, y0, x1, y1 float64) {
	cw, ch := float64(width)/float64(cols), float64(height)/float64(rows)
	c, r := float64(i%cols), float64(i/cols)
	return c * cw, r * ch, (c + 1) * cw, (r + 1) * ch
}

// Divide draws the white lines between cells, so the grid the user selects
// from is the grid the server checks against.
func Divide(img *image.RGBA, rows, cols int) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	white := color.RGBA{255, 255, 255, 255}
	for c := 1; c < cols; c++ {
		x := float64(w) * float64(c) / float64(cols)
		imagegen.FillShape(img, int(x)-3, 0, int(x)+4, h, white, 1, func(px, _ float64) bool { return math.Abs(px-x) <= 1.5 })
	}
	for r := 1; r < rows; r++ {
		y := float64(h) * float64(r) / float64(rows)
		imagegen.FillShape(img, 0, int(y)-3, w, int(y)+4, white, 1, func(_, py float64) bool { return math.Abs(py-y) <= 1.5 })
	}
}
