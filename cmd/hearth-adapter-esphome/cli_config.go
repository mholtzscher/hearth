package main

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/urfave/cli/v3"

	"github.com/mholtzscher/hearth/internal/app/esphome"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
	"github.com/mholtzscher/hearth/internal/platform/logging"
)

const defaultConfigPath = "configs/esphome.yaml"

var errAdapterFailed = errors.New("hearth-adapter-esphome failed")

type fileConfig struct {
	esphome.Config `yaml:",inline"`

	LogLevel  string `yaml:"log_level"`
	LogFormat string `yaml:"log_format"`
}

func newCommand(run func(context.Context, esphome.Config, *slog.Logger) error, stderr io.Writer) *cli.Command {
	return &cli.Command{Name: "hearth-adapter-esphome", Writer: stderr, ErrWriter: stderr, Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "config",
			Value:   defaultConfigPath,
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ESPHOME_CONFIG")),
		},
		&cli.StringFlag{
			Name:    "adapter-id",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ESPHOME_ADAPTER_ID")),
		},
		&cli.StringFlag{
			Name:    "nats-url",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ESPHOME_NATS_URL")),
		},
		&cli.StringFlag{
			Name:    "log-level",
			Value:   "info",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ESPHOME_LOG_LEVEL")),
		},
		&cli.StringFlag{
			Name:    "log-format",
			Value:   "text",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ESPHOME_LOG_FORMAT")),
		},
	}, Action: func(ctx context.Context, cmd *cli.Command) error {
		// Validate explicitly supplied log settings before loading configuration, as sibling processes do.
		logger, err := logging.NewApplicationLogger(stderr, "hearth-adapter-esphome",
			logging.LogOptions{Level: cmd.String("log-level"), Format: cmd.String("log-format")})
		if err != nil {
			return err
		}
		config, loadErr := resolvedConfig(cmd)
		if loadErr == nil {
			logger, err = logging.NewApplicationLogger(stderr, "hearth-adapter-esphome",
				logging.LogOptions{Level: config.LogLevel, Format: config.LogFormat})
			if err != nil {
				return err
			}
		}
		process := logger.With(slog.String("component", "process"))
		process.InfoContext(ctx, "hearth-adapter-esphome starting", slog.String("event", "process.starting"))
		if loadErr != nil {
			process.ErrorContext(
				ctx,
				"hearth-adapter-esphome configuration failed",
				slog.String("event", "process.failed"),
				slog.String("error_code", "config_invalid"),
				slog.String("stage", "load_config"),
				slog.String("error", platformconfig.Reason(loadErr)),
			)
			return errAdapterFailed
		}
		process.InfoContext(
			ctx,
			"hearth-adapter-esphome configuration loaded",
			slog.String("event", "process.config_loaded"),
		)
		if runErr := run(ctx, config.Config, logger); runErr != nil && !errors.Is(runErr, context.Canceled) {
			if keyErr, ok := errors.AsType[*esphome.KeyFileError](runErr); ok {
				process.ErrorContext(
					ctx,
					"hearth-adapter-esphome configuration failed",
					slog.String("event", "process.failed"),
					slog.String(
						"error_code",
						"config_invalid",
					),
					slog.String("stage", "run"),
					slog.String("error", keyErr.Error()),
				)
			} else {
				process.ErrorContext(ctx, "hearth-adapter-esphome failed", slog.String("event", "process.failed"),
					slog.String("error_code", "run_failed"), slog.String("stage", "run"))
			}
			return errAdapterFailed
		}
		process.InfoContext(ctx, "hearth-adapter-esphome stopped", slog.String("event", "process.stopped"))
		return nil
	}}
}

func resolvedConfig(cmd *cli.Command) (fileConfig, error) {
	config, err := platformconfig.LoadYAML[fileConfig](cmd.String("config"), cmd.IsSet("config"))
	if err != nil {
		return fileConfig{}, err
	}
	if config.AdapterID == "" {
		config.AdapterID = "esphome"
	}
	if config.NATSURL == "" {
		config.NATSURL = "nats://127.0.0.1:4222"
	}
	if config.LogLevel == "" {
		config.LogLevel = "info"
	}
	if config.LogFormat == "" {
		config.LogFormat = "text"
	}
	set := func(flag string, target *string) {
		if cmd.IsSet(flag) {
			*target = cmd.String(flag)
		}
	}
	set("adapter-id", &config.AdapterID)
	set("nats-url", &config.NATSURL)
	set("log-level", &config.LogLevel)
	set("log-format", &config.LogFormat)
	if err = config.Validate(); err != nil {
		return fileConfig{}, platformconfig.Invalid("", err)
	}
	return config, nil
}
