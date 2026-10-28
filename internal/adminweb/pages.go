package adminweb

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/logic"
	"github.com/dkeepersun/BotGuard/internal/repository"
	"github.com/zeromicro/go-zero/rest/pathvar"
)

// Dashboard.

type dashboardView struct {
	Apps     int
	Enabled  int
	Day      Counts
	Today    Counts
	Trend    Trend
	Kinds    []Ranked
	Risk     []Ranked
	Reasons  []Ranked
	Failures []Ranked
	Rejects  []Ranked
	Ranking  []AppRow
	Recent   []RecentEvent
	Checks   []Check
	// Update is set for those who may install a release, when one is newer
	// or the gateways run another version than the console.
	Update *updateView
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request, p *logic.Principal) {
	ctx := r.Context()
	apps, err := s.Repo.ListApps(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	now := s.Now()
	perApp, day, trend, err := s.collect(ctx, apps, now.Add(-23*time.Hour), now, false)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	midnight := now.In(cst).Truncate(time.Hour)
	midnight = midnight.Add(-time.Duration(midnight.Hour()) * time.Hour)
	_, today, _, err := s.collect(ctx, apps, midnight, now, false)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	recent, err := s.recentEvents(ctx, apps, 8, nil)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v := dashboardView{Apps: len(apps), Day: day, Today: today, Trend: trend,
		Kinds: day.Prefixed("issue", 0), Risk: day.Prefixed("risk", 0), Reasons: day.Prefixed("reason", 8),
		Failures: day.Prefixed("solve_fail", 6), Rejects: day.Prefixed("verify_reject", 6),
		Ranking: rankApps(apps, perApp), Recent: recent, Checks: s.Checks(ctx)}
	scaleBars(v.Failures, v.Rejects)
	if p.Can(logic.PermSystem) {
		up, err := s.versions(ctx)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if up.State.Newer || up.Mixed {
			v.Update = &up
		}
	}
	for _, a := range apps {
		if a.Enabled {
			v.Enabled++
		}
	}
	if len(v.Ranking) > 8 {
		v.Ranking = v.Ranking[:8]
	}
	s.render(w, r, "dashboard.html", View{Title: "控制台", Active: "console", Me: p, Data: v})
}

// Apps.

type appListView struct {
	Rows  []AppRow
	Query string
	// Open is a just-created app, opened in its own shell tab on load.
	Open *repository.AppSummary
}

func (s *Server) appList(w http.ResponseWriter, r *http.Request, p *logic.Principal) {
	ctx := r.Context()
	apps, err := s.Repo.ListApps(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v := appListView{Query: strings.TrimSpace(r.URL.Query().Get("q"))}
	if key := r.URL.Query().Get("open"); key != "" {
		for i := range apps {
			if apps[i].AppKey == key {
				v.Open = &apps[i]
			}
		}
	}
	if q := v.Query; q != "" {
		var hit []repository.AppSummary
		for _, a := range apps {
			if strings.Contains(strings.ToLower(a.Name+" "+a.AppKey+" "+a.Remark), strings.ToLower(q)) {
				hit = append(hit, a)
			}
		}
		apps = hit
	}
	perApp, _, _, err := s.collect(ctx, apps, s.Now().Add(-23*time.Hour), s.Now(), false)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v.Rows = make([]AppRow, 0, len(apps))
	for _, a := range apps {
		v.Rows = append(v.Rows, AppRow{App: a, Counts: perApp[a.ID]})
	}
	s.render(w, r, "apps.html", View{Title: "应用管理", Active: "apps", Me: p, Data: v})
}

func (s *Server) appCreate(w http.ResponseWriter, r *http.Request, p *logic.Principal) (string, error) {
	ctx := s.actor(r, p)
	key, err := s.Apps.CreateApp(ctx, r.PostFormValue("name"), strings.TrimSpace(r.PostFormValue("remark")))
	if err != nil {
		return "", err
	}
	// New apps start with the recommended login action, so the snippets work
	// as soon as an origin and a secret pair are added.
	_ = s.Apps.SetAction(ctx, key, "login", domain.ActionPolicy{Enabled: true, Mode: domain.ModeAdaptive, Kinds: domain.DefaultKinds,
		Puzzles: logic.DefaultPuzzles, Difficulty: logic.DefaultDifficulty})
	// Back to the list, which opens the new app in its own tab: every app
	// lives in a tab of its own and the list stays where it was.
	s.setFlash(w, "ok", "应用已创建，已默认添加 action「login」。下一步：在应用页添加允许来源、创建密钥对。")
	return "", errRedirect{"/apps?open=" + key}
}

// actor attaches the signed-in admin and IP for the audit log.
func (s *Server) actor(r *http.Request, p *logic.Principal) context.Context {
	return repository.WithActor(r.Context(), p.Actor(), s.clientIP(r))
}

type actionRow struct {
	repository.ActionDetail
	KindList []string
	// Hashes is the expected PoW work per challenge; Seconds estimates it on
	// a mid-range phone (about 1.5 million hashes a second in total).
	Hashes  uint64
	Seconds string
}

type secretRow struct {
	repository.SecretDetail
	State string // active, expiring, expired, revoked
}

type appDetailView struct {
	App      *repository.AppDetail
	Counts   Counts
	Trend    Trend
	Actions  []actionRow
	Secrets  []secretRow
	Endpoint string
	Tab      string
	Kinds    []Ranked
	// Apps feeds the app switcher.
	Apps []repository.AppSummary
	// Reveal is a secret pair just created or rotated, shown once in a dialog.
	Reveal *secretOnceView
	// BuiltinPhotos and MaxBackgrounds describe the backgrounds tab.
	BuiltinPhotos, MaxBackgrounds int
	// The appearance tab's choices and texts.
	UILangs, UIThemes []option
	UITexts           []uiText
}

type option struct{ Value, Label string }

type uiText struct{ Key, Label, Default, Value string }

// uiTextLabels names the texts the console can change, with the widget's
// Simplified Chinese default (packages/browser MESSAGES) as the placeholder.
var uiTextLabels = map[string][2]string{
	"idle":        {"入口标题", "点击进行人机验证"},
	"idleSub":     {"入口副标题", "大多数情况下无需操作，必要时会出现拼图或点选题"},
	"loading":     {"加载中", "正在获取验证…"},
	"verified":    {"通过后", "验证通过"},
	"sliderTitle": {"滑块标题", "请完成下方拼图"},
	"sliderHint":  {"滑块轨道提示", "向右拖动，补全拼图"},
	"clickTitle":  {"点选标题", "请按提示依次点击"},
}

func estimate(puzzles int, difficulty uint64) (uint64, string) {
	h := uint64(puzzles) * difficulty
	sec := float64(h) / 1.5e6
	switch {
	case sec < 0.1:
		return h, "< 0.1 秒"
	case sec < 10:
		return h, fmt.Sprintf("约 %.1f 秒", sec)
	default:
		return h, fmt.Sprintf("约 %.0f 秒", math.Round(sec))
	}
}

func (s *Server) appDetail(w http.ResponseWriter, r *http.Request, p *logic.Principal) {
	v, err := s.loadAppDetail(r.Context(), pathvar.Vars(r)["key"])
	if errors.Is(err, domain.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v.Tab = r.URL.Query().Get("tab")
	s.render(w, r, "app.html", View{Title: v.App.Name, Active: "apps", Me: p, Data: v})
}

func (s *Server) loadAppDetail(ctx context.Context, key string) (appDetailView, error) {
	d, err := s.Apps.AppDetail(ctx, key)
	if err != nil {
		return appDetailView{}, err
	}
	apps, err := s.Repo.ListApps(ctx)
	if err != nil {
		return appDetailView{}, err
	}
	perApp, _, trend, err := s.collect(ctx, []repository.AppSummary{d.AppSummary}, s.Now().Add(-23*time.Hour), s.Now(), false)
	if err != nil {
		return appDetailView{}, err
	}
	v := appDetailView{App: d, Counts: perApp[d.ID], Trend: trend, Endpoint: s.PublicEndpoint, Apps: apps,
		BuiltinPhotos: s.Photos.Len(), MaxBackgrounds: domain.MaxBackgroundUploads,
		UILangs:  []option{{"", "简体中文（默认）"}, {"zh-TW", "繁體中文"}, {"en", "English"}, {"auto", "跟随浏览器"}},
		UIThemes: []option{{"", "浅色（默认）"}, {"dark", "深色"}, {"auto", "跟随系统"}}}
	for _, k := range domain.UITextKeys {
		l := uiTextLabels[k]
		v.UITexts = append(v.UITexts, uiText{Key: k, Label: l[0], Default: l[1], Value: d.UI.Texts[k]})
	}
	v.Kinds = v.Counts.Prefixed("issue", 0)
	if v.Endpoint == "" {
		v.Endpoint = "https://你的-gateway-地址"
	}
	for _, a := range d.Actions {
		h, sec := estimate(a.Puzzles, a.Difficulty)
		v.Actions = append(v.Actions, actionRow{ActionDetail: a, KindList: repository.ParseKinds(a.Kinds), Hashes: h, Seconds: sec})
	}
	now := s.Now()
	for _, sp := range d.Secrets {
		row := secretRow{SecretDetail: sp, State: "active"}
		switch {
		case !sp.Enabled:
			row.State = "revoked"
		case sp.ExpiresAt.Valid && !sp.ExpiresAt.Time.After(now):
			row.State = "expired"
		case sp.ExpiresAt.Valid:
			row.State = "expiring"
		}
		v.Secrets = append(v.Secrets, row)
	}
	return v, nil
}

func appBack(key, tab string) string { return "/apps/" + key + "?tab=" + tab }

func (s *Server) appUpdate(w http.ResponseWriter, r *http.Request, p *logic.Principal) (string, error) {
	key := pathvar.Vars(r)["key"]
	return "应用信息已保存", s.Apps.UpdateApp(s.actor(r, p), key, r.PostFormValue("name"), strings.TrimSpace(r.PostFormValue("remark")))
}

func (s *Server) appStatus(w http.ResponseWriter, r *http.Request, p *logic.Principal) (string, error) {
	key := pathvar.Vars(r)["key"]
	enable := r.PostFormValue("enabled") == "1"
	if err := s.Apps.SetAppEnabled(s.actor(r, p), key, enable); err != nil {
		return "", err
	}
	if enable {
		return "应用已启用", nil
	}
	return "应用已停用：新的验证和 verify 请求将被拒绝（约 10 秒内生效）", nil
}

func (s *Server) originAdd(w http.ResponseWriter, r *http.Request, p *logic.Principal) (string, error) {
	key := pathvar.Vars(r)["key"]
	o, err := s.Apps.AddOrigin(s.actor(r, p), key, strings.TrimSpace(r.PostFormValue("origin")))
	if err != nil {
		return "", err
	}
	return "已添加来源 " + o, errRedirect{appBack(key, "origins")}
}

func (s *Server) originRemove(w http.ResponseWriter, r *http.Request, p *logic.Principal) (string, error) {
	key := pathvar.Vars(r)["key"]
	o, err := s.Apps.RemoveOrigin(s.actor(r, p), key, r.PostFormValue("origin"))
	if err != nil {
		return "", err
	}
	s.setFlash(w, "ok", "已移除来源 "+o+"，该来源签发的 challenge 和 token 随之失效")
	return "", errRedirect{appBack(key, "origins")}
}

func (s *Server) actionSave(w http.ResponseWriter, r *http.Request, p *logic.Principal) (string, error) {
	key := pathvar.Vars(r)["key"]
	puzzles, err1 := strconv.Atoi(r.PostFormValue("puzzles"))
	difficulty, err2 := strconv.ParseUint(r.PostFormValue("difficulty"), 10, 64)
	if err1 != nil || err2 != nil {
		return "", fmt.Errorf("%w: PoW 题数和难度必须是正整数", logic.ErrInvalid)
	}
	action := strings.TrimSpace(r.PostFormValue("action"))
	policy := domain.ActionPolicy{Enabled: r.PostFormValue("enabled") == "1", Mode: r.PostFormValue("mode"),
		Kinds: r.PostForm["kinds"], ClientSelect: r.PostFormValue("client_select") == "1", Puzzles: puzzles, Difficulty: difficulty}
	if err := s.Apps.SetAction(s.actor(r, p), key, action, policy); err != nil {
		return "", err
	}
	s.setFlash(w, "ok", "action「"+action+"」已保存，约 10 秒内对新签发的验证生效")
	return "", errRedirect{appBack(key, "actions")}
}

func (s *Server) actionDelete(w http.ResponseWriter, r *http.Request, p *logic.Principal) (string, error) {
	key := pathvar.Vars(r)["key"]
	action := r.PostFormValue("action")
	if err := s.Apps.DeleteAction(s.actor(r, p), key, action); err != nil {
		return "", err
	}
	s.setFlash(w, "ok", "已删除 action「"+action+"」")
	return "", errRedirect{appBack(key, "actions")}
}

type secretOnceView struct {
	SecretID, SecretKey string
	ExpiresAt           *time.Time
	Rotated             string
	OldUntil            time.Time
}

// revealSecret answers a create or rotate with the app page and the new pair
// in a dialog over its secrets tab. The secret key lives only in this
// response, never in a flash cookie or a redirect.
func (s *Server) revealSecret(w http.ResponseWriter, r *http.Request, p *logic.Principal, key string, once secretOnceView) error {
	v, err := s.loadAppDetail(r.Context(), key)
	if err != nil {
		return err
	}
	v.Tab, v.Reveal = "secrets", &once
	s.render(w, r, "app.html", View{Title: v.App.Name, Active: "apps", Me: p, Data: v})
	return errRendered
}

func (s *Server) secretCreate(w http.ResponseWriter, r *http.Request, p *logic.Principal) (string, error) {
	key := pathvar.Vars(r)["key"]
	ttlDays, _ := strconv.Atoi(r.PostFormValue("ttl_days"))
	if ttlDays < 0 || ttlDays > 3650 {
		return "", fmt.Errorf("%w: 有效期须在 0–3650 天之间（0 表示永不过期）", logic.ErrInvalid)
	}
	ctx := s.actor(r, p)
	if _, err := s.Apps.CheckKEK(ctx); err != nil {
		return "", err
	}
	pair, exp, err := s.Apps.CreateSecret(ctx, key, time.Duration(ttlDays)*24*time.Hour)
	if err != nil {
		return "", err
	}
	return "", s.revealSecret(w, r, p, key, secretOnceView{SecretID: pair.SecretID, SecretKey: pair.SecretKey, ExpiresAt: exp})
}

func (s *Server) secretRotate(w http.ResponseWriter, r *http.Request, p *logic.Principal) (string, error) {
	key := pathvar.Vars(r)["key"]
	graceHours, err := strconv.Atoi(r.PostFormValue("grace_hours"))
	if err != nil || graceHours < 0 || graceHours > 24*30 {
		return "", fmt.Errorf("%w: 宽限期须在 0–720 小时之间", logic.ErrInvalid)
	}
	ctx := s.actor(r, p)
	if _, err := s.Apps.CheckKEK(ctx); err != nil {
		return "", err
	}
	old := r.PostFormValue("secret_id")
	pair, until, err := s.Apps.RotateSecret(ctx, old, time.Duration(graceHours)*time.Hour, 0)
	if err != nil {
		return "", err
	}
	return "", s.revealSecret(w, r, p, key, secretOnceView{SecretID: pair.SecretID, SecretKey: pair.SecretKey, Rotated: old, OldUntil: until})
}

func (s *Server) secretRevoke(w http.ResponseWriter, r *http.Request, p *logic.Principal) (string, error) {
	key := pathvar.Vars(r)["key"]
	id := r.PostFormValue("secret_id")
	if err := s.Apps.RevokeSecret(s.actor(r, p), id); err != nil {
		return "", err
	}
	s.setFlash(w, "ok", "密钥对 "+id+" 已吊销，约 10 秒内生效")
	return "", errRedirect{appBack(key, "secrets")}
}

// Statistics and events.

type statsView struct {
	Apps     []repository.AppSummary
	App      string
	Range    string
	Counts   Counts
	Trend    Trend
	Kinds    []Ranked
	Passed   []Ranked
	Failed   []Ranked
	Risk     []Ranked
	Reasons  []Ranked
	Failures []Ranked
	Rejects  []Ranked
	Ranking  []AppRow
}

func (s *Server) statsPage(w http.ResponseWriter, r *http.Request, p *logic.Principal) {
	ctx := r.Context()
	apps, err := s.Repo.ListApps(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v := statsView{Apps: apps, App: r.URL.Query().Get("app"), Range: r.URL.Query().Get("range")}
	selected := apps
	if v.App != "" {
		selected = nil
		for _, a := range apps {
			if a.AppKey == v.App {
				selected = append(selected, a)
			}
		}
	}
	now := s.Now()
	from, daily := now.Add(-23*time.Hour), false
	switch v.Range {
	case "7d":
		from = now.Add(-7*24*time.Hour + time.Hour)
		daily = true
	case "30d":
		from = now.Add(-30*24*time.Hour + time.Hour)
		daily = true
	default:
		v.Range = "24h"
	}
	perApp, all, trend, err := s.collect(ctx, selected, from, now, daily)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v.Counts, v.Trend = all, trend
	v.Kinds, v.Passed, v.Failed = all.Prefixed("issue", 0), all.Prefixed("solve_ok", 0), all.Prefixed("solve_fail_kind", 0)
	v.Risk, v.Reasons = all.Prefixed("risk", 0), all.Prefixed("reason", 12)
	v.Failures, v.Rejects = all.Prefixed("solve_fail", 10), all.Prefixed("verify_reject", 10)
	v.Ranking = rankApps(selected, perApp)
	s.render(w, r, "stats.html", View{Title: "统计报表", Active: "stats", Me: p, Data: v})
}

type eventsView struct {
	Apps   []repository.AppSummary
	App    string
	Type   string
	Result string
	Events []RecentEvent
}

func (s *Server) loadEvents(r *http.Request) (eventsView, error) {
	ctx := r.Context()
	apps, err := s.Repo.ListApps(ctx)
	if err != nil {
		return eventsView{}, err
	}
	q := r.URL.Query()
	v := eventsView{Apps: apps, App: q.Get("app"), Type: q.Get("type"), Result: q.Get("result")}
	selected := apps
	if v.App != "" {
		selected = nil
		for _, a := range apps {
			if a.AppKey == v.App {
				selected = append(selected, a)
			}
		}
	}
	v.Events, err = s.recentEvents(ctx, selected, 200, func(e repository.Event) bool {
		if v.Type != "" && e.Type != v.Type {
			return false
		}
		switch v.Result {
		case "pass":
			return e.Result == "pass"
		case "fail":
			return e.Result != "pass"
		}
		return true
	})
	return v, err
}

func (s *Server) eventsPage(w http.ResponseWriter, r *http.Request, p *logic.Principal) {
	v, err := s.loadEvents(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, "events.html", View{Title: "实时事件", Active: "events", Me: p, Data: v})
}

// eventsAPI feeds the auto-refreshing event table.
func (s *Server) eventsAPI(w http.ResponseWriter, r *http.Request, p *logic.Principal) {
	v, err := s.loadEvents(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	type row struct {
		At      string   `json:"at"`
		App     string   `json:"app"`
		Type    string   `json:"type"`
		Action  string   `json:"action"`
		Kind    string   `json:"kind"`
		Result  string   `json:"result"`
		Label   string   `json:"label"`
		Pass    bool     `json:"pass"`
		Level   string   `json:"level"`
		Reasons []string `json:"reasons"`
		Pointer string   `json:"pointer"`
		Device  string   `json:"device"`
		IP      string   `json:"ip"`
		Request string   `json:"request"`
	}
	out := make([]row, 0, len(v.Events))
	for _, e := range v.Events {
		reasons := make([]string, 0, len(e.Reasons))
		for _, rc := range e.Reasons {
			reasons = append(reasons, reasonText(rc))
		}
		out = append(out, row{At: time.UnixMilli(e.At).In(cst).Format("01-02 15:04:05"), App: e.AppName, Type: e.Type, Action: e.Action,
			Kind: kindNames[e.Kind], Result: e.Result, Label: reasonText(e.Result), Pass: e.Result == "pass", Level: levelNames[e.Level],
			Reasons: reasons, Pointer: pointerNames[e.Pointer], Device: e.Device, IP: e.IP, Request: e.RequestID})
	}
	s.json(w, map[string]any{"events": out})
}

// Audit.

type auditView struct {
	Records                  []repository.AuditRecord
	Total, Page, Pages       int
	Actor, Operation, Target string
}

func (s *Server) auditPage(w http.ResponseWriter, r *http.Request, p *logic.Principal) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	page = max(1, page)
	const per = 30
	v := auditView{Actor: q.Get("actor"), Operation: q.Get("op"), Target: q.Get("target"), Page: page}
	var err error
	v.Records, v.Total, err = s.Repo.Audit(r.Context(), repository.AuditQuery{Actor: v.Actor, Operation: v.Operation, Target: v.Target,
		Offset: (page - 1) * per, Limit: per})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v.Pages = max(1, (v.Total+per-1)/per)
	s.render(w, r, "audit.html", View{Title: "审计日志", Active: "audit", Me: p, Data: v})
}

// Admins.

type adminsView struct {
	Admins []repository.Admin
	// Reveal is a password just generated, shown once in a dialog.
	Reveal *passwordOnceView
}

func (s *Server) adminList(w http.ResponseWriter, r *http.Request, p *logic.Principal) {
	if err := s.renderAdmins(w, r, p, nil); err != nil {
		s.serverError(w, r, err)
	}
}

func (s *Server) renderAdmins(w http.ResponseWriter, r *http.Request, p *logic.Principal, reveal *passwordOnceView) error {
	list, err := s.Console.Admins.List(r.Context())
	if err != nil {
		return err
	}
	s.render(w, r, "admins.html", View{Title: "管理员", Active: "admins", Me: p, Data: adminsView{Admins: list, Reveal: reveal}})
	return nil
}

type passwordOnceView struct {
	Title, Username, Password, Note string
	// SignedOut: the admin reset their own password and this session ended.
	SignedOut bool
}

// revealPassword answers with the admin list and the new password in a
// dialog; like secret keys, it is never stored in a cookie or redirect.
func (s *Server) revealPassword(w http.ResponseWriter, r *http.Request, p *logic.Principal, once passwordOnceView) error {
	if err := s.renderAdmins(w, r, p, &once); err != nil {
		return err
	}
	return errRendered
}

func (s *Server) adminCreate(w http.ResponseWriter, r *http.Request, p *logic.Principal) (string, error) {
	username := strings.ToLower(strings.TrimSpace(r.PostFormValue("username")))
	pw, err := s.Console.CreateAdmin(r.Context(), p, s.clientIP(r), username, r.PostFormValue("display_name"), r.PostFormValue("role"))
	if err != nil {
		return "", err
	}
	return "", s.revealPassword(w, r, p, passwordOnceView{Title: "管理员已创建", Username: username, Password: pw,
		Note: "新管理员首次登录时必须修改这个密码。"})
}

func adminID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(pathvar.Vars(r)["id"], 10, 64)
	return id
}

func (s *Server) adminStatus(w http.ResponseWriter, r *http.Request, p *logic.Principal) (string, error) {
	enable := r.PostFormValue("enabled") == "1"
	if err := s.Console.SetAdminStatus(r.Context(), p, s.clientIP(r), adminID(r), enable); err != nil {
		return "", err
	}
	if enable {
		return "账号已启用", nil
	}
	return "账号已停用，其所有登录会话已失效", nil
}

func (s *Server) adminRole(w http.ResponseWriter, r *http.Request, p *logic.Principal) (string, error) {
	if err := s.Console.SetAdminRole(r.Context(), p, s.clientIP(r), adminID(r), r.PostFormValue("role")); err != nil {
		return "", err
	}
	return "角色已修改，该管理员需要重新登录", nil
}

func (s *Server) adminReset(w http.ResponseWriter, r *http.Request, p *logic.Principal) (string, error) {
	target, err := s.Console.Admins.ByID(r.Context(), adminID(r))
	if err != nil {
		return "", err
	}
	pw, err := s.Console.ResetPassword(r.Context(), p, s.clientIP(r), target.ID)
	if err != nil {
		return "", err
	}
	once := passwordOnceView{Title: "密码已重置", Username: target.Username, Password: pw,
		Note: "该管理员所有已登录的会话已失效，下次登录时必须修改密码。"}
	if target.ID == p.Admin.ID {
		s.setSessionCookie(w, "", -1)
		once.Note, once.SignedOut = "你重置了自己的密码，当前会话已失效，请用新密码重新登录。", true
	}
	return "", s.revealPassword(w, r, p, once)
}

// Profile.

type profileView struct {
	Force    bool
	TOTP     bool
	Pending  bool
	Secret   string
	Grouped  string
	URI      string
	QR       template.HTML
	KEKReady bool
	Sessions string
}

func (s *Server) profilePage(w http.ResponseWriter, r *http.Request, p *logic.Principal) {
	v := profileView{Force: p.Admin.MustChangePassword, TOTP: p.Admin.TOTPEnabled(), KEKReady: s.Console.Sealer != nil}
	s.render(w, r, "profile.html", View{Title: "个人设置", Active: "profile", Me: p, Data: v})
}

func (s *Server) profileName(w http.ResponseWriter, r *http.Request, p *logic.Principal) (string, error) {
	return "显示名称已保存", s.Console.UpdateProfile(r.Context(), p, s.clientIP(r), r.PostFormValue("display_name"))
}

func (s *Server) profilePassword(w http.ResponseWriter, r *http.Request, p *logic.Principal) (string, error) {
	if r.PostFormValue("new") != r.PostFormValue("confirm") {
		return "", fmt.Errorf("%w: 两次输入的新密码不一致", logic.ErrInvalid)
	}
	if err := s.Console.ChangePassword(r.Context(), p, s.clientIP(r), r.PostFormValue("current"), r.PostFormValue("new")); err != nil {
		return "", err
	}
	s.setSessionCookie(w, "", -1)
	s.setFlash(w, "ok", "密码已修改，所有设备上的登录都已退出，请用新密码重新登录")
	return "", errRedirect{"/login"}
}

func (s *Server) totpBegin(w http.ResponseWriter, r *http.Request, p *logic.Principal) (string, error) {
	secret, uri, err := s.Console.BeginTOTP(r.Context(), p)
	if err != nil {
		return "", err
	}
	v := profileView{Pending: true, Secret: secret, URI: uri, KEKReady: true, QR: qrSVG(uri)}
	v.Grouped = groupSecret(secret)
	s.render(w, r, "profile.html", View{Title: "个人设置", Active: "profile", Me: p, Data: v})
	return "", errRendered
}

func groupSecret(s string) string {
	var parts []string
	for i := 0; i < len(s); i += 4 {
		parts = append(parts, s[i:min(i+4, len(s))])
	}
	return strings.Join(parts, " ")
}

func (s *Server) totpConfirm(w http.ResponseWriter, r *http.Request, p *logic.Principal) (string, error) {
	if err := s.Console.ConfirmTOTP(r.Context(), p, s.clientIP(r), r.PostFormValue("code")); err != nil {
		return "", err
	}
	s.setSessionCookie(w, "", -1)
	s.setFlash(w, "ok", "动态码已开启。为安全起见，所有登录已退出，请重新登录（需要输入动态码）")
	return "", errRedirect{"/login"}
}

func (s *Server) totpDisable(w http.ResponseWriter, r *http.Request, p *logic.Principal) (string, error) {
	if err := s.Console.DisableTOTP(r.Context(), p, s.clientIP(r), r.PostFormValue("current")); err != nil {
		return "", err
	}
	s.setSessionCookie(w, "", -1)
	s.setFlash(w, "ok", "动态码已关闭，请重新登录")
	return "", errRedirect{"/login"}
}

// System.

type systemView struct {
	Checks []Check
	Info   map[string]string
	Now    time.Time
	Update updateView
}

func (s *Server) systemPage(w http.ResponseWriter, r *http.Request, p *logic.Principal) {
	up, err := s.versions(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, "system.html", View{Title: "系统状态", Active: "sysinfo", Me: p,
		Data: systemView{Checks: s.Checks(r.Context()), Info: s.Info, Now: s.Now(), Update: up}})
}

func (s *Server) backgroundMode(w http.ResponseWriter, r *http.Request, p *logic.Principal) (string, error) {
	key := pathvar.Vars(r)["key"]
	if err := s.Apps.SetBackground(s.actor(r, p), key, r.PostFormValue("mode"), r.PostFormValue("builtin") == "1"); err != nil {
		return "", err
	}
	s.setFlash(w, "ok", "背景设置已保存，约 10 秒内对新签发的验证生效")
	return "", errRedirect{appBack(key, "backgrounds")}
}

// Upload limits: one picture, and one request of several.
const (
	maxUploadFile    = 10 << 20
	maxUploadRequest = 64 << 20
)

func (s *Server) backgroundUpload(w http.ResponseWriter, r *http.Request, p *logic.Principal) (string, error) {
	key := pathvar.Vars(r)["key"]
	back := errRedirect{appBack(key, "backgrounds")}
	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		return "", fmt.Errorf("%w: 请选择要上传的照片", logic.ErrInvalid)
	}
	ctx := s.actor(r, p)
	added := 0
	var failed []string
	for _, fh := range files {
		if fh.Size > maxUploadFile {
			failed = append(failed, fh.Filename+"：超过 10 MB")
			continue
		}
		data, err := readUpload(fh)
		if err == nil {
			_, err = s.Apps.AddBackground(ctx, key, fh.Filename, data)
		}
		if err != nil {
			if !errors.Is(err, logic.ErrInvalid) {
				return "", err
			}
			failed = append(failed, s.userMessage(r, err))
			continue
		}
		added++
	}
	switch {
	case len(failed) == 0:
		s.setFlash(w, "ok", fmt.Sprintf("已上传 %d 张照片，约 10 秒内用于新签发的验证", added))
	case added == 0:
		s.setFlash(w, "error", strings.Join(failed, "；"))
	default:
		s.setFlash(w, "error", fmt.Sprintf("已上传 %d 张；以下未上传：%s", added, strings.Join(failed, "；")))
	}
	return "", back
}

func readUpload(fh *multipart.FileHeader) ([]byte, error) {
	f, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxUploadFile+1))
}

