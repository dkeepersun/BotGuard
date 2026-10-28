// Package adminweb is the admin console (docs/admin.md): Go server-rendered
// pages inside the PearAdmin shell, backed by the same logic and
// repositories as botguard-ctl.
package adminweb

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/logic"
	"github.com/dkeepersun/BotGuard/internal/repository"
	"github.com/dkeepersun/BotGuard/internal/response"
	"github.com/dkeepersun/BotGuard/internal/service/clientip"
	"github.com/dkeepersun/BotGuard/internal/service/imagegen"
	"github.com/dkeepersun/BotGuard/internal/service/update"
	"github.com/dkeepersun/BotGuard/internal/webui"
	"github.com/zeromicro/go-zero/rest"
)

//go:embed templates
var files embed.FS

// Server holds the console's dependencies.
type Server struct {
	Console *logic.Console
	Apps    *logic.AppAdmin
	Repo    *repository.AppAdmin
	Stats   *repository.Stats
	// Health holds the gateways' per-minute summaries.
	Health *repository.HealthStore
	IPs    *clientip.Resolver
	// Checks reports the state of each dependency for the system page and
	// /readyz: name -> "" when healthy, otherwise a reason.
	Checks func(ctx context.Context) []Check
	// PublicEndpoint is the gateway URL shown in integration snippets.
	PublicEndpoint string
	CookieSecure   bool
	SessionTTL     time.Duration
	Now            func() time.Time
	Info           map[string]string
	// Version is this console's version.String(); Updates looks for newer
	// releases and is nil when Update.Check is off.
	Version string
	// Started is when this console started. A gateway whose latest summary
	// covers a minute that ended before it is awaiting its first report since
	// then (most often: both were just restarted by an upgrade), so its
	// version is not compared yet. Zero compares every gateway.
	Started time.Time
	Updates *update.Checker
	// Upgrader hands one-click upgrades to the root-owned updater; nil
	// when the console runs where none can be installed (Docker).
	Upgrader *update.Upgrader
	// Photos is the built-in background library, for previews; nil shows
	// procedural pictures only.
	Photos *imagegen.Library

	tpl *template.Template
}

// Check is one dependency's health for the system page.
type Check struct {
	Name   string
	OK     bool
	Detail string
}

func itoa(n int) string { return strconv.Itoa(n) }

var kindNames = map[string]string{
	domain.KindInvisible: "无感", domain.KindSlider: "滑块拼图", domain.KindRotate: "旋转对齐", domain.KindJigsaw: "推理拼图",
	domain.KindClickText: "文字点选", domain.KindClickOrder: "语序点选", domain.KindClickIcon: "图标点选",
	domain.KindClickShape: "图形点选", domain.KindClock: "时钟读数", domain.KindGridCount: "九宫格计数", domain.KindMirror: "镜像辨别", domain.KindSpatial: "空间推理", domain.KindFallback: "无障碍备用",
}

var modeNames = map[string]string{
	domain.ModeInvisible: "无感", domain.ModeInteractive: "交互（随机题型）", domain.ModeAdaptive: "自适应（按风险）",
}

var levelNames = map[string]string{"low": "低", "medium": "中", "high": "高"}

