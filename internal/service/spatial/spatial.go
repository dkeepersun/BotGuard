// Package spatial generates spatial-reasoning puzzles: "click the shape right
// of the red circle". The answer is never the shape the prompt names but its
// neighbour, so reading the prompt and finding the named shape is not enough;
// the relation has to be understood (docs/challenge-catalog.md §3.4).
//
// Shapes sit on a loose 4×3 grid. A question is only asked when the named
// shape is the only one of its colour and kind, and the neighbouring cell in
// that direction holds exactly one shape, so every answer is unambiguous.
package spatial

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"math/rand/v2"
	"strings"

	"github.com/dkeepersun/BotGuard/internal/service/click"
	"github.com/dkeepersun/BotGuard/internal/service/imagegen"
	"github.com/dkeepersun/BotGuard/internal/service/prompt"
)

const (
	Width  = 320
	Height = 200
	cols   = 4
	rows   = 3
)

type namedColor struct {
	name prompt.Text
	c    color.RGBA
}

var palette = []namedColor{
	{prompt.Text{prompt.ZhCN: "红色", prompt.ZhTW: "紅色", prompt.En: "red"}, color.RGBA{220, 50, 55, 255}},
	{prompt.Text{prompt.ZhCN: "蓝色", prompt.ZhTW: "藍色", prompt.En: "blue"}, color.RGBA{45, 95, 215, 255}},
	{prompt.Text{prompt.ZhCN: "绿色", prompt.ZhTW: "綠色", prompt.En: "green"}, color.RGBA{35, 150, 85, 255}},
	{prompt.Text{prompt.ZhCN: "黄色", prompt.ZhTW: "黃色", prompt.En: "yellow"}, color.RGBA{240, 185, 0, 255}},
	{prompt.Text{prompt.ZhCN: "紫色", prompt.ZhTW: "紫色", prompt.En: "purple"}, color.RGBA{135, 70, 190, 255}},
}

var shapeNames = []prompt.Text{
	{prompt.ZhCN: "圆形", prompt.ZhTW: "圓形", prompt.En: "circle"},
	{prompt.ZhCN: "正方形", prompt.ZhTW: "正方形", prompt.En: "square"},
	{prompt.ZhCN: "三角形", prompt.ZhTW: "三角形", prompt.En: "triangle"},
	{prompt.ZhCN: "五角星", prompt.ZhTW: "五角星", prompt.En: "star"},
	{prompt.ZhCN: "菱形", prompt.ZhTW: "菱形", prompt.En: "diamond"},
	{prompt.ZhCN: "六边形", prompt.ZhTW: "六邊形", prompt.En: "hexagon"},
}

type object struct {
	col, row int
	x, y, r  float64
	shape    int
	color    int
}

// name is the object's colour and shape in lang ("红色圆形", "red circle").
func (o object) name(lang string) string {
	if lang == prompt.En {
		return palette[o.color].name[lang] + " " + shapeNames[o.shape][lang]
	}
	return palette[o.color].name[lang] + shapeNames[o.shape][lang]
}

func (o object) inside(x, y float64) bool {
	dx, dy := (x-o.x)/o.r, (y-o.y)/o.r
	switch o.shape {
	case 0:
		return dx*dx+dy*dy <= 1
	case 1:
		return math.Abs(dx) <= 0.8 && math.Abs(dy) <= 0.8
	case 2:
		return imagegen.InPolygon(dx, dy, [][2]float64{{0, -1}, {0.95, 0.75}, {-0.95, 0.75}})
	case 3:
		var pts [][2]float64
		for i := 0; i < 10; i++ {
			rr := 1.0
			if i%2 == 1 {
				rr = 0.42
			}
			a := -math.Pi/2 + math.Pi*float64(i)/5
			pts = append(pts, [2]float64{math.Cos(a) * rr, math.Sin(a) * rr})
		}
		return imagegen.InPolygon(dx, dy, pts)
	case 4:
		return math.Abs(dx)+math.Abs(dy) <= 1
	default:
		var pts [][2]float64
		for i := 0; i < 6; i++ {
			a := math.Pi * float64(i) / 3
			pts = append(pts, [2]float64{math.Cos(a), math.Sin(a)})
		}
		return imagegen.InPolygon(dx, dy, pts)
	}
}

type direction struct {
	name   prompt.Text // "右边", "to the right of"
	dc, dr int
}

var directions = []direction{
	{prompt.Text{prompt.ZhCN: "右边", prompt.ZhTW: "右邊", prompt.En: "to the right of"}, 1, 0},
	{prompt.Text{prompt.ZhCN: "左边", prompt.ZhTW: "左邊", prompt.En: "to the left of"}, -1, 0},
	{prompt.Text{prompt.ZhCN: "上方", prompt.ZhTW: "上方", prompt.En: "above"}, 0, -1},
	{prompt.Text{prompt.ZhCN: "下方", prompt.ZhTW: "下方", prompt.En: "below"}, 0, 1},
}

