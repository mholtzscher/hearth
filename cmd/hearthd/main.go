package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mholtzscher/hearth/internal/app/hearthd"
)

func main() {
	os.Exit(run())
}

func run() int {
	command := newHearthdCommand(hearthd.Run, os.Args, os.Stderr)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := command.Run(ctx, os.Args); err != nil {
		if errors.Is(err, errHearthdFailed) {
			return 1
		}
		// CLI parse failures happen before an application logger is available.
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
