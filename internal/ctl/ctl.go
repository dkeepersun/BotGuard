// Package ctl implements the botguard-ctl operator commands.
package ctl

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dkeepersun/BotGuard/internal/config"
	"github.com/dkeepersun/BotGuard/internal/domain"
	"github.com/dkeepersun/BotGuard/internal/logic"
	"github.com/dkeepersun/BotGuard/internal/migration"
	"github.com/dkeepersun/BotGuard/internal/repository"
	"github.com/dkeepersun/BotGuard/internal/service/credential"
	"github.com/dkeepersun/BotGuard/internal/service/sealer"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const (
	DSNEnv = "BOTGUARD_MYSQL_DSN"
	KEKEnv = "BOTGUARD_SECRET_KEK"
)

const usage = `用法：botguard-ctl <命令> [参数]

环境变量（不接受命令行参数，避免进入 shell 历史）：
  BOTGUARD_MYSQL_DSN   数据库连接
  BOTGUARD_SECRET_KEK  加密 secret_key 的主密钥，格式 id:base64(32 字节)；
                       创建或轮换密钥对时需要，必须与 gateway 配置一致

  migrate up                                        执行未执行的迁移
  migrate status                                    查看迁移状态
  app create  -name 名称                            创建应用，输出 app_key
  app list                                          列出应用
  app show    -app app_key                          查看应用的来源、action 和密钥对
  app enable  -app app_key                          启用应用
  app disable -app app_key                          停用应用
  origin add    -app app_key -origin URL            添加允许来源，如 https://example.com
  origin remove -app app_key -origin URL            移除允许来源
  action set -app app_key -action 名称 [-mode adaptive] [-kinds slider,click_text,click_shape]
             [-enabled=true] [-puzzles 8] [-difficulty 2000]
                                                    新增或修改 action；只修改显式给出的参数。
                                                    mode：invisible（无感）、interactive（每次从 kinds 随机出题）、
                                                    adaptive（按风险出题，新建 action 的默认值）；slider 等同
                                                    interactive 且只出滑块。kinds：可出的交互题型，默认全部
  secret create -app app_key [-ttl 0]               生成密钥对（secret_id + secret_key），secret_key 只显示这一次
  secret revoke -secret secret_id                   吊销密钥对
  secret rotate -secret secret_id [-grace 24h] [-ttl 0]
                                                    生成替换密钥对，旧密钥对在宽限期后失效

应用、来源、action、密钥对的变更会在 gateway 的应用缓存过期后生效（默认 10 秒内）。
`

// App runs one command. Env and output are injected for tests. Errors and
// warnings go to Err, so scripts that capture Out (app_key, secret pairs)
// still show why a command failed.
type App struct {
	Env    func(string) string
	Out    io.Writer
	Err    io.Writer
	Now    func() time.Time
	openDB func(dsn string) sqlx.SqlConn
}

func New(env func(string) string, out, errOut io.Writer) *App {
	return &App{Env: env, Out: out, Err: errOut, Now: time.Now, openDB: func(dsn string) sqlx.SqlConn { return sqlx.NewMysql(dsn) }}
}

type usageError string

func (e usageError) Error() string { return string(e) }

// Run returns the process exit code: 0 ok, 1 operation failed, 2 usage error.
func (a *App) Run(ctx context.Context, args []string) int {
	err := a.run(ctx, args)
	if err == nil {
		return 0
	}
	var ue usageError
	if errors.As(err, &ue) {
		fmt.Fprintf(a.Err, "错误：%s\n\n%s", ue, usage)
		return 2
	}
	fmt.Fprintf(a.Err, "错误：%s\n", describe(err))
	return 1
}

func describe(err error) string {
	switch {
	case errors.Is(err, logic.ErrInvalid):
		return strings.TrimPrefix(err.Error(), logic.ErrInvalid.Error()+": ")
	case errors.Is(err, domain.ErrNotFound):
		return "未找到指定的应用、来源或密钥对"
	case errors.Is(err, domain.ErrDuplicate):
		return "记录已存在"
	case errors.Is(err, domain.ErrInvalidState):
		return "密钥对已吊销，不能轮换"
	case errors.Is(err, logic.ErrKEKMismatch):
		return strings.TrimPrefix(err.Error(), logic.ErrKEKMismatch.Error()+": ") +
			"。BOTGUARD_SECRET_KEK 与 gateway 使用的不一致，用它生成的密钥对在 gateway 上无法解密（verify 返回 503）。请使用与 gateway 完全相同的值"
	default:
		return err.Error()
	}
}

