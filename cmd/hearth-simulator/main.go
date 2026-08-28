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
	os.Exit(run())
}

func run() int {
	configPath := flag.String("config", "configs/simulator.yaml", "path to the simulator YAML configuration")
	flag.Parse()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	config, configErr := simulator.LoadConfig(*configPath)
	if configErr != nil {
		logger.Error("load configuration", "error", configErr)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := simulator.Run(ctx, config, logger); err != nil {
		logger.Error("run simulator", "error", err)
		return 1
	}
	return 0
}
