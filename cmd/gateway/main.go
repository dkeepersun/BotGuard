package main

import (
	"flag"
	"log"

	"github.com/dkeepersun/BotGuard/internal/config"
	"github.com/dkeepersun/BotGuard/internal/handler"
	"github.com/dkeepersun/BotGuard/internal/svc"
	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"github.com/zeromicro/go-zero/rest"
)

func main() {
	path := flag.String("f", "configs/gateway.yaml", "configuration file")
	flag.Parse()
	var c config.GatewayConfig
	// Secrets and DSNs come from the environment, never the YAML file.
	conf.MustLoad(*path, &c, conf.UseEnv())
	if err := c.Validate(); err != nil {
		log.Fatal(err)
	}
	// Never enable request logging: bodies carry tokens, headers carry signatures.
	c.Middlewares.Log = false
	c.Middlewares.Trace = false
	// go-zero logs every SQL statement with its arguments by default; app keys
	// in those come straight from unauthenticated callers.
	sqlx.DisableStmtLog()
	ctx, err := svc.NewGateway(c)
	if err != nil {
		log.Fatal(err)
	}
	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()
	(&handler.Gateway{Logic: ctx.Logic, IPs: ctx.IPs, Ready: ctx.Ready}).Register(server)
	server.Start()
}
