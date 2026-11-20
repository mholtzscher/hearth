package main

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/urfave/cli/v3"

	"github.com/mholtzscher/hearth/internal/app/homekit"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
	"github.com/mholtzscher/hearth/internal/platform/logging"
)

var errAdapterFailed = errors.New("hearth-adapter-homekit failed")

func newCommand(run func(context.Context, homekit.Config, *slog.Logger) error, stderr io.Writer) *cli.Command {
	const software = homekit.SoftwareName
	return &cli.Command{
		Name: software, Writer: stderr, ErrWriter: stderr, Flags: configFlags(),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			logger, err := logging.NewApplicationLogger(stderr, software,
				logging.LogOptions{Level: cmd.String("log-level"), Format: cmd.String("log-format")})
			if err != nil {
				return err
			}
			process := logger.With(slog.String("component", "process"))
			process.InfoContext(ctx, "HomeKit Adapter starting", slog.String("event", "process.starting"))
			config, err := resolvedConfig(cmd)
			if err != nil {
				process.ErrorContext(ctx, "HomeKit Adapter configuration failed",
					slog.String("event", "process.failed"), slog.String("stage", "load_config"),
					slog.String("error_code", "config_invalid"), slog.String("error", platformconfig.Reason(err)))
				return errAdapterFailed
			}
			process.InfoContext(
				ctx,
				"HomeKit Adapter configuration loaded",
				slog.String("event", "process.config_loaded"),
			)
			if err = run(ctx, config, logger); err != nil {
				process.ErrorContext(ctx, "HomeKit Adapter failed",
					slog.String("event", "process.failed"), slog.String("stage", "run"),
					slog.String("error_code", "run_failed"))
				return errAdapterFailed
			}
			process.InfoContext(ctx, "HomeKit Adapter stopped", slog.String("event", "process.stopped"))
			return nil
		},
	}
}

func configFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "config", Value: "configs/homekit.yaml", Sources: env("CONFIG")},
		&cli.StringFlag{Name: "adapter-id", Sources: env("ADAPTER_ID")},
		&cli.StringFlag{Name: "nats-url", Sources: env("NATS_URL")},
		&cli.StringFlag{Name: "core-url", Sources: env("CORE_URL")},
		&cli.StringFlag{Name: "data-dir", Sources: env("DATA_DIR")},
		&cli.StringFlag{Name: "bridge-name", Sources: env("BRIDGE_NAME")},
		&cli.IntFlag{Name: "listen-port", Sources: env("LISTEN_PORT")},
		&cli.StringSliceFlag{Name: "interfaces", Sources: env("INTERFACES")},
		&cli.DurationFlag{Name: "resync-interval", Sources: env("RESYNC_INTERVAL")},
		&cli.StringFlag{Name: "temperature-display-unit", Sources: env("TEMPERATURE_DISPLAY_UNIT")},
		&cli.StringFlag{Name: "log-level", Value: "info", Sources: env("LOG_LEVEL")},
		&cli.StringFlag{Name: "log-format", Value: "text", Sources: env("LOG_FORMAT")},
	}
}

func env(name string) cli.ValueSourceChain {
	return cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_HOMEKIT_" + name))
}

func resolvedConfig(cmd *cli.Command) (homekit.Config, error) {
	config, err := platformconfig.LoadYAML[homekit.Config](cmd.String("config"), cmd.IsSet("config"))
	if err != nil {
		return homekit.Config{}, err
	}
	applyConfigFlags(cmd, &config)
	config, err = config.Resolve()
	if err != nil {
		return homekit.Config{}, platformconfig.Invalid("", err)
	}
	return config, nil
}

func applyConfigFlags(cmd *cli.Command, config *homekit.Config) {
	if cmd.IsSet("adapter-id") {
		config.AdapterID = cmd.String("adapter-id")
	}
	if cmd.IsSet("nats-url") {
		config.NATSURL = new(cmd.String("nats-url"))
	}
	if cmd.IsSet("core-url") {
		config.CoreURL = cmd.String("core-url")
	}
	if cmd.IsSet("data-dir") {
		config.DataDir = cmd.String("data-dir")
	}
	if cmd.IsSet("bridge-name") {
		config.BridgeName = new(cmd.String("bridge-name"))
	}
	if cmd.IsSet("listen-port") {
		config.ListenPort = new(cmd.Int("listen-port"))
	}
	if cmd.IsSet("interfaces") {
		config.Interfaces = cmd.StringSlice("interfaces")
	}
	if cmd.IsSet("resync-interval") {
		config.ResyncInterval = new(cmd.Duration("resync-interval"))
	}
	if cmd.IsSet("temperature-display-unit") {
		config.TemperatureDisplayUnit = new(cmd.String("temperature-display-unit"))
	}
}
