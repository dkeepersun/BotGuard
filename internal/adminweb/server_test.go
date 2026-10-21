package adminweb

import (
	"context"
	"html"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/dkeepersun/BotGuard/internal/logic"
	"github.com/dkeepersun/BotGuard/internal/repository"
	"github.com/dkeepersun/BotGuard/internal/service/password"
	"github.com/dkeepersun/BotGuard/internal/service/sealer"
	"github.com/dkeepersun/BotGuard/internal/testdb"
	"github.com/dkeepersun/BotGuard/internal/webui"
	"github.com/zeromicro/go-zero/core/stores/redis"
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/rest/router"
)

func TestEmbeddedAssets(t *testing.T) {
	for _, path := range []string{"component/pear/pear.js", "component/layui/layui.js", "admin/css/console.css", "admin/js/console.js", "admin/js/shell.js"} {
		w := httptest.NewRecorder()
		webui.StaticHandler().ServeHTTP(w, httptest.NewRequest("GET", "/static/"+path, nil))
		if w.Code != 200 || w.Body.Len() == 0 {
			t.Fatalf("missing asset %s", path)
		}
	}
}

type env struct {
	t       *testing.T
	srv     *httptest.Server
	console *logic.Console
	stats   *repository.Stats
	apps    *repository.AppAdmin
}

