# Core CLI configuration

Status: Implemented in PR #140. Scope: `hearthd` only. This document records the current design, replacing the original `cli-altsrc` proposal. Adapter and simulator configuration remain separate.

## Decision

Core resolves each setting as **CLI flag > `HEARTHD_` environment variable > local YAML > built-in default**. `urfave/cli/v3` handles flags, environment sources, help, and typed duration/integer parsing. `cmd/hearthd/cli_config.go` decodes one YAML document directly into `hearthd.Config`, then applies explicitly set CLI/env values. `internal/app/hearthd/config.go` owns the types, normalization, validation, and secret-file read. The unused Core `LoadConfig`, `LoadConfigWithOverrides`, and `ConfigOverrides` APIs are removed; the simulator loader is unchanged.

The default path is `configs/hearthd.yaml`, selected with `--config` or `HEARTHD_CONFIG` (CLI wins). An absent **implicit** default file is allowed; an **explicitly** selected path must exist. A present file must decode successfully as one YAML document, including when every value is overridden. Unknown keys are ignored; duplicate keys, malformed YAML, and multiple documents fail. An empty file provides no values. Invalid YAML values or types fail decoding even when a higher-priority source would override them. No remote URI loading is supported.

`urfave/cli` accepts its supported flag spellings, including one-dash long flags; examples use `--`. Invalid typed CLI/env values fail during CLI parsing before application logging, with a standard CLI diagnostic rather than `process.failed`. YAML and domain-validation failures emit `process.failed` with `error_code=config_invalid` and `stage=load_config`. Logging-option errors are printed once before Core startup. File paths, config values, and secret contents must not appear in process failure records. `--help` does not need a config or secret.

## Settings

| YAML key | Flag | Environment | Default |
| --- | --- | --- | --- |
| `household_timezone` | `--household-timezone` | `HEARTHD_HOUSEHOLD_TIMEZONE` | required |
| `http_addr` | `--http-addr` | `HEARTHD_HTTP_ADDR` | required |
| `nats_url` | `--nats-url` | `HEARTHD_NATS_URL` | required |
| `sqlite_path` | `--sqlite-path` | `HEARTHD_SQLITE_PATH` | required |
| `observation_retention` | `--observation-retention` | `HEARTHD_OBSERVATION_RETENTION` | 720h; zero selects 720h |
| `automation_history_retention` | `--automation-history-retention` | `HEARTHD_AUTOMATION_HISTORY_RETENTION` | 720h; zero selects 720h |
| `agent.api_key_file` | `--agent-api-key-file` | `HEARTHD_AGENT_API_KEY_FILE` | required |
| `agent.model` | `--agent-model` | `HEARTHD_AGENT_MODEL` | `gpt-5.6-luna` |
| `agent.base_url` | `--agent-base-url` | `HEARTHD_AGENT_BASE_URL` | provider default |
| `agent.reasoning_effort` | `--agent-reasoning-effort` | `HEARTHD_AGENT_REASONING_EFFORT` | `none` for default model; provider default otherwise |
| `agent.max_steps` | `--agent-max-steps` | `HEARTHD_AGENT_MAX_STEPS` | zero, interpreted by agent module as 20 |
| `agent.history_retention` | `--agent-history-retention` | `HEARTHD_AGENT_HISTORY_RETENTION` | 720h; zero selects 720h |

Logging is process-only: `--log-level` / `HEARTHD_LOG_LEVEL` defaults to `info` and `--log-format` / `HEARTHD_LOG_FORMAT` defaults to `text`; neither has a YAML key. Durations use Go syntax (`720h`, not `30d`). Empty strings retain existing field semantics. Only the API key **file path** is configurable; the key value stays in a separate local secret file and is read during agent startup, not during config resolution.

## Boundaries and validation

- `cmd/hearthd/cli_config.go`: declare CLI/env sources; read YAML once; overlay only set flags; normalize, then call `Config.Validate()` once. Keep the existing process startup and logging lifecycle.
- `internal/app/hearthd/config.go`: retain `Config`, `AgentConfig`, `NormalizeConfig`, `Validate`, effective-default accessors, and `LoadAgentAPIKey`; no YAML-loading API for Core.
- `mise.toml` and operator examples use `--` flags; `configs/hearthd.simulator.yaml` intentionally supplies only the settings not injected by mise.
- `cmd/hearthd/cli_config_test.go` and `main_test.go`: test every source, flag ordering, empty/default settings, invalid file behavior, typed parse errors, help, and path/value-free failure logs. Direct `internal/app/hearthd/config_test.go` tests cover domain validation and secret loading. Use an injected run callback for CLI config tests; no NATS/SQLite/model dependency is needed there.

Run `mise run validate` as the final integrated gate. The default simulator stack has also been exercised with State, Command, Entity Event, and Core-offline recovery scenarios. Docker-only Mosquitto integration tests require a reachable Docker daemon.

## Accepted trade-offs

- Direct YAML decoding is simpler than a separate preflight and manual YAML-node lookup, but a present file must be type-correct even for overridden fields.
- Typed CLI flags reduce custom parsing, but their errors happen before the structured process logger starts.
- Unknown YAML keys are ignored; required-field validation catches omissions but cannot catch every typo in an optional key.
