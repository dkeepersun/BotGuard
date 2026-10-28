package prompt_test

import (
	"math/rand/v2"
	"testing"
	"unicode"

	"github.com/dkeepersun/BotGuard/internal/service/click"
	"github.com/dkeepersun/BotGuard/internal/service/clickicon"
	"github.com/dkeepersun/BotGuard/internal/service/clickshape"
	"github.com/dkeepersun/BotGuard/internal/service/clicktext"
	"github.com/dkeepersun/BotGuard/internal/service/clock"
	"github.com/dkeepersun/BotGuard/internal/service/grid"
	"github.com/dkeepersun/BotGuard/internal/service/gridcount"
	"github.com/dkeepersun/BotGuard/internal/service/imagegen"
	"github.com/dkeepersun/BotGuard/internal/service/mirror"
	"github.com/dkeepersun/BotGuard/internal/service/prompt"
	"github.com/dkeepersun/BotGuard/internal/service/spatial"
)

// Every prompted kind writes every language, and its English prompt has no
// Chinese except the characters to click (text kinds are not offered in
// English, see logic.textKinds).
func TestEveryKindHasEveryLanguage(t *testing.T) {
	clicks := map[string]func(*rand.Rand, imagegen.Source) (click.Puzzle, error){
		"click_text": clicktext.Generate, "click_icon": clickicon.Generate, "click_shape": clickshape.Generate,
		"clock": clock.Generate, "spatial": spatial.Generate,
	}
	grids := map[string]func(*rand.Rand, imagegen.Source) (grid.Puzzle, error){"grid_count": gridcount.Generate, "mirror": mirror.Generate}
	check := func(kind string, zh string, text prompt.Text) {
		if text[prompt.ZhCN] != zh {
			t.Errorf("%s: Prompt %q is not Prompts[zh-CN] %q", kind, zh, text[prompt.ZhCN])
		}
		for _, lang := range prompt.Langs {
			if text[lang] == "" {
				t.Errorf("%s: no %s prompt", kind, lang)
			}
		}
		if kind != "click_text" {
			for _, c := range text[prompt.En] {
				if unicode.Is(unicode.Han, c) {
					t.Errorf("%s: English prompt %q has Chinese", kind, text[prompt.En])
					break
				}
			}
		}
	}
	for seed := range uint64(5) {
		r := rand.New(rand.NewPCG(seed, 1))
		for kind, gen := range clicks {
			p, err := gen(r, imagegen.Source{})
			if err != nil {
				t.Fatal(kind, err)
			}
			check(kind, p.Prompt, p.Prompts)
		}
		for kind, gen := range grids {
			p, err := gen(r, imagegen.Source{})
			if err != nil {
				t.Fatal(kind, err)
			}
			check(kind, p.Prompt, p.Prompts)
		}
	}
	p, _ := spatial.Generate(rand.New(rand.NewPCG(1, 1)), imagegen.Source{})
	t.Log(p.Prompts[prompt.En])
	q, _ := clickshape.Generate(rand.New(rand.NewPCG(1, 1)), imagegen.Source{})
	t.Log(q.Prompts[prompt.En])
}
