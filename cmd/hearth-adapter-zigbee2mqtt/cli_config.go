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

	"github.com/mholtzscher/hearth/internal/app/zigbee2mqtt"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
	"github.com/mholtzscher/hearth/internal/platform/logging"
)

const defaultConfigPath = "configs/zigbee2mqtt.yaml"

var errAdapterFailed = errors.New("hearth-adapter-zigbee2mqtt failed")

func newCommand(run func(context.Context, zigbee2mqtt.Config, *slog.Logger) error, stderr io.Writer) *cli.Command {
	str := func(name string) *cli.StringFlag {
		return &cli.StringFlag{
			Name: name, Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ZIGBEE2MQTT_" + envName(name))),
		}
	}
	return &cli.Command{Name: "hearth-adapter-zigbee2mqtt", Writer: stderr, ErrWriter: stderr, Flags: []cli.Flag{
		&cli.StringFlag{
			Name: "config", Value: defaultConfigPath,
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ZIGBEE2MQTT_CONFIG")),
		},
		str("adapter-id"), str("nats-url"), str("mqtt-url"), str("mqtt-base-topic"),
		&cli.StringFlag{
			Name: "log-level", Value: "info",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ZIGBEE2MQTT_LOG_LEVEL")),
		},
		&cli.StringFlag{
			Name: "log-format", Value: "text",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ZIGBEE2MQTT_LOG_FORMAT")),
		},
	}, Action: func(ctx context.Context, cmd *cli.Command) error {
		logger, err := logging.NewApplicationLogger(
			stderr,
			"hearth-adapter-zigbee2mqtt",
			logging.LogOptions{Level: cmd.String("log-level"), Format: cmd.String("log-format")},
		)
		if err != nil {
			return err
		}
		process := logger.With(slog.String("component", "process"))
		process.InfoContext(ctx, "hearth-adapter-zigbee2mqtt starting", slog.String("event", "process.starting"))
		config, err := resolvedConfig(cmd)
		if err != nil {
			process.ErrorContext(
				ctx,
				"hearth-adapter-zigbee2mqtt configuration failed",
				slog.String("event", "process.failed"),
				slog.String("error_code", "config_invalid"),
				slog.String("stage", "load_config"),
				slog.String("error", platformconfig.Reason(err)),
			)
			return errAdapterFailed
		}
		process.InfoContext(
			ctx,
			"hearth-adapter-zigbee2mqtt configuration loaded",
			slog.String("event", "process.config_loaded"),
		)
		if runErr := run(ctx, config, logger); runErr != nil && !errors.Is(runErr, context.Canceled) {
			process.ErrorContext(
				ctx,
				"hearth-adapter-zigbee2mqtt failed",
				slog.String("event", "process.failed"),
				slog.String("error_code", "run_failed"),
				slog.String("stage", "run"),
			)
			return errAdapterFailed
		}
		process.InfoContext(ctx, "hearth-adapter-zigbee2mqtt stopped", slog.String("event", "process.stopped"))
		return nil
	}}
}

func resolvedConfig(cmd *cli.Command) (zigbee2mqtt.Config, error) {
	path, explicit := cmd.String("config"), cmd.IsSet("config")
	c, err := readYAML(path, explicit)
	if err != nil {
		return c, err
	}
	set := func(flag string, target *string) {
		if cmd.IsSet(flag) {
			*target = cmd.String(flag)
		}
	}
	set("adapter-id", &c.AdapterID)
	set("nats-url", &c.NATSURL)
	set("mqtt-url", &c.MQTT.URL)
	set("mqtt-base-topic", &c.MQTT.BaseTopic)
	return zigbee2mqtt.ValidateConfig(c, "")
}

func readYAML(path string, explicit bool) (zigbee2mqtt.Config, error) {
	var c zigbee2mqtt.Config
	f, err := os.Open(path)
	if err != nil {
		if !explicit && errors.Is(err, os.ErrNotExist) {
			return c, nil
		}
		return c, errors.New("configuration file could not be read")
	}
	defer f.Close()
	d := yaml.NewDecoder(f)
	if decodeErr := d.Decode(&c); decodeErr != nil && !errors.Is(decodeErr, io.EOF) {
		return c, errors.New("configuration file contains invalid YAML")
	}
	var extra any
	if decodeErr := d.Decode(&extra); !errors.Is(decodeErr, io.EOF) {
		return c, errors.New("configuration file must contain a single YAML document")
	}
	return c, nil
}

func envName(flag string) string {
	return strings.ToUpper(strings.ReplaceAll(flag, "-", "_"))
}