type question struct {
	anchor, target int
	dir            direction
}

// layout fills 8–10 of the 12 cells and returns the objects.
func layout(r *rand.Rand) []object {
	cw, ch := float64(Width)/cols, float64(Height)/rows
	cells := r.Perm(cols * rows)[:8+r.IntN(3)]
	objs := make([]object, 0, len(cells))
	for _, cell := range cells {
		col, row := cell%cols, cell/cols
		objs = append(objs, object{col: col, row: row,
			x: cw*(float64(col)+0.5) + (r.Float64()*2-1)*6, y: ch*(float64(row)+0.5) + (r.Float64()*2-1)*5,
			r: 17 + r.Float64()*4, shape: r.IntN(len(shapeNames)), color: r.IntN(len(palette))})
	}
	return objs
}

// questions lists every unambiguous "neighbour of a uniquely named shape".
func questions(objs []object) []question {
	at := map[[2]int]int{}
	names := map[string]int{}
	for i, o := range objs {
		at[[2]int{o.col, o.row}] = i
		names[o.name(prompt.ZhCN)]++
	}
	var out []question
	for i, o := range objs {
		if names[o.name(prompt.ZhCN)] != 1 {
			continue
		}
		for _, d := range directions {
			if j, ok := at[[2]int{o.col + d.dc, o.row + d.dr}]; ok {
				out = append(out, question{anchor: i, target: j, dir: d})
			}
		}
	}
	return out
}

// Generate asks two questions about different shapes.
func Generate(r *rand.Rand, src imagegen.Source) (click.Puzzle, error) {
	for attempt := 0; attempt < 100; attempt++ {
		objs := layout(r)
		qs := questions(objs)
		if len(qs) < 2 {
			continue
		}
		r.Shuffle(len(qs), func(i, j int) { qs[i], qs[j] = qs[j], qs[i] })
		first := qs[0]
		var second *question
		for k := 1; k < len(qs); k++ {
			q := qs[k]
			if q.target != first.target && q.anchor != first.anchor && q.target != first.anchor && q.anchor != first.target {
				second = &q
				break
			}
		}
		if second == nil {
			continue
		}
		bg, _ := src.Background(r, Width, Height)
		white := color.RGBA{255, 255, 255, 255}
		for y := 0; y < Height; y++ {
			for x := 0; x < Width; x++ {
				imagegen.Blend(bg, x, y, white, 0.5)
			}
		}
		imagegen.Clutter(r, bg, false)
		for _, o := range objs {
			drawObject(bg, o)
		}
		imagegen.Finish(r, bg)
		url, err := imagegen.JPEG(r, bg)
		if err != nil {
			return click.Puzzle{}, err
		}
		var cn, tw, en []string
		var targets []click.Target
		for n, q := range []question{first, *second} {
			num := []string{"①", "②"}[n]
			a := objs[q.anchor]
			cn = append(cn, fmt.Sprintf("%s%s%s紧挨着的图形", num, a.name(prompt.ZhCN), q.dir.name[prompt.ZhCN]))
			tw = append(tw, fmt.Sprintf("%s%s%s緊挨著的圖形", num, a.name(prompt.ZhTW), q.dir.name[prompt.ZhTW]))
			en = append(en, fmt.Sprintf("%s the shape directly %s the %s", num, q.dir.name[prompt.En], a.name(prompt.En)))
			t := objs[q.target]
			targets = append(targets, click.Target{X: t.x, Y: t.y, R: t.r})
		}
		t := prompt.Text{
			prompt.ZhCN: "依次点击：" + strings.Join(cn, "，"),
			prompt.ZhTW: "依次點擊：" + strings.Join(tw, "，"),
			prompt.En:   "Click in order: " + strings.Join(en, "; "),
		}
		return click.Puzzle{Image: url, Prompt: t[prompt.ZhCN], Prompts: t, Width: Width, Height: Height, Targets: targets}, nil
	}
	return click.Puzzle{}, fmt.Errorf("spatial: no unambiguous layout after 100 attempts")
}

func drawObject(img *image.RGBA, o object) {
	x0, y0, x1, y1 := int(o.x-o.r-4), int(o.y-o.r-4), int(o.x+o.r+5), int(o.y+o.r+5)
	outline := color.RGBA{30, 30, 30, 255}
	grown := o
	grown.r = o.r + 2.2
	imagegen.FillShape(img, x0, y0, x1, y1, outline, 0.85, grown.inside)
	imagegen.FillShape(img, x0, y0, x1, y1, palette[o.color].c, 1, o.inside)
}
