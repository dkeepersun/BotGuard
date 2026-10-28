package logic

import (
	"context"
	"time"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/zeromicro/go-zero/core/logx"
)

// puzzlePool keeps puzzles of every interactive kind generated ahead of
// time, so a burst of challenges is served without generating images on the
// request path. Each puzzle is handed out once: a reused image would let a
// solver replay a known answer.
type puzzlePool struct {
	queues map[string]chan any
	// need wakes idle workers after a take; one buffered signal per worker.
	need chan struct{}
}

// poolRetryDelay is the pause after a failed generation, so a broken
// generator does not spin a core.
const poolRetryDelay = time.Second

// StartPuzzlePool fills a pool of perKind puzzles per interactive kind with
// the given number of background workers, until ctx ends. Workers take the
// same generation slots as requests, so requests keep the slots the workers
// leave free: use fewer workers than CPUs. perKind or workers <= 0 leaves
// the pool off and every puzzle is generated on demand.
func (g *Gateway) StartPuzzlePool(ctx context.Context, perKind, workers int) {
	if perKind <= 0 || workers <= 0 {
		return
	}
	p := &puzzlePool{queues: make(map[string]chan any, len(domain.InteractiveKinds)), need: make(chan struct{}, workers)}
	for _, k := range domain.InteractiveKinds {
		p.queues[k] = make(chan any, perKind)
	}
	g.pool = p
	for range workers {
		go g.fillPool(ctx, p)
	}
}

// take returns a pooled puzzle of kind if one is ready.
func (p *puzzlePool) take(kind string) (any, bool) {
	if p == nil {
		return nil, false
	}
	select {
	case v := <-p.queues[kind]:
		select {
		case p.need <- struct{}{}:
		default:
		}
		return v, true
	default:
		return nil, false
	}
}

// short returns a kind whose queue has room, or "" when all are full.
func (p *puzzlePool) short() string {
	for _, k := range domain.InteractiveKinds {
		if q := p.queues[k]; len(q) < cap(q) {
			return k
		}
	}
	return ""
}

func (g *Gateway) fillPool(ctx context.Context, p *puzzlePool) {
	slots := g.slots()
	for {
		kind := p.short()
		if kind == "" {
			select {
			case <-ctx.Done():
				return
			case <-p.need:
				continue
			}
		}
		select {
		case <-ctx.Done():
			return
		case slots <- struct{}{}:
		}
		v, err := g.generate(kind, g.Backgrounds.Default())
		<-slots
		if err != nil {
			logx.Errorf("pool %s: %v", kind, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(poolRetryDelay):
			}
			continue
		}
		select {
		case p.queues[kind] <- v:
		default:
			// Another worker filled it first; drop rather than block.
		}
	}
}
