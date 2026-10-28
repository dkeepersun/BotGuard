package adminweb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/dkeepersun/BotGuard/internal/logic"
	"github.com/dkeepersun/BotGuard/internal/service/update"
)

// gatewayVersion groups the live gateways by the version they report.
type gatewayVersion struct {
	Version   string // empty: a gateway from before versions were reported
	Instances []string
	// Same as this console's version.
	Same bool
}

type updateView struct {
	// Enabled: the console checks for releases (Update.Check).
	Enabled bool
	State   update.State
	// Running is this console's version.String().
	Running  string
	Gateways []gatewayVersion
	// Mixed: the live gateways do not all run this console's version.
	Mixed bool
}

// gatewayStale matches the health page: no summary for this long and the
// gateway is presumed gone.
const gatewayStale = 3 * time.Minute

func (s *Server) versions(ctx context.Context) (updateView, error) {
	v := updateView{Enabled: s.Updates != nil, Running: s.Version}
	if s.Updates != nil {
		v.State = s.Updates.State()
	}
	if s.Health == nil {
		return v, nil
	}
	now := s.Now()
	instances, err := s.Health.Instances(ctx, now.Add(-gatewayStale))
	if err != nil {
		return v, err
	}
	byVersion := map[string]*gatewayVersion{}
	for _, in := range instances {
		ver := ""
		if last, err := s.Health.Recent(ctx, in.Name, 1); err != nil {
			return v, err
		} else if len(last) > 0 {
			ver = last[0].Version
		}
		g := byVersion[ver]
		if g == nil {
			g = &gatewayVersion{Version: ver, Same: ver == s.Version}
			byVersion[ver] = g
		}
		g.Instances = append(g.Instances, in.Name)
	}
	for _, g := range byVersion {
		sort.Strings(g.Instances)
		v.Gateways = append(v.Gateways, *g)
		if !g.Same {
			v.Mixed = true
		}
	}
	sort.Slice(v.Gateways, func(i, j int) bool { return v.Gateways[i].Version > v.Gateways[j].Version })
	return v, nil
}

// updateCheck looks for a release now instead of waiting for the next
// scheduled check.
func (s *Server) updateCheck(w http.ResponseWriter, r *http.Request, p *logic.Principal) (string, error) {
	if s.Updates == nil {
		return "", fmt.Errorf("%w: 版本检查已关闭（配置 Update.Check: false）", logic.ErrInvalid)
	}
	err := s.Updates.Check(r.Context())
	switch {
	case errors.Is(err, update.ErrTooSoon):
		return "", fmt.Errorf("%w: %s", logic.ErrInvalid, err.Error())
	case err != nil:
		return "", fmt.Errorf("%w: 检查失败：%s", logic.ErrInvalid, err.Error())
	}
	st := s.Updates.State()
	if st.Newer {
		return "发现新版本 " + st.Latest.Tag, nil
	}
	return "已检查，最新正式版本是 " + st.Latest.Tag, nil
}
