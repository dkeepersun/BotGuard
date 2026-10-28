package adminweb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dkeepersun/BotGuard/internal/logic"
	"github.com/dkeepersun/BotGuard/internal/repository"
	"github.com/dkeepersun/BotGuard/internal/service/update"
)

// signIn creates an admin with role and returns a browser signed in with a
// changed password.
func (e *env) signIn(username, role string) *browser {
	e.t.Helper()
	ctx := repository.WithActor(context.Background(), "cli:test", "")
	pw, err := e.console.CreateAdmin(ctx, nil, "", username, "", role)
	if err != nil {
		e.t.Fatal(err)
	}
	b := e.browser()
	b.login(username, pw)
	token := b.csrf("/profile?force=1")
	if resp, _ := b.post("/profile/password", url.Values{"_csrf": {token}, "current": {pw}, "new": {"Strong-Pass-2026"}, "confirm": {"Strong-Pass-2026"}}); resp.Header.Get("Location") != "/login" {
		e.t.Fatalf("password change: %s", resp.Header.Get("Location"))
	}
	b.login(username, "Strong-Pass-2026")
	return b
}

func TestUpdateNotice(t *testing.T) {
	e := newEnv(t)
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v0.6.0","html_url":"https://github.com/dkeepersun/BotGuard/releases/tag/v0.6.0","published_at":"2026-10-27T08:00:00Z"}`))
	}))
	t.Cleanup(gh.Close)
	e.web.Version = "v0.5.0 (d1fbb28)"
	e.web.Updates = &update.Checker{URL: gh.URL, Current: "v0.5.0", Interval: time.Hour}
	if err := e.web.Updates.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	// One gateway on the console's version, one left behind.
	minute := time.Now().Truncate(time.Minute).Add(-time.Minute).Unix()
	for _, m := range []repository.HealthMinute{{At: minute, Instance: "gw-1:8090", Version: "v0.5.0 (d1fbb28)"}, {At: minute, Instance: "gw-2:8090", Version: "v0.4.0 (1bffcb5)"}} {
		if err := e.health.Put(context.Background(), m, nil); err != nil {
			t.Fatal(err)
		}
	}

	owner := e.signIn("root", logic.RoleOwner)
	if _, body := owner.get("/console"); !strings.Contains(body, "BotGuard v0.6.0 已发布") || !strings.Contains(body, "gateway 版本不一致") ||
		!strings.Contains(body, `href="https://github.com/dkeepersun/BotGuard/releases/tag/v0.6.0"`) {
		t.Fatalf("owner dashboard: %.2000s", body)
	}
	_, body := owner.get("/system")
	for _, want := range []string{"v0.6.0", "有新版本", "立即检查", "v0.4.0 (1bffcb5)", "gw-2:8090", "与管理后台不同", "与管理后台一致", "sudo dnf upgrade botguard"} {
		if !strings.Contains(body, want) {
			t.Fatalf("system page lacks %q", want)
		}
	}
	if _, body := owner.get("/health"); !strings.Contains(body, "v0.4.0 (1bffcb5)") || !strings.Contains(body, "与管理后台不同") {
		t.Fatalf("health page: %.2000s", body)
	}
	// Checking again right away is refused, with a message rather than an error page.
	token := owner.csrf("/system")
	resp, _ := owner.post("/system/update-check", url.Values{"_csrf": {token}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("update check: %d", resp.StatusCode)
	}
	if _, body := owner.get("/system"); !strings.Contains(body, "刚刚检查过") {
		t.Fatalf("too-soon flash missing")
	}

	// Operators see versions but get no notice and cannot trigger a check.
	ops := e.signIn("ops", logic.RoleOperator)
	if _, body := ops.get("/console"); strings.Contains(body, "已发布") || strings.Contains(body, "版本不一致") {
		t.Fatal("operator sees the update notice")
	}
	if _, body := ops.get("/system"); !strings.Contains(body, "v0.6.0") || strings.Contains(body, "立即检查") {
		t.Fatal("operator system page")
	}
	// The form is not shown to operators; take a token from another page.
	token = ops.csrf("/profile")
	if resp, _ := ops.post("/system/update-check", url.Values{"_csrf": {token}}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("operator update check: %d", resp.StatusCode)
	}
}

// With checking off, the system page says so and nothing is announced.
func TestUpdateCheckOff(t *testing.T) {
	e := newEnv(t)
	e.web.Version = "v0.5.0 (d1fbb28)"
	owner := e.signIn("root", logic.RoleOwner)
	if _, body := owner.get("/console"); strings.Contains(body, "已发布") {
		t.Fatal("notice without a checker")
	}
	_, body := owner.get("/system")
	if !strings.Contains(body, "已关闭检查") || strings.Contains(body, "立即检查") || !strings.Contains(body, "最近 3 分钟没有 gateway 上报") {
		t.Fatalf("system page: %.3000s", body)
	}
}

