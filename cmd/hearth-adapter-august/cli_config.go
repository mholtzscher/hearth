package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/urfave/cli/v3"

	"github.com/mholtzscher/hearth/internal/app/august"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
	"github.com/mholtzscher/hearth/internal/platform/logging"
)

var errAdapterFailed = errors.New("hearth-adapter-august failed")

type runFunc func(context.Context, august.Config, *slog.Logger) error
type authorizeFunc func(context.Context, august.Config, func() (string, error), *slog.Logger) error

func flags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "config",
			Value:   "configs/august.yaml",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_AUGUST_CONFIG")),
		},
		&cli.StringFlag{
			Name:    "adapter-id",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_AUGUST_ADAPTER_ID")),
		},
		&cli.StringFlag{
			Name:    "nats-url",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_AUGUST_NATS_URL")),
		},
		&cli.StringFlag{
			Name:    "credentials-file",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_AUGUST_CREDENTIALS_FILE")),
		},
		&cli.StringFlag{
			Name:    "installation-id",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_AUGUST_INSTALLATION_ID")),
		},
		&cli.DurationFlag{
			Name:    "poll-interval",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_AUGUST_POLL_INTERVAL")),
		},
		&cli.DurationFlag{
			Name:    "inventory-interval",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_AUGUST_INVENTORY_INTERVAL")),
		},
		&cli.StringFlag{
			Name:    "log-level",
			Value:   "info",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_AUGUST_LOG_LEVEL")),
		},
		&cli.StringFlag{
			Name:    "log-format",
			Value:   "text",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_AUGUST_LOG_FORMAT")),
		},
	}
}

func newCommand(run runFunc, authorize authorizeFunc, stdin io.Reader, stderr io.Writer) *cli.Command {
	action := func(provision bool) cli.ActionFunc {
		return func(ctx context.Context, cmd *cli.Command) error {
			logger, err := logging.NewApplicationLogger(
				stderr,
				"hearth-adapter-august",
				logging.LogOptions{Level: cmd.String("log-level"), Format: cmd.String("log-format")},
			)
			if err != nil {
				return err
			}
			process := logger.With(slog.String("component", "process"))
			process.InfoContext(ctx, "August Adapter starting", slog.String("event", "process.starting"))
			config, err := resolvedConfig(cmd)
			if err != nil {
				process.ErrorContext(
					ctx,
					"configuration failed",
					slog.String("event", "process.failed"),
					slog.String("stage", "load_config"),
					slog.String("error_code", "config_invalid"),
				)
				return errAdapterFailed
			}
			if provision {
				err = authorize(ctx, config, func() (string, error) {
					_, _ = fmt.Fprint(stderr, "Enter the six-digit email verification code: ")
					return readCode(stdin)
				}, logger)
			} else {
				err = run(ctx, config, logger)
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				process.ErrorContext(
					ctx,
					"August Adapter failed",
					slog.String("event", "process.failed"),
					slog.String("stage", "run"),
					slog.String("error_code", "run_failed"),
				)
				return errAdapterFailed
			}
			process.InfoContext(ctx, "August Adapter stopped", slog.String("event", "process.stopped"))
			return nil
		}
	}
	return &cli.Command{
		Name:      "hearth-adapter-august",
		Writer:    stderr,
		ErrWriter: stderr,
		Flags:     flags(),
		Action:    action(false),
		Commands:  []*cli.Command{{Name: "authorize", Flags: flags(), Action: action(true)}},
	}
}

// Read at most seven input bytes plus a newline, without a background reader.
func readCode(input io.Reader) (string, error) {
	var data [8]byte
	for i := range data {
		n, err := io.ReadFull(input, data[i:i+1])
		if n == 1 && data[i] == '\n' {
			return string(data[:i]), nil
		}
		if err != nil {
			if errors.Is(err, io.EOF) && i > 0 {
				return string(data[:i]), nil
			}
			return "", errors.New("verification code input could not be read")
		}
	}
	return "", errors.New("verification code input is too long")
}

func resolvedConfig(cmd *cli.Command) (august.Config, error) {
	config, err := platformconfig.LoadYAML[august.Config](cmd.String("config"), cmd.IsSet("config"))
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
	if cmd.IsSet("installation-id") {
		config.InstallationID = cmd.String("installation-id")
	}
	if cmd.IsSet("poll-interval") {
		value := cmd.Duration("poll-interval")
		config.PollInterval = &value
	}
	if cmd.IsSet("inventory-interval") {
		value := cmd.Duration("inventory-interval")
		config.InventoryInterval = &value
	}
	if config.NATSURL == "" && !cmd.IsSet("nats-url") {
		config.NATSURL = "nats://127.0.0.1:4222"
	}
	if err = config.Validate(); err != nil {
		return august.Config{}, platformconfig.Invalid("", err)
	}
	return config, nil
}
