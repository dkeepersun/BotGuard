package adminweb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
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
