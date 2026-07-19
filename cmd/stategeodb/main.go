package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/vikewoods/stategeodb/internal/cli"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	exitCode := cli.Run(
		ctx,
		os.Args[1:],
		os.Stdout,
		os.Stderr,
		version,
	)
	stop()

	os.Exit(exitCode)
}
