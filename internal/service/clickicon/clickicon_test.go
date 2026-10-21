package clickicon

import (
	"encoding/base64"
	"math/rand/v2"
	"os"
	"strings"
	"testing"
	"time"
)

func TestIconsAreDistinctSilhouettes(t *testing.T) {
	// Sample each icon on a grid; no two may cover nearly the same cells.
	const n = 24
	masks := make([][n * n]bool, len(icons))
	for i, ic := range icons {
		filled := 0
		for y := 0; y < n; y++ {
			for x := 0; x < n; x++ {
				u, v := (float64(x)+0.5)/n*2-1, (float64(y)+0.5)/n*2-1
				masks[i][y*n+x] = ic.shape(u, v)
				if masks[i][y*n+x] {
					filled++
				}
			}
		}
		if filled < n*n/10 || filled > n*n*8/10 {
			t.Errorf("%s fills %d of %d cells", ic.name, filled, n*n)
		}
	}
	for i := range icons {
		for j := i + 1; j < len(icons); j++ {
			same := 0
			for k := range masks[i] {
				if masks[i][k] == masks[j][k] {
					same++
				}
			}
			if same > n*n*92/100 {
				t.Errorf("%s and %s look alike (%d%%)", icons[i].name, icons[j].name, same*100/(n*n))
			}
		}
	}
}

func TestGenerate(t *testing.T) {
	start := time.Now()
	for seed := uint64(1); seed <= 20; seed++ {
		p, err := Generate(rand.New(rand.NewPCG(seed, 11)))
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Targets) < 3 || len(p.Targets) > 4 || !strings.HasPrefix(p.PromptImage, "data:image/png;base64,") {
			t.Fatalf("%+v", p.Targets)
		}
	}
	if per := time.Since(start) / 20; per > 50*time.Millisecond {
		t.Fatalf("too slow: %v", per)
	}
	if dir := os.Getenv("KIND_SAMPLE_DIR"); dir != "" {
		p, _ := Generate(rand.New(rand.NewPCG(5, 5)))
		for name, url := range map[string]string{"icon.jpg": p.Image, "icon-prompt.png": p.PromptImage} {
			b, _ := base64.StdEncoding.DecodeString(url[strings.Index(url, ",")+1:])
			_ = os.WriteFile(dir+"/"+name, b, 0o644)
		}
	}
}
