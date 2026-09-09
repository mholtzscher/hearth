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
	processLogger := logger.With(slog.String("component", "process"))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	processLogger.InfoContext(
		ctx, "hearth-adapter-zigbee2mqtt starting", slog.String("event", "process.starting"),
	)
	config, configErr := zigbee2mqtt.LoadConfig(*configPath)
	if configErr != nil {
		processLogger.ErrorContext(
			ctx,
			"hearth-adapter-zigbee2mqtt configuration failed",
			slog.String("event", "process.failed"),
			slog.String("error_code", "config_invalid"),
			slog.String("stage", "load_config"),
		)
		return 1
	}
	processLogger.InfoContext(
		ctx,
		"hearth-adapter-zigbee2mqtt configuration loaded",
		slog.String("event", "process.config_loaded"),
	)
	if err := zigbee2mqtt.Run(ctx, config, logger); err != nil && !errors.Is(err, context.Canceled) {
		reportRunFailure(ctx, processLogger, err)
		return 1
	}
	processLogger.InfoContext(
		ctx, "hearth-adapter-zigbee2mqtt stopped", slog.String("event", "process.stopped"),
	)
	return 0
}

// reportRunFailure emits the process.failed record for a run failure with
// the failed startup stage and its error code. A catalog load failure
// reports stage load_profile_catalog with error code profile_catalog_invalid;
// any other run failure reports its stage with error code run_failed. The
// record never contains configuration values or connection details.
func reportRunFailure(ctx context.Context, logger *slog.Logger, err error) {
	logger.ErrorContext(
		ctx,
		"hearth-adapter-zigbee2mqtt failed",
		slog.String("event", "process.failed"),
		slog.String("error_code", zigbee2mqtt.ErrorCode(err)),
		slog.String("stage", zigbee2mqtt.ErrorStage(err)),
	)
}
