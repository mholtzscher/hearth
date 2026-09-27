package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

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
	flags := []cli.Flag{
		&cli.StringFlag{
			Name: "config", Value: defaultHearthdConfigPath,
			Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTHD_CONFIG")),
		},
		hearthdEnvFlag("household-timezone", "HEARTHD_HOUSEHOLD_TIMEZONE"),
		hearthdEnvFlag("http-addr", "HEARTHD_HTTP_ADDR"),
		hearthdEnvFlag("nats-url", "HEARTHD_NATS_URL"),
		hearthdEnvFlag("sqlite-path", "HEARTHD_SQLITE_PATH"),
		hearthdEnvFlag("observation-retention", "HEARTHD_OBSERVATION_RETENTION"),
		hearthdEnvFlag("automation-history-retention", "HEARTHD_AUTOMATION_HISTORY_RETENTION"),
		hearthdEnvFlag("agent-api-key-file", "HEARTHD_AGENT_API_KEY_FILE"),
		hearthdEnvFlag("agent-model", "HEARTHD_AGENT_MODEL"),
		hearthdEnvFlag("agent-base-url", "HEARTHD_AGENT_BASE_URL"),
		hearthdEnvFlag("agent-reasoning-effort", "HEARTHD_AGENT_REASONING_EFFORT"),
		hearthdEnvFlag("agent-max-steps", "HEARTHD_AGENT_MAX_STEPS"),
		hearthdEnvFlag("agent-history-retention", "HEARTHD_AGENT_HISTORY_RETENTION"),
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
		Before: func(ctx context.Context, _ *cli.Command) (context.Context, error) {
			if err := rejectSingleDashLongFlags(args[1:], hearthdLongFlagNames(flags)); err != nil {
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

func resolvedHearthdConfig(cmd *cli.Command) (hearthd.Config, error) {
	fileConfig, err := loadHearthdYAML(cmd.String("config"), cmd.IsSet("config"))
	if err != nil {
		return hearthd.Config{}, err
	}
	return configFromSources(cmd, fileConfig)
}

func loadHearthdYAML(path string, explicit bool) (hearthdYAML, error) {
	file, err := os.Open(path)
	if err != nil {
		if !explicit && errors.Is(err, os.ErrNotExist) {
			return hearthdYAML{}, nil
		}
		return hearthdYAML{}, errors.New("configuration file could not be read")
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	var document yaml.Node
	if decodeErr := decoder.Decode(&document); decodeErr != nil && !errors.Is(decodeErr, io.EOF) {
		return hearthdYAML{}, errors.New("configuration file contains invalid YAML")
	}
	if aliasErr := checkHearthdYAMLAliases(&document); aliasErr != nil {
		return hearthdYAML{}, aliasErr
	}
	if duplicateErr := checkHearthdYAML(&document); duplicateErr != nil {
		return hearthdYAML{}, duplicateErr
	}
	var extra yaml.Node
	if decodeErr := decoder.Decode(&extra); !errors.Is(decodeErr, io.EOF) {
		return hearthdYAML{}, errors.New("configuration file must contain a single YAML document")
	}
	if len(document.Content) == 0 {
		return hearthdYAML{}, nil
	}
	return hearthdYAMLValues(&document), nil
}

type hearthdYAML struct {
	HouseholdTimezone, HTTPAddr, NATSURL, SQLitePath                string
	ObservationRetention, AutomationHistoryRetention                string
	AgentAPIKeyFile, AgentModel, AgentBaseURL, AgentReasoningEffort string
	AgentMaxSteps, AgentHistoryRetention                            string
}

func hearthdYAMLValues(document *yaml.Node) hearthdYAML {
	var values hearthdYAML
	root := document.Content[0]
	get := func(node *yaml.Node, key string) string { return yamlScalar(yamlMappingValue(node, key)) }
	values.HouseholdTimezone = get(root, "household_timezone")
	values.HTTPAddr = get(root, "http_addr")
	values.NATSURL = get(root, "nats_url")
	values.SQLitePath = get(root, "sqlite_path")
	values.ObservationRetention = get(root, "observation_retention")
	values.AutomationHistoryRetention = get(root, "automation_history_retention")
	agent := yamlMappingValue(root, "agent")
	values.AgentAPIKeyFile = get(agent, "api_key_file")
	values.AgentModel = get(agent, "model")
	values.AgentBaseURL = get(agent, "base_url")
	values.AgentReasoningEffort = get(agent, "reasoning_effort")
	values.AgentMaxSteps = get(agent, "max_steps")
	values.AgentHistoryRetention = get(agent, "history_retention")
	return values
}

func yamlMappingValue(node *yaml.Node, key string) *yaml.Node {
	node = resolveHearthdYAMLAlias(node)
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func yamlScalar(node *yaml.Node) string {
	node = resolveHearthdYAMLAlias(node)
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag == "!!null" {
		return ""
	}
	return node.Value
}

func resolveHearthdYAMLAlias(node *yaml.Node) *yaml.Node {
	seen := make(map[*yaml.Node]struct{})
	for node != nil && node.Kind == yaml.AliasNode {
		if _, exists := seen[node]; exists {
			return nil
		}
		seen[node] = struct{}{}
		node = node.Alias
	}
	return node
}

func checkHearthdYAMLAliases(node *yaml.Node) error {
	visiting := make(map[*yaml.Node]bool)
	visited := make(map[*yaml.Node]bool)
	var visit func(*yaml.Node) bool
	visit = func(current *yaml.Node) bool {
		if current == nil {
			return false
		}
		if visiting[current] {
			return true
		}
		if visited[current] {
			return false
		}
		visiting[current] = true
		if current.Kind == yaml.AliasNode && visit(current.Alias) {
			return true
		}
		if slices.ContainsFunc(current.Content, visit) {
			return true
		}
		delete(visiting, current)
		visited[current] = true
		return false
	}
	if visit(node) {
		return errors.New("configuration file contains cyclic YAML aliases")
	}
	return nil
}

func checkHearthdYAML(node *yaml.Node) error {
	if node.Kind == yaml.MappingNode {
		const pairSize = 2
		seen := make(map[string]struct{}, len(node.Content)/pairSize)
		for i := 0; i+1 < len(node.Content); i += pairSize {
			key := node.Content[i]
			identity := key.Tag + ":" + key.Value
			if _, exists := seen[identity]; exists {
				return errors.New("configuration file contains duplicate YAML keys")
			}
			seen[identity] = struct{}{}
		}
	}
	for _, child := range node.Content {
		if err := checkHearthdYAML(child); err != nil {
			return err
		}
	}
	return nil
}

func configFromSources(cmd *cli.Command, file hearthdYAML) (hearthd.Config, error) {
	value := func(name, yamlValue string) string {
		if cmd.IsSet(name) {
			return cmd.String(name)
		}
		return yamlValue
	}
	config := hearthd.Config{
		HouseholdTimezone: value("household-timezone", file.HouseholdTimezone),
		HTTPAddr:          value("http-addr", file.HTTPAddr), NATSURL: value("nats-url", file.NATSURL),
		SQLitePath: value("sqlite-path", file.SQLitePath),
		Agent: hearthd.AgentConfig{
			APIKeyFile: value("agent-api-key-file", file.AgentAPIKeyFile),
			Model:      value("agent-model", file.AgentModel), BaseURL: value("agent-base-url", file.AgentBaseURL),
			ReasoningEffort: value("agent-reasoning-effort", file.AgentReasoningEffort),
		},
	}
	var err error
	if config.ObservationRetention, err = configDuration(
		value("observation-retention", file.ObservationRetention), "observation_retention",
	); err != nil {
		return hearthd.Config{}, err
	}
	if config.AutomationHistoryRetention, err = configDuration(
		value("automation-history-retention", file.AutomationHistoryRetention), "automation_history_retention",
	); err != nil {
		return hearthd.Config{}, err
	}
	if config.Agent.HistoryRetention, err = configDuration(
		value("agent-history-retention", file.AgentHistoryRetention), "agent.history_retention",
	); err != nil {
		return hearthd.Config{}, err
	}
	if config.Agent.MaxSteps, err = configInt(
		value("agent-max-steps", file.AgentMaxSteps), "agent.max_steps",
	); err != nil {
		return hearthd.Config{}, err
	}
	config = hearthd.NormalizeConfig(config)
	if validationErr := config.Validate(); validationErr != nil {
		return hearthd.Config{}, platformconfig.Invalid("", validationErr)
	}
	return config, nil
}

func configDuration(raw, field string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, platformconfig.Invalid("", fmt.Errorf("%s must be a duration", field))
	}
	return value, nil
}

func configInt(raw, field string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, platformconfig.Invalid("", fmt.Errorf("%s must be an integer", field))
	}
	return value, nil
}
