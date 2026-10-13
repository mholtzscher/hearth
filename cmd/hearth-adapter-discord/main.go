package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mholtzscher/hearth/internal/app/discord"
)

func main() { os.Exit(run()) }
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := newCommand(discord.Run, os.Stderr).Run(ctx, os.Args); err != nil {
		if !errors.Is(err, errAdapterFailed) {
			fmt.Fprintln(os.Stderr, err)
		}
		return 1
	}
	return 0
}
