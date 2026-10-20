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
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const DSNEnv = "BOTGUARD_MYSQL_DSN"

const usage = `用法：botguard-ctl <命令> [参数]

数据库连接从环境变量 BOTGUARD_MYSQL_DSN 读取（不接受命令行参数，避免进入 shell 历史）。

  migrate up                                   执行未执行的迁移
  migrate status                               查看迁移状态
  site create  -name 名称                      创建站点，输出 site_key
  site list                                    列出站点
  site show    -site site_key                  查看站点的来源、action 和密钥
  site enable  -site site_key                  启用站点
  site disable -site site_key                  停用站点
  origin add    -site site_key -origin URL     添加允许来源，如 https://example.com
  origin remove -site site_key -origin URL     移除允许来源
  action set -site site_key -action 名称 [-enabled=true] [-puzzles 8] [-difficulty 2000]
                                               新增或修改 action；只修改显式给出的参数
  key create -site site_key [-ttl 0]           生成服务端密钥，secret 只显示这一次
  key revoke -key key_id                       吊销密钥
  key rotate -key key_id [-grace 24h] [-ttl 0] 生成替换密钥，旧密钥在宽限期后失效

站点、来源、action、密钥的变更会在 gateway 的站点缓存过期后生效（默认 10 秒内）。
`

// App runs one command. Env and output are injected for tests.
type App struct {
	Env    func(string) string
	Out    io.Writer
	Now    func() time.Time
	openDB func(dsn string) sqlx.SqlConn
}

func New(env func(string) string, out io.Writer) *App {
	return &App{Env: env, Out: out, Now: time.Now, openDB: func(dsn string) sqlx.SqlConn { return sqlx.NewMysql(dsn) }}
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
		fmt.Fprintf(a.Out, "错误：%s\n\n%s", ue, usage)
		return 2
	}
	fmt.Fprintf(a.Out, "错误：%s\n", describe(err))
	return 1
}

func describe(err error) string {
	switch {
	case errors.Is(err, logic.ErrInvalid):
		return strings.TrimPrefix(err.Error(), logic.ErrInvalid.Error()+": ")
	case errors.Is(err, domain.ErrNotFound):
		return "未找到指定的站点、来源或密钥"
	case errors.Is(err, domain.ErrDuplicate):
		return "记录已存在"
	case errors.Is(err, domain.ErrInvalidState):
		return "密钥已吊销，不能轮换"
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

func (a *App) admin() (*logic.SiteAdmin, error) {
	c, err := a.conn()
	if err != nil {
		return nil, err
	}
	return &logic.SiteAdmin{Repo: repository.NewSiteAdmin(c), Now: a.Now}, nil
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
	case "site create":
		return a.siteCreate(ctx, rest)
	case "site list":
		return a.siteList(ctx, rest)
	case "site show":
		return a.siteShow(ctx, rest)
	case "site enable", "site disable":
		return a.siteEnable(ctx, args[1] == "enable", rest)
	case "origin add", "origin remove":
		return a.origin(ctx, args[1] == "add", rest)
	case "action set":
		return a.actionSet(ctx, rest)
	case "key create":
		return a.keyCreate(ctx, rest)
	case "key revoke":
		return a.keyRevoke(ctx, rest)
	case "key rotate":
		return a.keyRotate(ctx, rest)
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

func (a *App) siteCreate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("site create", flag.ContinueOnError)
	name := fs.String("name", "", "")
	if err := parse(fs, args, "name"); err != nil {
		return err
	}
	adm, err := a.admin()
	if err != nil {
		return err
	}
	key, err := adm.CreateSite(ctx, *name)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "已创建站点 %s\nsite_key: %s\n下一步：添加允许来源（origin add）、配置 action（action set）、生成服务端密钥（key create）。\n", *name, key)
	return nil
}

func (a *App) siteList(ctx context.Context, args []string) error {
	if err := parse(flag.NewFlagSet("site list", flag.ContinueOnError), args); err != nil {
		return err
	}
	adm, err := a.admin()
	if err != nil {
		return err
	}
	sites, err := adm.ListSites(ctx)
	if err != nil {
		return err
	}
	if len(sites) == 0 {
		fmt.Fprintln(a.Out, "还没有站点")
		return nil
	}
	w := tabwriter.NewWriter(a.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "site_key\t名称\t状态\t策略版本\t创建时间")
	for _, s := range sites {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", s.PublicKey, s.Name, enabledText(s.Enabled), s.PolicyVersion, s.CreatedAt.UTC().Format(time.RFC3339))
	}
	return w.Flush()
}

func enabledText(b bool) string {
	if b {
		return "启用"
	}
	return "停用"
}

func (a *App) siteShow(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("site show", flag.ContinueOnError)
	site := fs.String("site", "", "")
	if err := parse(fs, args, "site"); err != nil {
		return err
	}
	adm, err := a.admin()
	if err != nil {
		return err
	}
	d, err := adm.SiteDetail(ctx, *site)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "站点：%s（%s）\nsite_key：%s\n策略版本：%d\n\n允许来源：\n", d.Name, enabledText(d.Enabled), d.PublicKey, d.PolicyVersion)
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
		fmt.Fprintln(w, "  名称\t状态\tpuzzles\tdifficulty")
		for _, x := range d.Actions {
			fmt.Fprintf(w, "  %s\t%s\t%d\t%d\n", x.Action, enabledText(x.Enabled), x.Puzzles, x.Difficulty)
		}
		_ = w.Flush()
	}
	fmt.Fprintln(a.Out, "\n服务端密钥：")
	if len(d.Keys) == 0 {
		fmt.Fprintln(a.Out, "  （无）")
		return nil
	}
	w := tabwriter.NewWriter(a.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "  key_id\t状态\t过期时间\t替换自\t创建时间")
	now := a.Now()
	for _, k := range d.Keys {
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
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\n", k.KeyID, state, exp, from, k.CreatedAt.UTC().Format(time.RFC3339))
	}
	return w.Flush()
}

