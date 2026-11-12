package main

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/urfave/cli/v3"

	"github.com/mholtzscher/hearth/internal/app/levoit"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
	"github.com/mholtzscher/hearth/internal/platform/logging"
)

var errAdapterFailed = errors.New("hearth-adapter-levoit failed")

func newCommand(run func(context.Context, levoit.Config, *slog.Logger) error, stderr io.Writer) *cli.Command {
	return &cli.Command{Name: "hearth-adapter-levoit", Writer: stderr, ErrWriter: stderr, Flags: []cli.Flag{
		&cli.StringFlag{Name: "config", Value: "configs/levoit.yaml",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_LEVOIT_CONFIG"))},
		&cli.StringFlag{
			Name:    "adapter-id",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_LEVOIT_ADAPTER_ID")),
		},
		&cli.StringFlag{
			Name:    "nats-url",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_LEVOIT_NATS_URL")),
		},
		&cli.StringFlag{
			Name:    "credentials-file",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_LEVOIT_CREDENTIALS_FILE")),
		},
		&cli.DurationFlag{
			Name:    "poll-interval",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_LEVOIT_POLL_INTERVAL")),
		},
		&cli.StringFlag{
			Name:    "time-zone",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_LEVOIT_TIME_ZONE")),
		},
		&cli.StringFlag{
			Name:    "log-level",
			Value:   "info",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_LEVOIT_LOG_LEVEL")),
		},
		&cli.StringFlag{
			Name:    "log-format",
			Value:   "text",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_LEVOIT_LOG_FORMAT")),
		},
	}, Action: func(ctx context.Context, cmd *cli.Command) error {
		logger, err := logging.NewApplicationLogger(stderr, "hearth-adapter-levoit",
			logging.LogOptions{Level: cmd.String("log-level"), Format: cmd.String("log-format")})
		if err != nil {
			return err
		}
		process := logger.With(slog.String("component", "process"))
		process.InfoContext(ctx, "Levoit Adapter starting", slog.String("event", "process.starting"))
		config, err := resolvedConfig(cmd)
		if err != nil {
			process.ErrorContext(ctx, "configuration failed", slog.String("event", "process.failed"),
				slog.String("stage", "load_config"), slog.String("error_code", "config_invalid"))
			return errAdapterFailed
		}
		if err = run(ctx, config, logger); err != nil && !errors.Is(err, context.Canceled) {
			process.ErrorContext(ctx, "Levoit Adapter failed", slog.String("event", "process.failed"),
				slog.String("stage", "run"), slog.String("error_code", "run_failed"))
			return errAdapterFailed
		}
		process.InfoContext(ctx, "Levoit Adapter stopped", slog.String("event", "process.stopped"))
		return nil
	}}
}

func resolvedConfig(cmd *cli.Command) (levoit.Config, error) {
	config, err := platformconfig.LoadYAML[levoit.Config](cmd.String("config"), cmd.IsSet("config"))
	if err != nil {
		return config, err
	}
	if cmd.IsSet("adapter-id") {
		config.AdapterID = cmd.String("adapter-id")
	}
	if cmd.IsSet("nats-url") {
		config.NATSURL = cmd.String("nats-url")
	}
	if cmd.IsSet("credentials-file") {
		config.CredentialsFile = cmd.String("credentials-file")
	}
	if cmd.IsSet("poll-interval") {
		value := cmd.Duration("poll-interval")
		config.PollInterval = &value
	}
	if cmd.IsSet("time-zone") {
		config.TimeZone = cmd.String("time-zone")
	}
	if config.NATSURL == "" && !cmd.IsSet("nats-url") {
		config.NATSURL = "nats://127.0.0.1:4222"
	}
	if err = config.Validate(); err != nil {
		return levoit.Config{}, platformconfig.Invalid("", err)
	}
	return config, nil
}
