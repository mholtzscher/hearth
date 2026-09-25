package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/mholtzscher/hearth/internal/app/zwavejs"
	"github.com/mholtzscher/hearth/internal/platform/logging"
)

func main() {
	os.Exit(run())
}

func run() int {
	configPath := flag.String(
		"config",
		"configs/zwavejs.yaml",
		"path to the Z-Wave JS adapter YAML configuration",
	)
	logLevel := flag.String("log-level", "info", "log level: debug, info, warn, or error")
	logFormat := flag.String("log-format", "text", "log format: text or json")
	flag.Parse()
	logger, loggerErr := logging.NewApplicationLogger(os.Stderr, "hearth-adapter-zwavejs", logging.LogOptions{
		Level: *logLevel, Format: *logFormat,
	})
	if loggerErr != nil {
		fmt.Fprintln(os.Stderr, loggerErr)
		return 1
	}
	processLogger := logger.With(slog.String("component", "process"))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	processLogger.InfoContext(
		ctx, "hearth-adapter-zwavejs starting", slog.String("event", "process.starting"),
	)
	config, configErr := zwavejs.LoadConfig(*configPath)
	if configErr != nil {
		processLogger.ErrorContext(
			ctx,
			"hearth-adapter-zwavejs configuration failed",
			slog.String("event", "process.failed"),
			slog.String("error_code", "config_invalid"),
			slog.String("stage", "load_config"),
		)
		return 1
	}
	processLogger.InfoContext(
		ctx,
		"hearth-adapter-zwavejs configuration loaded",
		slog.String("event", "process.config_loaded"),
	)
	if err := zwavejs.Run(ctx, config, logger); err != nil && !errors.Is(err, context.Canceled) {
		processLogger.ErrorContext(
			ctx,
			"hearth-adapter-zwavejs failed",
			slog.String("event", "process.failed"),
			slog.String("error_code", "run_failed"),
			slog.String("stage", "run"),
		)
		return 1
	}
	processLogger.InfoContext(
		ctx, "hearth-adapter-zwavejs stopped", slog.String("event", "process.stopped"),
	)
	return 0
}
