package main

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/urfave/cli/v3"

	"github.com/mholtzscher/hearth/internal/app/homeassistant"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
	"github.com/mholtzscher/hearth/internal/platform/logging"
)

const defaultConfigPath = "configs/homeassistant.yaml"

var errAdapterFailed = errors.New("hearth-adapter-homeassistant failed")

func newCommand(run func(context.Context, homeassistant.Config, *slog.Logger) error, stderr io.Writer) *cli.Command {
	return &cli.Command{
		Name: "hearth-adapter-homeassistant", Writer: stderr, ErrWriter: stderr,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name: "config", Value: defaultConfigPath,
				Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_HOMEASSISTANT_CONFIG")),
			},
			&cli.StringFlag{
				Name:    "adapter-id",
				Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_HOMEASSISTANT_ADAPTER_ID")),
			},
			&cli.StringFlag{
				Name:    "nats-url",
				Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_HOMEASSISTANT_NATS_URL")),
			},
			&cli.StringFlag{
				Name:    "binding-key",
				Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_HOMEASSISTANT_BINDING_KEY")),
			},
			&cli.StringFlag{
				Name:    "binding-device-external-id",
				Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_HOMEASSISTANT_BINDING_DEVICE_EXTERNAL_ID")),
			},
			&cli.StringFlag{
				Name:    "binding-device-name",
				Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_HOMEASSISTANT_BINDING_DEVICE_NAME")),
			},
			&cli.StringFlag{
				Name:    "binding-entity-id",
				Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_HOMEASSISTANT_BINDING_ENTITY_ID")),
			},
			&cli.StringFlag{
				Name:    "binding-entity-name",
				Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_HOMEASSISTANT_BINDING_ENTITY_NAME")),
			},
			&cli.StringFlag{
				Name:    "home-assistant-url",
				Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_HOMEASSISTANT_HOME_ASSISTANT_URL")),
			},
			&cli.StringFlag{
				Name:    "home-assistant-token-file",
				Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_HOMEASSISTANT_HOME_ASSISTANT_TOKEN_FILE")),
			},
			&cli.StringFlag{
				Name: "log-level", Value: "info",
				Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_HOMEASSISTANT_LOG_LEVEL")),
			},
			&cli.StringFlag{
				Name: "log-format", Value: "text",
				Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_HOMEASSISTANT_LOG_FORMAT")),
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			logger, err := logging.NewApplicationLogger(
				stderr,
				"hearth-adapter-homeassistant",
				logging.LogOptions{Level: cmd.String("log-level"), Format: cmd.String("log-format")},
			)
			if err != nil {
				return err
			}
			process := logger.With(slog.String("component", "process"))
			process.InfoContext(ctx, "hearth-adapter-homeassistant starting", slog.String("event", "process.starting"))
			config, err := resolvedConfig(cmd)
			if err != nil {
				process.ErrorContext(
					ctx,
					"hearth-adapter-homeassistant configuration failed",
					slog.String("event", "process.failed"),
					slog.String("error_code", "config_invalid"),
					slog.String("stage", "load_config"),
					slog.String("error", platformconfig.Reason(err)),
				)
				return errAdapterFailed
			}
			process.InfoContext(
				ctx,
				"hearth-adapter-homeassistant configuration loaded",
				slog.String("event", "process.config_loaded"),
			)
			if runErr := run(ctx, config, logger); runErr != nil && !errors.Is(runErr, context.Canceled) {
				process.ErrorContext(
					ctx,
					"hearth-adapter-homeassistant failed",
					slog.String("event", "process.failed"),
					slog.String("error_code", homeassistant.ErrorCode(runErr)),
					slog.String("stage", "run"),
				)
				return errAdapterFailed
			}
			process.InfoContext(ctx, "hearth-adapter-homeassistant stopped", slog.String("event", "process.stopped"))
			return nil
		},
	}
}

func resolvedConfig(cmd *cli.Command) (homeassistant.Config, error) {
	path, explicit := cmd.String("config"), cmd.IsSet("config")
	config, err := platformconfig.LoadYAML[homeassistant.Config](path, explicit)
	if err != nil {
		return homeassistant.Config{}, err
	}
	set := func(flag string, target *string) {
		if cmd.IsSet(flag) {
			*target = cmd.String(flag)
		}
	}
	set("adapter-id", &config.AdapterID)
	set("nats-url", &config.NATSURL)
	set("binding-key", &config.Binding.Key)
	set("binding-device-name", &config.Binding.DeviceName)
	set("binding-entity-id", &config.Binding.EntityExternalID)
	set("binding-entity-name", &config.Binding.EntityName)
	set("home-assistant-url", &config.Upstream.URL)
	set("home-assistant-token-file", &config.Upstream.TokenFile)
	if cmd.IsSet("binding-device-external-id") {
		v := cmd.String("binding-device-external-id")
		config.Binding.DeviceExternalID = &v
	}
	if validationErr := config.Validate(); validationErr != nil {
		return homeassistant.Config{}, platformconfig.Invalid("", validationErr)
	}
	return config, nil
}