func (a *App) siteEnable(ctx context.Context, enable bool, args []string) error {
	fs := flag.NewFlagSet("site enable", flag.ContinueOnError)
	site := fs.String("site", "", "")
	if err := parse(fs, args, "site"); err != nil {
		return err
	}
	adm, err := a.admin()
	if err != nil {
		return err
	}
	if err := adm.SetSiteEnabled(ctx, *site, enable); err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "站点 %s 已%s\n", *site, enabledText(enable))
	return nil
}

func (a *App) origin(ctx context.Context, add bool, args []string) error {
	fs := flag.NewFlagSet("origin", flag.ContinueOnError)
	site := fs.String("site", "", "")
	origin := fs.String("origin", "", "")
	if err := parse(fs, args, "site", "origin"); err != nil {
		return err
	}
	adm, err := a.admin()
	if err != nil {
		return err
	}
	if add {
		o, err := adm.AddOrigin(ctx, *site, *origin)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.Out, "已添加允许来源 %s\n", o)
		return nil
	}
	o, err := adm.RemoveOrigin(ctx, *site, *origin)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "已移除允许来源 %s（已签发的 challenge 和 token 也会随之失效）\n", o)
	return nil
}

func (a *App) actionSet(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("action set", flag.ContinueOnError)
	site := fs.String("site", "", "")
	action := fs.String("action", "", "")
	enabled := fs.Bool("enabled", true, "")
	puzzles := fs.Int("puzzles", logic.DefaultPuzzles, "")
	difficulty := fs.Uint64("difficulty", logic.DefaultDifficulty, "")
	if err := parse(fs, args, "site", "action"); err != nil {
		return err
	}
	adm, err := a.admin()
	if err != nil {
		return err
	}
	d, err := adm.SiteDetail(ctx, *site)
	if err != nil {
		return err
	}
	// Start from the stored policy so unspecified flags keep their values.
	p := domain.ActionPolicy{Enabled: true, Puzzles: logic.DefaultPuzzles, Difficulty: logic.DefaultDifficulty}
	for _, x := range d.Actions {
		if x.Action == *action {
			p = domain.ActionPolicy{Enabled: x.Enabled, Puzzles: x.Puzzles, Difficulty: x.Difficulty}
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
		}
	})
	if err := adm.SetAction(ctx, *site, *action, p); err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "action %s：%s，puzzles=%d，difficulty=%d（每次验证期望总尝试次数约 %d）\n",
		*action, enabledText(p.Enabled), p.Puzzles, p.Difficulty, uint64(p.Puzzles)*p.Difficulty)
	fmt.Fprintln(a.Out, "注意：gateway 的 Challenge.MaxDifficulty 会限制实际难度。")
	return nil
}

func printSecret(out io.Writer, keyID, header string) {
	fmt.Fprintf(out, "key_id: %s\nAuthorization: %s\n\n", keyID, header)
	fmt.Fprintln(out, "这是 secret 唯一一次显示，服务端只保存其摘要。请立即存入业务服务器的密钥配置，不要提交到代码仓库或放进前端。")
}

func (a *App) keyCreate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("key create", flag.ContinueOnError)
	site := fs.String("site", "", "")
	ttl := fs.Duration("ttl", 0, "")
	if err := parse(fs, args, "site"); err != nil {
		return err
	}
	adm, err := a.admin()
	if err != nil {
		return err
	}
	k, exp, err := adm.CreateKey(ctx, *site, *ttl)
	if err != nil {
		return err
	}
	if exp != nil {
		fmt.Fprintf(a.Out, "已生成密钥，过期时间 %s\n", exp.Format(time.RFC3339))
	} else {
		fmt.Fprintln(a.Out, "已生成密钥，永不过期（可用 key revoke 吊销）")
	}
	printSecret(a.Out, k.KeyID, k.Header())
	return nil
}

func (a *App) keyRevoke(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("key revoke", flag.ContinueOnError)
	key := fs.String("key", "", "")
	if err := parse(fs, args, "key"); err != nil {
		return err
	}
	adm, err := a.admin()
	if err != nil {
		return err
	}
	if err := adm.RevokeKey(ctx, *key); err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "密钥 %s 已吊销\n", *key)
	return nil
}

func (a *App) keyRotate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("key rotate", flag.ContinueOnError)
	key := fs.String("key", "", "")
	grace := fs.Duration("grace", 24*time.Hour, "")
	ttl := fs.Duration("ttl", 0, "")
	if err := parse(fs, args, "key"); err != nil {
		return err
	}
	adm, err := a.admin()
	if err != nil {
		return err
	}
	k, oldUntil, err := adm.RotateKey(ctx, *key, *grace, *ttl)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "已生成替换密钥；旧密钥 %s 最晚在 %s 失效\n", *key, oldUntil.Format(time.RFC3339))
	printSecret(a.Out, k.KeyID, k.Header())
	return nil
}
