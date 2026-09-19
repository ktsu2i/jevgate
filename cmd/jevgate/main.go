package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/ktsu2i/jevgate/internal/cli"
)

func main() {
	os.Exit(run())
}

// Keep os.Exit outside run so deferred signal cleanup executes.
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr)
}
