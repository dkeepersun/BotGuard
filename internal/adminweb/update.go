package adminweb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/dkeepersun/BotGuard/internal/logic"
	"github.com/dkeepersun/BotGuard/internal/repository"
	"github.com/dkeepersun/BotGuard/internal/service/update"
	"github.com/zeromicro/go-zero/core/logx"
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
	// Pending lists live gateways that have not reported since this console
	// started; they count for neither Same nor Mixed.
	Pending []string
	// Upgrade is the one-click updater's progress; nil where it is not
	// installed (source and Docker installs, macOS).
	Upgrade *update.Progress
	// Busy: an upgrade request waits or runs.
	Busy bool
	// Stalled: a request was not picked up; the updater is not running.
	Stalled bool
	// CanUpgrade: a newer release is known and the updater is idle.
	CanUpgrade bool
}

// gatewayStale matches the health page: no summary for this long and the
// gateway is presumed gone.
const gatewayStale = 3 * time.Minute

func (s *Server) versions(ctx context.Context) (updateView, error) {
	v := updateView{Enabled: s.Updates != nil, Running: s.Version}
	if s.Updates != nil {
		v.State = s.Updates.State()
	}
	if s.Upgrader != nil {
		p, err := s.Upgrader.Progress()
		if err != nil {
			// Unreadable status: offer the commands instead of the button.
			logx.WithContext(ctx).Errorf("read upgrade status: %v", err)
		}
		v.Upgrade, v.Busy, v.Stalled = p, s.Upgrader.Busy(p), s.Upgrader.Stalled(p)
		v.CanUpgrade = p != nil && !v.Busy && v.State.Newer
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
		if s.awaitingReport(in.LastSeen) {
			v.Pending = append(v.Pending, in.Name)
			continue
		}
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
	sort.Strings(v.Pending)
	return v, nil
}

// awaitingReport reports whether a gateway's latest summary, for the
// minute starting at lastSeen, ended before this console started: it may
// come from the process an upgrade just replaced, and the version it carries
// says nothing about what runs now. Gateways report once a minute, so this
// clears within a minute or two of a restart.
func (s *Server) awaitingReport(lastSeen time.Time) bool {
	return !s.Started.IsZero() && !lastSeen.Add(time.Minute).After(s.Started)
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

// One-click upgrade (docs/admin.md 一键升级). The console only writes a
// request; the root-owned updater installs, migrates and restarts.

// upgradeStart asks the updater to install the latest release, after the
// owner has entered their password again.
func (s *Server) upgradeStart(w http.ResponseWriter, r *http.Request, p *logic.Principal) (string, error) {
	if s.Upgrader == nil || s.Updates == nil {
		return "", fmt.Errorf("%w: 这台服务器不支持一键升级，请用命令升级", logic.ErrInvalid)
	}
	if err := s.Console.ConfirmPassword(p, r.PostFormValue("password")); err != nil {
		return "", err
	}
	st := s.Updates.State()
	target := r.PostFormValue("version")
	if !st.Newer || st.Latest == nil || st.Latest.Tag != target {
		return "", fmt.Errorf("%w: 版本信息已变化，请刷新页面后重试", logic.ErrInvalid)
	}
	if err := s.Upgrader.Request(target, p.Admin.Username); err != nil {
		if errors.Is(err, update.ErrBusy) {
			return "", fmt.Errorf("%w: %s", logic.ErrInvalid, err.Error())
		}
		return "", err
	}
	if err := s.Repo.RecordAudit(r.Context(), repository.AuditEntry{Actor: p.Actor(), Operation: "system.upgrade", Target: "botguard",
		Detail: map[string]any{"from": st.Current, "to": target}, IP: s.clientIP(r)}); err != nil {
		logx.WithContext(r.Context()).Errorf("audit system.upgrade: %v", err)
	}
	return "", errRedirect{"/system/upgrade"}
}

type upgradeView struct {
	Progress *update.Progress
	Busy     bool
	Stalled  bool
	Running  string
	Log      []string
}

// upgradeLines is how much of the updater's log the console shows.
const upgradeLines = 60

func (s *Server) upgradeState() (upgradeView, error) {
	v := upgradeView{Running: s.Version}
	if s.Upgrader == nil {
		return v, nil
	}
	p, err := s.Upgrader.Progress()
	if err != nil {
		return v, err
	}
	v.Progress, v.Busy, v.Stalled, v.Log = p, s.Upgrader.Busy(p), s.Upgrader.Stalled(p), s.Upgrader.Log(upgradeLines)
	return v, nil
}

func (s *Server) upgradePage(w http.ResponseWriter, r *http.Request, p *logic.Principal) {
	v, err := s.upgradeState()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, "upgrade.html", View{Title: "一键升级", Active: "sysinfo", Me: p, Data: v})
}

// upgradeAPI feeds the progress page. While the updater restarts this
// console the request fails; the page keeps polling until it is back.
func (s *Server) upgradeAPI(w http.ResponseWriter, r *http.Request, p *logic.Principal) {
	v, err := s.upgradeState()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	out := map[string]any{"running": v.Running, "busy": v.Busy, "stalled": v.Stalled, "log": v.Log}
	if pr := v.Progress; pr != nil {
		out["state"], out["pending"], out["message"], out["from"], out["to"] = pr.State, pr.Pending, pr.Message, pr.From, pr.To
	}
	s.json(w, out)
}
