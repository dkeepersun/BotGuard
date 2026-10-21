package ctl

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/dkeepersun/BotGuard/internal/service/password"
	"github.com/dkeepersun/BotGuard/internal/store"
	"github.com/dkeepersun/BotGuard/internal/testdb"
)

type runner struct {
	t   *testing.T
	app *App
	out *bytes.Buffer
	kek string
	pw  string
}

func newRunner(t *testing.T, db store.Config) *runner {
	r := &runner{t: t, out: &bytes.Buffer{}}
	r.app = New(func(k string) string {
		switch k {
		case DriverEnv:
			return db.Driver
		case DSNEnv:
			return db.DataSource
		case KEKEnv:
			return r.kek
		case AdminPasswordEnv:
			return r.pw
		}
		return ""
	}, r.out, r.out)
	return r
}

func (r *runner) run(want int, args ...string) string {
	r.t.Helper()
	r.out.Reset()
	if code := r.app.Run(context.Background(), args); code != want {
		r.t.Fatalf("%v: exit %d, want %d\n%s", args, code, want, r.out.String())
	}
	return r.out.String()
}

func field(t *testing.T, out, name string) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `: (\S+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no %s in:\n%s", name, out)
	}
	return m[1]
}

func TestUsageErrorsNeedNoDatabase(t *testing.T) {
	r := newRunner(t, store.Config{Driver: "oracle"})
	r.run(2)
	r.run(2, "app")
	r.run(2, "app", "explode")
	r.run(2, "app", "create")
	r.run(2, "origin", "add", "-app", "pk_x")
	r.run(2, "app", "list", "extra")
	r.run(2, "action", "set", "-app", "pk_x", "-action", "login", "-mode", "slider", "-kinds", "click_text")
	if out := r.run(1, "app", "list"); !strings.Contains(out, DriverEnv) {
		t.Fatalf("missing DSN message: %s", out)
	}
	if out := r.run(0, "help"); !strings.Contains(out, "migrate up") {
		t.Fatal("help text")
	}
}

func TestOperatorWorkflow(t *testing.T) {
	dsn, _ := testdb.Open(t)
	r := newRunner(t, dsn)
	r.app.Now = func() time.Time { return time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC) }

	if out := r.run(0, "migrate", "up"); !strings.Contains(out, "没有待执行") {
		t.Fatalf("migrate up on migrated db: %s", out)
	}
	app := field(t, r.run(0, "app", "create", "-name", "测试应用"), "app_key")

	if out := r.run(0, "origin", "add", "-app", app, "-origin", "HTTPS://A.Example:443"); !strings.Contains(out, "https://a.example") {
		t.Fatalf("normalized origin: %s", out)
	}
	r.run(1, "origin", "add", "-app", app, "-origin", "https://a.example")
	r.run(1, "origin", "add", "-app", app, "-origin", "https://a.example/login")
	r.run(1, "origin", "add", "-app", "pk_missing_app", "-origin", "https://b.example")

	if out := r.run(0, "action", "set", "-app", app, "-action", "login", "-difficulty", "3000"); !strings.Contains(out, "模式 adaptive") {
		t.Fatalf("new action defaults to adaptive: %s", out)
	}
	r.run(0, "action", "set", "-app", app, "-action", "login", "-mode", "slider")
	if out := r.run(0, "action", "set", "-app", app, "-action", "login", "-enabled=false"); !strings.Contains(out, "模式 interactive，题型 slider，") {
		t.Fatalf("unspecified mode must be kept: %s", out)
	}
	r.run(1, "action", "set", "-app", app, "-action", "login", "-mode", "puzzle")
	r.run(1, "action", "set", "-app", app, "-action", "login", "-kinds", "no_such_kind")
	r.run(1, "action", "set", "-app", app, "-action", "login", "-kinds", "")
	if out := r.run(0, "action", "set", "-app", app, "-action", "post", "-mode", "interactive", "-kinds", "click_text, click_shape"); !strings.Contains(out, "题型 click_text,click_shape") {
		t.Fatalf("kinds: %s", out)
	}
	r.run(1, "action", "set", "-app", app, "-action", "login", "-puzzles", "33")
	r.run(1, "action", "set", "-app", app, "-action", "Bad")

	// Without the KEK, secret pairs cannot be sealed.
	if out := r.run(1, "secret", "create", "-app", app); !strings.Contains(out, "BOTGUARD_SECRET_KEK") {
		t.Fatalf("missing KEK: %s", out)
	}
	r.kek = "k1:" + strings.Repeat("A", 43) + "="
	created := r.run(0, "secret", "create", "-app", app, "-ttl", "720h")
	key := field(t, created, "secret_id")
	secretKey := field(t, created, "secret_key")
	if len(secretKey) != 43 {
		t.Fatalf("secret_key: %s", created)
	}
	rotated := r.run(0, "secret", "rotate", "-secret", key, "-grace", "1h")
	newKey := field(t, rotated, "secret_id")
	if !strings.Contains(rotated, "2026-10-20T01:00:00Z") {
		t.Fatalf("grace end: %s", rotated)
	}
	r.run(0, "secret", "revoke", "-secret", key)
	r.run(1, "secret", "rotate", "-secret", key)
	r.run(1, "secret", "revoke", "-secret", "sid_does_not_exist")

	// Rotation prints when the old pair really stops: a 30m pair stays 30m.
	short := field(t, r.run(0, "secret", "create", "-app", app, "-ttl", "30m"), "secret_id")
	if out := r.run(0, "secret", "rotate", "-secret", short, "-grace", "1h"); !strings.Contains(out, "2026-10-20T00:30:00Z") {
		t.Fatalf("short pair rotation: %s", out)
	}
	// A KEK with the gateway's id but another value would seal secrets the
	// gateway cannot open: refuse before creating anything.
	good := r.kek
	r.kek = "k1:" + strings.Repeat("B", 43) + "="
	if out := r.run(1, "secret", "create", "-app", app); !strings.Contains(out, "不一致") {
		t.Fatalf("KEK mismatch: %s", out)
	}
	r.kek = "k2:" + strings.Repeat("B", 43) + "="
	if out := r.run(0, "secret", "create", "-app", app); !strings.Contains(out, "其他 KEK id") {
		t.Fatalf("other KEK id: %s", out)
	}
	r.kek = good

	r.run(0, "app", "disable", "-app", app)
	show := r.run(0, "app", "show", "-app", app)
	flat := strings.Join(strings.Fields(show), " ")
	for _, want := range []string{"（停用）", "https://a.example", "login 停用 interactive slider 8 3000", "post 启用 interactive click_text,click_shape",
		// Rotation shortened the old pair's 720h life to the 1h grace.
		key + " 已吊销 2026-10-20T01:00:00Z", newKey + " 启用 永不 " + key} {
		if !strings.Contains(flat, want) {
			t.Errorf("app show missing %q:\n%s", want, show)
		}
	}
	if strings.Contains(show, secretKey) || strings.Contains(show, "secret_key") {
		t.Error("app show must not reveal secret keys")
	}
	// origin add, four action sets, rotate does not touch policy: 1 + 1 + 4.
	if !strings.Contains(show, "策略版本：6") {
		t.Errorf("policy version:\n%s", show)
	}
	if out := r.run(0, "migrate", "status"); !strings.Contains(out, "已执行") {
		t.Fatalf("status: %s", out)
	}
	// Everything above is recent: nothing to purge yet.
	if out := r.run(0, "data", "purge"); !strings.Contains(out, "审计日志 0 条、失效密钥对 0 个") {
		t.Fatalf("purge: %s", out)
	}
	r.run(2, "data", "purge", "-audit-days", "-1")
}

// Scripts capture stdout for app keys and secrets; errors must not vanish into it.
func TestErrorsGoToStderr(t *testing.T) {
	var out, errOut bytes.Buffer
	app := New(func(k string) string { return map[string]string{DriverEnv: "oracle"}[k] }, &out, &errOut)
	if code := app.Run(context.Background(), []string{"app", "list"}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if out.Len() != 0 || !strings.Contains(errOut.String(), DriverEnv) {
		t.Fatalf("stdout %q, stderr %q", out.String(), errOut.String())
	}
}

func TestAdminCommands(t *testing.T) {
	old := password.Iterations
	password.Iterations = 1000
	defer func() { password.Iterations = old }()
	dsn, _ := testdb.Open(t)
	r := newRunner(t, dsn)
	out := r.run(0, "admin", "create", "-username", "Root", "-name", "运维负责人")
	pw := field(t, out, "password")
	if field(t, out, "username") != "root" || len(pw) != 20 {
		t.Fatal(out)
	}
	r.run(1, "admin", "create", "-username", "root")
	r.run(1, "admin", "create", "-username", "x")
	r.run(1, "admin", "create", "-username", "ops", "-role", "god")
	r.run(0, "admin", "create", "-username", "ops", "-role", "viewer")
	// The only enabled owner cannot be disabled, even from the CLI.
	r.run(1, "admin", "disable", "-username", "root")
	r.run(0, "admin", "disable", "-username", "ops")
	reset := r.run(0, "admin", "reset-password", "-username", "root")
	if field(t, reset, "password") == pw {
		t.Fatal("reset kept the password")
	}
	// set-password: from the environment only, policy unless -allow-weak.
	r.run(2, "admin", "set-password", "-username", "root")
	r.pw = "123456"
	if out := r.run(1, "admin", "set-password", "-username", "root"); !strings.Contains(out, "10–128") {
		t.Fatalf("weak password accepted: %s", out)
	}
	if out := r.run(0, "admin", "set-password", "-username", "root", "-allow-weak"); !strings.Contains(out, "警告") {
		t.Fatalf("no warning: %s", out)
	}
	list := strings.Join(strings.Fields(r.run(0, "admin", "list")), " ")
	for _, want := range []string{"root 运维负责人 超级管理员 启用 未开启 从未", "ops 只读 停用"} {
		if !strings.Contains(list, want) {
			t.Errorf("admin list missing %q: %s", want, list)
		}
	}
	if strings.Contains(list, pw) {
		t.Fatal("password listed")
	}
}