func (s *Server) funcs() template.FuncMap {
	return template.FuncMap{
		"can": func(p *logic.Principal, perm string) bool { return p != nil && p.Can(perm) },
		"role": func(r string) string {
			if n, ok := logic.RoleNames[r]; ok {
				return n
			}
			return r
		},
		"kind": func(k string) string {
			if n, ok := kindNames[k]; ok {
				return n
			}
			return k
		},
		"mode":   func(m string) string { return modeNames[m] },
		"level":  func(l string) string { return levelNames[l] },
		"reason": reasonText,
		"time": func(t time.Time) string {
			if t.IsZero() {
				return "—"
			}
			return t.In(cst).Format("2006-01-02 15:04:05")
		},
		"ago": func(t time.Time) string { return since(t, s.Now()) },
		"ms": func(ms int64) string {
			return time.UnixMilli(ms).In(cst).Format("01-02 15:04:05")
		},
		"unix": func(sec int64) string {
			return time.Unix(sec, 0).In(cst).Format("01-02 15:04")
		},
		"anomaly": func(kind string) string { return anomalyLabels[kind] },
		"num":     formatNum,
		"pct":     formatPct,
		"json":    func(v any) template.JS { b, _ := json.Marshal(v); return template.JS(b) },
		"join":    strings.Join,
		"has":     func(list []string, v string) bool { return contains(list, v) },
		"kinds":   func() []string { return domain.InteractiveKinds },
		// defaultKinds pre-ticks a new action; newWidget marks the kinds that
		// need widget domain.MinWidget.
		"defaultKinds":  func() []string { return domain.DefaultKinds },
		"newWidget":     domain.NeedsNewWidget,
		"usesNewWidget": UsesNewWidget,
		"add":           func(a, b int) int { return a + b },
		"kb":            func(n int) string { return fmt.Sprintf("%d KB", (n+512)/1024) },
		"sub64":         func(a, b int64) int64 { return a - b },
		"opName":        opName,
		"pointer":       func(p string) string { return pointerNames[p] },
		// asset appends a content hash so a new release is never served
		// from a stale browser cache.
		"asset": func(path string) string {
			if v := webui.Version(strings.TrimPrefix(path, "/static/")); v != "" {
				return path + "?v=" + v
			}
			return path
		},
		"detail":  auditDetail,
		"opGroup": func(prefix string) string { return opGroups[prefix] },
		"opColor": func(op string) string {
			switch {
			case strings.HasPrefix(op, "auth.login_failed"), strings.HasSuffix(op, ".revoke"), strings.HasSuffix(op, ".delete"), strings.HasSuffix(op, ".disable"):
				return "bad"
			case strings.HasPrefix(op, "auth."):
				return "blue"
			case strings.HasPrefix(op, "secret."), strings.HasPrefix(op, "admin."):
				return "violet"
			}
			return "ok"
		},
		"sub":      func(a, b int) int { return a - b },
		"mul64":    func(a int, b uint64) uint64 { return uint64(a) * b },
		"split":    func(s string) []string { return splitList(s) },
		"query":    url.QueryEscape,
		"endpoint": func() string { return s.PublicEndpoint },
		"info":     func() map[string]string { return s.Info },
	}
}

var opGroups = map[string]string{"app.": "应用", "origin.": "允许来源", "action.": "验证策略", "secret.": "密钥对", "background.": "背景图", "ui.": "外观与文案", "admin.": "管理员", "auth.": "登录", "system.": "系统"}

var opNames = map[string]string{
	"app.create": "创建应用", "app.update": "修改应用", "app.enable": "启用应用", "app.disable": "停用应用",
	"origin.add": "添加来源", "origin.remove": "移除来源", "action.set": "保存策略", "action.delete": "删除策略",
	"secret.create": "创建密钥对", "secret.rotate": "轮换密钥对", "secret.revoke": "吊销密钥对",
	"admin.create": "创建管理员", "admin.enable": "启用管理员", "admin.disable": "停用管理员", "admin.role": "修改角色",
	"admin.password_reset": "重置密码", "admin.password_change": "修改密码", "admin.profile": "修改资料",
	"admin.totp_enable": "开启动态码", "admin.totp_disable": "关闭动态码",
	"auth.login": "登录", "auth.logout": "退出", "auth.login_failed": "登录失败",
	"system.upgrade": "一键升级",
	"background.set": "修改背景来源", "background.add": "上传背景图", "background.delete": "删除背景图",
	"ui.set": "修改外观与文案",
}

func opName(op string) string {
	if n, ok := opNames[op]; ok {
		return n
	}
	return op
}

// cst is how the console shows times; storage stays UTC.
var cst = time.FixedZone("CST", 8*3600)

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func formatNum(v any) string {
	var n int64
	switch x := v.(type) {
	case int:
		n = int64(x)
	case int64:
		n = x
	case uint64:
		n = int64(x)
	case float64:
		n = int64(x)
	}
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		return "-" + s
	}
	return s
}

