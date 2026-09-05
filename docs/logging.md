# Application logging

All four executables (`hearthd`, `hearth-simulator`, `hearth-adapter-homeassistant`, and `hearth-adapter-zigbee2mqtt`) write structured, one-record-per-line logs to stderr.

```sh
go run ./cmd/hearthd --config configs/hearthd.yaml --log-format json 2>core.log
go run ./cmd/hearth-simulator --config configs/simulator.yaml --log-format json 2>simulator.log
```

`--log-level` accepts `debug`, `info` (default), `warn`, or `error`. `--log-format` accepts `text` (default) or `json`. Values are case-sensitive. Invalid logging options fail before configuration is read or connections open. YAML configuration is unchanged. There is no dynamic level switching: restart the affected process with `--log-level debug` for Observation and protocol progress. Restarting an Adapter creates a new runtime ID and may wait for the previous lease.

## Find the evidence

| Event | Meaning |
| --- | --- |
| `process.starting`, `process.config_loaded` | Process identity and successful configuration load |
| `core.startup_stage_completed`, `core.http_listening` | Completed startup work and a successfully bound HTTP socket |
| `core.readiness_changed` | Dependency readiness as defined by `/readyz`, not Entity availability |
| `core.lease_expiry_resumed` | Recovery grace ended and lease-expiry polling resumed |
| `dependency.retrying`, `dependency.recovered` | Failed operation retry episode and actual operation recovery |
| `dependency.disconnected`, `dependency.reconnected` | Transport loss/reconnection, not restored Adapter health |
| `adapter.session_claimed`, `adapter.registration_completed` | Claimed runtime and canonical registered identities |
| `adapter.upstream_ready`, `adapter.health_reported` | Reconciled upstream and acknowledged health evidence |
| `simulator.initialized` | Scenario initialization and its canonical Entity ID |
| `command.created`, `command.received`, `command.accepted` | Durable creation, validated delivery, and acknowledged acceptance |
| `command.completed`, `command.execution_failed` | Known durable terminal outcome or unexpected execution failure |
| `observation.published`, `observation.projected` | Debug-level acknowledged publication and committed projection |
| `observation.invalid`, `observation.processing_failed` | Invalid input or processing/acknowledgement failure |
| `process.stopping`, `process.stopped`, `process.cleanup_failed` | Teardown, returned execution, or a failed cleanup operation |
| `process.failed` | Fatal configuration or runtime failure, with a safe stage/code |

Filter JSON output by event or business lookup key:

```sh
jq -c 'select(.event == "core.readiness_changed")' core.log
jq -c 'select(.event == "adapter.registration_completed")' simulator.log
jq -c --arg id 'cmd_...' 'select(.command_id == $id)' core.log simulator.log
jq -c 'select(.event == "command.completed") | {command_id, status, failure_code, observation_id}' core.log
```

Use the registered canonical Entity ID with the existing HTTP API:

```sh
curl -X POST http://127.0.0.1:8080/v1/entities/ent_.../commands \
  -H 'content-type: application/json' \
  -d '{"operation":"set","parameters":{"value":true}}'
curl http://127.0.0.1:8080/v1/commands/cmd_...
```

Search both process logs for the created `command_id`. A happy Command has SDK receipt/acceptance and one core `command.completed` with `status=satisfied` and its satisfying `observation_id`. Debug adds linked publication/projection records. Acceptance is **not** satisfaction; only committed linked Observation evidence satisfies a Command. Compare logs to the Command API/history when diagnosing rejected or timed-out scenarios. A crash can lose a log record after a commit, and startup interruption emits a stage, not invented per-Command outcomes or counts.

Silence is not evidence of current health. Use `/readyz` and Adapter/Entity reads. Reconnection does not mean the upstream has reconciled or that every Entity is available. `process.stopped` means Run and its deferred cleanup returned, not that every cleanup operation succeeded. Logs are best-effort diagnostics, not authoritative State, durable Command history, or proof of physical causation.

## Developer conventions and safety

- Use injected `*slog.Logger` directly. Executables construct the shared handler without mutating the global default, attach `app` and `pid` once, and pass the root logger to assembly.
- Set `component` once at each subsystem boundary. Scope validated Adapter/runtime identities to the actual claimed session. Keep attributes flat and unique; omit unknown IDs and optional codes rather than emitting empty placeholders.
- Write whole, stable lower-case dotted `event` literals at emission sites. Include a short human message and bounded component. Keep operation fields explicit, with `command_id` as the cross-process lookup key and existing correlation/causation IDs where available.
- Always use context-aware slog methods with the actual operation context, including asynchronous Command work. Do not carry loggers in context or replace operation context with a background context. Future trace enrichment belongs at the handler/OTel bridge boundary; do not manually attach trace/span IDs or add telemetry dependencies.
- Emit successful milestones only after their owning operation succeeds. The Devices Service alone summarizes terminal Commands; SDK acceptance and Observation processing are separate evidence. Failed persistence must not claim a durable completion.
- Routine Observations and repeated retry attempts are Debug. First retry failures are Warn, recovery is Info, and unchanged heartbeats/readiness polls are silent. Normal cancellation is not an Error.
- Never log credentials, token paths/contents, authorization headers, full configuration, configured upstream URLs, subjects/topics, protocol bodies, raw envelopes, parameters, or State values—even at Debug.
- Unknown errors can contain secrets or rejected values. Use fixed diagnostic codes and safe stage/operation metadata, not arbitrary upstream error strings or regex redaction. Prefer canonical IDs and aggregate counts over vendor identities or display names.
- Test parsed records and relevant attributes, not prose or cross-goroutine line order. Use concurrency-safe handlers/writers and preserve existing persistence, deadline, fencing, and acknowledgement assertions. Run `mise run validate`.

Log captures still contain household identity metadata. Do not commit them or share them without review. Collection infrastructure, log files/rotation, sampling, HTTP access logging, and OpenTelemetry export are outside this logging contract.