func (a *App) conn() (sqlx.SqlConn, error) {
	dsn, err := config.NormalizeMySQLDSN(a.Env(DSNEnv))
	if err != nil {
		return nil, fmt.Errorf("%s 无效：%w", DSNEnv, err)
	}
	return a.openDB(dsn), nil
}

func (a *App) admin() (*logic.AppAdmin, error) {
	c, err := a.conn()
	if err != nil {
		return nil, err
	}
	adm := &logic.AppAdmin{Repo: repository.NewAppAdmin(c), Now: a.Now}
	// Only secret create/rotate need the KEK; other commands work without it.
	if raw := a.Env(KEKEnv); raw != "" {
		if adm.Sealer, err = sealer.New(raw); err != nil {
			return nil, fmt.Errorf("%s 无效：%w", KEKEnv, err)
		}
	}
	return adm, nil
}

func (a *App) run(ctx context.Context, args []string) error {
	if len(args) < 2 {
		if len(args) == 1 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
			fmt.Fprint(a.Out, usage)
			return nil
		}
		return usageError("缺少命令")
	}
	cmd, rest := args[0]+" "+args[1], args[2:]
	switch cmd {
	case "migrate up", "migrate status":
		return a.migrate(ctx, args[1], rest)
	case "app create":
		return a.appCreate(ctx, rest)
	case "app list":
		return a.appList(ctx, rest)
	case "app show":
		return a.appShow(ctx, rest)
	case "app enable", "app disable":
		return a.appEnable(ctx, args[1] == "enable", rest)
	case "origin add", "origin remove":
		return a.origin(ctx, args[1] == "add", rest)
	case "action set":
		return a.actionSet(ctx, rest)
	case "secret create":
		return a.secretCreate(ctx, rest)
	case "secret revoke":
		return a.secretRevoke(ctx, rest)
	case "secret rotate":
		return a.secretRotate(ctx, rest)
	default:
		return usageError("未知命令：" + cmd)
	}
}

func parse(fs *flag.FlagSet, args []string, required ...string) error {
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return usageError(err.Error())
	}
	if fs.NArg() > 0 {
		return usageError("多余的参数：" + strings.Join(fs.Args(), " "))
	}
	for _, name := range required {
		if fs.Lookup(name).Value.String() == "" {
			return usageError("缺少参数 -" + name)
		}
	}
	return nil
}

func (a *App) migrate(ctx context.Context, sub string, args []string) error {
	if err := parse(flag.NewFlagSet("migrate", flag.ContinueOnError), args); err != nil {
		return err
	}
	c, err := a.conn()
	if err != nil {
		return err
	}
	db, err := c.RawDB()
	if err != nil {
		return err
	}
	r, err := migration.Open(ctx, db)
	if err != nil {
		return err
	}
	defer r.Close()
	if sub == "up" {
		done, err := r.Up(ctx)
		for _, m := range done {
			fmt.Fprintf(a.Out, "已执行 %04d_%s\n", m.Version, m.Name)
		}
		if err != nil {
			return err
		}
		if len(done) == 0 {
			fmt.Fprintln(a.Out, "没有待执行的迁移")
		}
		return nil
	}
	status, err := r.Status(ctx)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(a.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "版本\t名称\t状态")
	for _, s := range status {
		state := "待执行"
		if s.AppliedAt != nil {
			state = "已执行 " + s.AppliedAt.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(w, "%04d\t%s\t%s\n", s.Version, s.Name, state)
	}
	return w.Flush()
}

func (a *App) appCreate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("app create", flag.ContinueOnError)
	name := fs.String("name", "", "")
	if err := parse(fs, args, "name"); err != nil {
		return err
	}
	adm, err := a.admin()
	if err != nil {
		return err
	}
	key, err := adm.CreateApp(ctx, *name)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "已创建应用 %s\napp_key: %s\n下一步：添加允许来源（origin add）、配置 action（action set）、生成密钥对（secret create）。\n", *name, key)
	return nil
}

func (a *App) appList(ctx context.Context, args []string) error {
	if err := parse(flag.NewFlagSet("app list", flag.ContinueOnError), args); err != nil {
		return err
	}
	adm, err := a.admin()
	if err != nil {
		return err
	}
	apps, err := adm.ListApps(ctx)
	if err != nil {
		return err
	}
	if len(apps) == 0 {
		fmt.Fprintln(a.Out, "还没有应用")
		return nil
	}
	w := tabwriter.NewWriter(a.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "app_key\t名称\t状态\t策略版本\t创建时间")
	for _, s := range apps {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", s.AppKey, s.Name, enabledText(s.Enabled), s.PolicyVersion, s.CreatedAt.UTC().Format(time.RFC3339))
	}
	return w.Flush()
}

