package main

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/urfave/cli/v3"

	"github.com/mholtzscher/hearth/internal/app/zwavejs"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
	"github.com/mholtzscher/hearth/internal/platform/logging"
)

const defaultConfigPath = "configs/zwavejs.yaml"

var errAdapterFailed = errors.New("hearth-adapter-zwavejs failed")

func newCommand(run func(context.Context, zwavejs.Config, *slog.Logger) error, stderr io.Writer) *cli.Command {
	return &cli.Command{Name: "hearth-adapter-zwavejs", Writer: stderr, ErrWriter: stderr, Flags: []cli.Flag{
		&cli.StringFlag{
			Name: "config", Value: defaultConfigPath,
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ZWAVEJS_CONFIG")),
		},
		&cli.StringFlag{
			Name:    "adapter-id",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ZWAVEJS_ADAPTER_ID")),
		},
		&cli.StringFlag{
			Name:    "nats-url",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ZWAVEJS_NATS_URL")),
		},
		&cli.StringFlag{
			Name:    "zwave-js-url",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ZWAVEJS_ZWAVE_JS_URL")),
		},
		&cli.StringFlag{
			Name: "log-level", Value: "info",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ZWAVEJS_LOG_LEVEL")),
		},
		&cli.StringFlag{
			Name: "log-format", Value: "text",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ZWAVEJS_LOG_FORMAT")),
		},
	}, Action: func(ctx context.Context, cmd *cli.Command) error {
		logger, err := logging.NewApplicationLogger(
			stderr,
			"hearth-adapter-zwavejs",
			logging.LogOptions{Level: cmd.String("log-level"), Format: cmd.String("log-format")},
		)
		if err != nil {
			return err
		}
		process := logger.With(slog.String("component", "process"))
		process.InfoContext(ctx, "hearth-adapter-zwavejs starting", slog.String("event", "process.starting"))
		config, err := resolvedConfig(cmd)
		if err != nil {
			process.ErrorContext(
				ctx,
				"hearth-adapter-zwavejs configuration failed",
				slog.String("event", "process.failed"),
				slog.String("error_code", "config_invalid"),
				slog.String("stage", "load_config"),
				slog.String("error", platformconfig.Reason(err)),
			)
			return errAdapterFailed
		}
		process.InfoContext(
			ctx,
			"hearth-adapter-zwavejs configuration loaded",
			slog.String("event", "process.config_loaded"),
		)
		if runErr := run(ctx, config, logger); runErr != nil && !errors.Is(runErr, context.Canceled) {
			process.ErrorContext(
				ctx,
				"hearth-adapter-zwavejs failed",
				slog.String("event", "process.failed"),
				slog.String("error_code", "run_failed"),
				slog.String("stage", "run"),
			)
			return errAdapterFailed
		}
		process.InfoContext(ctx, "hearth-adapter-zwavejs stopped", slog.String("event", "process.stopped"))
		return nil
	}}
}

func resolvedConfig(cmd *cli.Command) (zwavejs.Config, error) {
	config, err := platformconfig.LoadYAML[zwavejs.Config](cmd.String("config"), cmd.IsSet("config"))
	if err != nil {
		return zwavejs.Config{}, err
	}
	set := func(flag string, target *string) {
		if cmd.IsSet(flag) {
			*target = cmd.String(flag)
		}
	}
	set("adapter-id", &config.AdapterID)
	set("nats-url", &config.NATSURL)
	set("zwave-js-url", &config.ZWaveJS.URL)
	if validationErr := config.Validate(); validationErr != nil {
		return zwavejs.Config{}, platformconfig.Invalid("", validationErr)
	}
	return config, nil
}
