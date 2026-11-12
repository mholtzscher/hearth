package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/mholtzscher/hearth/internal/app/august"
)

func main() { os.Exit(run()) }

func run() int {
	// Authorization keeps the default signal action so Ctrl-C interrupts stdin
	// without an abandoned reader. Only the monitoring process needs a graceful join.
	if err := newCommand(
		runMonitor,
		august.Authorize,
		os.Stdin,
		os.Stderr,
	).Run(context.Background(), os.Args); err != nil {
		if !errors.Is(err, errAdapterFailed) {
			fmt.Fprintln(os.Stderr, err)
		}
		return 1
	}
	return 0
}

func runMonitor(ctx context.Context, config august.Config, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return august.Run(ctx, config, logger)
}
