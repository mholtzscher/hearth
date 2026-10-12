package main

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/urfave/cli/v3"

	"github.com/mholtzscher/hearth/internal/app/discord"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
	"github.com/mholtzscher/hearth/internal/platform/logging"
)

var errAdapterFailed = errors.New("hearth-discord failed")

func newCommand(run func(context.Context, discord.Config, *slog.Logger) error, stderr io.Writer) *cli.Command {
	return &cli.Command{Name: "hearth-discord", Writer: stderr, ErrWriter: stderr, Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "config",
			Value:   "configs/discord.yaml",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_DISCORD_CONFIG")),
		},
		&cli.StringFlag{Name: "adapter-id", Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_DISCORD_ADAPTER_ID"))},
		&cli.StringFlag{Name: "nats-url", Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_DISCORD_NATS_URL"))},
		&cli.StringFlag{
			Name:    "log-level",
			Value:   "info",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_DISCORD_LOG_LEVEL")),
		},
		&cli.StringFlag{
			Name:    "log-format",
			Value:   "text",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_DISCORD_LOG_FORMAT")),
		},
	}, Action: func(ctx context.Context, cmd *cli.Command) error {
		logger, err := logging.NewApplicationLogger(
			stderr,
			"hearth-discord",
			logging.LogOptions{Level: cmd.String("log-level"), Format: cmd.String("log-format")},
		)
		if err != nil {
			return err
		}
		process := logger.With(slog.String("component", "process"))
		process.InfoContext(ctx, "hearth-discord starting", slog.String("event", "process.starting"))
		config, err := resolvedConfig(cmd)
		if err != nil {
			process.ErrorContext(
				ctx,
				"hearth-discord configuration failed",
				slog.String("event", "process.failed"),
				slog.String("error_code", "config_invalid"),
				slog.String("stage", "load_config"),
				slog.String("error", platformconfig.Reason(err)),
			)
			return errAdapterFailed
		}
		process.InfoContext(ctx, "hearth-discord configuration loaded", slog.String("event", "process.config_loaded"))
		if err = run(ctx, config, logger); err != nil && !errors.Is(err, context.Canceled) {
			process.ErrorContext(
				ctx,
				"hearth-discord failed",
				slog.String("event", "process.failed"),
				slog.String("error_code", "run_failed"),
				slog.String("stage", "run"),
			)
			return errAdapterFailed
		}
		process.InfoContext(ctx, "hearth-discord stopped", slog.String("event", "process.stopped"))
		return nil
	}}
}

func resolvedConfig(cmd *cli.Command) (discord.Config, error) {
	config, err := platformconfig.LoadYAML[discord.Config](cmd.String("config"), cmd.IsSet("config"))
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
		return discord.Config{}, platformconfig.Invalid("", err)
	}
	return config, nil
}
