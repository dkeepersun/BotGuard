package logic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/repository"
	"github.com/dkeepersun/BotGuard/internal/service/credential"
	"github.com/dkeepersun/BotGuard/internal/service/imagegen"
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

func validAppText(name, remark string) error {
	if n := utf8.RuneCountInString(strings.TrimSpace(name)); n < 1 || n > 128 {
		return invalid("应用名称长度须为 1–128 个字符")
	}
	if utf8.RuneCountInString(remark) > 255 {
		return invalid("备注不能超过 255 个字符")
	}
	// Pages escape these, but a name is shown in many places (tabs, the app
	// switcher, integration snippets); there is no reason to store markup.
	if strings.ContainsAny(name+remark, "<>") {
		return invalid("应用名称和备注不能包含 < 或 >")
	}
	return nil
}

func (a *AppAdmin) CreateApp(ctx context.Context, name, remark string) (string, error) {
	if err := validAppText(name, remark); err != nil {
		return "", err
	}
	key, err := credential.NewAppKey()
	if err != nil {
		return "", err
	}
	name = strings.TrimSpace(name)
	ctx = repository.Audited(ctx, "app.create", key, map[string]any{"name": name, "remark": remark})
	return key, a.Repo.CreateApp(ctx, key, name, remark)
}

func (a *AppAdmin) UpdateApp(ctx context.Context, appKey, name, remark string) error {
	if err := validAppText(name, remark); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	ctx = repository.Audited(ctx, "app.update", appKey, map[string]any{"name": name, "remark": remark})
	return a.Repo.UpdateApp(ctx, appKey, name, remark)
}

func (a *AppAdmin) SetAppEnabled(ctx context.Context, appKey string, enabled bool) error {
	op := "app.disable"
	if enabled {
		op = "app.enable"
	}
	return a.Repo.SetAppEnabled(repository.Audited(ctx, op, appKey, nil), appKey, enabled)
}

func (a *AppAdmin) DeleteAction(ctx context.Context, appKey, action string) error {
	if !domain.ValidAction(action) {
		return invalid("action 格式错误")
	}
	return a.Repo.DeleteAction(repository.Audited(ctx, "action.delete", appKey, map[string]any{"action": action}), appKey, action)
}

// NormalizeOriginInput only accepts input that is already a bare origin or
// origin pattern, so an operator typo such as a trailing path is rejected
// rather than trimmed.
func NormalizeOriginInput(raw string) (string, error) {
	n, ok := domain.NormalizeOriginPattern(raw)
	if !ok {
		return "", invalid("来源必须是 http(s)://主机[:端口]（不能带路径、查询参数或账号）、子域名通配 https://*.example.com、全部允许 *，或 App 的 app://包名：%q", raw)
	}
	return n, nil
}

func (a *AppAdmin) AddOrigin(ctx context.Context, appKey, raw string) (string, error) {
	o, err := NormalizeOriginInput(raw)
	if err != nil {
		return "", err
	}
	return o, a.Repo.AddOrigin(repository.Audited(ctx, "origin.add", appKey, map[string]any{"origin": o}), appKey, o)
}

