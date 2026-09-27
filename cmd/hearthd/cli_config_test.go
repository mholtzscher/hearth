package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/app/hearthd"
)

const validYAML = `household_timezone: UTC
http_addr: 127.0.0.1:8080
nats_url: nats://127.0.0.1:4222
sqlite_path: from-yaml.db
observation_retention: 192h
automation_history_retention: 200h
agent:
  api_key_file: yaml-key
  model: yaml-model
  base_url: https://yaml.example/v1
  reasoning_effort: low
  max_steps: 9
  history_retention: 48h
`

// TestHearthdSourcesPrecedence protects every CLI setting's source chain and
// fails if flags, env, YAML, or defaults are accidentally reordered or omitted.
func TestHearthdSourcesPrecedence(t *testing.T) {
	fields := []struct {
		name, env, flag, yamlKey, yamlValue, envValue, flagValue string
		wantFlag, wantEnv, wantYAML                              string
	}{
		{
			"timezone", "HEARTHD_HOUSEHOLD_TIMEZONE", "household-timezone", "household_timezone", "UTC",
			"Pacific/Auckland", "Europe/Paris", "Europe/Paris", "Pacific/Auckland", "UTC",
		},
		{
			"http", "HEARTHD_HTTP_ADDR", "http-addr", "http_addr", "127.0.0.1:8080", "127.0.0.1:8081",
			"127.0.0.1:8082", "127.0.0.1:8082", "127.0.0.1:8081", "127.0.0.1:8080",
		},
		{
			"nats", "HEARTHD_NATS_URL", "nats-url", "nats_url", "nats://127.0.0.1:4222", "nats://127.0.0.1:4223",
			"nats://127.0.0.1:4224", "nats://127.0.0.1:4224", "nats://127.0.0.1:4223", "nats://127.0.0.1:4222",
		},
		{
			"sqlite", "HEARTHD_SQLITE_PATH", "sqlite-path", "sqlite_path", "from-yaml.db", "from-env.db",
			"from-flag.db", "from-flag.db", "from-env.db", "from-yaml.db",
		},
		{
			"observation retention",
			"HEARTHD_OBSERVATION_RETENTION",
			"observation-retention",
			"observation_retention",
			"192h",
			"200h",
			"208h",
			"208h",
			"200h",
			"192h",
		},
		{
			"automation retention",
			"HEARTHD_AUTOMATION_HISTORY_RETENTION",
			"automation-history-retention",
			"automation_history_retention",
			"200h",
			"208h",
			"216h",
			"216h",
			"208h",
			"200h",
		},
		{
			"key file",
			"HEARTHD_AGENT_API_KEY_FILE",
			"agent-api-key-file",
			"api_key_file",
			"yaml-key",
			"env-key",
			"flag-key",
			"flag-key",
			"env-key",
			"yaml-key",
		},
		{
			"model",
			"HEARTHD_AGENT_MODEL",
			"agent-model",
			"model",
			"yaml-model",
			"env-model",
			"flag-model",
			"flag-model",
			"env-model",
			"yaml-model",
		},
		{
			"base URL",
			"HEARTHD_AGENT_BASE_URL",
			"agent-base-url",
			"base_url",
			"https://yaml.example/v1",
			"https://env.example/v1",
			"https://flag.example/v1",
			"https://flag.example/v1",
			"https://env.example/v1",
			"https://yaml.example/v1",
		},
		{
			"reasoning",
			"HEARTHD_AGENT_REASONING_EFFORT",
			"agent-reasoning-effort",
			"reasoning_effort",
			"low",
			"medium",
			"high",
			"high",
			"medium",
			"low",
		},
		{"steps", "HEARTHD_AGENT_MAX_STEPS", "agent-max-steps", "max_steps", "9", "11", "13", "13", "11", "9"},
		{
			"agent retention",
			"HEARTHD_AGENT_HISTORY_RETENTION",
			"agent-history-retention",
			"history_retention",
			"48h",
			"56h",
			"64h",
			"64h",
			"56h",
			"48h",
		},
	}
	for _, field := range fields {
		t.Run(field.name, func(t *testing.T) {
			temp := t.TempDir()
			path := filepath.Join(temp, "config.yaml")
			contents := validYAML
			if field.name == "timezone" {
				contents = strings.Replace(
					contents,
					"household_timezone: UTC",
					"household_timezone: "+field.yamlValue,
					1,
				)
			}
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(field.env, field.envValue)
			gotFlag := invokeConfig(t, []string{"--config", path, "--" + field.flag, field.flagValue})
			assertField(t, gotFlag, field.name, field.wantFlag)
			t.Setenv(field.env, field.envValue)
			gotEnv := invokeConfig(t, []string{"--config", path})
			assertField(t, gotEnv, field.name, field.wantEnv)
			if err := os.Unsetenv(field.env); err != nil {
				t.Fatal(err)
			}
			gotYAML := invokeConfig(t, []string{"--config", path})
			assertField(t, gotYAML, field.name, field.wantYAML)
		})
	}
}

