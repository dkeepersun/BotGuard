package spatial

import (
	"encoding/base64"
	"github.com/dkeepersun/BotGuard/internal/service/imagegen"
	"github.com/dkeepersun/BotGuard/internal/service/prompt"
	"math/rand/v2"
	"os"
	"strings"
	"testing"
)

// Every question names a unique shape whose neighbour in that direction is
// exactly the target.
func TestQuestionsAreUnambiguous(t *testing.T) {
	for seed := uint64(1); seed <= 200; seed++ {
		r := rand.New(rand.NewPCG(seed, 3))
		objs := layout(r)
		seen := map[[2]int]bool{}
		for _, o := range objs {
			if seen[[2]int{o.col, o.row}] {
				t.Fatal("two shapes in one cell")
			}
			seen[[2]int{o.col, o.row}] = true
		}
		for _, q := range questions(objs) {
			a, b := objs[q.anchor], objs[q.target]
			if b.col-a.col != q.dir.dc || b.row-a.row != q.dir.dr {
				t.Fatalf("seed %d: %s is not %s of %s", seed, b.name(prompt.ZhCN), q.dir.name[prompt.ZhCN], a.name(prompt.ZhCN))
			}
			n := 0
			for _, o := range objs {
				if o.name(prompt.ZhCN) == a.name(prompt.ZhCN) {
					n++
				}
			}
			if n != 1 {
				t.Fatalf("seed %d: %s is not unique", seed, a.name(prompt.ZhCN))
			}
		}
	}
}

func TestGenerate(t *testing.T) {
	for seed := uint64(1); seed <= 50; seed++ {
		p, err := Generate(rand.New(rand.NewPCG(seed, 9)), imagegen.Source{})
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Targets) != 2 || !strings.Contains(p.Prompt, "①") || !strings.Contains(p.Prompt, "紧挨着的图形") {
			t.Fatalf("%q", p.Prompt)
		}
	}
	if dir := os.Getenv("KIND_SAMPLE_DIR"); dir != "" {
		p, _ := Generate(rand.New(rand.NewPCG(7, 7)), imagegen.Source{})
		b, _ := base64.StdEncoding.DecodeString(p.Image[strings.Index(p.Image, ",")+1:])
		_ = os.WriteFile(dir+"/spatial.jpg", b, 0o644)
		_ = os.WriteFile(dir+"/spatial.txt", []byte(p.Prompt), 0o644)
	}
}