// newUpgradeEnv is a console that knows v0.6.0 is out and has the updater
// installed (status ready) in temporary directories.
func newUpgradeEnv(t *testing.T) (*env, *update.Upgrader) {
	t.Helper()
	e := newEnv(t)
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v0.6.0"}`))
	}))
	t.Cleanup(gh.Close)
	e.web.Version = "v0.5.0 (d1fbb28)"
	e.web.Updates = &update.Checker{URL: gh.URL, Current: "v0.5.0"}
	if err := e.web.Updates.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	u := &update.Upgrader{RequestFile: filepath.Join(t.TempDir(), "update-request"), Dir: t.TempDir()}
	if err := os.WriteFile(filepath.Join(u.Dir, "status.json"), []byte(`{"state":"ready","method":"binary"}`), 0o640); err != nil {
		t.Fatal(err)
	}
	e.web.Upgrader = u
	return e, u
}

func TestOneClickUpgrade(t *testing.T) {
	e, u := newUpgradeEnv(t)
	owner := e.signIn("root", logic.RoleOwner)
	if _, body := owner.get("/console"); !strings.Contains(body, "去升级") {
		t.Fatal("dashboard offers no upgrade")
	}
	_, body := owner.get("/system")
	for _, want := range []string{"升级到 v0.6.0", `id="dlg-upgrade"`, `name="version" value="v0.6.0"`, "发布包安装", "其他服务器的升级方法"} {
		if !strings.Contains(body, want) {
			t.Fatalf("system page lacks %q", want)
		}
	}
	token := owner.csrf("/system")
	post := func(form url.Values) string {
		t.Helper()
		form.Set("_csrf", token)
		resp, _ := owner.post("/system/upgrade", form)
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("upgrade post: %d", resp.StatusCode)
		}
		return resp.Header.Get("Location")
	}
	// A wrong password or a version the console does not offer writes nothing.
	post(url.Values{"version": {"v0.6.0"}, "password": {"wrong"}})
	if _, body := owner.get("/system"); !strings.Contains(body, "当前密码不正确") {
		t.Fatal("wrong password not reported")
	}
	post(url.Values{"version": {"v0.4.0"}, "password": {"Strong-Pass-2026"}})
	if _, body := owner.get("/system"); !strings.Contains(body, "版本信息已变化") {
		t.Fatal("stale version not reported")
	}
	if _, err := os.Stat(u.RequestFile); !os.IsNotExist(err) {
		t.Fatalf("request written: %v", err)
	}

	if loc := post(url.Values{"version": {"v0.6.0"}, "password": {"Strong-Pass-2026"}}); loc != "/system/upgrade" {
		t.Fatalf("upgrade redirect %q", loc)
	}
	b, err := os.ReadFile(u.RequestFile)
	if err != nil || !strings.HasPrefix(string(b), "version=v0.6.0\nrequested_by=root\n") {
		t.Fatalf("request %q %v", b, err)
	}
	if _, body := owner.get("/system/upgrade"); !strings.Contains(body, "已提交，等待升级服务启动") || !strings.Contains(body, "data-upgrade-poll") {
		t.Fatalf("progress page: %.1500s", body)
	}
	if _, body := owner.get("/api/upgrade"); !strings.Contains(body, `"busy":true`) || !strings.Contains(body, `"pending":true`) {
		t.Fatalf("progress api: %s", body)
	}
	if _, body := owner.get("/audit"); !strings.Contains(body, "一键升级") || !strings.Contains(body, "v0.6.0") {
		t.Fatal("upgrade not audited")
	}
	// While a request waits there is no second one, and the page says so.
	post(url.Values{"version": {"v0.6.0"}, "password": {"Strong-Pass-2026"}})
	if _, body := owner.get("/system"); !strings.Contains(body, "已有一个升级正在进行") || !strings.Contains(body, "查看进度") || strings.Contains(body, `id="dlg-upgrade"`) {
		t.Fatal("busy updater still offers an upgrade")
	}

	// The updater's result and log show on the progress page.
	_ = os.Remove(u.RequestFile)
	if err := os.WriteFile(filepath.Join(u.Dir, "status.json"), []byte(`{"state":"failed","method":"binary","from":"v0.5.0","to":"v0.6.0","requested_by":"root","message":"安装失败（退出码 1），见日志","started_at":"2026-10-28T08:00:00Z","finished_at":"2026-10-28T08:01:00Z"}`), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(u.Dir, "update.log"), []byte("下载失败 <b>x</b>\n失败: 安装失败\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	_, body = owner.get("/system/upgrade")
	if !strings.Contains(body, "安装失败（退出码 1），见日志") || !strings.Contains(body, "下载失败 &lt;b&gt;x&lt;/b&gt;") || !strings.Contains(body, "v0.5.0 → v0.6.0") {
		t.Fatalf("failed run: %.3000s", body)
	}

	// Operators can neither start an upgrade nor watch one.
	ops := e.signIn("ops", logic.RoleOperator)
	if resp, _ := ops.get("/system/upgrade"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("operator progress page: %d", resp.StatusCode)
	}
	if _, body := ops.get("/system"); strings.Contains(body, "dlg-upgrade") {
		t.Fatal("operator sees the upgrade dialog")
	}
	token = ops.csrf("/profile")
	if resp, _ := ops.post("/system/upgrade", url.Values{"_csrf": {token}, "version": {"v0.6.0"}, "password": {"Strong-Pass-2026"}}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("operator upgrade: %d", resp.StatusCode)
	}
}

// Without the updater (source or Docker installs) the page gives commands only.
func TestOneClickUpgradeUnavailable(t *testing.T) {
	e, u := newUpgradeEnv(t)
	_ = os.Remove(filepath.Join(u.Dir, "status.json"))
	owner := e.signIn("root", logic.RoleOwner)
	_, body := owner.get("/system")
	if !strings.Contains(body, "这台服务器不可用") || strings.Contains(body, "dlg-upgrade") || !strings.Contains(body, "sudo botguard upgrade") {
		t.Fatalf("system page: %.3000s", body)
	}
	token := owner.csrf("/system")
	owner.post("/system/upgrade", url.Values{"_csrf": {token}, "version": {"v0.6.0"}, "password": {"Strong-Pass-2026"}})
	if _, err := os.Stat(u.RequestFile); !os.IsNotExist(err) {
		t.Fatal("request written without an updater")
	}
}

// A request the updater never picked up is reported, and can be submitted again.
func TestOneClickUpgradeStalled(t *testing.T) {
	e, u := newUpgradeEnv(t)
	if err := u.Request("v0.6.0", "root"); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-5 * time.Minute)
	if err := os.Chtimes(u.RequestFile, old, old); err != nil {
		t.Fatal(err)
	}
	owner := e.signIn("root", logic.RoleOwner)
	_, body := owner.get("/system")
	if !strings.Contains(body, "botguard-update.path") || !strings.Contains(body, `id="dlg-upgrade"`) {
		t.Fatalf("system page: %.3000s", body)
	}
	if _, body := owner.get("/api/upgrade"); !strings.Contains(body, `"stalled":true`) || !strings.Contains(body, `"busy":false`) {
		t.Fatalf("api: %s", body)
	}
	token := owner.csrf("/system")
	if resp, _ := owner.post("/system/upgrade", url.Values{"_csrf": {token}, "version": {"v0.6.0"}, "password": {"Strong-Pass-2026"}}); resp.Header.Get("Location") != "/system/upgrade" {
		t.Fatalf("resubmit: %s", resp.Header.Get("Location"))
	}
	if st, err := os.Stat(u.RequestFile); err != nil || time.Since(st.ModTime()) > time.Minute {
		t.Fatalf("request not replaced: %v", err)
	}
}

