package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mholtzscher/hearth/internal/app/simulator"
	"github.com/mholtzscher/hearth/internal/platform/logging"
)

func main() {
	os.Exit(run())
}

func run() int {
	configPath := flag.String("config", "configs/simulator.yaml", "path to the simulator YAML configuration")
	logLevel := flag.String("log-level", "info", "log level: debug, info, warn, or error")
	logFormat := flag.String("log-format", "text", "log format: text or json")
	flag.Parse()
	logger, loggerErr := logging.NewApplicationLogger(os.Stderr, "hearth-simulator", logging.LogOptions{
		Level: *logLevel, Format: *logFormat,
	})
	if loggerErr != nil {
		fmt.Fprintln(os.Stderr, loggerErr)
		return 1
	}
	processLogger := logger.With("component", "process")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	processLogger.InfoContext(ctx, "hearth-simulator starting", "event", "process.starting")
	config, configErr := simulator.LoadConfig(*configPath)
	if configErr != nil {
		processLogger.ErrorContext(
			ctx,
			"hearth-simulator configuration failed",
			"event",
			"process.failed",
			"error_code",
			"config_invalid",
			"stage",
			"load_config",
		)
		return 1
	}
	processLogger.InfoContext(ctx, "hearth-simulator configuration loaded", "event", "process.config_loaded")
	if err := simulator.Run(ctx, config, logger); err != nil {
		processLogger.ErrorContext(
			ctx,
			"hearth-simulator failed",
			"event",
			"process.failed",
			"error_code",
			"run_failed",
			"stage",
			"run",
		)
		return 1
	}
	processLogger.InfoContext(ctx, "hearth-simulator stopped", "event", "process.stopped")
	return 0
}
