package logic

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/repository"
	"github.com/dkeepersun/BotGuard/internal/service/password"
	"github.com/dkeepersun/BotGuard/internal/service/sealer"
	"github.com/dkeepersun/BotGuard/internal/service/totp"
	"github.com/zeromicro/go-zero/core/logx"
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
	// PermSystem covers installing a release (docs/admin.md 版本更新).
	PermSystem = "system.manage"
)

// Allowed reports whether role grants perm.
func Allowed(role, perm string) bool {
	switch role {
	case RoleOwner:
		return true
	case RoleOperator:
		return perm != PermManageAdmins && perm != PermSystem
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
	// Pending is the ticket from an OTPChallenge: the second step sends it
	// with the code instead of the password again.
	Pending string
}

// OTPChallenge is returned after a correct password when the account has
// TOTP: the ticket lets the second step submit only the code. It matches
// errors.Is(err, ErrOTPRequired).
type OTPChallenge struct {
	Token    string
	Username string
	// retry is set when a code was wrong: the ticket stays usable.
	retry error
}

func (e *OTPChallenge) Error() string {
	if e.retry != nil {
		return ErrOTPInvalid.Error()
	}
	return ErrOTPRequired.Error()
}

func (e *OTPChallenge) Unwrap() []error {
	if e.retry != nil {
		return []error{ErrOTPRequired, ErrOTPInvalid}
	}
	return []error{ErrOTPRequired}
}

const (
	pendingTTL      = 5 * time.Minute
	pendingAttempts = 5
)

// ErrOTPExpired means the password step has to be repeated.
var ErrOTPExpired = errors.New("动态码验证已超时或尝试次数过多，请重新输入密码")

func pendingKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "bg:admin:mfa:" + hex.EncodeToString(sum[:])
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

type loginSubject struct{ dim, subject string }

func loginSubjects(ip, username string) []loginSubject {
	return []loginSubject{{dimLoginIP, ip}, {dimLoginUser, username}}
}

// takeAttempt counts a login attempt for every subject before anything is
// checked, so concurrent guesses cannot all pass a read-only check, and
// reports whether any subject is over the limit. Only a successful login
// gives its attempt back (refundAttempt); failures, and the pending ticket
// a correct password yields when TOTP is on, stay counted.
func (c *Console) takeAttempt(ctx context.Context, subjects []loginSubject) error {
	locked := false
	for _, s := range subjects {
		n, _, err := c.Limiter.Take(ctx, s.dim, s.subject, loginMaxFails, loginWindow)
		if err != nil {
			return err
		}
		locked = locked || n > loginMaxFails
	}
	if locked {
		return ErrLoginLocked
	}
	return nil
}

func (c *Console) refundAttempt(ctx context.Context, subjects []loginSubject) {
	for _, s := range subjects {
		if err := c.Limiter.Refund(ctx, s.dim, s.subject, loginWindow); err != nil {
			logx.WithContext(ctx).Errorf("refund login attempt: %v", err)
		}
	}
}

// Login checks the password (and TOTP when enabled) and starts a session.
// Attempts are rate limited per IP and per username.
func (c *Console) Login(ctx context.Context, in LoginInput) (*Principal, error) {
	username := strings.ToLower(strings.TrimSpace(in.Username))
	if in.Pending != "" {
		return c.completeOTP(ctx, in)
	}
	subjects := loginSubjects(in.IP, username)
	if err := c.takeAttempt(ctx, subjects); err != nil {
		return nil, err
	}
	fail := func(err error, reason string) (*Principal, error) {
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
			token, err := newPendingToken()
			if err != nil {
				return nil, err
			}
			if err := c.Redis.HmsetCtx(ctx, pendingKey(token), map[string]string{"admin": strconv.FormatInt(admin.ID, 10), "tries": "0"}); err != nil {
				return nil, err
			}
			if err := c.Redis.ExpireCtx(ctx, pendingKey(token), int(pendingTTL/time.Second)); err != nil {
				return nil, err
			}
			return nil, &OTPChallenge{Token: token, Username: admin.Username}
		}
		if err := c.checkOTP(ctx, admin, in.OTP); err != nil {
			if errors.Is(err, ErrOTPInvalid) {
				return fail(err, otpReason(err))
			}
			return nil, err
		}
	}
	c.refundAttempt(ctx, subjects)
	return c.startSession(ctx, admin, in.IP)
}

func newPendingToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

var errOTPReused = fmt.Errorf("%w (reused)", ErrOTPInvalid)

