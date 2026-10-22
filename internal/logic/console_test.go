package logic

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/dkeepersun/BotGuard/internal/repository"
	"github.com/dkeepersun/BotGuard/internal/service/password"
	"github.com/dkeepersun/BotGuard/internal/service/totp"
	"github.com/dkeepersun/BotGuard/internal/testdb"
	"github.com/zeromicro/go-zero/core/stores/redis"
)

func TestRolePermissions(t *testing.T) {
	for _, c := range []struct {
		role, perm string
		ok         bool
	}{
		{RoleOwner, PermManageAdmins, true}, {RoleOperator, PermManageAdmins, false}, {RoleOperator, PermManageSecrets, true},
		{RoleViewer, PermView, true}, {RoleViewer, PermManageApps, false}, {RoleViewer, PermAudit, false}, {"", PermView, false},
	} {
		if Allowed(c.role, c.perm) != c.ok {
			t.Errorf("%s %s", c.role, c.perm)
		}
	}
}

func newConsole(t *testing.T) (*Console, *time.Time) {
	t.Helper()
	old := password.Iterations
	password.Iterations = 1000
	t.Cleanup(func() { password.Iterations = old })
	_, conn := testdb.Open(t)
	mr := miniredis.RunT(t)
	rds := redis.New(mr.Addr(), redis.WithIdentity())
	clock := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	c := &Console{Admins: repository.NewAdmins(conn), Audit: repository.NewAppAdmin(conn),
		Sessions: repository.NewSessions(rds, 30*time.Minute, 8*time.Hour), Limiter: repository.NewRateLimiter(rds),
		Redis: rds, Sealer: testSealer(t), Now: func() time.Time { return clock }}
	return c, &clock
}

