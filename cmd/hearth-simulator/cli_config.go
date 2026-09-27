package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/urfave/cli/v3"
	"gopkg.in/yaml.v3"

	"github.com/mholtzscher/hearth/internal/app/simulator"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
	"github.com/mholtzscher/hearth/internal/platform/logging"
)

const defaultSimulatorConfigPath = "configs/simulator.yaml"

var errSimulatorFailed = errors.New("hearth-simulator failed")

func newSimulatorCommand(
	run func(context.Context, simulator.Config, *slog.Logger) error,
	stdout, stderr io.Writer,
) *cli.Command {
	flags := []cli.Flag{
		&cli.StringFlag{Name: "config", Value: defaultSimulatorConfigPath,
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_SIMULATOR_CONFIG"))},
		simulatorEnvFlag("adapter-id"),
		simulatorEnvFlag("nats-url"),
		simulatorEnvFlag("control-addr"),
		&cli.BoolFlag{Name: "validate-config"},
		&cli.StringFlag{Name: "log-level", Value: "info",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_SIMULATOR_LOG_LEVEL"))},
		&cli.StringFlag{Name: "log-format", Value: "text",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_SIMULATOR_LOG_FORMAT"))},
	}
	return &cli.Command{
		Name: "hearth-simulator", Usage: "run the Hearth simulator", Flags: flags,
		Writer: stdout, ErrWriter: stderr,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			var logger *slog.Logger
			if !cmd.Bool("validate-config") {
				var err error
				logger, err = logging.NewApplicationLogger(stderr, "hearth-simulator", logging.LogOptions{
					Level: cmd.String("log-level"), Format: cmd.String("log-format"),
				})
				if err != nil {
					return err
				}
				logger.With(slog.String("component", "process")).InfoContext(
					ctx, "hearth-simulator starting", slog.String("event", "process.starting"),
				)
			}
			config, err := resolvedSimulatorConfig(cmd)
			if err != nil {
				if cmd.Bool("validate-config") {
					fmt.Fprintln(stderr, platformconfig.Reason(err))
				} else if logger != nil {
					logger.With(slog.String("component", "process")).ErrorContext(ctx,
						"hearth-simulator configuration failed", slog.String("event", "process.failed"),
						slog.String("error_code", "config_invalid"), slog.String("stage", "load_config"),
						slog.String("error", platformconfig.Reason(err)))
				}
				return errSimulatorFailed
			}
			if cmd.Bool("validate-config") {
				fmt.Fprintln(stdout, "configuration valid:", cmd.String("config"))
				return nil
			}
			process := logger.With(slog.String("component", "process"))
			process.InfoContext(
				ctx, "hearth-simulator configuration loaded", slog.String("event", "process.config_loaded"),
			)
			if runErr := run(ctx, config, logger); runErr != nil && !errors.Is(runErr, context.Canceled) {
				process.ErrorContext(ctx, "hearth-simulator failed", slog.String("event", "process.failed"),
					slog.String("error_code", "run_failed"), slog.String("stage", "run"))
				return errSimulatorFailed
			}
			process.InfoContext(ctx, "hearth-simulator stopped", slog.String("event", "process.stopped"))
			return nil
		},
	}
}

func simulatorEnvFlag(name string) *cli.StringFlag {
	env := "HEARTH_SIMULATOR_" + map[string]string{
		"adapter-id": "ADAPTER_ID", "nats-url": "NATS_URL", "control-addr": "CONTROL_ADDR",
	}[name]
	return &cli.StringFlag{Name: name, Sources: cli.NewValueSourceChain(cli.EnvVar(env))}
}

func resolvedSimulatorConfig(cmd *cli.Command) (simulator.Config, error) {
	config, err := loadSimulatorYAML(cmd.String("config"), cmd.IsSet("config"))
	if err != nil {
		return simulator.Config{}, err
	}
	if cmd.IsSet("adapter-id") {
		config.AdapterID = cmd.String("adapter-id")
	}
	if cmd.IsSet("nats-url") {
		config.NATSURL = cmd.String("nats-url")
	}
	if cmd.IsSet("control-addr") {
		config.ControlAddr = cmd.String("control-addr")
	}
	if err = config.Validate(); err != nil {
		return simulator.Config{}, platformconfig.Invalid("", err)
	}
	return config, nil
}

// Unknown fields are allowed; collections are supplied only by YAML.
func loadSimulatorYAML(path string, explicit bool) (simulator.Config, error) {
	file, err := os.Open(path)
	if err != nil {
		if !explicit && errors.Is(err, os.ErrNotExist) {
			return simulator.Config{}, nil
		}
		return simulator.Config{}, errors.New("configuration file could not be read")
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	var config simulator.Config
	if err = decoder.Decode(&config); err != nil && !errors.Is(err, io.EOF) {
		return simulator.Config{}, errors.New("configuration file contains invalid YAML")
	}
	var extra any
	if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return simulator.Config{}, errors.New("configuration file must contain a single YAML document")
	}
	return config, nil
}
