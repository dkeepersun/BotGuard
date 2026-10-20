package main

import (
	"flag"
	"github.com/dkeepersun/BotGuard/internal/adminweb"
	"github.com/dkeepersun/BotGuard/internal/config"
	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
	"log"
)

func main() {
	path := flag.String("f", "configs/admin-web.yaml", "configuration file")
	flag.Parse()
	var c config.AdminConfig
	conf.MustLoad(*path, &c)
	if err := c.ValidateBootstrap(); err != nil {
		log.Fatal(err)
	}
	// Never enable request logging for future tokens or API credentials.
	c.Middlewares.Log = false
	c.Middlewares.Trace = false
	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()
	adminweb.RegisterHandlers(server)
	server.Start()
}