func TestConsoleLoginSessionsAndTOTP(t *testing.T) {
	c, clock := newConsole(t)
	ctx := context.Background()
	// Bootstrap (botguard-ctl) acts as the operating system user.
	pw, err := c.CreateAdmin(repository.WithActor(ctx, "cli:test", ""), nil, "", "Owner", "所有者", RoleOwner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateAdmin(ctx, nil, "", "owner", "", RoleOwner); err == nil {
		t.Fatal("duplicate username accepted")
	}
	if _, err := c.Login(ctx, LoginInput{Username: "owner", Password: "wrong", IP: "203.0.113.1"}); !errors.Is(err, ErrLoginFailed) {
		t.Fatal(err)
	}
	if _, err := c.Login(ctx, LoginInput{Username: "ghost", Password: pw, IP: "203.0.113.1"}); !errors.Is(err, ErrLoginFailed) {
		t.Fatal("unknown user must look like a wrong password", err)
	}
	p, err := c.Login(ctx, LoginInput{Username: " OWNER ", Password: pw, IP: "203.0.113.1"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Admin.MustChangePassword || p.Session.CSRF == "" {
		t.Fatalf("%+v", p.Admin)
	}
	got, err := c.Authenticate(ctx, p.Session.ID)
	if err != nil || got == nil || got.Admin.Username != "owner" || got.Session.CSRF != p.Session.CSRF {
		t.Fatal(got, err)
	}

	// Changing the password ends every session, this one included.
	if err := c.ChangePassword(ctx, got, "", pw, "short"); err == nil {
		t.Fatal("weak password accepted")
	}
	if err := c.ChangePassword(ctx, got, "", pw, "New-Secret-2026"); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.Authenticate(ctx, p.Session.ID); got != nil {
		t.Fatal("session survived a password change")
	}
	p, err = c.Login(ctx, LoginInput{Username: "owner", Password: "New-Secret-2026", IP: "203.0.113.1"})
	if err != nil || p.Admin.MustChangePassword {
		t.Fatal(p, err)
	}

	// TOTP: enrol, then the code is required and cannot be replayed.
	secret, uri, err := c.BeginTOTP(ctx, p)
	if err != nil || !strings.Contains(uri, secret) {
		t.Fatal(err)
	}
	code, _ := totp.Code(secret, *clock)
	if err := c.ConfirmTOTP(ctx, p, "", code); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Login(ctx, LoginInput{Username: "owner", Password: "New-Secret-2026"}); !errors.Is(err, ErrOTPRequired) {
		t.Fatal(err)
	}
	if _, err := c.Login(ctx, LoginInput{Username: "owner", Password: "New-Secret-2026", OTP: code}); !errors.Is(err, ErrOTPInvalid) {
		t.Fatal("enrolment code reused", err)
	}
	*clock = clock.Add(30 * time.Second)
	code, _ = totp.Code(secret, *clock)
	p, err = c.Login(ctx, LoginInput{Username: "owner", Password: "New-Secret-2026", OTP: code})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Login(ctx, LoginInput{Username: "owner", Password: "New-Secret-2026", OTP: code}); !errors.Is(err, ErrOTPInvalid) {
		t.Fatal("login code reused", err)
	}

	// The audit says whether the second factor was used.
	recent, _, _ := c.Audit.Audit(ctx, repository.AuditQuery{Operation: "auth.login", Limit: 20})
	var last *repository.AuditRecord
	for i := range recent {
		if recent[i].Operation == "auth.login" {
			last = &recent[i]
			break
		}
	}
	if last == nil || last.Detail != `{"mfa":true}` {
		t.Fatalf("login audit: %+v", last)
	}
	// Sessions expire after idle and absolute limits.
	*clock = clock.Add(9 * time.Hour)
	if got, _ := c.Authenticate(ctx, p.Session.ID); got != nil {
		t.Fatal("session outlived its absolute limit")
	}

	// Every step above is in the audit log, and no secret is.
	recs, total, err := c.Audit.Audit(ctx, repository.AuditQuery{Limit: 100})
	if err != nil || total < 8 {
		t.Fatal(total, err)
	}
	ops := map[string]int{}
	for _, r := range recs {
		ops[r.Operation]++
		if strings.Contains(r.Detail, secret) || strings.Contains(r.Detail, "New-Secret") {
			t.Fatalf("secret in audit: %+v", r)
		}
	}
	for _, op := range []string{"admin.create", "auth.login", "auth.login_failed", "admin.password_change", "admin.totp_enable"} {
		if ops[op] == 0 {
			t.Errorf("no %s in %v", op, ops)
		}
	}
}

func TestConsoleLockoutAndOwnerGuard(t *testing.T) {
	c, _ := newConsole(t)
	ctx := context.Background()
	pw, _ := c.CreateAdmin(ctx, nil, "", "owner", "", RoleOwner)
	for i := 0; i < 10; i++ {
		_, _ = c.Login(ctx, LoginInput{Username: "owner", Password: "nope", IP: "203.0.113.9"})
	}
	if _, err := c.Login(ctx, LoginInput{Username: "owner", Password: pw, IP: "198.51.100.1"}); !errors.Is(err, ErrLoginLocked) {
		t.Fatal("username not locked after 10 failures:", err)
	}
	c2, _ := newConsole(t)
	pw, _ = c2.CreateAdmin(ctx, nil, "", "owner", "", RoleOwner)
	p, err := c2.Login(ctx, LoginInput{Username: "owner", Password: pw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c2.CreateAdmin(ctx, p, "", "ops", "运维", "root"); err == nil {
		t.Fatal("unknown role accepted")
	}
	opsPW, err := c2.CreateAdmin(ctx, p, "", "ops", "运维", RoleOperator)
	if err != nil {
		t.Fatal(err)
	}
	ops, _ := c2.Admins.ByUsername(ctx, "ops")
	if err := c2.SetAdminStatus(ctx, p, "", p.Admin.ID, false); err == nil {
		t.Fatal("disabled self")
	}
	if err := c2.SetAdminRole(ctx, p, "", ops.ID, RoleOwner); err != nil {
		t.Fatal(err)
	}
	// Now there are two owners; demoting one is fine, the last one is not.
	opsP, err := c2.Login(ctx, LoginInput{Username: "ops", Password: opsPW})
	if err != nil {
		t.Fatal(err)
	}
	if err := c2.SetAdminRole(ctx, opsP, "", p.Admin.ID, RoleViewer); err != nil {
		t.Fatal(err)
	}
	if err := c2.SetAdminStatus(ctx, p, "", ops.ID, false); err == nil {
		t.Fatal("the last enabled owner was disabled")
	}
	// Reset ends the target's sessions and forces a new password.
	newPW, err := c2.ResetPassword(ctx, opsP, "", p.Admin.ID)
	if err != nil || newPW == pw {
		t.Fatal(err)
	}
	if got, _ := c2.Authenticate(ctx, p.Session.ID); got != nil {
		t.Fatal("session survived a reset")
	}
}

// The second login step takes only the code, with the ticket from the
// password step: wrong codes keep the ticket, a used ticket is gone, and a
// ticket allows a few attempts only.
func TestConsoleTwoStepTOTP(t *testing.T) {
	c, clock := newConsole(t)
	ctx := context.Background()
	pw, _ := c.CreateAdmin(ctx, nil, "", "ops", "", RoleOperator)
	p, err := c.Login(ctx, LoginInput{Username: "ops", Password: pw})
	if err != nil {
		t.Fatal(err)
	}
	secret, _, _ := c.BeginTOTP(ctx, p)
	code, _ := totp.Code(secret, *clock)
	if err := c.ConfirmTOTP(ctx, p, "", code); err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(30 * time.Second)

	_, err = c.Login(ctx, LoginInput{Username: "ops", Password: pw, IP: "203.0.113.5"})
	var ch *OTPChallenge
	if !errors.As(err, &ch) || ch.Token == "" || ch.Username != "ops" || !errors.Is(err, ErrOTPRequired) {
		t.Fatalf("password step: %v", err)
	}
	_, err = c.Login(ctx, LoginInput{Pending: ch.Token, OTP: "000000", IP: "203.0.113.5"})
	var again *OTPChallenge
	if !errors.As(err, &again) || again.Token != ch.Token || !errors.Is(err, ErrOTPInvalid) {
		t.Fatalf("wrong code: %v", err)
	}
	code, _ = totp.Code(secret, *clock)
	got, err := c.Login(ctx, LoginInput{Pending: ch.Token, OTP: code, IP: "203.0.113.5"})
	if err != nil || got.Admin.Username != "ops" {
		t.Fatalf("code step: %v", err)
	}
	if _, err := c.Login(ctx, LoginInput{Pending: ch.Token, OTP: code}); !errors.Is(err, ErrOTPExpired) {
		t.Fatalf("ticket reused: %v", err)
	}
	if _, err := c.Login(ctx, LoginInput{Pending: "forged-ticket", OTP: code}); !errors.Is(err, ErrOTPExpired) {
		t.Fatalf("forged ticket: %v", err)
	}
	_, err = c.Login(ctx, LoginInput{Username: "ops", Password: pw})
	errors.As(err, &ch)
	for i := 0; i < 5; i++ {
		_, _ = c.Login(ctx, LoginInput{Pending: ch.Token, OTP: "111111"})
	}
	*clock = clock.Add(30 * time.Second)
	code, _ = totp.Code(secret, *clock)
	if _, err := c.Login(ctx, LoginInput{Pending: ch.Token, OTP: code}); !errors.Is(err, ErrOTPExpired) {
		t.Fatalf("sixth attempt: %v", err)
	}
}

// Attempts are counted before the password is checked, so a burst of
// concurrent guesses cannot all slip past the lockout.
func TestConsoleLockoutHoldsUnderConcurrency(t *testing.T) {
	c, _ := newConsole(t)
	ctx := context.Background()
	_, _ = c.CreateAdmin(ctx, nil, "", "owner", "", RoleOwner)
	var wg sync.WaitGroup
	var checked atomic.Int32
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Login(ctx, LoginInput{Username: "owner", Password: "guess", IP: "203.0.113.7"}); errors.Is(err, ErrLoginFailed) {
				checked.Add(1)
			}
		}()
	}
	wg.Wait()
	if n := checked.Load(); n > loginMaxFails {
		t.Fatalf("%d passwords checked in one window, limit %d", n, loginMaxFails)
	}
}

// A successful login gives its attempt back; failures stay counted, and so
// does each pending TOTP ticket a correct password yields.
func TestConsoleLoginRefundsSuccess(t *testing.T) {
	c, clock := newConsole(t)
	ctx := context.Background()
	pw, _ := c.CreateAdmin(ctx, nil, "", "owner", "", RoleOwner)
	for i := 0; i < loginMaxFails-1; i++ {
		_, _ = c.Login(ctx, LoginInput{Username: "owner", Password: "nope", IP: "203.0.113.8"})
	}
	for i := 0; i < 5; i++ {
		if _, err := c.Login(ctx, LoginInput{Username: "owner", Password: pw, IP: "203.0.113.8"}); err != nil {
			t.Fatalf("success %d after %d failures: %v", i, loginMaxFails-1, err)
		}
	}
	_, _ = c.Login(ctx, LoginInput{Username: "owner", Password: "nope", IP: "203.0.113.8"})
	if _, err := c.Login(ctx, LoginInput{Username: "owner", Password: pw, IP: "203.0.113.8"}); !errors.Is(err, ErrLoginLocked) {
		t.Fatalf("not locked after %d failures: %v", loginMaxFails, err)
	}

	// TOTP on: a correct password mints a ticket and the attempt stays counted.
	c2, _ := newConsole(t)
	pw2, _ := c2.CreateAdmin(ctx, nil, "", "ops", "", RoleOperator)
	p, _ := c2.Login(ctx, LoginInput{Username: "ops", Password: pw2})
	secret, _, _ := c2.BeginTOTP(ctx, p)
	code, _ := totp.Code(secret, *clock)
	if err := c2.ConfirmTOTP(ctx, p, "", code); err != nil {
		t.Fatal(err)
	}
	tickets := 0
	for i := 0; i < loginMaxFails+5; i++ {
		var ch *OTPChallenge
		if _, err := c2.Login(ctx, LoginInput{Username: "ops", Password: pw2, IP: "203.0.113.9"}); errors.As(err, &ch) {
			tickets++
		}
	}
	if tickets > loginMaxFails {
		t.Fatalf("%d pending tickets minted in one window, limit %d", tickets, loginMaxFails)
	}
}
