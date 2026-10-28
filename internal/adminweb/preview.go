package adminweb

import (
	cryptorand "crypto/rand"
	"html/template"
	"math/rand/v2"
	"net/http"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/logic"
	"github.com/dkeepersun/BotGuard/internal/service/click"
	"github.com/dkeepersun/BotGuard/internal/service/clickicon"
	"github.com/dkeepersun/BotGuard/internal/service/clickshape"
	"github.com/dkeepersun/BotGuard/internal/service/clicktext"
	"github.com/dkeepersun/BotGuard/internal/service/clock"
	"github.com/dkeepersun/BotGuard/internal/service/jigsaw"
	"github.com/dkeepersun/BotGuard/internal/service/qrcode"
	"github.com/dkeepersun/BotGuard/internal/service/rotate"
	"github.com/dkeepersun/BotGuard/internal/service/slider"
	"github.com/dkeepersun/BotGuard/internal/service/spatial"
)

// qrSVG renders an otpauth URI for authenticator apps; empty if it does not
// fit (the secret is shown as text either way).
func qrSVG(uri string) template.HTML {
	c, err := qrcode.Encode([]byte(uri))
	if err != nil {
		return ""
	}
	return template.HTML(c.SVG(200))
}

func previewRand() *rand.Rand {
	var seed [32]byte
	_, _ = cryptorand.Read(seed[:])
	return rand.New(rand.NewChaCha8(seed))
}

func (s *Server) previewPage(w http.ResponseWriter, r *http.Request, p *logic.Principal) {
	s.render(w, r, "preview.html", View{Title: "题型预览", Active: "preview", Me: p})
}

// previewAPI generates one sample with its answer, so admins can compare
// kinds before choosing them. It uses the gateway's own generators.
func (s *Server) previewAPI(w http.ResponseWriter, r *http.Request, p *logic.Principal) {
	kind := r.URL.Query().Get("kind")
	rnd := previewRand()
	out := map[string]any{"kind": kind, "name": kindNames[kind]}
	switch kind {
	case domain.KindSlider:
		pz, err := slider.Generate(rnd)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		out["slider"] = map[string]any{"width": slider.Width, "height": slider.Height, "background": pz.Background, "piece": pz.Piece,
			"piece_y": pz.Y, "piece_size": slider.PieceSize, "answer_x": pz.X}
	case domain.KindRotate:
		pz, err := rotate.Generate(rnd)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		out["rotate"] = map[string]any{"width": rotate.Width, "height": rotate.Height, "background": pz.Background, "disc": pz.Disc,
			"cx": pz.CX, "cy": pz.CY, "radius": pz.Radius, "track": rotate.Track, "angle": pz.Angle}
	case domain.KindJigsaw:
		pz, err := jigsaw.Generate(rnd)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		out["jigsaw"] = map[string]any{"width": jigsaw.Width, "height": jigsaw.Height, "image": pz.Image, "rows": jigsaw.Rows, "cols": jigsaw.Cols,
			"swap": []int{pz.A, pz.B}}
	case domain.KindClickText, domain.KindClickOrder, domain.KindClickIcon, domain.KindClickShape, domain.KindClock, domain.KindSpatial:
		generate := map[string]func(*rand.Rand) (click.Puzzle, error){
			domain.KindClickText: clicktext.Generate, domain.KindClickOrder: clicktext.GenerateOrder, domain.KindClickIcon: clickicon.Generate,
			domain.KindClickShape: clickshape.Generate, domain.KindClock: clock.Generate, domain.KindSpatial: spatial.Generate,
		}[kind]
		pz, err := generate(rnd)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		out["click"] = map[string]any{"width": pz.Width, "height": pz.Height, "image": pz.Image, "prompt": pz.Prompt,
			"prompt_image": pz.PromptImage, "targets": pz.Targets}
	default:
		http.Error(w, "unknown kind", http.StatusBadRequest)
		return
	}
	s.json(w, out)
}