func formatPct(part, total int64) string {
	if total == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f%%", float64(part)*100/float64(total))
}

func describeDomain(err error) string {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return "记录不存在，可能已被其他管理员删除"
	case errors.Is(err, domain.ErrDuplicate):
		return "记录已存在"
	case errors.Is(err, domain.ErrInvalidState):
		return "当前状态不允许该操作（例如密钥对已吊销）"
	case errors.Is(err, logic.ErrKEKMismatch):
		return "BOTGUARD_SECRET_KEK 与 gateway 不一致，已拒绝创建密钥对"
	}
	return ""
}

// Register adds every console route.
func (s *Server) Register(srv *rest.Server) error {
	tpl, err := template.New("").Funcs(s.funcs()).ParseFS(files, "templates/*.html")
	if err != nil {
		return err
	}
	s.tpl = tpl
	if s.Now == nil {
		s.Now = time.Now
	}
	get := func(path string, h http.HandlerFunc) rest.Route {
		return rest.Route{Method: http.MethodGet, Path: path, Handler: h}
	}
	post := func(path string, h http.HandlerFunc) rest.Route {
		return rest.Route{Method: http.MethodPost, Path: path, Handler: h}
	}
	srv.AddRoutes([]rest.Route{
		get("/healthz", func(w http.ResponseWriter, r *http.Request) {
			response.JSON(w, 200, map[string]string{"status": "alive"})
		}),
		get("/readyz", s.readyz),
		get("/login", s.secure(s.loginPage)),
		post("/login", s.secure(s.login)),
		post("/logout", s.action("", s.logout)),

		get("/", s.page(logic.PermView, s.shell)),
		get("/pear-config", s.page(logic.PermView, s.pearConfig)),
		get("/menus", s.page(logic.PermView, s.menus)),
		get("/console", s.page(logic.PermView, s.dashboard)),

		get("/apps", s.page(logic.PermView, s.appList)),
		post("/apps/create", s.action(logic.PermManageApps, s.appCreate)),
		get("/apps/:key", s.page(logic.PermView, s.appDetail)),
		post("/apps/:key/update", s.action(logic.PermManageApps, s.appUpdate)),
		post("/apps/:key/status", s.action(logic.PermManageApps, s.appStatus)),
		post("/apps/:key/origins/add", s.action(logic.PermManageApps, s.originAdd)),
		post("/apps/:key/origins/remove", s.action(logic.PermManageApps, s.originRemove)),
		post("/apps/:key/actions/save", s.action(logic.PermManageApps, s.actionSave)),
		post("/apps/:key/actions/delete", s.action(logic.PermManageApps, s.actionDelete)),
		post("/apps/:key/secrets/create", s.action(logic.PermManageSecrets, s.secretCreate)),
		post("/apps/:key/secrets/rotate", s.action(logic.PermManageSecrets, s.secretRotate)),
		post("/apps/:key/secrets/revoke", s.action(logic.PermManageSecrets, s.secretRevoke)),
		post("/apps/:key/backgrounds/mode", s.action(logic.PermManageApps, s.backgroundMode)),
		post("/apps/:key/ui", s.action(logic.PermManageApps, s.uiSave)),
		post("/apps/:key/backgrounds/delete", s.action(logic.PermManageApps, s.backgroundDelete)),
		get("/apps/:key/backgrounds/:id", s.page(logic.PermView, s.backgroundImage)),

		get("/stats", s.page(logic.PermView, s.statsPage)),
		get("/events", s.page(logic.PermView, s.eventsPage)),
		get("/health", s.page(logic.PermView, s.healthPage)),
		get("/api/events", s.page(logic.PermView, s.eventsAPI)),
		get("/preview", s.page(logic.PermView, s.previewPage)),
		get("/api/preview", s.page(logic.PermView, s.previewAPI)),

		get("/audit", s.page(logic.PermAudit, s.auditPage)),
		get("/admins", s.page(logic.PermManageAdmins, s.adminList)),
		post("/admins/create", s.action(logic.PermManageAdmins, s.adminCreate)),
		post("/admins/:id/status", s.action(logic.PermManageAdmins, s.adminStatus)),
		post("/admins/:id/role", s.action(logic.PermManageAdmins, s.adminRole)),
		post("/admins/:id/reset", s.action(logic.PermManageAdmins, s.adminReset)),

		get("/profile", s.page("", s.profilePage)),
		post("/profile/name", s.action("", s.profileName)),
		post("/profile/password", s.action("", s.profilePassword)),
		post("/profile/totp/begin", s.action("", s.totpBegin)),
		post("/profile/totp/confirm", s.action("", s.totpConfirm)),
		post("/profile/totp/disable", s.action("", s.totpDisable)),

		get("/system", s.page(logic.PermView, s.systemPage)),
		post("/system/update-check", s.action(logic.PermSystem, s.updateCheck)),
		post("/system/upgrade", s.action(logic.PermSystem, s.upgradeStart)),
		get("/system/upgrade", s.page(logic.PermSystem, s.upgradePage)),
		get("/api/upgrade", s.page(logic.PermSystem, s.upgradeAPI)),
	})
	// Uploads need a larger body and longer than the console's timeout:
	// each picture is decoded, scaled and re-encoded.
	srv.AddRoute(post("/apps/:key/backgrounds/upload", s.upload(logic.PermManageApps, s.backgroundUpload)),
		rest.WithMaxBytes(maxUploadRequest), rest.WithTimeout(2*time.Minute))
	static := webui.StaticHandler()
	for _, path := range webui.Paths() {
		srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/static/" + path, Handler: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "public, max-age=3600")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			static.ServeHTTP(w, r)
		}})
	}
	return nil
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	for _, c := range s.Checks(r.Context()) {
		// The gateway is informational for the console; only its own stores
		// decide readiness.
		if !c.OK && c.Name != "gateway" {
			response.JSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "reason": c.Name})
			return
		}
	}
	response.JSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// Login and logout.

