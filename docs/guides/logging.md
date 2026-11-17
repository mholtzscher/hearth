# Application logging

**Logs explain startup and failures. APIs provide current health and durable Command outcomes.**

Hearth executables write structured, one-record-per-line logs to stderr.
Use a configured local NATS service and supply the selected Core provider's
credential through your supervisor or secret manager before launch. Prepare
private configuration from the examples, then edit broker addresses and Core
settings for this installation:

```sh
cp -n configs/hearthd.example.yaml configs/hearthd.yaml
cp -n configs/simulator.example.yaml configs/simulator.yaml
```

In the first terminal, create an external log directory and print its path, then
start Core. The process runs in the foreground; stop it with Ctrl+C when
collection is complete.

```sh
log_dir=$(mktemp -d /tmp/hearth-logs.XXXXXX)
printf '%s\n' "$log_dir"
go run ./cmd/hearthd --config configs/hearthd.yaml --log-format json 2>"$log_dir/core.log"
```

In a second terminal, replace the example path with the printed directory and
start the simulator. Stop it with Ctrl+C when collection is complete.

```sh
log_dir=/tmp/hearth-logs.REPLACE
go run ./cmd/hearth-simulator --config configs/simulator.yaml --log-format json 2>"$log_dir/simulator.log"
```

`--log-level` accepts `debug`, `info` (default), `warn`, or `error`. `--log-format` accepts `text` (default) or `json`. Values are case-sensitive. Invalid logging options fail before configuration is read or connections open. YAML configuration is unchanged.

## Find startup and failure evidence

Filter by `event`, canonical IDs, `stage`, and fixed `error_code`, not message
text. For the current event fields, inspect the owning emission site rather than
a copied catalog:

```sh
git grep -n '"agent.turn_failed"' -- internal
git grep -n '"device_fact.poison"' -- internal
```

`core.http_listening` proves a bound socket, not readiness. Likewise, Adapter
registration and Command subscriptions do not prove upstream health.
`agent.turn_failed` diagnoses unexpected turns without provider/tool/persistence
error text. Expected caller/Drain cancellation and caller expiry stay silent;
dependency context errors retain their failure origin rather than implying caller
cancellation. A transient provider status is reported after its single retry
fails. See [Agent failure semantics](../reference/agent-skills.md#public-turn-failures).
Registration and Session persistence faults remain no-reply outcomes, not modeled
rejections.

In a third terminal, set `log_dir` to that same printed directory before filtering:

```sh
log_dir=/tmp/hearth-logs.REPLACE
jq -c 'select(.event == "core.http_listening")' "$log_dir/core.log"
jq -c 'select(.event == "adapter.registration_completed")' "$log_dir/simulator.log"
jq -c 'select(.level == "ERROR" or .level == "WARN")' "$log_dir/core.log" "$log_dir/simulator.log"
jq -c --arg id 'cmd_...' 'select(.command_id == $id)' "$log_dir/core.log" "$log_dir/simulator.log"
```

Debug adds routine transport/Observation progress and retry attempts. There is no retry-episode warning/recovery summary. Restart the affected process with `--log-level debug` for focused investigation; restarting an Adapter creates a new runtime ID and may wait for its previous lease.

## Use APIs for outcomes and health

Use read APIs with canonical IDs from the operation you are investigating.
These examples do not submit new Commands or Notifications:

```sh
curl http://127.0.0.1:8080/v1/commands/cmd_...
curl http://127.0.0.1:8080/v1/entities/ent_.../commands
curl 'http://127.0.0.1:8080/v1/entities/ent_.../events?limit=50'
curl http://127.0.0.1:8080/v1/automations/aut_.../history
curl http://127.0.0.1:8080/v1/automations/aut_.../history/arn_...
curl http://127.0.0.1:8080/readyz
curl http://127.0.0.1:8080/v1/adapters/simulator
curl http://127.0.0.1:8080/v1/entities/ent_...
```

Entity Event history records accepted and rejected reports, never State. Missing
history does not prove that no physical event happened. Clock skew is diagnostic,
not a rejection reason. See
[Entity Event history](../architecture/architecture.md#entity-event-durable-history).

Investigate `device_fact.poison` first: a preserved deterministic row stops the
relay, fails readiness, and queues later Facts. `device_fact.retry` keeps the row
for another publication attempt. Logs cannot reconstruct delivery. Durable readers
and retention limits belong to
[Device Fact delivery](../architecture/architecture.md#device-fact-delivery-and-recovery);
Command outcomes have no Fact and remain in HTTP/SQLite history.

Use Automation history for immutable snapshots, matched Triggers, Steps, Skips,
and Command links. An executor fault stops only its Run with one scoped
interruption attempt; failed cleanup can leave that Automation busy until startup
recovery. A schedule-worker fault instead logs `core.automation_schedule_failed`,
closes Automation admission, and fails readiness. Missed schedule occurrences have no history or replay, and no-match
ticks have no per-second logs. See [schedules](../reference/automation-schedules.md)
and [Automation ownership](../../internal/modules/automations/README.md).

Decision logs may carry canonical IDs, family, disposition/event, revision,
status, and fixed codes. Never include definition JSON, Fact/Observation values,
Command parameters, full subjects, raw envelopes, or upstream error text.

`command.created` supplies an ID for lookup when the HTTP caller disconnects or gets an error. For running Commands it is deferred until execution returns, so diagnostic output cannot delay dispatch or the caller's cancellation handling; it may follow other Command records. Immediately rejected creations are logged before returning. Command history supplies the durable satisfied/rejected/timeout/interrupted outcome and linked Observation ID; no terminal log record is promised. Acceptance is **not** satisfaction: only committed linked Observation evidence satisfies a Command.

Silence is not current-health evidence. Reconnection does not prove upstream reconciliation or Entity availability. `/readyz` defines Core dependency readiness; Adapter and Entity reads describe their evidence. Logs do not reproduce those state machines or the lease-expiry grace sequence.

Logs are best-effort diagnostics, not authoritative State, durable Command history, or proof of physical causation. A crash may lose a record after commit. An optional `process.stopped` record means Run returned successfully, not that every cleanup succeeded. No complete teardown sequence is promised.

## Measure agent prompt caching

Run Core with `--log-level debug --log-format json` and capture stderr outside the
repository. Hold a multi-turn conversation without changing the provider, model,
reasoning setting, or tools. Then compute the token-weighted cache-hit rate for
that conversation:

```sh
jq -s --arg id 'aconv_...' '
  [.[] | select(.event == "agent.model_usage" and .conversation_id == $id)]
  | {calls: length, input: (map(.input_tokens) | add),
     cached: (map(.cached_input_tokens) | add)}
  | .hit_rate = (if .input > 0 then .cached / .input else null end)
' core.jsonl
```

Sum the tokens rather than averaging per-call percentages. The first call in a
conversation usually reports no hits. These counts cover completed responses only,
so use provider billing for total charges. The same counts are also stored in each
assistant message's `response_meta.usage` in `agent_messages.message_json`.
`agent.model_usage` is Debug-only and includes completed tool-calling responses.
Missing provider counts are zero; OpenCode Go does not report cache writes.

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
