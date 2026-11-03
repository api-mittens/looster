package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/dominikbb/looster/internal/cli"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	args := os.Args[1:]
	if len(args) > 0 && strings.HasSuffix(args[0], ".toml") {
		args = append([]string{"--config", args[0], "serve"}, args[1:]...)
	}
	if e := cli.Execute(ctx, args, os.Stdin, os.Stdout, os.Stderr); e != nil {
		fmt.Fprintln(os.Stderr, "looster:", e)
		os.Exit(1)
	}
}
