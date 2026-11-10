package main

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/urfave/cli/v3"

	"github.com/mholtzscher/hearth/internal/app/pirateweather"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
	"github.com/mholtzscher/hearth/internal/platform/logging"
)

var errAdapterFailed = errors.New("hearth-adapter-pirateweather failed")

func newCommand(run func(context.Context, pirateweather.Config, *slog.Logger) error, stderr io.Writer) *cli.Command {
	return &cli.Command{Name: "hearth-adapter-pirateweather", Writer: stderr, ErrWriter: stderr, Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "config",
			Value:   "configs/pirateweather.yaml",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_PIRATEWEATHER_CONFIG")),
		},
		&cli.StringFlag{
			Name:    "adapter-id",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_PIRATEWEATHER_ADAPTER_ID")),
		},
		&cli.StringFlag{
			Name:    "nats-url",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_PIRATEWEATHER_NATS_URL")),
		},
		&cli.StringFlag{
			Name:    "log-level",
			Value:   "info",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_PIRATEWEATHER_LOG_LEVEL")),
		},
		&cli.StringFlag{
			Name:    "log-format",
			Value:   "text",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_PIRATEWEATHER_LOG_FORMAT")),
		},
	}, Action: func(ctx context.Context, cmd *cli.Command) error {
		logger, err := logging.NewApplicationLogger(stderr, "hearth-adapter-pirateweather",
			logging.LogOptions{Level: cmd.String("log-level"), Format: cmd.String("log-format")})
		if err != nil {
			return err
		}
		process := logger.With(slog.String("component", "process"))
		process.InfoContext(ctx, "Pirate Weather Adapter starting", slog.String("event", "process.starting"))
		config, err := resolvedConfig(cmd)
		if err != nil {
			process.ErrorContext(ctx, "configuration failed", slog.String("event", "process.failed"),
				slog.String("stage", "load_config"), slog.String("error_code", "config_invalid"))
			return errAdapterFailed
		}
		process.InfoContext(ctx, "configuration loaded", slog.String("event", "process.config_loaded"))
		if err = run(ctx, config, logger); err != nil && !errors.Is(err, context.Canceled) {
			process.ErrorContext(ctx, "Pirate Weather Adapter failed", slog.String("event", "process.failed"),
				slog.String("stage", "run"), slog.String("error_code", "run_failed"))
			return errAdapterFailed
		}
		process.InfoContext(ctx, "Pirate Weather Adapter stopped", slog.String("event", "process.stopped"))
		return nil
	}}
}

func resolvedConfig(cmd *cli.Command) (pirateweather.Config, error) {
	config, err := platformconfig.LoadYAML[pirateweather.Config](cmd.String("config"), cmd.IsSet("config"))
	if err != nil {
		return config, err
	}
	if cmd.IsSet("adapter-id") {
		config.AdapterID = cmd.String("adapter-id")
	}
	if cmd.IsSet("nats-url") {
		config.NATSURL = cmd.String("nats-url")
	}
	if config.NATSURL == "" && !cmd.IsSet("nats-url") {
		config.NATSURL = "nats://127.0.0.1:4222"
	}
	if err = config.Validate(); err != nil {
		return pirateweather.Config{}, platformconfig.Invalid("", err)
	}
	return config, nil
}
