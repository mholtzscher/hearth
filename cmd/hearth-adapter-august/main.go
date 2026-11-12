package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"syscall"

	"github.com/mholtzscher/hearth/internal/app/august"
)

func main() { os.Exit(run()) }

func run() int {
	ctx := context.Background()
	// Authorization reads stdin synchronously. Leave the default signal action in
	// place so Ctrl-C interrupts terminal input without an abandoned reader.
	if !slices.Contains(os.Args[1:], "authorize") {
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
	}
	if err := newCommand(august.Run, august.Authorize, os.Stdin, os.Stderr).Run(ctx, os.Args); err != nil {
		if !errors.Is(err, errAdapterFailed) {
			fmt.Fprintln(os.Stderr, err)
		}
		return 1
	}
	return 0
}
