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
| `entity_event.invalid`, `entity_event.processing_failed` | Invalid Entity Event input or processing/acknowledgement failure, including a failed termination of a permanently uninterpretable report |
| `entity_event.recorded`, `entity_event.identity_conflict` | Committed Entity Event disposition, or changed input for an already recorded event ID (Debug and Warn) |
| `entity_event.clock_skew` | Adapter Entity Event publication time is ahead of Core receive time; diagnostic only, never a rejection reason |
| `core.devices_history_prune_failed` | The startup or hourly devices history retention pass failed (`module=devices`); one safe app-owned record per failed pass, no raw error text |
| `automation.created`, `automation.replaced`, `automation.deleted` | A definition mutation committed; includes identity and revision, never definition JSON |
| `automation.run_started`, `automation.run_completed`, `automation.run_interrupted` | A Run was admitted or reached a durable terminal outcome |
| `automation.skipped` | An admission decision started no Run; `source` is `device_fact`, `manual`, `held_state`, or `schedule`, and `reason` is `automation_busy`, `stale_fact`, `conditions_false`, or `conditions_unknown`. Only Device Facts use `stale_fact` |
| `automation.fact_invalid`, `automation.fact_processing_failed` | The automation consumer rejected malformed input or could not durably admit valid input |
| `automation.executor_fault` | Command ownership or Automation progress could not be established safely; only that Run stops, with one atomic scoped interruption attempt |
| `automation.run_interruption_failed` | The single scoped cleanup attempt failed; that Automation may remain busy until startup recovery, while independent Automations remain eligible |
| `core.automation_history_prune_failed` | The startup or hourly Automation history retention pass failed (`module=automations`); one safe app-owned record per failed pass, no raw error text |
| `core.automation_schedule_failed` | Unexpected calendar worker failure closes Automation admission and fails readiness; Error-level record with fixed `error_code=automation_schedule_failed`, no raw error text. Normal shutdown cancellation does not emit it |
| `agent.turn_failed` | One unexpected household agent turn failure, with the canonical `conversation_id`, a fixed `stage` (`queue`, `history`, `persist`, `model`, `tool`, `graph`), and a fixed `error_code` (`conversation_not_found`, `admission_unavailable`, `model_call_failed`, `model_response_invalid`, `tool_call_failed`, `trace_persist_failed`, or `turn_failed`); expected caller/Drain cancellation or caller expiry produces no failure record. Dependency context errors retain their origin. Provider, tool, and persistence error text is never logged |
| `mcp.tool_internal_failure` | One retained unexpected MCP handler failure, with the tool name, fixed `error_code`, and Go error type; no raw cause text. A single-cause wrapping chain ending in the canceled or expired request's context error produces no failure record. Dependency cancellation with an active caller and joined or unrelated faults remain diagnostic, even after caller cancellation. Tool result codes and SDK disconnect behavior are unchanged |
| `notification.http_internal_failure` | Unexpected Notification HTTP failure, with the fixed operation ID, `error_code=internal_error`, and Go error type; no raw error text or retained data. Caller cancellation alone does not emit it; joined persistence failures still do |
| `device.http_internal_failure`, `adapter.http_internal_failure`, `automation.http_internal_failure`, `agent.http_internal_failure` | Unexpected HTTP operation failure not already diagnosed by its service or worker. Includes a fixed operation ID, `stage=service`, `mapping`, or `preflight`, `error_code=internal_error`, and Go error type. No raw cause text or payload fields. Expected request cancellation stays silent; dependency context failures and joined faults remain diagnostic |
| `device_fact.retry` | Warn-level relay retry: a pending fact was not published and its durable outbox row was kept, with `stage` (`list`, `publish`, `ack`, `delete`) and a fixed `error_code` (`list_failed`, `publish_failed`, `ack_missing`, `unexpected_stream`, `delete_failed`), plus `family` and the safe source ID (`observation_id` or `event_id`) when the failing stage identified a row |
| `device_fact.poison` | Error-level relay fault: a deterministic row Core cannot map or decode stopped publication, with `stage` (`list`, `map`, `encode`), fixed `error_code` (`invalid_row`, `fact_invalid`, `subject_invalid`, `unknown_family`, `encode_failed`) and the `fact_id`, plus `family` and the safe source ID when the row decoded that far; the row is preserved and readiness fails until an operator resolves it |
| `simulator.entity_event_input_dropped`, `simulator.entity_event_input_failed` | Standard input typed while a report was publishing, or a report that was not published |
| `dependency.retrying` | Debug-level retry attempt with safe diagnostic code |
| `dependency.connected`, `dependency.disconnected`, `dependency.reconnected`, `dependency.operation_failed`, `dependency.closed` | NATS connection lifecycle for the one shared Core connection, which carries subscriptions, JetStream ingestion and Device Fact publication |
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
curl http://127.0.0.1:8080/v1/automations/aut_.../history
curl http://127.0.0.1:8080/v1/automations/aut_.../history/arn_...
curl http://127.0.0.1:8080/readyz
curl http://127.0.0.1:8080/v1/adapters/simulator
curl http://127.0.0.1:8080/v1/entities/ent_...
```

`GET /v1/entities/{entity_id}/events` is the durable Entity Event history for one Entity: it shows whether Core recorded a report and why Core rejected it, and it never contains State. A missing row does not prove that no physical event happened, and the code does not execute work. `entity_event.recorded` and `entity_event.identity_conflict` are diagnostics only; the history endpoint is authoritative for what Core retained.

Device Facts follow the same rule. `device_fact.retry` reports a transient failure whose durable outbox row is kept and retried, and `device_fact.poison` reports the deterministic row that stopped the relay and failed readiness; neither contains State values, raw envelopes or full subjects, and neither reconstructs durable fact delivery. Command status transitions have no fact to diagnose: Command outcomes appear only in durable HTTP/SQLite history. The stream `HEARTH_DEVICE_FACTS_V1` stores what Core published, and the durable SQLite record and HTTP read APIs stay authoritative, so a missing fact — a reader that never chose a durable consumer, a fact evicted past the seven-day or one-GiB bound, or a poison row an operator has not resolved — is expected under the documented durability limits, not a logging failure. Investigate `device_fact.poison` first: it means the relay stopped and every later pending fact is queued behind the preserved row.

Automation logs are likewise diagnostics rather than execution history. Use the Automation history endpoints for immutable Run snapshots, matched Trigger IDs, Fact summaries, Step outcomes, Skips, and verified Command links. Automation records may include canonical IDs, family, disposition or event name, revision, status, and fixed reason/error codes; they never include definition JSON, Observation values, Command parameters, full subjects, raw envelopes, or upstream error text.

[Scheduled admission](../specs/scheduled-automation-triggers.md) uses the ordinary Run/Skip logs with `source=schedule`, without Fact or Held-State evidence. Missed occurrences have no history entries or dedicated logs, and no-match ticks produce no per-second logs. A failed calendar worker is not silently restarted and does not replay missed minutes.

`command.created` supplies an ID for lookup when the HTTP caller disconnects or gets an error. For running Commands it is deferred until execution returns, so diagnostic output cannot delay dispatch or the caller's cancellation handling; it may follow other Command records. Immediately rejected creations are logged before returning. Command history supplies the durable satisfied/rejected/timeout/interrupted outcome and linked Observation ID; no terminal log record is promised. Acceptance is **not** satisfaction: only committed linked Observation evidence satisfies a Command.

Silence is not current-health evidence. Reconnection does not prove upstream reconciliation or Entity availability. `/readyz` defines Core dependency readiness; Adapter and Entity reads describe their evidence. Logs do not reproduce those state machines or the lease-expiry grace sequence.

Logs are best-effort diagnostics, not authoritative State, durable Command history, or proof of physical causation. A crash may lose a record after commit. An optional `process.stopped` record means Run returned successfully, not that every cleanup succeeded. No complete teardown sequence is promised.

## Developer conventions and safety

### HTTP failure ownership

Module HTTP handlers diagnose unexpected management, read, admission, and response-mapping failures before rendering safe problems. Causes remain private until that decision and are not stored in returned problems after diagnosis. Application assembly injects component-scoped loggers; it installs no failure access logger or global Huma error factory.

Existing owners take precedence. Command workers keep `command.execution_failed` for unexpected execution failures returned as `CommandExecutionError`. Manual Automation admission keeps its service-owned `automation.condition_state_corrupt` record. Agent turns keep `agent.turn_failed`; Agent HTTP owns only conversation operations and stream preflight. Notification HTTP remains its operation-error owner. These paths produce no second HTTP diagnostic for the same owned failure. Durable rejected or uncertain outcomes retain their existing API representation rather than becoming generic failures.

Expected request cancellation requires an ended owner and a single-cause wrapping chain ending in that owner's exact context error. A joined or unrelated operational fault remains diagnostic even after caller cancellation. This affects diagnostic silence, not public HTTP status, receipt, or cancellation contracts.

### Shared conventions

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