type loginView struct {
	Username, Next string
	// Pending is set on the second step: password verified, code needed.
	Pending string
	Error   string
}

func safeNext(next string) string {
	if !localPath(next) || strings.HasPrefix(next, "/login") {
		return "/"
	}
	return next
}

// localPath reports whether p is a path on this site. Browsers treat "\"
// as "/" and drop tabs and newlines, so "/\evil.com", or "/" + tab +
// "/evil.com", would leave the site although they start with one slash.
func localPath(p string) bool {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsRune(p, '\\') {
		return false
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	u, err := url.Parse(p)
	return err == nil && u.Scheme == "" && u.Host == ""
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if p, _ := s.authenticate(r); p != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, r, "login.html", View{Title: "登录", Data: loginView{Next: safeNext(r.URL.Query().Get("next"))}})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if !sameOrigin(r) || r.ParseForm() != nil {
		http.Error(w, "请求被拒绝", http.StatusForbidden)
		return
	}
	v := loginView{Username: strings.TrimSpace(r.PostFormValue("username")), Next: safeNext(r.PostFormValue("next"))}
	p, err := s.Console.Login(r.Context(), logic.LoginInput{Username: v.Username, Password: r.PostFormValue("password"),
		OTP: r.PostFormValue("otp"), IP: s.clientIP(r), Pending: r.PostFormValue("pending")})
	var otp *logic.OTPChallenge
	switch {
	case errors.As(err, &otp):
		// Second step: only the code is asked for; a wrong code keeps the ticket.
		v.Pending, v.Username = otp.Token, otp.Username
		if errors.Is(err, logic.ErrOTPInvalid) {
			v.Error = err.Error()
		}
		s.render(w, r, "login.html", View{Title: "动态码验证", Data: v})
		return
	case errors.Is(err, logic.ErrOTPInvalid), errors.Is(err, logic.ErrLoginFailed), errors.Is(err, logic.ErrLoginLocked), errors.Is(err, logic.ErrOTPExpired):
		v.Error = err.Error()
	case err != nil:
		s.serverError(w, r, err)
		return
	default:
		s.setSessionCookie(w, p.Session.ID, int(s.SessionTTL/time.Second))
		http.Redirect(w, r, v.Next, http.StatusSeeOther)
		return
	}
	w.WriteHeader(http.StatusUnauthorized)
	s.render(w, r, "login.html", View{Title: "登录", Data: v})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, p *logic.Principal) (string, error) {
	_ = s.Console.Logout(r.Context(), p, s.clientIP(r))
	s.setSessionCookie(w, "", -1)
	return "", errRedirect{"/login"}
}