func otpReason(err error) string {
	if errors.Is(err, errOTPReused) {
		return "otp_reused"
	}
	return "bad_otp"
}

// checkOTP verifies a code and records its step so it is never accepted
// again; ErrOTPInvalid for a wrong or reused code.
func (c *Console) checkOTP(ctx context.Context, admin *repository.Admin, code string) error {
	secret, err := c.openTOTP(admin)
	if err != nil {
		return err
	}
	step, ok := totp.Verify(secret, code, c.Now(), admin.TOTPLastStep.Int64)
	if !ok {
		return ErrOTPInvalid
	}
	fresh, err := c.Admins.UseTOTPStep(ctx, admin.ID, step)
	if err != nil {
		return err
	}
	if !fresh {
		return errOTPReused
	}
	return nil
}

// completeOTP is the second login step: the pending ticket stands for the
// verified password. Each ticket allows a few attempts and five minutes;
// failures also count towards the username and IP lockout.
func (c *Console) completeOTP(ctx context.Context, in LoginInput) (*Principal, error) {
	key := pendingKey(in.Pending)
	m, err := c.Redis.HgetallCtx(ctx, key)
	if err != nil {
		return nil, err
	}
	adminID, _ := strconv.ParseInt(m["admin"], 10, 64)
	if adminID == 0 {
		return nil, ErrOTPExpired
	}
	tries, err := c.Redis.HincrbyCtx(ctx, key, "tries", 1)
	if err != nil {
		return nil, err
	}
	if tries > pendingAttempts {
		_, _ = c.Redis.DelCtx(ctx, key)
		return nil, ErrOTPExpired
	}
	admin, err := c.Admins.ByID(ctx, adminID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, ErrOTPExpired
		}
		return nil, err
	}
	subjects := loginSubjects(in.IP, admin.Username)
	if err := c.takeAttempt(ctx, subjects); err != nil {
		if errors.Is(err, ErrLoginLocked) {
			_, _ = c.Redis.DelCtx(ctx, key)
		}
		return nil, err
	}
	if !admin.Enabled() || !admin.TOTPEnabled() {
		_, _ = c.Redis.DelCtx(ctx, key)
		return nil, ErrOTPExpired
	}
	if err := c.checkOTP(ctx, admin, in.OTP); err != nil {
		if !errors.Is(err, ErrOTPInvalid) {
			return nil, err
		}
		c.auditLogin(ctx, admin.Username, "auth.login_failed", in.IP, map[string]any{"reason": otpReason(err)})
		return nil, &OTPChallenge{Token: in.Pending, Username: admin.Username, retry: err}
	}
	_, _ = c.Redis.DelCtx(ctx, key)
	c.refundAttempt(ctx, subjects)
	return c.startSession(ctx, admin, in.IP)
}

func (c *Console) startSession(ctx context.Context, admin *repository.Admin, ip string) (*Principal, error) {
	sess, err := c.Sessions.Create(ctx, admin.ID, admin.SessionVersion, c.Now())
	if err != nil {
		return nil, err
	}
	_ = c.Admins.RecordLogin(ctx, admin.ID, ip, c.Now())
	c.auditLogin(ctx, admin.Username, "auth.login", ip, map[string]any{"mfa": admin.TOTPEnabled()})
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

// ConfirmPassword checks the admin's own password again before a sensitive
// action (installing a release): a stolen session alone must not start it.
func (c *Console) ConfirmPassword(p *Principal, current string) error {
	if !password.Verify(p.Admin.PasswordHash, current) {
		return invalid("当前密码不正确")
	}
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

// SetPassword sets a chosen password for an admin (botguard-ctl, for
// recovery and test environments). The usual policy applies unless
// allowWeak; either way every session of the admin ends, and no change is
// forced at next sign-in.
func (c *Console) SetPassword(ctx context.Context, id int64, pw string, allowWeak bool) error {
	target, err := c.Admins.ByID(ctx, id)
	if err != nil {
		return err
	}
	if pw == "" || len(pw) > 128 {
		return invalid("密码不能为空，且不能超过 128 个字符")
	}
	if !allowWeak {
		if err := password.Check(pw, target.Username); err != nil {
			return invalid("%s", strings.TrimPrefix(err.Error(), password.ErrWeak.Error()+": "))
		}
	}
	hash, err := password.Hash(pw)
	if err != nil {
		return err
	}
	ctx = repository.Audited(ctx, "admin.password_set", "", map[string]any{"policy_waived": allowWeak})
	return c.Admins.SetPassword(ctx, id, hash, false)
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