// Right after an upgrade restarts everything, a gateway's latest summary is
// still the replaced process's: it waits for a report instead of raising the
// version notice, and is compared again once it reports.
func TestGatewayAwaitingReport(t *testing.T) {
	e := newEnv(t)
	now := time.Now()
	e.web.Version = "v0.6.0 (401fba6)"
	e.web.Started = now
	before := now.Truncate(time.Minute).Add(-time.Minute).Unix()
	if err := e.health.Put(context.Background(), repository.HealthMinute{At: before, Instance: "gw-1:8090"}, nil); err != nil {
		t.Fatal(err)
	}
	owner := e.signIn("root", logic.RoleOwner)
	if _, body := owner.get("/console"); strings.Contains(body, "版本不一致") {
		t.Fatal("version notice for a gateway that has not reported since the restart")
	}
	if _, body := owner.get("/system"); !strings.Contains(body, "等待上报") || !strings.Contains(body, "gw-1:8090") || strings.Contains(body, "与管理后台不同") {
		t.Fatalf("system page: %.3000s", body)
	}
	if _, body := owner.get("/health"); !strings.Contains(body, "等待上报") {
		t.Fatal("health page lacks the pending badge")
	}

	// Its first report since the start is compared as usual.
	if err := e.health.Put(context.Background(), repository.HealthMinute{At: now.Truncate(time.Minute).Unix(), Instance: "gw-1:8090", Version: "v0.5.0 (d1fbb28)"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, body := owner.get("/console"); !strings.Contains(body, "版本不一致") {
		t.Fatal("no notice once the gateway reported another version")
	}
	if _, body := owner.get("/system"); strings.Contains(body, "等待上报") || !strings.Contains(body, "与管理后台不同") {
		t.Fatal("system page after the report")
	}
}
