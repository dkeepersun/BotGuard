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
	kek string
}

func newRunner(t *testing.T, dsn string) *runner {
	r := &runner{t: t, out: &bytes.Buffer{}}
	r.app = New(func(k string) string {
		switch k {
		case DSNEnv:
			return dsn
		case KEKEnv:
			return r.kek
		}
		return ""
	}, r.out)
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
	r := newRunner(t, "")
	r.run(2)
	r.run(2, "app")
	r.run(2, "app", "explode")
	r.run(2, "app", "create")
	r.run(2, "origin", "add", "-app", "pk_x")
	r.run(2, "app", "list", "extra")
	if out := r.run(1, "app", "list"); !strings.Contains(out, "BOTGUARD_MYSQL_DSN") {
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

	r.run(0, "action", "set", "-app", app, "-action", "login", "-difficulty", "3000")
	r.run(0, "action", "set", "-app", app, "-action", "login", "-enabled=false")
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

	r.run(0, "app", "disable", "-app", app)
	show := r.run(0, "app", "show", "-app", app)
	flat := strings.Join(strings.Fields(show), " ")
	for _, want := range []string{"（停用）", "https://a.example", "login 停用 8 3000",
		// Rotation shortened the old pair's 720h life to the 1h grace.
		key + " 已吊销 2026-10-20T01:00:00Z", newKey + " 启用 永不 " + key} {
		if !strings.Contains(flat, want) {
			t.Errorf("app show missing %q:\n%s", want, show)
		}
	}
	if strings.Contains(show, secretKey) || strings.Contains(show, "secret_key") {
		t.Error("app show must not reveal secret keys")
	}
	// origin add, two action sets, rotate does not touch policy: 1 + 1 + 2.
	if !strings.Contains(show, "策略版本：4") {
		t.Errorf("policy version:\n%s", show)
	}
	if out := r.run(0, "migrate", "status"); !strings.Contains(out, "已执行") {
		t.Fatalf("status: %s", out)
	}
}
