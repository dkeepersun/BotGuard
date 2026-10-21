package logic

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/repository"
	"github.com/dkeepersun/BotGuard/internal/service/password"
	"github.com/dkeepersun/BotGuard/internal/service/sealer"
	"github.com/dkeepersun/BotGuard/internal/service/totp"
	"github.com/zeromicro/go-zero/core/stores/redis"
)

// Console roles (docs/admin.md §2).
const (
	RoleOwner    = "owner"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

var RoleNames = map[string]string{RoleOwner: "超级管理员", RoleOperator: "运维", RoleViewer: "只读"}

// Permissions checked on every console route.
const (
	PermView          = "view"
	PermManageApps    = "apps.manage"
	PermManageSecrets = "secrets.manage"
	PermAudit         = "audit.view"
	PermManageAdmins  = "admins.manage"
)

// Allowed reports whether role grants perm.
func Allowed(role, perm string) bool {
	switch role {
	case RoleOwner:
		return true
	case RoleOperator:
		return perm != PermManageAdmins
	case RoleViewer:
		return perm == PermView
	}
	return false
}

func ValidRole(r string) bool { _, ok := RoleNames[r]; return ok }

var usernamePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{2,31}$`)

// Console errors shown to the person signing in. Wrong user and wrong
// password share one error so usernames cannot be probed.
var (
	ErrLoginFailed  = errors.New("用户名或密码错误")
	ErrOTPRequired  = errors.New("请输入身份验证器中的 6 位动态码")
	ErrOTPInvalid   = errors.New("动态码错误或已使用，请等待下一个动态码")
	ErrLoginLocked  = errors.New("登录失败次数过多，请稍后再试")
	ErrNotPermitted = errors.New("没有权限执行该操作")
)

const (
	loginWindow   = 15 * time.Minute
	loginMaxFails = 10
	dimLoginIP    = "adminip"
	dimLoginUser  = "adminuser"
)

// Principal is the signed-in admin for one request.
type Principal struct {
	Admin   *repository.Admin
	Session *repository.AdminSession
}

func (p *Principal) Can(perm string) bool { return Allowed(p.Admin.Role, perm) }

// Actor is how this admin appears in the audit log.
func (p *Principal) Actor() string { return "admin:" + p.Admin.Username }

// Console is the admin console's account and session logic.
type Console struct {
	Admins   *repository.Admins
	Audit    *repository.AppAdmin
	Sessions *repository.Sessions
	Limiter  *repository.RateLimiter
	Redis    *redis.Redis
	// Sealer protects TOTP secrets; required to enable TOTP.
	Sealer *sealer.Sealer
	Now    func() time.Time
}

type LoginInput struct {
	Username, Password, OTP, IP string
}

func (c *Console) auditLogin(ctx context.Context, username, op, ip string, detail map[string]any) {
	_ = c.Audit.RecordAudit(ctx, repository.AuditEntry{Actor: "admin:" + truncateRunes(username, 32), Operation: op,
		Target: "admin:" + truncateRunes(username, 32), Detail: detail, IP: ip})
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// Login checks the password (and TOTP when enabled) and starts a session.
// Failures are rate limited per IP and per username.
func (c *Console) Login(ctx context.Context, in LoginInput) (*Principal, error) {
	username := strings.ToLower(strings.TrimSpace(in.Username))
	for _, dim := range []struct{ name, subject string }{{dimLoginIP, in.IP}, {dimLoginUser, username}} {
		n, _, err := c.Limiter.Count(ctx, dim.name, dim.subject, loginWindow)
		if err != nil {
			return nil, err
		}
		if n >= loginMaxFails {
			return nil, ErrLoginLocked
		}
	}
	fail := func(err error, reason string) (*Principal, error) {
		for _, dim := range []struct{ name, subject string }{{dimLoginIP, in.IP}, {dimLoginUser, username}} {
			_, _, _ = c.Limiter.Take(ctx, dim.name, dim.subject, loginWindow)
		}
		c.auditLogin(ctx, username, "auth.login_failed", in.IP, map[string]any{"reason": reason})
		return nil, err
	}
	admin, err := c.Admins.ByUsername(ctx, username)
	if errors.Is(err, domain.ErrNotFound) {
		password.Burn(in.Password)
		return fail(ErrLoginFailed, "unknown_user")
	}
	if err != nil {
		return nil, err
	}
	if !password.Verify(admin.PasswordHash, in.Password) {
		return fail(ErrLoginFailed, "bad_password")
	}
	if !admin.Enabled() {
		return fail(ErrLoginFailed, "disabled")
	}
	if admin.TOTPEnabled() {
		if strings.TrimSpace(in.OTP) == "" {
			return nil, ErrOTPRequired
		}
		secret, err := c.openTOTP(admin)
		if err != nil {
			return nil, err
		}
		step, ok := totp.Verify(secret, in.OTP, c.Now(), admin.TOTPLastStep.Int64)
		if !ok {
			return fail(ErrOTPInvalid, "bad_otp")
		}
		if fresh, err := c.Admins.UseTOTPStep(ctx, admin.ID, step); err != nil || !fresh {
			if err != nil {
				return nil, err
			}
			return fail(ErrOTPInvalid, "otp_reused")
		}
	}
	sess, err := c.Sessions.Create(ctx, admin.ID, admin.SessionVersion, c.Now())
	if err != nil {
		return nil, err
	}
	_ = c.Admins.RecordLogin(ctx, admin.ID, in.IP, c.Now())
	c.auditLogin(ctx, username, "auth.login", in.IP, map[string]any{"totp": admin.TOTPEnabled()})
	return &Principal{Admin: admin, Session: sess}, nil
}

// Authenticate resolves a session cookie into a principal, or nil when the
// session is missing, expired, or invalidated (password change, disabled,
// role change).
func (c *Console) Authenticate(ctx context.Context, sessionID string) (*Principal, error) {
	sess, err := c.Sessions.Get(ctx, sessionID, c.Now())
	if err != nil || sess == nil {
		return nil, err
	}
	admin, err := c.Admins.ByID(ctx, sess.AdminID)
	if errors.Is(err, domain.ErrNotFound) {
		_ = c.Sessions.Delete(ctx, sessionID)
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !admin.Enabled() || admin.SessionVersion != sess.Version {
		_ = c.Sessions.Delete(ctx, sessionID)
		return nil, nil
	}
	sess.ID = sessionID
	return &Principal{Admin: admin, Session: sess}, nil
}

func (c *Console) Logout(ctx context.Context, p *Principal, ip string) error {
	c.auditLogin(ctx, p.Admin.Username, "auth.logout", ip, nil)
	return c.Sessions.Delete(ctx, p.Session.ID)
}

// withActor audits console changes as the signed-in admin.
func withActor(ctx context.Context, p *Principal, ip string) context.Context {
	return repository.WithActor(ctx, p.Actor(), ip)
}

// ChangePassword replaces the admin's own password after checking the
// current one. Every session ends, including this one: the caller signs in
// again.
func (c *Console) ChangePassword(ctx context.Context, p *Principal, ip, current, next string) error {
	if !password.Verify(p.Admin.PasswordHash, current) {
		return invalid("当前密码不正确")
	}
	if current == next {
		return invalid("新密码不能与当前密码相同")
	}
	if err := password.Check(next, p.Admin.Username); err != nil {
		return invalid("%s", strings.TrimPrefix(err.Error(), password.ErrWeak.Error()+": "))
	}
	hash, err := password.Hash(next)
	if err != nil {
		return err
	}
	ctx = repository.Audited(withActor(ctx, p, ip), "admin.password_change", "", nil)
	return c.Admins.SetPassword(ctx, p.Admin.ID, hash, false)
}

func (c *Console) UpdateProfile(ctx context.Context, p *Principal, ip, displayName string) error {
	displayName = strings.TrimSpace(displayName)
	if utf8.RuneCountInString(displayName) > 64 {
		return invalid("显示名称不能超过 64 个字符")
	}
	ctx = repository.Audited(withActor(ctx, p, ip), "admin.profile", "", map[string]any{"display_name": displayName})
	return c.Admins.SetDisplayName(ctx, p.Admin.ID, displayName)
}

// TOTP enrolment: Begin stores a pending secret for ten minutes; Confirm
// checks a code from the app before switching TOTP on.
func totpPendingKey(adminID int64) string { return fmt.Sprintf("bg:admin:totp:pending:%d", adminID) }

func (c *Console) BeginTOTP(ctx context.Context, p *Principal) (secret, uri string, err error) {
	if c.Sealer == nil {
		return "", "", errors.New("未配置 BOTGUARD_SECRET_KEK，无法启用动态码")
	}
	secret, err = totp.NewSecret()
	if err != nil {
		return "", "", err
	}
	if err := c.Redis.SetexCtx(ctx, totpPendingKey(p.Admin.ID), secret, 600); err != nil {
		return "", "", err
	}
	return secret, totp.URI("BotGuard", p.Admin.Username, secret), nil
}

func (c *Console) ConfirmTOTP(ctx context.Context, p *Principal, ip, code string) error {
	secret, err := c.Redis.GetCtx(ctx, totpPendingKey(p.Admin.ID))
	if err != nil {
		return err
	}
	if secret == "" {
		return invalid("绑定已超时，请重新开始")
	}
	step, ok := totp.Verify(secret, code, c.Now(), 0)
	if !ok {
		return invalid("动态码不正确，请确认手机时间准确后重试")
	}
	kek, ct, err := c.Sealer.Seal(totpAAD(p.Admin.ID), []byte(secret))
	if err != nil {
		return err
	}
	ctx = repository.Audited(withActor(ctx, p, ip), "admin.totp_enable", "", nil)
	if err := c.Admins.SetTOTP(ctx, p.Admin.ID, ct, kek, step); err != nil {
		return err
	}
	_, _ = c.Redis.DelCtx(ctx, totpPendingKey(p.Admin.ID))
	return nil
}

// DisableTOTP needs the password again: a stolen session alone must not be
// able to remove the second factor.
func (c *Console) DisableTOTP(ctx context.Context, p *Principal, ip, current string) error {
	if !password.Verify(p.Admin.PasswordHash, current) {
		return invalid("当前密码不正确")
	}
	ctx = repository.Audited(withActor(ctx, p, ip), "admin.totp_disable", "", nil)
	return c.Admins.SetTOTP(ctx, p.Admin.ID, nil, "", 0)
}

func totpAAD(adminID int64) string { return fmt.Sprintf("totp:%d", adminID) }

func (c *Console) openTOTP(a *repository.Admin) (string, error) {
	if c.Sealer == nil {
		return "", errors.New("未配置 BOTGUARD_SECRET_KEK，无法校验动态码")
	}
	b, err := c.Sealer.Open(a.TOTPKEKID.String, totpAAD(a.ID), a.TOTPCiphertext)
	if err != nil {
		return "", fmt.Errorf("totp secret of admin %d: %w", a.ID, err)
	}
	return string(b), nil
}

// Admin management (owner only; the web layer checks PermManageAdmins).

func validUsername(u string) error {
	if !usernamePattern.MatchString(u) {
		return invalid("用户名须为 3–32 位小写字母、数字、下划线、点或连字符，并以字母开头")
	}
	return nil
}

// CreateAdmin returns a generated initial password, shown once; the new
// admin must change it at first sign-in.
func (c *Console) CreateAdmin(ctx context.Context, actor *Principal, ip, username, displayName, role string) (string, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if err := validUsername(username); err != nil {
		return "", err
	}
	if !ValidRole(role) {
		return "", invalid("未知角色")
	}
	if utf8.RuneCountInString(displayName) > 64 {
		return "", invalid("显示名称不能超过 64 个字符")
	}
	pw, err := password.Generate()
	if err != nil {
		return "", err
	}
	hash, err := password.Hash(pw)
	if err != nil {
		return "", err
	}
	if actor != nil {
		ctx = withActor(ctx, actor, ip)
	}
	ctx = repository.Audited(ctx, "admin.create", "", map[string]any{"username": username, "role": role})
	if _, err := c.Admins.Create(ctx, username, strings.TrimSpace(displayName), hash, role, true); err != nil {
		if errors.Is(err, domain.ErrDuplicate) {
			return "", invalid("用户名已存在")
		}
		return "", err
	}
	return pw, nil
}

// ResetPassword generates a new password, ends the admin's sessions and
// forces a change at next sign-in.
func (c *Console) ResetPassword(ctx context.Context, actor *Principal, ip string, id int64) (string, error) {
	pw, err := password.Generate()
	if err != nil {
		return "", err
	}
	hash, err := password.Hash(pw)
	if err != nil {
		return "", err
	}
	if actor != nil {
		ctx = withActor(ctx, actor, ip)
	}
	ctx = repository.Audited(ctx, "admin.password_reset", "", nil)
	return pw, c.Admins.SetPassword(ctx, id, hash, true)
}

// guardOwner refuses changes that would leave no enabled owner.
func (c *Console) guardOwner(ctx context.Context, target *repository.Admin) error {
	if target.Role != RoleOwner || !target.Enabled() {
		return nil
	}
	n, err := c.Admins.CountEnabledOwners(ctx)
	if err != nil {
		return err
	}
	if n <= 1 {
		return invalid("这是唯一启用的超级管理员，不能停用或降级")
	}
	return nil
}

func (c *Console) SetAdminStatus(ctx context.Context, actor *Principal, ip string, id int64, enabled bool) error {
	if actor.Admin.ID == id && !enabled {
		return invalid("不能停用自己的账号")
	}
	target, err := c.Admins.ByID(ctx, id)
	if err != nil {
		return err
	}
	if !enabled {
		if err := c.guardOwner(ctx, target); err != nil {
			return err
		}
	}
	op := "admin.disable"
	if enabled {
		op = "admin.enable"
	}
	return c.Admins.SetStatus(repository.Audited(withActor(ctx, actor, ip), op, "", nil), id, enabled)
}

func (c *Console) SetAdminRole(ctx context.Context, actor *Principal, ip string, id int64, role string) error {
	if !ValidRole(role) {
		return invalid("未知角色")
	}
	if actor.Admin.ID == id {
		return invalid("不能修改自己的角色")
	}
	target, err := c.Admins.ByID(ctx, id)
	if err != nil {
		return err
	}
	if role != RoleOwner {
		if err := c.guardOwner(ctx, target); err != nil {
			return err
		}
	}
	return c.Admins.SetRole(repository.Audited(withActor(ctx, actor, ip), "admin.role", "", map[string]any{"role": role}), id, role)
}