func enabledText(b bool) string {
	if b {
		return "启用"
	}
	return "停用"
}

func (a *App) appShow(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("app show", flag.ContinueOnError)
	app := fs.String("app", "", "")
	if err := parse(fs, args, "app"); err != nil {
		return err
	}
	adm, err := a.admin()
	if err != nil {
		return err
	}
	d, err := adm.AppDetail(ctx, *app)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "应用：%s（%s）\napp_key：%s\n策略版本：%d\n\n允许来源：\n", d.Name, enabledText(d.Enabled), d.AppKey, d.PolicyVersion)
	if len(d.Origins) == 0 {
		fmt.Fprintln(a.Out, "  （无，浏览器请求会全部被拒绝）")
	}
	for _, o := range d.Origins {
		fmt.Fprintf(a.Out, "  %s\n", o)
	}
	fmt.Fprintln(a.Out, "\naction：")
	if len(d.Actions) == 0 {
		fmt.Fprintln(a.Out, "  （无）")
	} else {
		w := tabwriter.NewWriter(a.Out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "  名称\t状态\t模式\t题型\tpuzzles\tdifficulty")
		for _, x := range d.Actions {
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%d\t%d\n", x.Action, enabledText(x.Enabled), x.Mode, x.Kinds, x.Puzzles, x.Difficulty)
		}
		_ = w.Flush()
	}
	fmt.Fprintln(a.Out, "\n密钥对：")
	if len(d.Secrets) == 0 {
		fmt.Fprintln(a.Out, "  （无）")
		return nil
	}
	w := tabwriter.NewWriter(a.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "  secret_id\t状态\t过期时间\t替换自\t创建时间")
	now := a.Now()
	for _, k := range d.Secrets {
		state := enabledText(k.Enabled)
		if !k.Enabled {
			state = "已吊销"
		} else if k.ExpiresAt.Valid && !now.Before(k.ExpiresAt.Time) {
			state = "已过期"
		}
		exp := "永不"
		if k.ExpiresAt.Valid {
			exp = k.ExpiresAt.Time.UTC().Format(time.RFC3339)
		}
		from := "-"
		if k.RotatedFrom.Valid {
			from = k.RotatedFrom.String
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\n", k.SecretID, state, exp, from, k.CreatedAt.UTC().Format(time.RFC3339))
	}
	return w.Flush()
}

func (a *App) appEnable(ctx context.Context, enable bool, args []string) error {
	fs := flag.NewFlagSet("app enable", flag.ContinueOnError)
	app := fs.String("app", "", "")
	if err := parse(fs, args, "app"); err != nil {
		return err
	}
	adm, err := a.admin()
	if err != nil {
		return err
	}
	if err := adm.SetAppEnabled(ctx, *app, enable); err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "应用 %s 已%s\n", *app, enabledText(enable))
	return nil
}

func (a *App) origin(ctx context.Context, add bool, args []string) error {
	fs := flag.NewFlagSet("origin", flag.ContinueOnError)
	app := fs.String("app", "", "")
	origin := fs.String("origin", "", "")
	if err := parse(fs, args, "app", "origin"); err != nil {
		return err
	}
	adm, err := a.admin()
	if err != nil {
		return err
	}
	if add {
		o, err := adm.AddOrigin(ctx, *app, *origin)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.Out, "已添加允许来源 %s\n", o)
		return nil
	}
	o, err := adm.RemoveOrigin(ctx, *app, *origin)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "已移除允许来源 %s（已签发的 challenge 和 token 也会随之失效）\n", o)
	return nil
}

