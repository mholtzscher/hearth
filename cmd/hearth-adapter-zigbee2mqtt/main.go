package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mholtzscher/hearth/internal/app/zigbee2mqtt"
)

func main() { os.Exit(run()) }
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := newCommand(zigbee2mqtt.Run, os.Stderr).Run(ctx, os.Args); err != nil {
		if errors.Is(err, errAdapterFailed) {
			return 1
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
