# Application logging

**Logs explain startup and failures. APIs provide current health and durable Command outcomes.**

Hearth executables write structured, one-record-per-line logs to stderr.

```sh
log_dir=$(mktemp -d /tmp/hearth-logs.XXXXXX)
go run ./cmd/hearthd --config configs/hearthd.yaml --log-format json 2>"$log_dir/core.log"
go run ./cmd/hearth-simulator --config configs/simulator.yaml --log-format json 2>"$log_dir/simulator.log"
```

`--log-level` accepts `debug`, `info` (default), `warn`, or `error`. `--log-format` accepts `text` (default) or `json`. Values are case-sensitive. Invalid logging options fail before configuration is read or connections open. YAML configuration is unchanged.

## Find startup and failure evidence

| Event | Meaning |
| --- | --- |
| `process.starting`, `process.config_loaded` | Process identity and successful configuration load |
| `core.startup_stage_completed` | A startup step succeeded; no interruption count is implied |
| `core.http_listening` | HTTP socket successfully bound, not a health assessment |
| `adapter.session_claimed`, `adapter.registration_completed` | Claimed runtime and canonical registered identities |
| `adapter.commands_listening` | Command subscription ready, not upstream health |
| `simulator.initialized` | Scenario initialization and canonical Entity ID |
| `command.created` | Durable creation, including immediate rejection; deferred until execution returns for running Commands, not a startup or success signal |
| `command.execution_failed` | Unexpected execution/persistence failure, not a terminal status summary |
| `observation.invalid`, `observation.processing_failed` | Invalid input or processing/acknowledgement failure |
| `device_event.invalid`, `device_event.processing_failed` | Invalid Device Event input or processing/acknowledgement failure |
| `device_event.recorded`, `device_event.identity_conflict` | Committed Device Event disposition, or changed input for an already recorded event ID (Debug and Warn) |
| `device_event.clock_skew` | Adapter Device Event publication time is ahead of Core receive time; diagnostic only, never a rejection reason |
| `core.device_events_prune_failed` | The hourly Device Event retention sweep failed |
| `simulator.device_event_input_dropped`, `simulator.device_event_input_failed` | Standard input typed while a report was publishing, or a report that was not published |
| `dependency.retrying` | Debug-level retry attempt with safe diagnostic code |
| `process.cleanup_failed` | An otherwise ignored cleanup operation failed |
| `process.failed` | Fatal configuration or runtime failure, with safe stage/code |

```sh
jq -c 'select(.event == "core.http_listening")' "$log_dir/core.log"
jq -c 'select(.event == "adapter.registration_completed")' "$log_dir/simulator.log"
jq -c 'select(.level == "ERROR" or .level == "WARN")' "$log_dir/core.log" "$log_dir/simulator.log"
jq -c --arg id 'cmd_...' 'select(.command_id == $id)' "$log_dir/core.log" "$log_dir/simulator.log"
```

Debug adds routine transport/Observation progress and retry attempts. There is no retry-episode warning/recovery summary. Restart the affected process with `--log-level debug` for focused investigation; restarting an Adapter creates a new runtime ID and may wait for its previous lease.

## Use APIs for outcomes and health

Use the registered canonical Entity ID with the existing HTTP API:

```sh
curl -X POST http://127.0.0.1:8080/v1/entities/ent_.../commands \
  -H 'content-type: application/json' \
  -d '{"operation":"set","parameters":{"value":true}}'
curl http://127.0.0.1:8080/v1/commands/cmd_...
curl http://127.0.0.1:8080/v1/entities/ent_.../commands
curl 'http://127.0.0.1:8080/v1/entities/ent_.../events?limit=50'
curl http://127.0.0.1:8080/readyz
curl http://127.0.0.1:8080/v1/adapters/simulator
curl http://127.0.0.1:8080/v1/entities/ent_...
```

