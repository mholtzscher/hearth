package main

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/urfave/cli/v3"

	"github.com/mholtzscher/hearth/internal/app/hearthd"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
	"github.com/mholtzscher/hearth/internal/platform/logging"
)

const defaultHearthdConfigPath = "configs/hearthd.yaml"

var errHearthdFailed = errors.New("hearthd failed")

func newHearthdCommand(
	run func(context.Context, hearthd.Config, *slog.Logger) error,
	stderr io.Writer,
) *cli.Command {
	flags := []cli.Flag{
		&cli.StringFlag{
			Name: "config", Value: defaultHearthdConfigPath,
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTHD_CONFIG")),
		},
		hearthdEnvFlag("household-timezone", "HEARTHD_HOUSEHOLD_TIMEZONE"),
		hearthdEnvFlag("http-addr", "HEARTHD_HTTP_ADDR"),
		hearthdEnvFlag("nats-url", "HEARTHD_NATS_URL"),
		hearthdEnvFlag("sqlite-path", "HEARTHD_SQLITE_PATH"),
		&cli.DurationFlag{
			Name: "observation-retention",
			Sources: cli.NewValueSourceChain(
				cli.EnvVar("HEARTHD_OBSERVATION_RETENTION"),
			),
		},
		&cli.DurationFlag{
			Name: "automation-history-retention",
			Sources: cli.NewValueSourceChain(
				cli.EnvVar("HEARTHD_AUTOMATION_HISTORY_RETENTION"),
			),
		},
		hearthdEnvFlag("agent-api-key-file", "HEARTHD_AGENT_API_KEY_FILE"),
		hearthdEnvFlag("agent-model", "HEARTHD_AGENT_MODEL"),
		hearthdEnvFlag("agent-base-url", "HEARTHD_AGENT_BASE_URL"),
		hearthdEnvFlag("agent-reasoning-effort", "HEARTHD_AGENT_REASONING_EFFORT"),
		&cli.IntFlag{Name: "agent-max-steps", Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTHD_AGENT_MAX_STEPS"))},
		&cli.DurationFlag{
			Name: "agent-history-retention",
			Sources: cli.NewValueSourceChain(
				cli.EnvVar("HEARTHD_AGENT_HISTORY_RETENTION"),
			),
		},
		&cli.StringFlag{
			Name: "log-level", Value: "info", Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTHD_LOG_LEVEL")),
			Usage: "log level: debug, info, warn, or error",
		},
		&cli.StringFlag{
			Name: "log-format", Value: "text", Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTHD_LOG_FORMAT")),
			Usage: "log format: text or json",
		},
	}
	return &cli.Command{
		Name: "hearthd", Usage: "run Hearth Core", Flags: flags, Writer: stderr, ErrWriter: stderr,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			logger, err := logging.NewApplicationLogger(stderr, "hearthd", logging.LogOptions{
				Level: cmd.String("log-level"), Format: cmd.String("log-format"),
			})
			if err != nil {
				return err
			}
			processLogger := logger.With(slog.String("component", "process"))
			processLogger.InfoContext(ctx, "hearthd starting", slog.String("event", "process.starting"))
			config, err := resolvedHearthdConfig(cmd)
			if err != nil {
				processLogger.ErrorContext(ctx, "hearthd configuration failed",
					slog.String("event", "process.failed"), slog.String("error_code", "config_invalid"),
					slog.String("stage", "load_config"), slog.String("error", platformconfig.Reason(err)))
				return errHearthdFailed
			}
			processLogger.InfoContext(
				ctx, "hearthd configuration loaded", slog.String("event", "process.config_loaded"),
			)
			if runErr := run(ctx, config, logger); runErr != nil && !errors.Is(runErr, context.Canceled) {
				processLogger.ErrorContext(ctx, "hearthd failed", slog.String("event", "process.failed"),
					slog.String("error_code", "run_failed"), slog.String("stage", hearthd.ErrorStage(runErr)))
				return errHearthdFailed
			}
			processLogger.InfoContext(ctx, "hearthd stopped", slog.String("event", "process.stopped"))
			return nil
		},
	}
}

func hearthdEnvFlag(name, env string) *cli.StringFlag {
	return &cli.StringFlag{Name: name, Sources: cli.NewValueSourceChain(cli.EnvVar(env))}
}

func resolvedHearthdConfig(cmd *cli.Command) (hearthd.Config, error) {
	config, err := platformconfig.LoadYAML[hearthd.Config](cmd.String("config"), cmd.IsSet("config"))
	if err != nil {
		return hearthd.Config{}, err
	}
	if cmd.IsSet("household-timezone") {
		config.HouseholdTimezone = cmd.String("household-timezone")
	}
	if cmd.IsSet("http-addr") {
		config.HTTPAddr = cmd.String("http-addr")
	}
	if cmd.IsSet("nats-url") {
		config.NATSURL = cmd.String("nats-url")
	}
	if cmd.IsSet("sqlite-path") {
		config.SQLitePath = cmd.String("sqlite-path")
	}
	if cmd.IsSet("observation-retention") {
		config.ObservationRetention = cmd.Duration("observation-retention")
	}
	if cmd.IsSet("automation-history-retention") {
		config.AutomationHistoryRetention = cmd.Duration("automation-history-retention")
	}
	if cmd.IsSet("agent-api-key-file") {
		config.Agent.APIKeyFile = cmd.String("agent-api-key-file")
	}
	if cmd.IsSet("agent-model") {
		config.Agent.Model = cmd.String("agent-model")
	}
	if cmd.IsSet("agent-base-url") {
		config.Agent.BaseURL = cmd.String("agent-base-url")
	}
	if cmd.IsSet("agent-reasoning-effort") {
		config.Agent.ReasoningEffort = cmd.String("agent-reasoning-effort")
	}
	if cmd.IsSet("agent-max-steps") {
		config.Agent.MaxSteps = cmd.Int("agent-max-steps")
	}
	if cmd.IsSet("agent-history-retention") {
		config.Agent.HistoryRetention = cmd.Duration("agent-history-retention")
	}
	config = hearthd.NormalizeConfig(config)
	if validationErr := config.Validate(); validationErr != nil {
		return hearthd.Config{}, platformconfig.Invalid("", validationErr)
	}
	return config, nil
}
