package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	altsrc "github.com/urfave/cli-altsrc/v3"
	altsrcyaml "github.com/urfave/cli-altsrc/v3/yaml"
	"github.com/urfave/cli/v3"
	"gopkg.in/yaml.v3"

	"github.com/mholtzscher/hearth/internal/app/hearthd"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
	"github.com/mholtzscher/hearth/internal/platform/logging"
)

const defaultHearthdConfigPath = "configs/hearthd.yaml"

var errHearthdFailed = errors.New("hearthd failed")

func newHearthdCommand(
	run func(context.Context, hearthd.Config, *slog.Logger) error,
	args []string,
	stderr io.Writer,
) *cli.Command {
	configPath := selectedConfigPath(args[1:])
	configSource := func(key, env string) cli.ValueSourceChain {
		return cli.NewValueSourceChain(cli.EnvVar(env), altsrcyaml.YAML(key, altsrc.NewStringPtrSourcer(&configPath)))
	}
	flags := []cli.Flag{
		&cli.StringFlag{
			Name: "config", Value: defaultHearthdConfigPath,
			Usage:       "path to the local hearthd YAML configuration",
			Destination: &configPath, Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTHD_CONFIG")),
		},
		&cli.StringFlag{
			Name: "household-timezone", Sources: configSource("household_timezone", "HEARTHD_HOUSEHOLD_TIMEZONE"),
		},
		&cli.StringFlag{Name: "http-addr", Sources: configSource("http_addr", "HEARTHD_HTTP_ADDR")},
		&cli.StringFlag{Name: "nats-url", Sources: configSource("nats_url", "HEARTHD_NATS_URL")},
		&cli.StringFlag{Name: "sqlite-path", Sources: configSource("sqlite_path", "HEARTHD_SQLITE_PATH")},
		&cli.StringFlag{
			Name:    "observation-retention",
			Sources: configSource("observation_retention", "HEARTHD_OBSERVATION_RETENTION"),
		},
		&cli.StringFlag{
			Name:    "automation-history-retention",
			Sources: configSource("automation_history_retention", "HEARTHD_AUTOMATION_HISTORY_RETENTION"),
		},
		&cli.StringFlag{
			Name:    "agent-api-key-file",
			Sources: configSource("agent.api_key_file", "HEARTHD_AGENT_API_KEY_FILE"),
		},
		&cli.StringFlag{Name: "agent-model", Sources: configSource("agent.model", "HEARTHD_AGENT_MODEL")},
		&cli.StringFlag{Name: "agent-base-url", Sources: configSource("agent.base_url", "HEARTHD_AGENT_BASE_URL")},
		&cli.StringFlag{
			Name:    "agent-reasoning-effort",
			Sources: configSource("agent.reasoning_effort", "HEARTHD_AGENT_REASONING_EFFORT"),
		},
		&cli.StringFlag{Name: "agent-max-steps", Sources: configSource("agent.max_steps", "HEARTHD_AGENT_MAX_STEPS")},
		&cli.StringFlag{
			Name:    "agent-history-retention",
			Sources: configSource("agent.history_retention", "HEARTHD_AGENT_HISTORY_RETENTION"),
		},
		&cli.StringFlag{
			Name:    "log-level",
			Value:   "info",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTHD_LOG_LEVEL")),
			Usage:   "log level: debug, info, warn, or error",
		},
		&cli.StringFlag{
			Name:    "log-format",
			Value:   "text",
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTHD_LOG_FORMAT")),
			Usage:   "log format: text or json",
		},
	}
	longFlagNames := hearthdLongFlagNames(flags)
	return &cli.Command{
		Name: "hearthd", Usage: "run Hearth Core", Flags: flags,
		Writer: stderr, ErrWriter: stderr,
		Before: func(ctx context.Context, _ *cli.Command) (context.Context, error) {
			if err := rejectSingleDashLongFlags(args[1:], longFlagNames); err != nil {
				return ctx, err
			}
			return ctx, nil
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			logger, err := logging.NewApplicationLogger(stderr, "hearthd", logging.LogOptions{
				Level: cmd.String("log-level"), Format: cmd.String("log-format"),
			})
			if err != nil {
				fmt.Fprintln(stderr, err)
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
				ctx,
				"hearthd configuration loaded",
				slog.String("event", "process.config_loaded"),
			)
			runErr := run(ctx, config, logger)
			if runErr != nil && !errors.Is(runErr, context.Canceled) {
				processLogger.ErrorContext(ctx, "hearthd failed",
					slog.String("event", "process.failed"), slog.String("error_code", "run_failed"),
					slog.String("stage", hearthd.ErrorStage(runErr)))
				return errHearthdFailed
			}
			processLogger.InfoContext(ctx, "hearthd stopped", slog.String("event", "process.stopped"))
			return nil
		},
	}
}

