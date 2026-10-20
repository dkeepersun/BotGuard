package main

import (
	"context"
	"os"

	"github.com/dkeepersun/BotGuard/internal/ctl"
	"github.com/zeromicro/go-zero/core/logx"
)

func main() {
	// Errors are reported by the command itself; go-zero's JSON logs would
	// only add noise, and could echo connection details.
	logx.Disable()
	os.Exit(ctl.New(os.Getenv, os.Stdout, os.Stderr).Run(context.Background(), os.Args[1:]))
}
