package ctl

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/dkeepersun/BotGuard/internal/testdb"
)

type runner struct {
	t   *testing.T
	app *App
	out *bytes.Buffer
}

func newRunner(t *testing.T, dsn string) *runner {
	out := &bytes.Buffer{}
	app := New(func(k string) string {
		if k == DSNEnv {
			return dsn
		}
		return ""
	}, out)
	return &runner{t: t, app: app, out: out}
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
	r := newRunner(t, "")
	r.run(2)
	r.run(2, "site")
	r.run(2, "site", "explode")
	r.run(2, "site", "create")
	r.run(2, "origin", "add", "-site", "pk_x")
	r.run(2, "site", "list", "extra")
	if out := r.run(1, "site", "list"); !strings.Contains(out, "BOTGUARD_MYSQL_DSN") {
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
	site := field(t, r.run(0, "site", "create", "-name", "测试站点"), "site_key")

	if out := r.run(0, "origin", "add", "-site", site, "-origin", "HTTPS://A.Example:443"); !strings.Contains(out, "https://a.example") {
		t.Fatalf("normalized origin: %s", out)
	}
	r.run(1, "origin", "add", "-site", site, "-origin", "https://a.example")
	r.run(1, "origin", "add", "-site", site, "-origin", "https://a.example/login")
	r.run(1, "origin", "add", "-site", "pk_missing_site", "-origin", "https://b.example")

	r.run(0, "action", "set", "-site", site, "-action", "login", "-difficulty", "3000")
	r.run(0, "action", "set", "-site", site, "-action", "login", "-enabled=false")
	r.run(1, "action", "set", "-site", site, "-action", "login", "-puzzles", "33")
	r.run(1, "action", "set", "-site", site, "-action", "Bad")

	key := field(t, r.run(0, "key", "create", "-site", site, "-ttl", "720h"), "key_id")
	rotated := r.run(0, "key", "rotate", "-key", key, "-grace", "1h")
	newKey := field(t, rotated, "key_id")
	if !strings.Contains(rotated, "2026-10-20T01:00:00Z") {
		t.Fatalf("grace end: %s", rotated)
	}
	r.run(0, "key", "revoke", "-key", key)
	r.run(1, "key", "rotate", "-key", key)
	r.run(1, "key", "revoke", "-key", "sk_does_not_exist")

	r.run(0, "site", "disable", "-site", site)
	show := r.run(0, "site", "show", "-site", site)
	flat := strings.Join(strings.Fields(show), " ")
	for _, want := range []string{"（停用）", "https://a.example", "login 停用 8 3000",
		// Rotation shortened the old key's 720h life to the 1h grace.
		key + " 已吊销 2026-10-20T01:00:00Z", newKey + " 启用 永不 " + key} {
		if !strings.Contains(flat, want) {
			t.Errorf("site show missing %q:\n%s", want, show)
		}
	}
	if strings.Contains(show, "secret") {
		t.Error("site show must not mention secrets")
	}
	// origin add, two action sets, rotate does not touch policy: 1 + 1 + 2.
	if !strings.Contains(show, "策略版本：4") {
		t.Errorf("policy version:\n%s", show)
	}
	if out := r.run(0, "migrate", "status"); !strings.Contains(out, "已执行") {
		t.Fatalf("status: %s", out)
	}
}
