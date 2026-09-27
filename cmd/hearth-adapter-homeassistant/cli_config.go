package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/urfave/cli/v3"
	"gopkg.in/yaml.v3"

	"github.com/mholtzscher/hearth/internal/app/homeassistant"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
	"github.com/mholtzscher/hearth/internal/platform/logging"
)

const defaultConfigPath = "configs/homeassistant.yaml"

var errAdapterFailed = errors.New("hearth-adapter-homeassistant failed")

func newCommand(run func(context.Context, homeassistant.Config, *slog.Logger) error, stderr io.Writer) *cli.Command {
	str := func(name string) *cli.StringFlag {
		return &cli.StringFlag{
			Name: name, Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_HOMEASSISTANT_" + envName(name))),
		}
	}
	return &cli.Command{
		Name: "hearth-adapter-homeassistant", Writer: stderr, ErrWriter: stderr,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name: "config", Value: defaultConfigPath,
				Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_HOMEASSISTANT_CONFIG")),
			},
			str("adapter-id"), str("nats-url"), str("binding-key"), str("binding-device-external-id"),
			str("binding-device-name"), str("binding-entity-id"), str("binding-entity-name"),
			str("home-assistant-url"), str("home-assistant-token-file"),
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

func envName(flag string) string {
	return strings.ToUpper(strings.ReplaceAll(flag, "-", "_"))
}

func resolvedConfig(cmd *cli.Command) (homeassistant.Config, error) {
	path, explicit := cmd.String("config"), cmd.IsSet("config")
	config, err := readYAML(path, explicit)
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
	return homeassistant.ValidateConfig(config, "")
}

func readYAML(path string, explicit bool) (homeassistant.Config, error) {
	var config homeassistant.Config
	f, err := os.Open(path)
	if err != nil {
		if !explicit && errors.Is(err, os.ErrNotExist) {
			return config, nil
		}
		return config, errors.New("configuration file could not be read")
	}
	defer f.Close()
	d := yaml.NewDecoder(f)
	if decodeErr := d.Decode(&config); decodeErr != nil && !errors.Is(decodeErr, io.EOF) {
		return config, errors.New("configuration file contains invalid YAML")
	}
	var extra any
	if decodeErr := d.Decode(&extra); !errors.Is(decodeErr, io.EOF) {
		return config, errors.New("configuration file must contain a single YAML document")
	}
	return config, nil
}