func hearthdLongFlagNames(flags []cli.Flag) map[string]struct{} {
	names := make(map[string]struct{}, len(flags))
	for _, flag := range flags {
		for _, name := range flag.Names() {
			names[name] = struct{}{}
		}
	}
	return names
}

func rejectSingleDashLongFlags(args []string, names map[string]struct{}) error {
	for _, arg := range args {
		if arg == "--" {
			return nil
		}
		if !strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "--") {
			continue
		}
		name, _, _ := strings.Cut(strings.TrimPrefix(arg, "-"), "=")
		if _, ok := names[name]; ok {
			return fmt.Errorf("-%s is not supported; use --%s", name, name)
		}
	}
	return nil
}

func selectedConfigPath(args []string) string {
	path := defaultHearthdConfigPath
	if configured, ok := os.LookupEnv("HEARTHD_CONFIG"); ok && configured != "" {
		path = configured
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if arg == "--config" && i+1 < len(args) {
			i++
			path = args[i]
		} else if value, ok := strings.CutPrefix(arg, "--config="); ok {
			path = value
		}
	}
	return path
}

func resolvedHearthdConfig(cmd *cli.Command) (hearthd.Config, error) {
	path := cmd.String("config")
	explicit := cmd.IsSet("config")
	if err := validateHearthdConfigFile(path, explicit); err != nil {
		return hearthd.Config{}, err
	}
	fileConfig, err := readHearthdYAML(path)
	if err != nil {
		return hearthd.Config{}, err
	}
	config := hearthd.Config{
		HouseholdTimezone: resolvedString(cmd, "household-timezone", fileConfig.HouseholdTimezone),
		HTTPAddr:          resolvedString(cmd, "http-addr", fileConfig.HTTPAddr),
		NATSURL:           resolvedString(cmd, "nats-url", fileConfig.NATSURL),
		SQLitePath:        resolvedString(cmd, "sqlite-path", fileConfig.SQLitePath),
		Agent: hearthd.AgentConfig{
			APIKeyFile:      resolvedString(cmd, "agent-api-key-file", fileConfig.Agent.APIKeyFile),
			Model:           resolvedString(cmd, "agent-model", fileConfig.Agent.Model),
			BaseURL:         resolvedString(cmd, "agent-base-url", fileConfig.Agent.BaseURL),
			ReasoningEffort: resolvedString(cmd, "agent-reasoning-effort", fileConfig.Agent.ReasoningEffort),
		},
	}
	if config.ObservationRetention, err = resolvedDuration(
		cmd,
		"observation-retention",
		fileConfig.ObservationRetention,
		"observation_retention",
	); err != nil {
		return hearthd.Config{}, err
	}
	if config.AutomationHistoryRetention, err = resolvedDuration(
		cmd,
		"automation-history-retention",
		fileConfig.AutomationHistoryRetention,
		"automation_history_retention",
	); err != nil {
		return hearthd.Config{}, err
	}
	if config.Agent.MaxSteps, err = resolvedInt(
		cmd,
		"agent-max-steps",
		fileConfig.Agent.MaxSteps,
		"agent.max_steps",
	); err != nil {
		return hearthd.Config{}, err
	}
	if config.Agent.HistoryRetention, err = resolvedDuration(
		cmd,
		"agent-history-retention",
		fileConfig.Agent.HistoryRetention,
		"agent.history_retention",
	); err != nil {
		return hearthd.Config{}, err
	}
	config = hearthd.NormalizeConfig(config)
	if validationErr := config.Validate(); validationErr != nil {
		return hearthd.Config{}, platformconfig.Invalid("", validationErr)
	}
	return config, nil
}

type hearthdYAML struct {
	HouseholdTimezone          string `yaml:"household_timezone"`
	HTTPAddr                   string `yaml:"http_addr"`
	NATSURL                    string `yaml:"nats_url"`
	SQLitePath                 string `yaml:"sqlite_path"`
	ObservationRetention       string `yaml:"observation_retention"`
	AutomationHistoryRetention string `yaml:"automation_history_retention"`
	Agent                      struct {
		APIKeyFile       string `yaml:"api_key_file"`
		Model            string `yaml:"model"`
		BaseURL          string `yaml:"base_url"`
		ReasoningEffort  string `yaml:"reasoning_effort"`
		MaxSteps         string `yaml:"max_steps"`
		HistoryRetention string `yaml:"history_retention"`
	} `yaml:"agent"`
}

