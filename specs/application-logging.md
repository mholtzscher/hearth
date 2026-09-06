# Application logging

**Status:** Implemented; simplified by explicit maintainer request.
**Scope:** `hearthd`, `hearth-simulator`, `hearth-adapter-homeassistant`, and `hearth-adapter-zigbee2mqtt`, including their SDK and runtime paths.

## Goal and scope decision

**Logs explain startup and actionable failures. APIs provide current health and durable Command outcomes.**

An operator should be able to identify the process, see successful startup milestones, and investigate failures using safe diagnostic codes and canonical IDs. Logs do not need to reconstruct a complete Command, connection, health, or teardown lifecycle.

This deliberately replaces the previous requirement for terminal Command summaries, acknowledged health-transition logs, readiness/grace events, and retry-episode tracking. Those guarantees duplicated business state and required logging-only state machines and extensive concurrency tests.

Logs are best-effort diagnostic evidence, not authoritative State, a health probe, durable Command history, or proof of physical causation. Use `/readyz`, Adapter/Entity reads, and Command-history APIs for those purposes. Silence is not current-health evidence.

## Configuration and handler

Every executable accepts these case-sensitive CLI flags alongside the unchanged `--config` flag:

| Flag | Default | Allowed values |
| --- | --- | --- |
| `--log-level` | `info` | `debug`, `info`, `warn`, `error` |
| `--log-format` | `text` | `text`, `json` |

Invalid logging options fail before configuration is read or connections open, with a concise nonzero-exit stderr diagnostic listing allowed values without echoing input. YAML configuration shapes do not change.

`internal/platform/logging` owns the shared factory:

```go
// LogOptions controls process log output; zero values select info and text.
type LogOptions struct {
    Level  string
    Format string
}

// NewApplicationLogger creates a logger without changing the global default.
// The caller owns output; logger writes never close it.
func NewApplicationLogger(output io.Writer, app string, options LogOptions) (*slog.Logger, error)
```

Require a non-nil writer. Use standard-library text/JSON handlers with the same threshold and one record per line, writing to stderr in production. Attach trusted executable `app` and integer `pid` once. Do not mutate `slog.Default`, hand-build records, add a logger interface, or carry loggers in context.

Existing constructor injection remains. `devices.Dependencies.Logger` stays optional, falling back to the default logger with `component=devices`; Core explicitly injects its devices-scoped logger. SDK public signatures remain unchanged.

## Emission conventions

Each application record carries a short human-readable message, a whole lower-case dotted `event` literal, and one bounded `component`: `process`, `core`, `devices`, `nats`, `adapter_session`, `zigbee2mqtt`, `homeassistant`, or `simulator`.

Use typed slog attributes (`slog.String`, `slog.Int`, `slog.Int64`, etc.). Use `With` for fields shared by multiple records in a subsystem or operation; keep event-specific attributes at emission sites. Dynamic lists use `[]slog.Attr` and `LogAttrs`. Preserve existing field types, including integer milliseconds. Do not add logger plumbing or wrappers merely for single-use fields.

Set component at the owning subsystem boundary. Avoid duplicate attributes. Scope canonical Adapter/runtime identities only when known; a runtime ID labels the actual claimed session, never the process generally. Operation records may carry existing command/correlation/observation/Entity IDs. Omit unknown IDs and absent optional codes.

Always use context-aware slog methods with the actual operation context. Retain context values in existing asynchronous work without changing its cancellation, deadlines, or lifetime. Do not manually attach trace/span IDs; future handler-level telemetry integration is outside scope.

| Level | Use |
| --- | --- |
| Debug | Routine transport/Observation progress and every retry attempt |
| Info | Successful startup/registration/reconciliation milestones and durable Command creation |
| Warn | Permanent invalid external input, expected rejection, useful connection-loss diagnostics, cleanup failures |
| Error | Fatal process failure or unexpected operation/persistence/acknowledgement failure requiring investigation |

### Keep emissions stateless

- Do not add logging-only remembered health/readiness states, counters, warned-code sets, recovery flags, timers, or ordering channels.
- Existing retry loops may emit `dependency.retrying` at Debug with fixed dependency/operation/error code and the already-known next delay. No first-failure Warn policy, attempt count, elapsed episode duration, or recovered event is required.
- Existing connection callbacks may report actual connection failures using safe fields. A reconnect does not assert restored Adapter health. No complete connected/disconnected/reconnected/closed sequence is required.
- Normal cancellation is not an Error. Use available context and existing lifecycle state to avoid false shutdown diagnostics, rather than introducing a second lifecycle for logging.
- Do not require a `process.stopping` event before every resource cleanup, or an event for every health/grace transition.
- Ordinary successful heartbeats and availability reports remain silent at Info.

