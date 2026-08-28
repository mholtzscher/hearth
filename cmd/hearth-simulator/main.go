package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/mholtzscher/hearth/internal/app/simulator"
)

func main() {
	configPath := flag.String("config", "configs/simulator.yaml", "path to the simulator YAML configuration")
	flag.Parse()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	config, err := simulator.LoadConfig(*configPath)
	if err != nil {
		logger.Error("load configuration", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := simulator.Run(ctx, config, logger); err != nil {
		logger.Error("run simulator", "error", err)
		os.Exit(1)
	}
}