func readHearthdYAML(path string) (hearthdYAML, error) {
	var config hearthdYAML
	contents, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && path == defaultHearthdConfigPath {
			return config, nil
		}
		return hearthdYAML{}, errors.New("configuration file could not be read")
	}
	if len(strings.TrimSpace(string(contents))) == 0 {
		return config, nil
	}
	var document map[string]any
	if decodeErr := yaml.Unmarshal(contents, &document); decodeErr != nil {
		return hearthdYAML{}, errors.New("configuration file contains invalid YAML")
	}
	config.HouseholdTimezone = yamlString(document["household_timezone"])
	config.HTTPAddr = yamlString(document["http_addr"])
	config.NATSURL = yamlString(document["nats_url"])
	config.SQLitePath = yamlString(document["sqlite_path"])
	config.ObservationRetention = yamlString(document["observation_retention"])
	config.AutomationHistoryRetention = yamlString(document["automation_history_retention"])
	if agent, ok := yamlMap(document["agent"]); ok {
		config.Agent.APIKeyFile = yamlString(agent["api_key_file"])
		config.Agent.Model = yamlString(agent["model"])
		config.Agent.BaseURL = yamlString(agent["base_url"])
		config.Agent.ReasoningEffort = yamlString(agent["reasoning_effort"])
		config.Agent.MaxSteps = yamlString(agent["max_steps"])
		config.Agent.HistoryRetention = yamlString(agent["history_retention"])
	}
	return config, nil
}

func yamlMap(value any) (map[string]any, bool) {
	switch value := value.(type) {
	case map[string]any:
		return value, true
	case map[any]any:
		result := make(map[string]any, len(value))
		for key, nested := range value {
			if stringKey, ok := key.(string); ok {
				result[stringKey] = nested
			}
		}
		return result, true
	default:
		return nil, false
	}
}

func yamlString(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func resolvedString(cmd *cli.Command, name, fromYAML string) string {
	if cmd.IsSet(name) {
		return cmd.String(name)
	}
	return fromYAML
}

func resolvedInt(cmd *cli.Command, name, fromYAML, field string) (int, error) {
	value := fromYAML
	if cmd.IsSet(name) {
		value = cmd.String(name)
	}
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, platformconfig.Invalid("", fmt.Errorf("%s must be an integer", field))
	}
	return parsed, nil
}

func resolvedDuration(cmd *cli.Command, name, fromYAML, field string) (time.Duration, error) {
	value := fromYAML
	if cmd.IsSet(name) {
		value = cmd.String(name)
	}
	if value == "" {
		return 0, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, platformconfig.Invalid("", fmt.Errorf("%s must be a duration", field))
	}
	return parsed, nil
}

func validateHearthdConfigFile(path string, explicit bool) error {
	file, err := os.Open(path)
	if err != nil {
		if !explicit && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return errors.New("configuration file could not be read")
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	var document yaml.Node
	if decodeErr := decoder.Decode(&document); decodeErr != nil && !errors.Is(decodeErr, io.EOF) {
		return errors.New("configuration file contains invalid YAML")
	}
	if len(document.Content) > 0 {
		if duplicateErr := checkDuplicateYAMLKeys(&document); duplicateErr != nil {
			return errors.New("configuration file contains duplicate YAML keys")
		}
	}
	var extra yaml.Node
	if decodeErr := decoder.Decode(&extra); !errors.Is(decodeErr, io.EOF) {
		return errors.New("configuration file must contain a single YAML document")
	}
	return nil
}

func checkDuplicateYAMLKeys(node *yaml.Node) error {
	if node.Kind == yaml.MappingNode {
		const mappingPairSize = 2
		seen := make(map[string]struct{}, len(node.Content)/mappingPairSize)
		for i := 0; i < len(node.Content); i += mappingPairSize {
			key := node.Content[i]
			identity := key.Tag + ":" + key.Value
			if _, ok := seen[identity]; ok {
				return errors.New("duplicate key")
			}
			seen[identity] = struct{}{}
		}
	}
	for _, child := range node.Content {
		if err := checkDuplicateYAMLKeys(child); err != nil {
			return err
		}
	}
	return nil
}
