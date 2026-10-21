package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/dkeepersun/BotGuard/internal/adminweb"
	"github.com/dkeepersun/BotGuard/internal/config"
	"github.com/dkeepersun/BotGuard/internal/logic"
	"github.com/dkeepersun/BotGuard/internal/migration"
	"github.com/dkeepersun/BotGuard/internal/repository"
	"github.com/dkeepersun/BotGuard/internal/service/clientip"
	"github.com/dkeepersun/BotGuard/internal/store"
	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/redis"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"github.com/zeromicro/go-zero/rest"
)

func main() {
	path := flag.String("f", "configs/admin-web.yaml", "configuration file")
	flag.Parse()
	var c config.AdminConfig
	// Secrets and DSNs come from the environment, never the YAML file.
	conf.MustLoad(*path, &c, conf.UseEnv())
	if err := c.Validate(); err != nil {
		log.Fatal(err)
	}
	// Request bodies carry passwords and CSRF tokens: never log them.
	c.Middlewares.Log = false
	c.Middlewares.Trace = false
	c.Middlewares.Breaker = false
	// Statements carry password hashes and TOTP ciphertexts.
	sqlx.DisableStmtLog()
	// Before anything logs, so file logging and its retention cover startup.
	if c.Log.ServiceName == "" {
		c.Log.ServiceName = c.Name
	}
	logx.MustSetup(c.Log)

	dbc, err := c.DB()
	if err != nil {
		log.Fatal(err)
	}
	db, err := store.Open(dbc)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := migration.Prepare(context.Background(), db); err != nil {
		log.Fatal(err)
	}
	logx.Infof("database: %s %s", db.Driver, db.Name)
	rds, err := redis.NewRedis(c.Redis)
	if err != nil {
		log.Fatal(err)
	}
	seal, err := c.Sealer()
	if err != nil {
		log.Fatal(err)
	}
	ips, err := clientip.New(c.TrustedProxies)
	if err != nil {
		log.Fatal(err)
	}
	repo := repository.NewAppAdmin(db)
	idle, absolute := time.Duration(c.Session.IdleMinutes)*time.Minute, time.Duration(c.Session.AbsoluteHours)*time.Hour
	console := &logic.Console{Admins: repository.NewAdmins(db), Audit: repo, Sessions: repository.NewSessions(rds, idle, absolute),
		Limiter: repository.NewRateLimiter(rds), Redis: rds, Sealer: seal, Now: time.Now}
	latest, err := migration.Latest(db.Driver)
	if err != nil {
		log.Fatal(err)
	}

	kek := "未配置（不能创建密钥对、不能开启动态码）"
	if seal != nil {
		kek = seal.Active()
	}
	dbName := map[store.Driver]string{store.SQLite: "SQLite", store.MySQL: "MySQL"}[db.Driver]
	info := map[string]string{
		"数据库":          dbName + "（" + db.Name + "）",
		"数据清理":         cleanupInfo(c),
		"gateway 公开地址": orDash(c.PublicEndpoint),
		"程序内置库结构版本":    fmt.Sprintf("%04d", latest),
		"KEK":          kek,
		"会话时长":         fmt.Sprintf("空闲 %d 分钟 / 最长 %d 小时", c.Session.IdleMinutes, c.Session.AbsoluteHours),
		"Cookie":       map[bool]string{true: "仅 HTTPS（Secure）", false: "允许 HTTP（仅限本机或内网）"}[c.CookieSecure],
		"启动时间":         time.Now().Format("2006-01-02 15:04:05"),
		"程序版本":         buildVersion(),
	}
	httpClient := &http.Client{Timeout: 2 * time.Second}
	checks := func(ctx context.Context) []adminweb.Check {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		var out []adminweb.Check
		raw, err := db.Raw()
		dbOK := err == nil && raw.PingContext(ctx) == nil
		out = append(out, adminweb.Check{Name: dbName, OK: dbOK, Detail: map[bool]string{true: "连接正常", false: "无法连接"}[dbOK]})
		redisOK := rds.PingCtx(ctx)
		out = append(out, adminweb.Check{Name: "Redis", OK: redisOK, Detail: map[bool]string{true: "连接正常", false: "无法连接（会话与统计不可用）"}[redisOK]})
		if dbOK {
			applied, err := migration.Applied(ctx, raw, db.Driver)
			ok := err == nil && applied >= latest
			detail := fmt.Sprintf("已执行 %04d，程序需要 %04d", applied, latest)
			if !ok {
				detail += "：请执行 botguard-ctl migrate up"
			}
			out = append(out, adminweb.Check{Name: "库结构", OK: ok, Detail: detail})
		}
		out = append(out, adminweb.Check{Name: "KEK", OK: seal != nil, Detail: kek})
		if c.GatewayURL != "" {
			gw := adminweb.Check{Name: "gateway", Detail: c.GatewayURL}
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.GatewayURL, "/")+"/readyz", nil)
			if resp, err := httpClient.Do(req); err == nil {
				resp.Body.Close()
				gw.OK = resp.StatusCode == http.StatusOK
				gw.Detail += fmt.Sprintf("（/readyz %d）", resp.StatusCode)
			} else {
				gw.Detail += "（无法访问）"
			}
			out = append(out, gw)
		}
		return out
	}

	app := &logic.AppAdmin{Repo: repo, Sealer: seal, Now: time.Now}
	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()
	web := &adminweb.Server{Console: console, Apps: app, Repo: repo, Stats: repository.NewStats(rds), IPs: ips, Checks: checks,
		PublicEndpoint: strings.TrimRight(c.PublicEndpoint, "/"), CookieSecure: c.CookieSecure, SessionTTL: absolute, Info: info}
	if err := web.Register(server); err != nil {
		log.Fatal(err)
	}
	if c.Cleanup.Enabled {
		days := func(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }
		retention := repository.NewRetention(db, days(c.Cleanup.AuditLogDays), days(c.Cleanup.RetiredSecretDays))
		go retention.Run(context.Background(), time.Duration(c.Cleanup.IntervalMinutes)*time.Minute, time.Now)
	}
	log.Printf("BotGuard admin console on %s:%d", c.Host, c.Port)
	server.Start()
}

func cleanupInfo(c config.AdminConfig) string {
	if !c.Cleanup.Enabled {
		return "已关闭"
	}
	keep := func(n int) string {
		if n == 0 {
			return "永久"
		}
		return fmt.Sprintf("%d 天", n)
	}
	return fmt.Sprintf("每 %d 分钟；审计日志保留 %s，失效密钥对保留 %s", c.Cleanup.IntervalMinutes,
		keep(c.Cleanup.AuditLogDays), keep(c.Cleanup.RetiredSecretDays))
}

func orDash(s string) string {
	if s == "" {
		return "未配置（接入代码中会显示占位地址）"
	}
	return s
}

func buildVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	rev, dirty := "", false
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if rev == "" {
		if bi.Main.Version == "" || bi.Main.Version == "(devel)" {
			return "开发构建（无版本信息）"
		}
		return bi.Main.Version
	}
	if dirty {
		rev += "（含未提交改动）"
	}
	return rev
}