// TestConfigFileSelectionAndPolicy protects explicit-file requirements and
// config selection independent of flag order; it fails if absent defaults,
// remote-style paths, malformed YAML, duplicate keys, or multiple documents slip through.
func TestConfigFileSelectionAndPolicy(t *testing.T) {
	unsetEnv(t, "HEARTHD_CONFIG")
	path := filepath.Join(t.TempDir(), "selected.yaml")
	if err := os.WriteFile(path, []byte(validYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--config", path, "--nats-url", "nats://127.0.0.1:4223"},
		{"--nats-url", "nats://127.0.0.1:4223", "--config", path},
		{"-nats-url", "nats://127.0.0.1:4223", "-config", path},
	} {
		got := invokeConfig(t, args)
		if got.NATSURL != "nats://127.0.0.1:4223" {
			t.Fatalf("NATS URL = %q", got.NATSURL)
		}
	}
	t.Setenv("HEARTHD_CONFIG", path)
	if got := invokeConfig(t, nil); got.SQLitePath != "from-yaml.db" {
		t.Fatalf("env-selected config not loaded: %+v", got)
	}
	otherPath := filepath.Join(t.TempDir(), "cli-selected.yaml")
	otherYAML := strings.Replace(validYAML, "from-yaml.db", "cli-selected.db", 1)
	if err := os.WriteFile(otherPath, []byte(otherYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := invokeConfig(t, []string{"--config", otherPath}); got.SQLitePath != "cli-selected.db" {
		t.Fatalf("CLI config path did not override HEARTHD_CONFIG: %+v", got)
	}

	unsetEnv(t, "HEARTHD_CONFIG")
	t.Setenv("HEARTHD_HOUSEHOLD_TIMEZONE", "UTC")
	t.Setenv("HEARTHD_HTTP_ADDR", "127.0.0.1:8080")
	t.Setenv("HEARTHD_NATS_URL", "nats://127.0.0.1:4222")
	t.Setenv("HEARTHD_SQLITE_PATH", "from-env.db")
	t.Setenv("HEARTHD_AGENT_API_KEY_FILE", "secret-file")
	if got := invokeConfig(t, nil); got.SQLitePath != "from-env.db" {
		t.Fatalf("implicit missing file blocked env config: %+v", got)
	}
	assertHearthdYAMLPolicy(t)
}

func TestHearthdYAMLResolvesAliases(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "aliases.yaml")
	body := `unused: &database_path from-alias.db
sqlite_path: *database_path
agent_defaults: &agent_config
  model: aliased-model
  max_steps: 7
agent: *agent_config
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadHearthdYAML(path, true)
	if err != nil {
		t.Fatalf("load aliased config: %v", err)
	}
	if got.SQLitePath != "from-alias.db" || got.Agent.Model != "aliased-model" || got.Agent.MaxSteps != 7 {
		t.Fatalf("aliases not resolved: %+v", got)
	}
}

func assertHearthdYAMLPolicy(t *testing.T) {
	t.Helper()
	missing := filepath.Join(t.TempDir(), "absent.yaml")
	if got := invokeConfig(t, nil); got.SQLitePath != "from-env.db" {
		t.Fatalf("implicit missing file blocked env config: %+v", got)
	}
	if _, err := invokeConfigErr(t, []string{"--config", missing}); err == nil {
		t.Fatal("explicit missing file accepted")
	}
	if _, err := invokeConfigErr(t, []string{"--config", t.TempDir()}); err == nil {
		t.Fatal("explicit unreadable config path accepted")
	}
	for name, body := range map[string]string{"zero-byte": "", "empty-document": "---\n"} {
		p := filepath.Join(t.TempDir(), name+".yaml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := invokeConfigErr(t, []string{"--config", p}); err != nil {
			t.Errorf("%s file with complete environment configuration rejected: %v", name, err)
		}
	}
	for name, body := range map[string]string{
		"malformed": "http_addr: [\n", "duplicate top": "http_addr: a\nhttp_addr: b\n",
		"duplicate nested": "agent:\n  model: a\n  model: b\n", "multiple docs": "---\nhttp_addr: a\n---\nhttp_addr: b\n",
	} {
		p := filepath.Join(t.TempDir(), name+".yaml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := invokeConfigErr(t, []string{"--config", p}); err == nil {
			t.Errorf("%s: invalid file accepted", name)
		}
	}
	unknown := filepath.Join(t.TempDir(), "unknown.yaml")
	unknownYAML := strings.Replace(validYAML, "agent:\n", "unknown_key: ignored\nagent:\n  future: ignored\n", 1)
	if err := os.WriteFile(unknown, []byte(unknownYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := invokeConfigErr(t, []string{"--config", unknown}); err != nil {
		t.Fatalf("unknown YAML keys rejected: %v", err)
	}
}

// TestExplicitEmptySourcesAndDefaults protects post-resolution normalization
// and the documented meaning of empty optional strings; it fails if an empty
// CLI source falls back to YAML or bypasses the built-in defaults.
//
//nolint:paralleltest // Uses process-wide environment for CLI input.
func TestExplicitEmptySourcesAndDefaults(t *testing.T) {
	unsetEnv(t, "HEARTHD_CONFIG")
	for _, key := range []string{
		"HEARTHD_HOUSEHOLD_TIMEZONE", "HEARTHD_HTTP_ADDR", "HEARTHD_NATS_URL", "HEARTHD_SQLITE_PATH",
		"HEARTHD_AGENT_API_KEY_FILE", "HEARTHD_AGENT_MODEL", "HEARTHD_AGENT_BASE_URL",
	} {
		unsetEnv(t, key)
	}
	path := filepath.Join(t.TempDir(), "optional.yaml")
	body := `household_timezone: UTC
http_addr: 127.0.0.1:8080
nats_url: nats://127.0.0.1:4222
sqlite_path: hearth.db
agent:
  api_key_file: key-file
  model: yaml-model
  base_url: https://yaml.example/v1
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := invokeConfig(t, []string{"--config", path, "--agent-model", "", "--agent-base-url", ""})
	if got.Agent.Model != hearthd.DefaultAgentModel || got.Agent.BaseURL != "" {
		t.Fatalf("empty source semantics = model %q, base URL %q", got.Agent.Model, got.Agent.BaseURL)
	}
	if got.ObservationRetention != hearthd.DefaultObservationRetention ||
		got.AutomationHistoryRetention != hearthd.DefaultAutomationHistoryRetention ||
		got.Agent.HistoryRetention != hearthd.DefaultAgentHistoryRetention {
		t.Fatalf("built-in retention defaults not applied: %+v", got)
	}
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
}

// TestNormalizationAndInvalidValueMasking protects model-dependent and zero
// defaults after source resolution, while proving malformed effective types fail.
//
//nolint:paralleltest // Uses process environment as CLI input; t.Setenv is intentionally non-parallel.
func TestNormalizationAndInvalidValueMasking(t *testing.T) {
	for _, key := range []string{
		"HEARTHD_CONFIG", "HEARTHD_HOUSEHOLD_TIMEZONE", "HEARTHD_HTTP_ADDR", "HEARTHD_NATS_URL",
		"HEARTHD_SQLITE_PATH", "HEARTHD_OBSERVATION_RETENTION", "HEARTHD_AUTOMATION_HISTORY_RETENTION",
		"HEARTHD_AGENT_API_KEY_FILE", "HEARTHD_AGENT_MODEL", "HEARTHD_AGENT_BASE_URL",
		"HEARTHD_AGENT_REASONING_EFFORT", "HEARTHD_AGENT_MAX_STEPS", "HEARTHD_AGENT_HISTORY_RETENTION",
	} {
		unsetEnv(t, key)
	}
	valid := `household_timezone: UTC
http_addr: 127.0.0.1:8080
nats_url: nats://127.0.0.1:4222
sqlite_path: db
observation_retention: 1s
agent: {api_key_file: key, model: "", reasoning_effort: invalid, max_steps: -1, history_retention: 1s}
`
	path := filepath.Join(t.TempDir(), "masked.yaml")
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	got := invokeConfig(
		t,
		[]string{
			"--config",
			path,
			"--observation-retention",
			"0s",
			"--agent-model",
			"other",
			"--agent-reasoning-effort",
			"",
			"--agent-max-steps",
			"0",
			"--agent-history-retention",
			"0s",
		},
	)
	if got.ObservationRetention != 720*time.Hour || got.AutomationHistoryRetention != 720*time.Hour ||
		got.Agent.HistoryRetention != 720*time.Hour {
		t.Fatalf("zero retention did not normalize: %+v", got)
	}
	if got.Agent.Model != "other" || got.Agent.ReasoningEffort != "" || got.Agent.MaxSteps != 0 {
		t.Fatalf("explicit values not preserved: %+v", got.Agent)
	}
	if got.Agent.APIKeyFile != "key" {
		t.Fatalf("key path = %q", got.Agent.APIKeyFile)
	}
	if _, err := invokeConfigErr(t, []string{"--config", path}); err == nil {
		t.Fatal("invalid effective YAML values accepted")
	}
}

// TestInvalidYAMLTypedValuesFailDuringConfigLoading protects path-free structured
// config failure reporting when YAML contains malformed typed values.
//
//nolint:paralleltest // Uses process environment to isolate typed CLI sources.
func TestInvalidYAMLTypedValuesFailDuringConfigLoading(t *testing.T) {
	tests := []struct {
		name, field, env, invalid string
	}{
		{"YAML duration", "observation-retention", "", "not-a-duration"},
		{"YAML integer", "agent-max-steps", "", "not-an-integer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertInvalidTypedValue(t, tt.name, tt.field, tt.env, tt.invalid)
		})
	}
}

// TestInvalidNetworkConfigLogsDoNotExposeValues protects process log redaction;
// it fails if parser errors leak the configured HTTP address or NATS URL.
//
//nolint:paralleltest // Uses process environment to isolate CLI configuration.
func TestInvalidNetworkConfigLogsDoNotExposeValues(t *testing.T) {
	for _, key := range []string{
		"HEARTHD_CONFIG", "HEARTHD_HOUSEHOLD_TIMEZONE", "HEARTHD_HTTP_ADDR", "HEARTHD_NATS_URL",
		"HEARTHD_SQLITE_PATH", "HEARTHD_AGENT_API_KEY_FILE",
	} {
		unsetEnv(t, key)
	}
	tests := []struct {
		name, field, valid, invalid, wantReason string
	}{
		{
			"HTTP address", "http_addr", "127.0.0.1:8080", "HTTP_ADDR_SENTINEL",
			"http_addr must contain a valid host and port",
		},
		{
			"NATS URL", "nats_url", "nats://127.0.0.1:4222", "nats://NATS_URL_SENTINEL\x00",
			"nats_url must be a valid absolute nats:// URL",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := strings.Replace(validYAML,
				tt.field+": "+tt.valid,
				tt.field+": "+strconv.Quote(tt.invalid), 1)
			path := filepath.Join(t.TempDir(), "invalid-network.yaml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			logs, err := invokeConfigErrCaptured(t, []string{"--config", path})
			if !errors.Is(err, errHearthdFailed) {
				t.Fatalf("error = %v, want process-level configuration failure", err)
			}
			if !strings.Contains(logs, tt.wantReason) {
				t.Errorf("configuration failure log missing safe diagnosis %q: %s", tt.wantReason, logs)
			}
			if strings.Contains(logs, tt.invalid) {
				t.Errorf("configuration failure log exposed configured value %q: %s", tt.invalid, logs)
			}
		})
	}
}

func assertInvalidTypedValue(t *testing.T, name, field, env, invalid string) {
	t.Helper()
	unsetEnv(t, "HEARTHD_CONFIG")
	unsetEnv(t, "HEARTHD_OBSERVATION_RETENTION")
	unsetEnv(t, "HEARTHD_AGENT_MAX_STEPS")
	body := invalidTypedValueYAML(name, field, env, invalid)
	path := filepath.Join(t.TempDir(), "invalid.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--config", path}
	if env != "" {
		t.Setenv(env, invalid)
	} else if strings.HasPrefix(name, "CLI") {
		args = append(args, "--"+field, invalid)
	}
	logs, err := invokeConfigErrCaptured(t, args)
	if !errors.Is(err, errHearthdFailed) {
		t.Fatalf("error = %v, want process-level configuration failure", err)
	}
	for _, expected := range []string{"event=process.failed", "error_code=config_invalid", "stage=load_config"} {
		if !strings.Contains(logs, expected) {
			t.Errorf("configuration failure log missing %q: %s", expected, logs)
		}
	}
	if strings.Contains(logs, path) || strings.Contains(logs, invalid) {
		t.Errorf("configuration failure log exposed a path or value: %s", logs)
	}
}

func invalidTypedValueYAML(name, field, env, invalid string) string {
	body := validYAML
	if env == "" && !strings.HasPrefix(name, "CLI") {
		if field == "observation-retention" {
			return strings.Replace(body, "observation_retention: 192h", "observation_retention: "+invalid, 1)
		}
		return strings.Replace(body, "max_steps: 9", "max_steps: "+invalid, 1)
	}
	return body
}

func invokeConfigErrCaptured(t *testing.T, args []string) (string, error) {
	t.Helper()
	var output strings.Builder
	_, runErr := invokeConfigErrWithOutput(t, args, &output)
	return output.String(), runErr
}

// TestInvalidYAMLIsRejectedEvenWhenOverridden protects file self-validity: a
// winning CLI value must not hide a malformed value in a present config file.
//
//nolint:paralleltest // Clears process environment variables via unsetEnv.
func TestInvalidYAMLIsRejectedEvenWhenOverridden(t *testing.T) {
	unsetEnv(t, "HEARTHD_CONFIG")
	path := filepath.Join(t.TempDir(), "masked-invalid.yaml")
	body := strings.Replace(validYAML, "observation_retention: 192h", "observation_retention: invalid-yaml", 1)
	body = strings.Replace(body, "max_steps: 9", "max_steps: invalid-yaml", 1)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := invokeConfigErr(
		t,
		[]string{"--config", path, "--observation-retention", "200h", "--agent-max-steps", "4"},
	); !errors.Is(err, errHearthdFailed) {
		t.Fatalf("CLI overrides masked invalid YAML: %v", err)
	}
}

func invokeConfig(t *testing.T, args []string) hearthd.Config {
	t.Helper()
	config, err := invokeConfigErr(t, args)
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func invokeConfigErr(t *testing.T, args []string) (hearthd.Config, error) {
	t.Helper()
	return invokeConfigErrWithOutput(t, args, io.Discard)
}

func invokeConfigErrWithOutput(t *testing.T, args []string, output io.Writer) (hearthd.Config, error) {
	t.Helper()
	var config hearthd.Config
	full := append([]string{"hearthd"}, args...)
	command := newHearthdCommand(
		func(_ context.Context, got hearthd.Config, _ *slog.Logger) error { config = got; return nil },
		output,
	)
	if err := command.Run(context.Background(), full); err != nil {
		return hearthd.Config{}, err
	}
	return config, nil
}

func assertField(t *testing.T, config hearthd.Config, name, want string) {
	t.Helper()
	var got string
	switch name {
	case "timezone":
		got = config.HouseholdTimezone
	case "http":
		got = config.HTTPAddr
	case "nats":
		got = config.NATSURL
	case "sqlite":
		got = config.SQLitePath
	case "observation retention":
		got = durationString(t, config.ObservationRetention)
	case "automation retention":
		got = durationString(t, config.AutomationHistoryRetention)
	case "key file":
		got = config.Agent.APIKeyFile
	case "model":
		got = config.Agent.Model
	case "base URL":
		got = config.Agent.BaseURL
	case "reasoning":
		got = config.Agent.ReasoningEffort
	case "steps":
		got = strconv.Itoa(config.Agent.MaxSteps)
	case "agent retention":
		got = durationString(t, config.Agent.HistoryRetention)
	default:
		t.Fatalf("unknown field %q", name)
	}
	if name == "observation retention" || name == "automation retention" || name == "agent retention" {
		parsed, err := time.ParseDuration(want)
		if err != nil {
			t.Fatal(err)
		}
		want = parsed.String()
	}
	if got != want {
		t.Fatalf("%s = %q, want %q", name, got, want)
	}
}

func durationString(t *testing.T, got time.Duration) string {
	t.Helper()
	return got.String()
}
