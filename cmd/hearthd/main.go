package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mholtzscher/hearth/internal/app/hearthd"
	"github.com/mholtzscher/hearth/internal/platform/logging"
)

func main() {
	os.Exit(run())
}

func run() int {
	configPath := flag.String("config", "configs/hearthd.yaml", "path to the hearthd YAML configuration")
	logLevel := flag.String("log-level", "info", "log level: debug, info, warn, or error")
	logFormat := flag.String("log-format", "text", "log format: text or json")
	flag.Parse()
	logger, loggerErr := logging.NewApplicationLogger(os.Stderr, "hearthd", logging.LogOptions{
		Level: *logLevel, Format: *logFormat,
	})
	if loggerErr != nil {
		fmt.Fprintln(os.Stderr, loggerErr)
		return 1
	}
	processLogger := logger.With("component", "process")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	processLogger.InfoContext(ctx, "hearthd starting", "event", "process.starting")
	config, configErr := hearthd.LoadConfig(*configPath)
	if configErr != nil {
		processLogger.ErrorContext(
			ctx,
			"hearthd configuration failed",
			"event",
			"process.failed",
			"error_code",
			"config_invalid",
			"stage",
			"load_config",
		)
		return 1
	}
	processLogger.InfoContext(ctx, "hearthd configuration loaded", "event", "process.config_loaded")
	if err := hearthd.Run(ctx, config, logger); err != nil {
		processLogger.ErrorContext(
			ctx,
			"hearthd failed",
			"event",
			"process.failed",
			"error_code",
			"run_failed",
			"stage",
			hearthd.ErrorStage(err),
		)
		return 1
	}
	processLogger.InfoContext(ctx, "hearthd stopped", "event", "process.stopped")
	return 0
}
