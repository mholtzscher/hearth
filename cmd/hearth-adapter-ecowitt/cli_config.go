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

	"github.com/mholtzscher/hearth/internal/app/ecowitt"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
	"github.com/mholtzscher/hearth/internal/platform/logging"
)

const defaultConfigPath = "configs/ecowitt.yaml"

var errAdapterFailed = errors.New("hearth-adapter-ecowitt failed")

func newCommand(run func(context.Context, ecowitt.Config, *slog.Logger) error, stderr io.Writer) *cli.Command {
	str := func(name string) *cli.StringFlag {
		return &cli.StringFlag{
			Name: name, Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ECOWITT_" + envName(name))),
		}
	}
	return &cli.Command{Name: "hearth-adapter-ecowitt", Writer: stderr, ErrWriter: stderr, Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "config",
			Value:   defaultConfigPath,
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ECOWITT_CONFIG")),
		},
		str("adapter-id"),
		str("nats-url"),
		str("mqtt-url"),
		str("mqtt-topic"),
		str("station-gateway-name"),
		str("station-outdoor-array-name"),
		str("station-passkey-file"),
		&cli.IntFlag{
			Name:    "station-upload-interval-seconds",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ECOWITT_STATION_UPLOAD_INTERVAL_SECONDS")),
		},
		&cli.StringFlag{
			Name: "log-level", Value: "info",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ECOWITT_LOG_LEVEL")),
		},
		&cli.StringFlag{
			Name: "log-format", Value: "text",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ECOWITT_LOG_FORMAT")),
		},
	}, Action: func(ctx context.Context, cmd *cli.Command) error {
		logger, err := logging.NewApplicationLogger(
			stderr,
			"hearth-adapter-ecowitt",
			logging.LogOptions{Level: cmd.String("log-level"), Format: cmd.String("log-format")},
		)
		if err != nil {
			return err
		}
		process := logger.With(slog.String("component", "process"))
		process.InfoContext(ctx, "hearth-adapter-ecowitt starting", slog.String("event", "process.starting"))
		c, err := resolvedConfig(cmd)
		if err != nil {
			process.ErrorContext(
				ctx,
				"hearth-adapter-ecowitt configuration failed",
				slog.String("event", "process.failed"),
				slog.String("error_code", "config_invalid"),
				slog.String("stage", "load_config"),
				slog.String("error", platformconfig.Reason(err)),
			)
			return errAdapterFailed
		}
		process.InfoContext(
			ctx,
			"hearth-adapter-ecowitt configuration loaded",
			slog.String("event", "process.config_loaded"),
		)
		if runErr := run(ctx, c, logger); runErr != nil && !errors.Is(runErr, context.Canceled) {
			process.ErrorContext(
				ctx,
				"hearth-adapter-ecowitt failed",
				slog.String("event", "process.failed"),
				slog.String("error_code", "run_failed"),
				slog.String("stage", "run"),
			)
			return errAdapterFailed
		}
		process.InfoContext(ctx, "hearth-adapter-ecowitt stopped", slog.String("event", "process.stopped"))
		return nil
	}}
}
func resolvedConfig(cmd *cli.Command) (ecowitt.Config, error) {
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
	set("mqtt-topic", &c.MQTT.Topic)
	set("station-gateway-name", &c.Station.GatewayName)
	set("station-outdoor-array-name", &c.Station.OutdoorArrayName)
	set("station-passkey-file", &c.Station.PasskeyFile)
	if cmd.IsSet("station-upload-interval-seconds") {
		c.Station.UploadIntervalSeconds = cmd.Int("station-upload-interval-seconds")
	}
	return ecowitt.ValidateConfig(c, "")
}
func readYAML(path string, explicit bool) (ecowitt.Config, error) {
	var c ecowitt.Config
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