// The PearAdmin shell.

func (s *Server) shell(w http.ResponseWriter, r *http.Request, p *logic.Principal) {
	s.render(w, r, "shell.html", View{Title: "BotGuard 管理后台", Me: p})
}

func (s *Server) pearConfig(w http.ResponseWriter, r *http.Request, p *logic.Principal) {
	home := map[string]string{"id": "console", "href": "/console", "title": "控制台"}
	if p.Admin.MustChangePassword {
		home = map[string]string{"id": "profile", "href": "/profile?force=1", "title": "修改密码"}
	}
	s.json(w, map[string]any{
		"logo":   map[string]any{"title": "BotGuard", "image": "/static/admin/logo.svg"},
		"menu":   map[string]any{"data": "/menus", "method": "GET", "accordion": true, "collapse": false, "control": false, "select": home["id"], "async": true},
		"tab":    map[string]any{"enable": true, "keepState": true, "session": false, "preload": false, "max": "12", "index": home},
		"theme":  map[string]any{"defaultColor": "1", "defaultMenu": "dark-theme", "defaultHeader": "light-theme", "allowCustom": false, "banner": false},
		"colors": []map[string]string{{"id": "1", "color": "#2563eb", "second": "#eff6ff"}},
		"other":  map[string]any{"keepLoad": "300", "autoHead": false, "footer": false},
		"header": map[string]bool{"message": false},
	})
}

type menu struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Type     int    `json:"type"`
	Icon     string `json:"icon"`
	Href     string `json:"href,omitempty"`
	OpenType string `json:"openType,omitempty"`
	Children []menu `json:"children,omitempty"`
}

func item(id, title, icon, href string) menu {
	return menu{ID: id, Title: title, Type: 1, Icon: "layui-icon " + icon, Href: href, OpenType: "_iframe"}
}

// Menus are filtered by permission for convenience only; every route checks
// its own permission.
func (s *Server) menus(w http.ResponseWriter, r *http.Request, p *logic.Principal) {
	out := []menu{
		item("console", "控制台", "layui-icon-console", "/console"),
		item("apps", "应用管理", "layui-icon-app", "/apps"),
		{ID: "analysis", Title: "数据分析", Type: 0, Icon: "layui-icon layui-icon-chart", Children: []menu{
			item("stats", "统计报表", "layui-icon-chart-screen", "/stats"),
			item("events", "实时事件", "layui-icon-list", "/events"),
			item("health", "运行状况", "layui-icon-chart", "/health"),
		}},
		item("preview", "题型预览", "layui-icon-picture", "/preview"),
	}
	sys := menu{ID: "system", Title: "系统管理", Type: 0, Icon: "layui-icon layui-icon-set"}
	if p.Can(logic.PermManageAdmins) {
		sys.Children = append(sys.Children, item("admins", "管理员", "layui-icon-user", "/admins"))
	}
	if p.Can(logic.PermAudit) {
		sys.Children = append(sys.Children, item("audit", "审计日志", "layui-icon-log", "/audit"))
	}
	sys.Children = append(sys.Children, item("sysinfo", "系统状态", "layui-icon-engine", "/system"), item("profile", "个人设置", "layui-icon-username", "/profile"))
	out = append(out, sys)
	s.json(w, out)
}