func (a *AppAdmin) RemoveOrigin(ctx context.Context, appKey, raw string) (string, error) {
	o, err := NormalizeOriginInput(raw)
	if err != nil {
		return "", err
	}
	return o, a.Repo.RemoveOrigin(repository.Audited(ctx, "origin.remove", appKey, map[string]any{"origin": o}), appKey, o)
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
	var kinds []string
	for _, k := range p.Kinds {
		if !slices.Contains(kinds, k) {
			kinds = append(kinds, k)
		}
	}
	p.Kinds = kinds
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
	ctx = repository.Audited(ctx, "action.set", appKey, map[string]any{"action": action, "enabled": p.Enabled, "mode": p.Mode,
		"kinds": p.Kinds, "client_select": p.ClientSelect, "puzzles": p.Puzzles, "difficulty": p.Difficulty})
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

// ErrKEKMismatch means the operator's KEK cannot open a secret the gateway
// sealed under the same id: new secrets would fail on the gateway with 503.
var ErrKEKMismatch = errors.New("KEK mismatch")

// CheckKEK opens the newest secret sealed under the active KEK id. It returns
// otherKEKs when secrets exist under other ids only, which is expected during
// a KEK rotation but also what a wrong id looks like.
func (a *AppAdmin) CheckKEK(ctx context.Context) (otherKEKs bool, err error) {
	if a.Sealer == nil {
		return false, errors.New("未配置 BOTGUARD_SECRET_KEK，无法加密保存 secret_key")
	}
	kek := a.Sealer.Active()
	sample, others, err := a.Repo.SampleSealed(ctx, kek)
	if err != nil || sample == nil {
		return others, err
	}
	if _, err := a.Sealer.Open(kek, sample.SecretID, sample.Ciphertext); err != nil {
		return others, fmt.Errorf("%w: 无法用 KEK %q 解开库中已有的密钥对 %s", ErrKEKMismatch, kek, sample.SecretID)
	}
	return others, nil
}

// CreateSecret returns the only plaintext copy of the secret key; the database
// keeps it sealed with the KEK, and it is never shown again.
func (a *AppAdmin) CreateSecret(ctx context.Context, appKey string, ttl time.Duration) (credential.SecretPair, *time.Time, error) {
	p, rec, err := a.sealed(ttl)
	if err != nil {
		return credential.SecretPair{}, nil, err
	}
	ctx = repository.Audited(ctx, "secret.create", appKey, map[string]any{"app_key": appKey, "expires_at": rec.ExpiresAt})
	return p, rec.ExpiresAt, a.Repo.CreateSecret(ctx, appKey, rec)
}

func (a *AppAdmin) RevokeSecret(ctx context.Context, secretID string) error {
	if !domain.ValidSecretID(secretID) {
		return invalid("secret_id 格式错误")
	}
	return a.Repo.RevokeSecret(repository.Audited(ctx, "secret.revoke", secretID, nil), secretID)
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
	ctx = repository.Audited(ctx, "secret.rotate", oldSecretID, map[string]any{"new_secret_id": rec.SecretID, "grace": grace.String()})
	until, err := a.Repo.RotateSecret(ctx, oldSecretID, rec, a.Now().Add(grace).UTC())
	return p, until, err
}

func (a *AppAdmin) ListApps(ctx context.Context) ([]repository.AppSummary, error) {
	return a.Repo.ListApps(ctx)
}

func (a *AppAdmin) AppDetail(ctx context.Context, appKey string) (*repository.AppDetail, error) {
	return a.Repo.AppDetail(ctx, appKey)
}

// SetBackground chooses where the app's challenge pictures come from.
func (a *AppAdmin) SetBackground(ctx context.Context, appKey, mode string, builtin bool) error {
	if !domain.ValidBackgroundMode(mode) {
		return invalid("背景来源只能是 mixed、photo 或 procedural")
	}
	ctx = repository.Audited(ctx, "background.set", appKey, map[string]any{"mode": mode, "builtin": builtin})
	return a.Repo.SetBackground(ctx, appKey, mode, builtin)
}

// AddBackground checks and re-encodes an uploaded picture (see
// imagegen.Normalize) and stores it for the app.
func (a *AppAdmin) AddBackground(ctx context.Context, appKey, name string, data []byte) (int64, error) {
	img, err := imagegen.Normalize(data)
	if err != nil {
		return 0, invalid("%s：%v", name, err)
	}
	sum := sha256.Sum256(img)
	ctx = repository.Audited(ctx, "background.add", appKey, map[string]any{"file": name, "bytes": len(img)})
	id, err := a.Repo.AddBackground(ctx, appKey, img, hex.EncodeToString(sum[:]))
	switch {
	case errors.Is(err, repository.ErrTooMany):
		return 0, invalid("每个应用最多 %d 张背景图，请先删除不用的", domain.MaxBackgroundUploads)
	case errors.Is(err, domain.ErrDuplicate):
		return 0, invalid("%s：这张图片已经上传过", name)
	}
	return id, err
}

func (a *AppAdmin) DeleteBackground(ctx context.Context, appKey string, id int64) error {
	return a.Repo.DeleteBackground(repository.Audited(ctx, "background.delete", appKey, map[string]any{"id": id}), appKey, id)
}

func (a *AppAdmin) BackgroundImage(ctx context.Context, appKey string, id int64) ([]byte, error) {
	return a.Repo.BackgroundImage(ctx, appKey, id)
}

// SetUI sets how the app's widgets look and what they say.
func (a *AppAdmin) SetUI(ctx context.Context, appKey string, ui domain.AppUI) error {
	for k, v := range ui.Texts {
		if v = strings.TrimSpace(v); v == "" {
			delete(ui.Texts, k)
		} else {
			ui.Texts[k] = v
		}
	}
	if err := ui.Validate(); err != nil {
		return invalid("%v", err)
	}
	ctx = repository.Audited(ctx, "ui.set", appKey, map[string]any{"lang": ui.Lang, "theme": ui.Theme, "accent": ui.Accent, "radius": ui.Radius, "texts": ui.Texts})
	return a.Repo.SetUI(ctx, appKey, ui)
}
