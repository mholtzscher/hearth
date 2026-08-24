package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/mholtzscher/hearth/internal/app/homeassistant"
)

func main() {
	configPath := flag.String("config", "configs/homeassistant.yaml", "path to the Home Assistant adapter YAML configuration")
	flag.Parse()

	config, err := homeassistant.LoadConfig(*configPath)
	if err != nil {
		slog.Error("load configuration", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := homeassistant.Run(ctx, config, slog.Default()); err != nil {
		slog.Error("run Home Assistant adapter", "error", err)
		os.Exit(1)
	}
}