`GET /v1/entities/{entity_id}/events` is the durable Device Event history for one Entity: it shows whether Core recorded a report and why Core rejected it, and it never contains State. A missing row does not prove that no physical event happened, and the code does not execute work. `device_event.recorded` and `device_event.identity_conflict` are diagnostics only; the history endpoint is authoritative for what Core retained.

`command.created` supplies an ID for lookup when the HTTP caller disconnects or gets an error. For running Commands it is deferred until execution returns, so diagnostic output cannot delay dispatch or the caller's cancellation handling; it may follow other Command records. Immediately rejected creations are logged before returning. Command history supplies the durable satisfied/rejected/timeout/interrupted outcome and linked Observation ID; no terminal log record is promised. Acceptance is **not** satisfaction: only committed linked Observation evidence satisfies a Command.

Silence is not current-health evidence. Reconnection does not prove upstream reconciliation or Entity availability. `/readyz` defines Core dependency readiness; Adapter and Entity reads describe their evidence. Logs do not reproduce those state machines or the lease-expiry grace sequence.

Logs are best-effort diagnostics, not authoritative State, durable Command history, or proof of physical causation. A crash may lose a record after commit. An optional `process.stopped` record means Run returned successfully, not that every cleanup succeeded. No complete teardown sequence is promised.

## Developer conventions and safety

- Use injected `*slog.Logger` directly. The shared factory attaches `app` and `pid` without changing the global default; subsystem boundaries attach one `component`. Keep fields flat and unique.
- Use typed attributes (`slog.String`, `slog.Int`, `slog.Int64`, etc.), not alternating key/value arguments. Attach fields shared by multiple records with a child `logger.With`; keep event-specific fields at the emission site. Preserve integer milliseconds with `slog.Int64`, rather than changing the field to a duration string.
- Write whole, stable lower-case dotted `event` literals with short messages. Add existing canonical IDs and safe typed/fixed codes where useful. Omit unknown fields.
- Always use context-aware slog methods with the actual operation context, including asynchronous work. Do not carry loggers in context or manually attach trace/span IDs.
- **Do not add logging-only state:** no health/readiness snapshots, retry counters or warned-class sets, recovery flags, timing loops, or channels enforcing log ordering. Log ordinary retries at Debug; use existing successful boundaries for milestones.
- Preserve business behavior, cancellation, deadlines, waiter ordering, fencing, and persistence. Do not add repository reads or private terminal-outcome plumbing just for diagnostics.
- Emit success only after the owning operation succeeds. Normal cancellation is not an Error; diagnose swallowed unexpected failures safely where recovery decisions occur.
- Never log credentials, token paths/contents, authorization headers, full config, upstream URLs, full subjects/topics, bodies, raw envelopes, parameters, or State—even at Debug. Prefer canonical IDs and available counts over vendor identities/display names.
- Unknown error strings can contain secrets or rejected values. Use fixed codes and safe stage/operation metadata, not raw errors or regex redaction. NATS asynchronous errors must also pass through a safe handler.
- Keep tests small: options/formatting, a few positive and negative startup boundaries, sensitive-input sentinels, and operation-context preservation. Preserve business tests; do not duplicate lifecycle matrices just to assert log sequences. Use locked recorders for concurrent logs and `mise run validate` for checks.

For example, when assembly has already attached the component:

```go
commandLogger := logger.With(
    slog.String("command_id", string(command.ID)),
    slog.String("entity_id", string(command.EntityID)),
)
commandLogger.InfoContext(ctx, "command created",
    slog.String("event", "command.created"),
)
commandLogger.DebugContext(ctx, "dispatching command",
    slog.String("event", "command.dispatched"),
    slog.String("runtime_id", string(runtimeID)),
)
```

`With` creates a child without mutating its parent. Do not repeat inherited keys, introduce logger-in-context plumbing, or add a helper solely to wrap one log call. Dynamic attribute lists use `[]slog.Attr` with `LogAttrs`.

Log captures still contain household identity metadata. Keep them outside the repository and review before sharing. Collection infrastructure, rotation, sampling, HTTP access logging, and OpenTelemetry export remain outside scope.
