package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/thornity/steam-release-tool/internal/cli"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	out, errOut := cli.DefaultWriters()
	err := cli.Execute(ctx, os.Args[1:], out, errOut)
	cli.PrintError(errOut, err)
	os.Exit(cli.ExitCode(err))
}
