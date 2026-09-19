// Command jevgate decides whether an AI code reviewer's approval may replace a
// human approval for a change.
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

// run keeps os.Exit in main so that the signal handler is released before the
// process exits.
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr)
}
