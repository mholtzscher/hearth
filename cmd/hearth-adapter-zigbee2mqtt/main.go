package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mholtzscher/hearth/internal/app/zigbee2mqtt"
	"github.com/mholtzscher/hearth/internal/platform/logging"
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
	logLevel := flag.String("log-level", "info", "log level: debug, info, warn, or error")
	logFormat := flag.String("log-format", "text", "log format: text or json")
	flag.Parse()
	logger, loggerErr := logging.NewApplicationLogger(os.Stderr, "hearth-adapter-zigbee2mqtt", logging.LogOptions{
		Level: *logLevel, Format: *logFormat,
	})
	if loggerErr != nil {
		fmt.Fprintln(os.Stderr, loggerErr)
		return 1
	}
	processLogger := logger.With("component", "process")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	processLogger.InfoContext(ctx, "hearth-adapter-zigbee2mqtt starting", "event", "process.starting")
	config, configErr := zigbee2mqtt.LoadConfig(*configPath)
	if configErr != nil {
		processLogger.ErrorContext(
			ctx,
			"hearth-adapter-zigbee2mqtt configuration failed",
			"event",
			"process.failed",
			"error_code",
			"config_invalid",
			"stage",
			"load_config",
		)
		return 1
	}
	processLogger.InfoContext(
		ctx, "hearth-adapter-zigbee2mqtt configuration loaded", "event", "process.config_loaded",
	)
	if err := zigbee2mqtt.Run(ctx, config, logger); err != nil {
		processLogger.ErrorContext(
			ctx,
			"hearth-adapter-zigbee2mqtt failed",
			"event",
			"process.failed",
			"error_code",
			"run_failed",
			"stage",
			"run",
		)
		return 1
	}
	processLogger.InfoContext(ctx, "hearth-adapter-zigbee2mqtt stopped", "event", "process.stopped")
	return 0
}