func (s *Server) backgroundDelete(w http.ResponseWriter, r *http.Request, p *logic.Principal) (string, error) {
	key := pathvar.Vars(r)["key"]
	id, err := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	if err != nil {
		return "", fmt.Errorf("%w: 参数错误", logic.ErrInvalid)
	}
	if err := s.Apps.DeleteBackground(s.actor(r, p), key, id); err != nil {
		return "", err
	}
	s.setFlash(w, "ok", "背景图已删除")
	return "", errRedirect{appBack(key, "backgrounds")}
}

func (s *Server) backgroundImage(w http.ResponseWriter, r *http.Request, p *logic.Principal) {
	id, err := strconv.ParseInt(pathvar.Vars(r)["id"], 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	img, err := s.Apps.BackgroundImage(r.Context(), pathvar.Vars(r)["key"], id)
	if errors.Is(err, domain.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(img)
}

func (s *Server) uiSave(w http.ResponseWriter, r *http.Request, p *logic.Principal) (string, error) {
	key := pathvar.Vars(r)["key"]
	ui := domain.AppUI{Lang: r.PostFormValue("lang"), Theme: r.PostFormValue("theme"), Accent: strings.TrimSpace(r.PostFormValue("accent"))}
	if v := strings.TrimSpace(r.PostFormValue("radius")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return "", fmt.Errorf("%w: 圆角须为整数", logic.ErrInvalid)
		}
		ui.Radius = &n
	}
	for _, k := range domain.UITextKeys {
		if v := r.PostFormValue("text_" + k); v != "" {
			if ui.Texts == nil {
				ui.Texts = map[string]string{}
			}
			ui.Texts[k] = v
		}
	}
	if err := s.Apps.SetUI(s.actor(r, p), key, ui); err != nil {
		return "", err
	}
	s.setFlash(w, "ok", "外观与文案已保存，组件约 1 分钟内用上")
	return "", errRedirect{appBack(key, "ui")}
}
