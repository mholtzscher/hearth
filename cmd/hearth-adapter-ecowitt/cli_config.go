package main

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/urfave/cli/v3"

	"github.com/mholtzscher/hearth/internal/app/ecowitt"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
	"github.com/mholtzscher/hearth/internal/platform/logging"
)

const defaultConfigPath = "configs/ecowitt.yaml"

var errAdapterFailed = errors.New("hearth-adapter-ecowitt failed")

func newCommand(run func(context.Context, ecowitt.Config, *slog.Logger) error, stderr io.Writer) *cli.Command {
	return &cli.Command{Name: "hearth-adapter-ecowitt", Writer: stderr, ErrWriter: stderr, Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "config",
			Value:   defaultConfigPath,
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ECOWITT_CONFIG")),
		},
		&cli.StringFlag{
			Name:    "adapter-id",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ECOWITT_ADAPTER_ID")),
		},
		&cli.StringFlag{
			Name:    "nats-url",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ECOWITT_NATS_URL")),
		},
		&cli.StringFlag{
			Name:    "mqtt-url",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ECOWITT_MQTT_URL")),
		},
		&cli.StringFlag{
			Name:    "mqtt-topic",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ECOWITT_MQTT_TOPIC")),
		},
		&cli.StringFlag{
			Name:    "station-gateway-name",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ECOWITT_STATION_GATEWAY_NAME")),
		},
		&cli.StringFlag{
			Name:    "station-outdoor-array-name",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ECOWITT_STATION_OUTDOOR_ARRAY_NAME")),
		},
		&cli.StringFlag{
			Name:    "station-passkey-file",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_ECOWITT_STATION_PASSKEY_FILE")),
		},
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
	c, err := platformconfig.LoadYAML[ecowitt.Config](path, explicit)
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
	c = ecowitt.NormalizeConfig(c)
	if validationErr := c.Validate(); validationErr != nil {
		return ecowitt.Config{}, platformconfig.Invalid("", validationErr)
	}
	return c, nil
}
