package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/mholtzscher/hearth/internal/app/hearthd"
)

func main() {
	configPath := flag.String("config", "configs/hearthd.yaml", "path to the hearthd YAML configuration")
	flag.Parse()

	config, err := hearthd.LoadConfig(*configPath)
	if err != nil {
		slog.Error("load configuration", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := hearthd.Run(ctx, config, slog.Default()); err != nil {
		slog.Error("run hearthd", "error", err)
		os.Exit(1)
	}
}
