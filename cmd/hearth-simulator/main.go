package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mholtzscher/hearth/internal/app/simulator"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	command := newSimulatorCommand(simulator.Run, os.Stdout, os.Stderr)
	if err := command.Run(ctx, os.Args); err != nil {
		if errors.Is(err, errSimulatorFailed) {
			return 1
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
