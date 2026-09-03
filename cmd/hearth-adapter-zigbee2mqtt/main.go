package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/mholtzscher/hearth/internal/app/zigbee2mqtt"
)

func main() {
	os.Exit(run())
}

func run() int {
	configPath := flag.String(
		"config",
		"configs/zigbee2mqtt.yaml",
		"path to the Zigbee2MQTT adapter YAML configuration",
	)
	flag.Parse()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	config, configErr := zigbee2mqtt.LoadConfig(*configPath)
	if configErr != nil {
		logger.Error("load configuration", "error", configErr)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := zigbee2mqtt.Run(ctx, config, logger); err != nil {
		logger.Error("run Zigbee2MQTT adapter", "error", err)
		return 1
	}
	return 0
}