func newEnv(t *testing.T) *env {
	t.Helper()
	old := password.Iterations
	password.Iterations = 1000
	t.Cleanup(func() { password.Iterations = old })
	_, conn := testdb.Open(t)
	mr := miniredis.RunT(t)
	rds := redis.New(mr.Addr(), redis.WithIdentity())
	seal, err := sealer.New("k1:" + strings.Repeat("A", 43) + "=")
	if err != nil {
		t.Fatal(err)
	}
	repo := repository.NewAppAdmin(conn)
	console := &logic.Console{Admins: repository.NewAdmins(conn), Audit: repo, Sessions: repository.NewSessions(rds, 30*time.Minute, 8*time.Hour),
		Limiter: repository.NewRateLimiter(rds), Redis: rds, Sealer: seal, Now: time.Now}
	stats := repository.NewStats(rds)
	web := &Server{Console: console, Apps: &logic.AppAdmin{Repo: repo, Sealer: seal, Now: time.Now}, Repo: repo, Stats: stats,
		Checks:         func(context.Context) []Check { return []Check{{Name: "MySQL", OK: true}, {Name: "Redis", OK: true}} },
		PublicEndpoint: "https://gw.example", SessionTTL: 8 * time.Hour, Info: map[string]string{"KEK": "k1"}}
	rs := rest.MustNewServer(rest.RestConf{Host: "127.0.0.1", Port: 0})
	if err := web.Register(rs); err != nil {
		t.Fatal(err)
	}
	r := router.NewRouter()
	for _, route := range rs.Routes() {
		if err := r.Handle(route.Method, route.Path, route.Handler); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return &env{t: t, srv: srv, console: console, stats: stats, apps: repo}
}

type browser struct {
	t       *testing.T
	base    string
	client  *http.Client
	referer string
}

func (e *env) browser() *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t: e.t, base: e.srv.URL, client: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (b *browser) do(method, path string, form url.Values, origin string) (*http.Response, string) {
	b.t.Helper()
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req, _ := http.NewRequest(method, b.base+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if b.referer != "" {
		req.Header.Set("Referer", b.base+b.referer)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp, sb.String()
}

func (b *browser) get(path string) (*http.Response, string) { return b.do("GET", path, nil, "") }

func (b *browser) post(path string, form url.Values) (*http.Response, string) {
	return b.do("POST", path, form, b.base)
}

var csrfRe = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)

func (b *browser) csrf(page string) string {
	_, body := b.get(page)
	b.referer = page
	m := csrfRe.FindStringSubmatch(body)
	if m == nil {
		b.t.Fatalf("no csrf on %s", page)
	}
	return m[1]
}

func (b *browser) login(user, pw string) {
	b.t.Helper()
	resp, body := b.post("/login", url.Values{"username": {user}, "password": {pw}, "next": {"/"}})
	if resp.StatusCode != http.StatusSeeOther {
		b.t.Fatalf("login %s: %d %s", user, resp.StatusCode, body)
	}
}

func TestConsoleEndToEnd(t *testing.T) {
	e := newEnv(t)
	ctx := repository.WithActor(context.Background(), "cli:test", "")
	pw, err := e.console.CreateAdmin(ctx, nil, "", "root", "根", logic.RoleOwner)
	if err != nil {
		t.Fatal(err)
	}
	b := e.browser()

	// Signed out: pages redirect to the login form with a safe next.
	if resp, _ := b.get("/console"); resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/login?next=%2Fconsole") {
		t.Fatalf("signed out: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, body := b.get("/login")
	if resp.StatusCode != 200 || !strings.Contains(body, "登录管理后台") || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'self'") {
		t.Fatalf("login page: %d", resp.StatusCode)
	}
	if resp, body := b.post("/login", url.Values{"username": {"root"}, "password": {"wrong"}}); resp.StatusCode != 401 || !strings.Contains(body, "用户名或密码错误") {
		t.Fatalf("bad password: %d", resp.StatusCode)
	}
	// A cross-site login post is refused outright.
	if resp, _ := b.do("POST", "/login", url.Values{"username": {"root"}, "password": {pw}}, "https://evil.example"); resp.StatusCode != 403 {
		t.Fatalf("cross-site login: %d", resp.StatusCode)
	}
	b.login("root", pw)

	// A generated password must be changed before anything else.
	if resp, _ := b.get("/apps"); resp.Header.Get("Location") != "/profile?force=1" {
		t.Fatalf("forced change: %s", resp.Header.Get("Location"))
	}
	token := b.csrf("/profile?force=1")
	if resp, _ := b.post("/profile/password", url.Values{"_csrf": {token}, "current": {pw}, "new": {"Strong-Pass-2026"}, "confirm": {"Strong-Pass-2026"}}); resp.Header.Get("Location") != "/login" {
		t.Fatalf("password change: %s", resp.Header.Get("Location"))
	}
	b.login("root", "Strong-Pass-2026")

	// Every page renders for the owner.
	for _, page := range []string{"/", "/console", "/apps", "/stats", "/stats?range=7d", "/stats?range=30d", "/events", "/preview", "/audit", "/admins", "/profile", "/system", "/menus", "/pear-config"} {
		if resp, body := b.get(page); resp.StatusCode != 200 || strings.Contains(body, "页面渲染失败") {
			t.Fatalf("%s: %d %.300s", page, resp.StatusCode, body)
		}
	}

	// Create an app; it starts with the login action. The list comes back
	// and opens the new app in a tab of its own.
	token = b.csrf("/apps")
	resp, _ = b.post("/apps/create", url.Values{"_csrf": {token}, "name": {"商城"}, "remark": {"主站"}})
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/apps?open=app_") {
		t.Fatalf("create: %d %s", resp.StatusCode, loc)
	}
	key := strings.TrimPrefix(loc, "/apps?open=")
	if _, body := b.get(loc); !strings.Contains(body, `data-auto-open href="/apps/`+key+`" data-tab-open="app-`+key+`"`) {
		t.Fatal("new app not opened")
	}
	b.post("/apps/create", url.Values{"_csrf": {token}, "name": {"论坛"}})
	_, body = b.get("/apps/" + key)
	for _, want := range []string{"商城", key, "https://gw.example/sdk/v1/load.js", "还没有允许来源", "login", "data-app-switch", "论坛"} {
		if !strings.Contains(body, want) {
			t.Fatalf("app page missing %q", want)
		}
	}
	// A failed action returns to the page it came from, with the message.
	b.referer = "/apps/" + key + "?tab=origins"
	if resp, _ := b.post("/apps/"+key+"/origins/add", url.Values{"_csrf": {token}, "origin": {"https://shop.example/path"}}); resp.Header.Get("Location") != "/apps/"+key+"?tab=origins" {
		t.Fatalf("failed action went to %q", resp.Header.Get("Location"))
	}
	if _, body := b.get("/apps/" + key + "?tab=origins"); !strings.Contains(body, "不能带路径") {
		t.Fatal("error flash missing")
	}
	// Forms without the CSRF token are refused.
	if resp, _ := b.post("/apps/"+key+"/origins/add", url.Values{"origin": {"https://shop.example"}}); resp.StatusCode != 403 {
		t.Fatalf("no csrf: %d", resp.StatusCode)
	}
	b.post("/apps/"+key+"/origins/add", url.Values{"_csrf": {token}, "origin": {"https://shop.example"}})
	b.post("/apps/"+key+"/actions/save", url.Values{"_csrf": {token}, "action": {"pay"}, "enabled": {"1"}, "mode": {"interactive"},
		"kinds": {"rotate", "click_shape"}, "puzzles": {"8"}, "difficulty": {"4000"}})
	_, body = b.get("/apps/" + key + "?tab=actions")
	if !strings.Contains(body, "https://shop.example") || !strings.Contains(body, "旋转对齐") || !strings.Contains(body, "4,000") {
		t.Fatalf("origin/action not shown")
	}
	// The secret key is shown once, in a dialog over the app's secrets tab.
	resp, body = b.post("/apps/"+key+"/secrets/create", url.Values{"_csrf": {token}, "ttl_days": {"0"}})
	m := regexp.MustCompile(`id="skey">([A-Za-z0-9_-]{20,})<`).FindStringSubmatch(body)
	if resp.StatusCode != 200 || m == nil || !strings.Contains(body, `id="dlg-reveal" data-autoshow data-locked`) || !strings.Contains(body, "密钥对已创建") {
		t.Fatalf("secret dialog: %d", resp.StatusCode)
	}
	_, detail := b.get("/apps/" + key + "?tab=secrets")
	if strings.Contains(detail, m[1]) {
		t.Fatal("secret key shown again")
	}

	// Statistics written by the gateway show up.
	app, _ := e.apps.AppDetail(context.Background(), key)
	_ = e.stats.Record(context.Background(), app.ID, time.Now(), []string{"issue", "issue:rotate", "solve", "solve_ok", "solve_ok:rotate"},
		&repository.Event{Type: "solve", Action: "pay", Kind: "rotate", Result: "pass", At: time.Now().UnixMilli()})
	if _, body := b.get("/api/events"); !strings.Contains(body, `"kind":"旋转对齐"`) {
		t.Fatalf("events api: %s", body)
	}
	if _, body := b.get("/console"); !strings.Contains(body, "100.0%") {
		t.Fatal("dashboard pass rate missing")
	}
	if _, body := b.get("/api/preview?kind=rotate"); !strings.Contains(body, `"disc":"data:image/png;base64,`) {
		t.Fatal("rotate preview")
	}

	// The audit log has the whole story, and no secret.
	_, body = b.get("/audit")
	for _, want := range []string{"创建应用", "添加来源", "保存策略", "创建密钥对", "修改密码", "登录失败"} {
		if !strings.Contains(body, want) {
			t.Errorf("audit missing %s", want)
		}
	}
	if strings.Contains(body, m[1]) || strings.Contains(body, "Strong-Pass") {
		t.Fatal("secret in audit page")
	}

	// A viewer can look but not touch.
	token = b.csrf("/admins")
	resp, body = b.post("/admins/create", url.Values{"_csrf": {token}, "username": {"viewer1"}, "role": {"viewer"}})
	vp := regexp.MustCompile(`id="pw">([^<]+)<`).FindStringSubmatch(body)
	if resp.StatusCode != 200 || vp == nil || !strings.Contains(body, `id="dlg-reveal"`) || !strings.Contains(body, `data-form-action="/admins/`) {
		t.Fatalf("create viewer: %d", resp.StatusCode)
	}
	vp[1] = html.UnescapeString(vp[1])
	v := e.browser()
	v.login("viewer1", vp[1])
	vt := v.csrf("/profile?force=1")
	v.post("/profile/password", url.Values{"_csrf": {vt}, "current": {vp[1]}, "new": {"Viewer-Pass-2026"}, "confirm": {"Viewer-Pass-2026"}})
	v.login("viewer1", "Viewer-Pass-2026")
	if resp, _ := v.get("/apps/" + key); resp.StatusCode != 200 {
		t.Fatalf("viewer view: %d", resp.StatusCode)
	}
	if resp, _ := v.get("/admins"); resp.StatusCode != 403 {
		t.Fatalf("viewer admins: %d", resp.StatusCode)
	}
	if resp, _ := v.get("/audit"); resp.StatusCode != 403 {
		t.Fatalf("viewer audit: %d", resp.StatusCode)
	}
	vt = v.csrf("/profile")
	if resp, _ := v.post("/apps/"+key+"/secrets/create", url.Values{"_csrf": {vt}}); resp.StatusCode != 403 {
		t.Fatalf("viewer secret create: %d", resp.StatusCode)
	}
	if _, body := v.get("/menus"); strings.Contains(body, "/admins") || strings.Contains(body, "/audit") {
		t.Fatal("viewer menu shows admin pages")
	}

	// Logging out ends the session.
	token = b.csrf("/profile")
	b.post("/logout", url.Values{"_csrf": {token}})
	if resp, _ := b.get("/console"); resp.StatusCode != http.StatusSeeOther {
		t.Fatal("still signed in after logout")
	}
}