func (a *App) actionSet(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("action set", flag.ContinueOnError)
	app := fs.String("app", "", "")
	action := fs.String("action", "", "")
	enabled := fs.Bool("enabled", true, "")
	puzzles := fs.Int("puzzles", logic.DefaultPuzzles, "")
	difficulty := fs.Uint64("difficulty", logic.DefaultDifficulty, "")
	mode := fs.String("mode", domain.ModeAdaptive, "")
	kinds := fs.String("kinds", strings.Join(domain.InteractiveKinds, ","), "")
	if err := parse(fs, args, "app", "action"); err != nil {
		return err
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if set["mode"] && set["kinds"] && *mode == domain.ModeSlider {
		return usageError("-mode slider 只出滑块，不能同时指定 -kinds；请改用 -mode interactive -kinds ...")
	}
	adm, err := a.admin()
	if err != nil {
		return err
	}
	d, err := adm.AppDetail(ctx, *app)
	if err != nil {
		return err
	}
	// Start from the stored policy so unspecified flags keep their values.
	p := domain.ActionPolicy{Enabled: true, Mode: domain.ModeAdaptive, Kinds: domain.InteractiveKinds,
		Puzzles: logic.DefaultPuzzles, Difficulty: logic.DefaultDifficulty}
	for _, x := range d.Actions {
		if x.Action == *action {
			p = domain.ActionPolicy{Enabled: x.Enabled, Mode: x.Mode, Kinds: repository.ParseKinds(x.Kinds),
				Puzzles: x.Puzzles, Difficulty: x.Difficulty}
		}
	}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "enabled":
			p.Enabled = *enabled
		case "puzzles":
			p.Puzzles = *puzzles
		case "difficulty":
			p.Difficulty = *difficulty
		case "mode":
			p.Mode = *mode
		case "kinds":
			p.Kinds = nil
			for _, k := range strings.Split(*kinds, ",") {
				if k = strings.TrimSpace(k); k != "" {
					p.Kinds = append(p.Kinds, k)
				}
			}
		}
	})
	if err := adm.SetAction(ctx, *app, *action, p); err != nil {
		return err
	}
	if p.Mode == domain.ModeSlider {
		p.Mode, p.Kinds = domain.ModeInteractive, []string{domain.KindSlider}
	}
	fmt.Fprintf(a.Out, "action %s：%s，模式 %s，题型 %s，puzzles=%d，difficulty=%d（每次验证期望总尝试次数约 %d）\n",
		*action, enabledText(p.Enabled), p.Mode, strings.Join(p.Kinds, ","), p.Puzzles, p.Difficulty, uint64(p.Puzzles)*p.Difficulty)
	fmt.Fprintln(a.Out, "注意：gateway 的 Challenge.MaxDifficulty 会限制实际难度。")
	return nil
}

// checkKEK refuses to seal new secrets with a KEK that cannot open the
// existing ones under the same id.
func (a *App) checkKEK(ctx context.Context, adm *logic.AppAdmin) error {
	others, err := adm.CheckKEK(ctx)
	if err != nil {
		return err
	}
	if others {
		fmt.Fprintf(a.Err, "提示：库中有用其他 KEK id 加密的密钥对。如果不是正在轮换 KEK，请确认 %s 的 id 与 gateway 一致。\n", KEKEnv)
	}
	return nil
}

func printSecret(out io.Writer, p credential.SecretPair) {
	fmt.Fprintf(out, "secret_id: %s\nsecret_key: %s\n\n", p.SecretID, p.SecretKey)
	fmt.Fprintln(out, "这是 secret_key 唯一一次显示，数据库只保存加密后的密文。请立即存入业务服务器的配置，不要提交到代码仓库或放进前端。")
}

func (a *App) secretCreate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("secret create", flag.ContinueOnError)
	app := fs.String("app", "", "")
	ttl := fs.Duration("ttl", 0, "")
	if err := parse(fs, args, "app"); err != nil {
		return err
	}
	adm, err := a.admin()
	if err != nil {
		return err
	}
	if err := a.checkKEK(ctx, adm); err != nil {
		return err
	}
	p, exp, err := adm.CreateSecret(ctx, *app, *ttl)
	if err != nil {
		return err
	}
	if exp != nil {
		fmt.Fprintf(a.Out, "已生成密钥对，过期时间 %s\n", exp.Format(time.RFC3339))
	} else {
		fmt.Fprintln(a.Out, "已生成密钥对，永不过期（可用 secret revoke 吊销）")
	}
	printSecret(a.Out, p)
	return nil
}

func (a *App) secretRevoke(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("secret revoke", flag.ContinueOnError)
	id := fs.String("secret", "", "")
	if err := parse(fs, args, "secret"); err != nil {
		return err
	}
	adm, err := a.admin()
	if err != nil {
		return err
	}
	if err := adm.RevokeSecret(ctx, *id); err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "密钥对 %s 已吊销\n", *id)
	return nil
}

func (a *App) secretRotate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("secret rotate", flag.ContinueOnError)
	id := fs.String("secret", "", "")
	grace := fs.Duration("grace", 24*time.Hour, "")
	ttl := fs.Duration("ttl", 0, "")
	if err := parse(fs, args, "secret"); err != nil {
		return err
	}
	adm, err := a.admin()
	if err != nil {
		return err
	}
	if err := a.checkKEK(ctx, adm); err != nil {
		return err
	}
	p, oldUntil, err := adm.RotateSecret(ctx, *id, *grace, *ttl)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "已生成替换密钥对；旧密钥对 %s 最晚在 %s 失效\n", *id, oldUntil.Format(time.RFC3339))
	printSecret(a.Out, p)
	return nil
}