## Useful event ownership

This is a small vocabulary of useful evidence, not an exhaustive required lifecycle sequence. Additional migrated failure sites may use their own whole, bounded event literals.

| Event | Owner / boundary |
| --- | --- |
| `process.starting`, `process.config_loaded` | Executable before config load / after successful validation |
| `process.failed` | Executable's fatal config/Run diagnostic, safe stage and code; never raw error text |
| `process.stopped` | Optional executable event after Run returns successfully; not proof all cleanup succeeded |
| `process.cleanup_failed` | Owner of an otherwise ignored cleanup failure; retain return semantics |
| `core.startup_stage_completed` | Core after successful migration, interruption, provisioning, or server/consumer start |
| `core.http_listening` | Core only after the HTTP socket is bound; acquire and correctly close a listener |
| `adapter.session_claimed`, `adapter.registration_completed`, `adapter.commands_listening` | SDK after the owning operation succeeds; canonical registration IDs remain discoverable |
| `simulator.initialized` | Simulator after registration/initialization, with scenario and canonical Entity ID |
| `adapter.reconcile_completed` | Optional aggregate summary from successfully activated work; no extra queries |
| `command.created` | Devices Service after creation commits, including immediately rejected records; include `command_id` |
| `command.dispatched` | Debug attempt before transport dispatch, not delivery proof |
| `command.execution_failed` | Safe unexpected execution/persistence diagnostic, including swallowed async failures; not a durable terminal summary |
| `observation.invalid`, `observation.processing_failed` | Core transport's safe validation, commit, metadata, or acknowledgement failures |
| `observation.clock_skew` | Existing safe timestamp diagnosis |
| `dependency.retrying` | Debug at the existing retry boundary, without episode tracking |

Routine SDK receipt/acceptance and acknowledged Observation publication/projection may remain Debug where useful for investigation. Expected rejection may be Warn with a typed code. None of these records replaces Command history.

There is **no `command.completed` guarantee or terminal-log ordering contract**. Do not carry committed statuses through private results solely to log them, re-read the database for diagnostics, or alter waiter/delivery behavior for log ordering. Existing persistence and HTTP APIs own satisfied/rejected/timeout/interrupted outcomes. An acceptance alone is never described as satisfaction.

## Safety (all levels)

- Never log credentials, authorization headers, token-file paths/contents, full configuration, raw envelopes, parameters, State values, protocol bodies, or MQTT payloads.
- Do not log configured upstream URLs or full subjects/topics. Core may log its validated `http_addr`.
- Prefer canonical IDs and existing aggregate counts over vendor IDs, display names, friendly names, and IEEE addresses. Do not introduce database queries for counts or summaries.
- Unknown errors may contain secrets or rejected values. Use fixed `error_code`, safe `stage`/`operation`, and typed domain codes rather than arbitrary error text. Do not implement generic regex redaction.
- Install safe NATS asynchronous-error handlers where the default client handler would print raw errors/subjects directly to stderr.
- Success events must follow the owning successful operation. Failed persistence must not claim completion. A successful close attempt alone does not justify a success event.

## Verification and implementation discipline

Keep tests proportional to the reduced contract:

1. Factory levels/formats, safe invalid options, root identity, and unchanged global logger.
2. Executable bootstrap diagnostics before dependency creation; a small shared harness is sufficient.
3. A few owning-path tests for canonical startup IDs and negative milestones (especially failed HTTP bind).
4. Sentinel tests protecting unsafe boundaries and context preservation, including swallowed async failures after caller cancellation.
5. Preserve existing business tests for persistence, cancellation, races, fencing, readiness/grace behavior, upstream recovery, and acknowledgement/redelivery.

Remove tests and helper scaffolding whose only purpose is proving discarded log sequences, retry deduplication, terminal summaries, or logging-only state transitions. Do not duplicate a business integration matrix just to assert log records. Use concurrency-safe recording handlers/writers when needed; do not read buffers while goroutines write.

Run `mise run validate`, review generated/formatting changes, and monitor PR checks. No schema, repository interface, public HTTP model, protocol, persistence semantic, or physical-device change is needed. No collector, exporter, new span, rotation, dynamic level system, HTTP access log, or sampling framework is introduced.

## Operator workflow

Run the existing local NATS/Core/simulator workflow. Find `core.http_listening`, canonical registration IDs, and `simulator.initialized`. Send a Command, use `command.created` to find its ID, and query `/v1/commands/{command_id}` for its durable outcome. Use `/readyz` and Adapter/Entity reads for current health. Restart with Debug only when routine transport/Observation/retry progress is needed.

See `docs/logging.md` for concrete commands. Keep log captures outside the repository; they still contain household identity metadata.
