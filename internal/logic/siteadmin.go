package logic

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/repository"
	"github.com/dkeepersun/BotGuard/internal/service/credential"
	"github.com/dkeepersun/BotGuard/internal/service/pow"
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

// SiteAdmin is the management use case shared by botguard-ctl and, later,
// the admin web UI.
type SiteAdmin struct {
	Repo *repository.SiteAdmin
	Now  func() time.Time
}

func (a *SiteAdmin) CreateSite(ctx context.Context, name string) (string, error) {
	if n := utf8.RuneCountInString(name); n < 1 || n > 128 {
		return "", invalid("站点名称长度须为 1–128 个字符")
	}
	key, err := credential.NewSiteKey()
	if err != nil {
		return "", err
	}
	return key, a.Repo.CreateSite(ctx, key, name)
}

func (a *SiteAdmin) SetSiteEnabled(ctx context.Context, siteKey string, enabled bool) error {
	return a.Repo.SetSiteEnabled(ctx, siteKey, enabled)
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

func (a *SiteAdmin) AddOrigin(ctx context.Context, siteKey, raw string) (string, error) {
	o, err := NormalizeOriginInput(raw)
	if err != nil {
		return "", err
	}
	return o, a.Repo.AddOrigin(ctx, siteKey, o)
}

func (a *SiteAdmin) RemoveOrigin(ctx context.Context, siteKey, raw string) (string, error) {
	o, err := NormalizeOriginInput(raw)
	if err != nil {
		return "", err
	}
	return o, a.Repo.RemoveOrigin(ctx, siteKey, o)
}

func (a *SiteAdmin) SetAction(ctx context.Context, siteKey, action string, p domain.ActionPolicy) error {
	if !domain.ValidAction(action) {
		return invalid("action 只能包含小写字母、数字和下划线，长度 1–32")
	}
	if p.Puzzles < 1 || p.Puzzles > pow.MaxPuzzles {
		return invalid("puzzles 须在 1–%d 之间", pow.MaxPuzzles)
	}
	if p.Difficulty < 1 || p.Difficulty > MaxDifficulty {
		return invalid("difficulty 须在 1–%d 之间", MaxDifficulty)
	}
	return a.Repo.UpsertAction(ctx, siteKey, action, p)
}

func (a *SiteAdmin) expiry(ttl time.Duration) (*time.Time, error) {
	if ttl < 0 {
		return nil, invalid("有效期不能为负数")
	}
	if ttl == 0 {
		return nil, nil
	}
	t := a.Now().Add(ttl).UTC()
	return &t, nil
}

// CreateKey returns the only copy of the secret; it is never stored or shown again.
func (a *SiteAdmin) CreateKey(ctx context.Context, siteKey string, ttl time.Duration) (credential.APIKey, *time.Time, error) {
	exp, err := a.expiry(ttl)
	if err != nil {
		return credential.APIKey{}, nil, err
	}
	k, err := credential.NewAPIKey()
	if err != nil {
		return credential.APIKey{}, nil, err
	}
	err = a.Repo.CreateAPIKey(ctx, siteKey, repository.NewAPIKey{KeyID: k.KeyID, SecretHash: k.SecretHash, ExpiresAt: exp})
	return k, exp, err
}

func (a *SiteAdmin) RevokeKey(ctx context.Context, keyID string) error {
	if !domain.ValidKeyID(keyID) {
		return invalid("密钥 ID 格式错误")
	}
	return a.Repo.RevokeAPIKey(ctx, keyID)
}

// RotateKey issues a replacement and lets the old key keep working for grace,
// so business servers can switch without downtime.
func (a *SiteAdmin) RotateKey(ctx context.Context, oldKeyID string, grace, ttl time.Duration) (credential.APIKey, time.Time, error) {
	if !domain.ValidKeyID(oldKeyID) {
		return credential.APIKey{}, time.Time{}, invalid("密钥 ID 格式错误")
	}
	if grace < 0 {
		return credential.APIKey{}, time.Time{}, invalid("宽限期不能为负数")
	}
	exp, err := a.expiry(ttl)
	if err != nil {
		return credential.APIKey{}, time.Time{}, err
	}
	k, err := credential.NewAPIKey()
	if err != nil {
		return credential.APIKey{}, time.Time{}, err
	}
	oldUntil := a.Now().Add(grace).UTC()
	err = a.Repo.RotateAPIKey(ctx, oldKeyID, repository.NewAPIKey{KeyID: k.KeyID, SecretHash: k.SecretHash, ExpiresAt: exp}, oldUntil)
	return k, oldUntil, err
}

func (a *SiteAdmin) ListSites(ctx context.Context) ([]repository.SiteSummary, error) {
	return a.Repo.ListSites(ctx)
}

func (a *SiteAdmin) SiteDetail(ctx context.Context, siteKey string) (*repository.SiteDetail, error) {
	return a.Repo.SiteDetail(ctx, siteKey)
}
