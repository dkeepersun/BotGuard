package logic

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/repository"
	"github.com/dkeepersun/BotGuard/internal/service/credential"
	"github.com/dkeepersun/BotGuard/internal/service/pow"
	"github.com/dkeepersun/BotGuard/internal/service/sealer"
)

// Policy defaults for a newly configured action. The difficulty is a
// placeholder until browser solve times are measured on real devices.
const (
	DefaultPuzzles    = 8
	DefaultDifficulty = 2000
	MaxDifficulty     = 1_000_000_000
)

// ErrInvalid wraps operator input errors; its message is user-facing.
var ErrInvalid = errors.New("invalid input")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// AppAdmin is the management use case shared by botguard-ctl and, later,
// the admin web UI.
type AppAdmin struct {
	Repo *repository.AppAdmin
	// Sealer is required only for creating or rotating secret pairs.
	Sealer *sealer.Sealer
	Now    func() time.Time
}

func (a *AppAdmin) CreateApp(ctx context.Context, name string) (string, error) {
	if n := utf8.RuneCountInString(name); n < 1 || n > 128 {
		return "", invalid("应用名称长度须为 1–128 个字符")
	}
	key, err := credential.NewAppKey()
	if err != nil {
		return "", err
	}
	return key, a.Repo.CreateApp(ctx, key, name)
}

func (a *AppAdmin) SetAppEnabled(ctx context.Context, appKey string, enabled bool) error {
	return a.Repo.SetAppEnabled(ctx, appKey, enabled)
}

// NormalizeOriginInput only accepts input that is already a bare origin, so
// an operator typo such as a trailing path is rejected rather than trimmed.
func NormalizeOriginInput(raw string) (string, error) {
	n, ok := domain.NormalizeOrigin(raw)
	if !ok {
		return "", invalid("来源必须是 http(s)://主机[:端口]，不能带路径、查询参数或账号：%q", raw)
	}
	return n, nil
}

func (a *AppAdmin) AddOrigin(ctx context.Context, appKey, raw string) (string, error) {
	o, err := NormalizeOriginInput(raw)
	if err != nil {
		return "", err
	}
	return o, a.Repo.AddOrigin(ctx, appKey, o)
}

func (a *AppAdmin) RemoveOrigin(ctx context.Context, appKey, raw string) (string, error) {
	o, err := NormalizeOriginInput(raw)
	if err != nil {
		return "", err
	}
	return o, a.Repo.RemoveOrigin(ctx, appKey, o)
}

func (a *AppAdmin) SetAction(ctx context.Context, appKey, action string, p domain.ActionPolicy) error {
	if !domain.ValidAction(action) {
		return invalid("action 只能包含小写字母、数字和下划线，长度 1–32")
	}
	if p.Mode == domain.ModeSlider {
		// Phase-one alias: always the slider.
		p.Mode, p.Kinds = domain.ModeInteractive, []string{domain.KindSlider}
	}
	if !domain.ValidMode(p.Mode) {
		return invalid("mode 只能是 invisible、interactive、adaptive（或 slider，等同 interactive 且只出滑块）")
	}
	if len(p.Kinds) == 0 {
		return invalid("kinds 至少要有一种题型")
	}
	for _, k := range p.Kinds {
		if !domain.ValidInteractiveKind(k) {
			return invalid("未知题型 %q，可选：%s", k, strings.Join(domain.InteractiveKinds, "、"))
		}
	}
	if p.Puzzles < 1 || p.Puzzles > pow.MaxPuzzles {
		return invalid("puzzles 须在 1–%d 之间", pow.MaxPuzzles)
	}
	if p.Difficulty < 1 || p.Difficulty > MaxDifficulty {
		return invalid("difficulty 须在 1–%d 之间", MaxDifficulty)
	}
	return a.Repo.UpsertAction(ctx, appKey, action, p)
}

func (a *AppAdmin) expiry(ttl time.Duration) (*time.Time, error) {
	if ttl < 0 {
		return nil, invalid("有效期不能为负数")
	}
	if ttl == 0 {
		return nil, nil
	}
	t := a.Now().Add(ttl).UTC()
	return &t, nil
}

// sealed generates a secret pair and seals its key for storage.
func (a *AppAdmin) sealed(ttl time.Duration) (credential.SecretPair, repository.NewSecret, error) {
	if a.Sealer == nil {
		return credential.SecretPair{}, repository.NewSecret{}, errors.New("未配置 BOTGUARD_SECRET_KEK，无法加密保存 secret_key")
	}
	exp, err := a.expiry(ttl)
	if err != nil {
		return credential.SecretPair{}, repository.NewSecret{}, err
	}
	p, err := credential.NewSecretPair()
	if err != nil {
		return credential.SecretPair{}, repository.NewSecret{}, err
	}
	kek, ct, err := a.Sealer.Seal(p.SecretID, []byte(p.SecretKey))
	if err != nil {
		return credential.SecretPair{}, repository.NewSecret{}, err
	}
	return p, repository.NewSecret{SecretID: p.SecretID, Ciphertext: ct, KEKID: kek, ExpiresAt: exp}, nil
}

// CreateSecret returns the only plaintext copy of the secret key; the database
// keeps it sealed with the KEK, and it is never shown again.
func (a *AppAdmin) CreateSecret(ctx context.Context, appKey string, ttl time.Duration) (credential.SecretPair, *time.Time, error) {
	p, rec, err := a.sealed(ttl)
	if err != nil {
		return credential.SecretPair{}, nil, err
	}
	return p, rec.ExpiresAt, a.Repo.CreateSecret(ctx, appKey, rec)
}

func (a *AppAdmin) RevokeSecret(ctx context.Context, secretID string) error {
	if !domain.ValidSecretID(secretID) {
		return invalid("secret_id 格式错误")
	}
	return a.Repo.RevokeSecret(ctx, secretID)
}

// RotateSecret issues a replacement and lets the old pair keep working for
// grace, so business servers can switch without downtime.
func (a *AppAdmin) RotateSecret(ctx context.Context, oldSecretID string, grace, ttl time.Duration) (credential.SecretPair, time.Time, error) {
	if !domain.ValidSecretID(oldSecretID) {
		return credential.SecretPair{}, time.Time{}, invalid("secret_id 格式错误")
	}
	if grace < 0 {
		return credential.SecretPair{}, time.Time{}, invalid("宽限期不能为负数")
	}
	p, rec, err := a.sealed(ttl)
	if err != nil {
		return credential.SecretPair{}, time.Time{}, err
	}
	oldUntil := a.Now().Add(grace).UTC()
	return p, oldUntil, a.Repo.RotateSecret(ctx, oldSecretID, rec, oldUntil)
}

func (a *AppAdmin) ListApps(ctx context.Context) ([]repository.AppSummary, error) {
	return a.Repo.ListApps(ctx)
}

func (a *AppAdmin) AppDetail(ctx context.Context, appKey string) (*repository.AppDetail, error) {
	return a.Repo.AppDetail(ctx, appKey)
}
