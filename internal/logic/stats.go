package logic

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/netip"

	"github.com/dkeepersun/BotGuard/internal/errx"
	"github.com/dkeepersun/BotGuard/internal/repository"
	"github.com/dkeepersun/BotGuard/internal/service/clientip"
	"github.com/zeromicro/go-zero/core/logx"
)

// ipTag is a short salted hash of the client IP bucket: enough to see that
// events come from the same client, useless for recovering the address.
func (g *Gateway) ipTag(ip netip.Addr) string {
	if len(g.Settings.IPHashKey) == 0 || !ip.IsValid() {
		return ""
	}
	m := hmac.New(sha256.New, g.Settings.IPHashKey)
	m.Write([]byte(clientip.Bucket(ip)))
	return hex.EncodeToString(m.Sum(nil))[:12]
}

// record updates the console statistics. It never fails the request:
// statistics are best effort (docs/admin.md §4).
func (g *Gateway) record(ctx context.Context, appID int64, fields []string, ev *repository.Event) {
	if g.Stats == nil || appID == 0 {
		return
	}
	if ev != nil {
		ev.At = g.Now().UnixMilli()
	}
	if err := g.Stats.Record(ctx, appID, g.Now(), fields, ev); err != nil {
		logx.WithContext(ctx).Errorf("record stats: %v", err)
	}
}

// errCode is the contract code of err, or UNAVAILABLE for anything untyped.
func errCode(err error) string {
	var e *errx.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return errx.ErrUnavailable.Code
}

// reasonFields turns risk reasons into counter fields.
func reasonFields(prefix string, reasons []string) []string {
	out := make([]string, 0, len(reasons))
	for _, r := range reasons {
		out = append(out, prefix+r)
	}
	return out
}
