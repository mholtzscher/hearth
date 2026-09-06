# Application logging

**Status:** Implemented; runtime implementation authorized by the end-to-end implementation request.
**Scope:** `hearthd`, `hearth-simulator`, `hearth-adapter-homeassistant`, and `hearth-adapter-zigbee2mqtt`, including their SDK and runtime paths.
**Effort:** L (approximately two days); no infrastructure deployment required.

## Problem and goals

An operator watching terminal or container logs cannot reliably tell what started, which dependency is blocking progress, whether a Command reached its Adapter, whether its outcome was observed, or whether a disconnected process recovered. The existing applications already pass `*slog.Logger`, but predominantly emit errors and isolated warnings.

Establish one logging pattern and adopt it across all four applications. At the default level, an operator must be able to:

1. Identify the application, startup progress, readiness/dependency transitions, and shutdown outcome.
2. Follow a Command from durable creation through Adapter acceptance to its actual terminal outcome using existing IDs.
3. Understand failure, retry, and recovery without opening raw protocol payloads.

Debug logging adds routine Observation and transport progress for focused investigations. Logs are best-effort diagnostic evidence, not authoritative State, a health probe, durable Command history, or proof of physical causation.

## Existing architecture and constraints

- Each `cmd/*/main.go` constructs a text `slog` handler writing to stderr with the default Info threshold. Only `--config` is exposed.
- All four `internal/app/*/run.go` entry points accept `Run(context.Context, Config, *slog.Logger) error`. Assembly owns connection creation and process lifecycle.
- `sdk/adapter.Config.Logger` already supplies the SDK logger. The SDK owns session claims, retries, heartbeats, fencing, Command serving, and Observation publication.
- `internal/modules/devices/nats` already accepts loggers for incoming transports. `devices.Service` currently has no logger; it owns asynchronous Command execution even after the HTTP caller disconnects.
- Command and Observation envelopes already carry correlation/causation IDs. Runtime-scoped subjects carry Adapter and runtime identity. W3C trace context is propagated today; no new correlation protocol is necessary.
- `/readyz` checks SQLite, NATS, JetStream resources, and the Observation consumer. `health_supervisor.go` polls that checker and pauses lease expiry while unready.
- Command acceptance is not satisfaction. Only committed linked Observation evidence can satisfy a Command.
- Registration, Command, Observation, health, and availability persistence semantics remain unchanged.

## Decision and alternatives

Use standard-library `log/slog` directly, existing constructor injection, a small shared handler factory, and a documented event/field vocabulary. No logger interface, logging framework, global logger mutation, event bus, or context-carried logger.

| Option | Benefit | Cost / decision |
| --- | --- | --- |
| Shared slog setup + explicit event ownership | Searchable output, small dependency footprint, testable semantics | Chosen; requires coordinated adoption and some concurrency-sensitive tests |
| Centralized observability stack and automatic tracing | Cross-host search and richer diagnostics | Deferred; not required for terminal/container use and does not fix missing semantic events |

Text remains the default. JSON is opt-in for container tools and `jq`, not a prerequisite for a collector. Operational logging options are CLI flags alongside the existing `--config`; existing application YAML shapes do not change.

### Execution context and future OpenTelemetry integration

Pass the actual operation's `context.Context` to `DebugContext`, `InfoContext`, `WarnContext`, `ErrorContext`, or `LogAttrs` at every new or migrated emission site. Constructor injection supplies the logger; context never carries a logger. Lifecycle records without an operation context use the existing process lifecycle context.

Preserve incoming context through transport handling and asynchronous work. Do not substitute `context.Background()` or `context.TODO()` for logging. Existing cancellation and deadline behavior is unchanged, including the Command lifecycle's `context.WithoutCancel`. This adds no spans and does not alter asynchronous execution semantics. Business fields stay explicit at emission sites: attach process, subsystem, and session fields with scoped `With` calls and supply operation fields from the owning operation. Standard handlers do not extract arbitrary context values, so future trace and span enrichment belongs at the handler or OpenTelemetry bridge boundary. Do not attach `trace_id` or `span_id` manually. Command and correlation IDs stay independent business lookup keys, not trace substitutes.

Keep `*slog.Logger` as the application and SDK logging API, so a future OpenTelemetry slog bridge can receive execution context through `slog.Handler` without rewriting event sites. Provider, resource, exporter, and shutdown configuration will belong to application assembly. OpenTelemetry dependencies, bridge implementation, automatic enrichment, and span lifetime, parent, and link decisions remain out of scope.

### Non-goals

- Collectors, dashboards, log files, rotation, retention services, OTLP export, new spans, metrics, or periodic health summaries.
- HTTP access logging, request bodies, query strings, or a new HTTP request-ID protocol. Command IDs already cover the primary activity flow.
- Dynamic level changes, per-component level configuration, sampling framework, or an environment-variable configuration system.
- New wire fields, persistence tables, SQL queries for logging, or repository interfaces.
- Logging every health poll, heartbeat, unchanged State update, or individual successful reconciliation mapping at Info.
- Logging build-time generators as if they were long-running applications.

## 1. Output and configuration contract

Each executable accepts:

| Flag | Default | Allowed values |
| --- | --- | --- |
| `--log-level` | `info` | `debug`, `info`, `warn`, `error` |
| `--log-format` | `text` | `text`, `json` |

Values are case-sensitive. Invalid values fail before reading configuration or opening connections, with a nonzero exit and a concise stderr diagnostic that does not echo the supplied value. The existing `--config` default is unchanged. Flags do not override YAML values because no YAML logging fields are added.

Both formats write one record per line to stderr. Use built-in `slog.NewTextHandler` and `slog.NewJSONHandler`, the same level threshold, and no color/TTY detection. Do not hand-build JSON or text lines. Handler construction must not call `slog.SetDefault`.

The executable attaches `app` (exact binary name) and `pid` once. App assembly and SDK add narrower fields with `With`; event sites add operation-specific fields. A key must occur at most once in each record. Do not reattach `app`, `adapter_id`, or `runtime_id` if already inherited. A runtime ID must only label that actual claimed session, not be used as a general process ID.

### New shared type and interface

Owning path: `internal/platform/logging/logger.go` (D1).

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

`output` is a required non-nil writer; production passes `os.Stderr`. `app` is a trusted literal from the executable. The factory validates options, constructs the handler, and attaches `app` and `pid` via `os.Getpid()`. Invalid option errors contain a fixed explanation and allowed values, not input. This package knows nothing about Devices, NATS, or the SDK. No exported wrapper around `InfoContext`/`DebugContext` is introduced.

Focused entry-point shape (repeat for the other three binary names):

```diff
--- a/cmd/hearthd/main.go
+++ b/cmd/hearthd/main.go
@@
- logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
+ logger, err := logging.NewApplicationLogger(os.Stderr, "hearthd", logging.LogOptions{
+     Level: *logLevel, Format: *logFormat,
+ })
+ if err != nil {
+     fmt.Fprintln(os.Stderr, err)
+     return 1
+ }
```

Register the two flags before the existing `flag.Parse`; remove only imports made unused by this substitution. Preserve `Run(ctx, config, logger)` and SDK public signatures.

## 2. Record vocabulary and levels

Every new or migrated application record has a stable `event` string, a short human-readable `msg`, and a `component`. Event names are lower-case dotted literals written whole at the emission site, so searching the literal finds the implementation. Message wording is not a machine contract; event names and field meanings are. Existing message strings used in tests may be preserved while adding structured fields.

Components use bounded literals: `process`, `core`, `devices`, `nats`, `adapter_session`, `zigbee2mqtt`, `homeassistant`, `simulator`. Set the component once at a subsystem boundary, not by layering conflicting `With("component", ...)` calls.

| Level | Meaning |
| --- | --- |
| Debug | Routine progress needed to reconstruct protocol/Observation handling; off by default |
| Info | Successful lifecycle milestones, recovered dependencies, bounded registration summaries, Command creation/acceptance/satisfaction |
| Warn | Recoverable degradation, retry episode beginning, permanent invalid external input, rejected/timed-out Commands |
| Error | Process failure, failed persistence/acknowledgement or other unexpected operation failure requiring investigation |

Normal cancellation and graceful shutdown are not errors. Fenced sessions terminate and are explicitly diagnosed. An expected rejection is not an internal Error merely because Go returns an `error`.

### Fields

| Field | Type / rule |
| --- | --- |
| `app`, `component`, `event` | Required strings, trusted bounded literals as above |
| `pid` | Integer process identifier, attached by executable factory |
| `adapter_id`, `runtime_id`, `device_id`, `entity_id` | Existing validated identities, only when known |
| `command_id`, `correlation_id`, `observation_id`, `causation_id` | Existing validated IDs; never fabricate IDs for logging |
| `operation` | Validated Operation name for Commands; otherwise a fixed local operation label |
| `status`, `failure_code`, `disposition`, `rejection_code`, `reason_code` | Existing typed domain/wire codes where available; omit absent optional codes |
| `stage`, `dependency` | Fixed local labels, not upstream free-form text |
| `duration_ms`, `retry_in_ms` | Nonnegative integer milliseconds; compute elapsed durations with Go's monotonic clock where available |
| `attempt` | Integer starting at 1, local to a retry episode |
| `device_count`, `entity_count`, `isolated_device_count` | Integer counts from already available reconciliation results; no added database queries |
| `error_code` | Fixed diagnostic classification for errors without a domain code |
| `error` | Optional reviewed, safe local error text; never arbitrary remote error text |

Omit unknown values rather than logging empty IDs, zero durations pretending to be measured, or guessed counts. Keep fields flat. No arrays of IDs at Info. The mandated cross-process lookup key is `command_id`, with `correlation_id` carried where already available.

### Safety

This applies at every level, including Debug:

- Never log tokens, authorization headers, token-file contents, full configuration structs, raw envelopes, command parameters, State values, HTTP/WebSocket bodies, or MQTT payloads.
- Do not log configured upstream URLs or full subjects/topics. Log the dependency name instead. Core's validated HTTP listen address may be logged as `http_addr`.
- Prefer canonical IDs over display names, HA external IDs, friendly names, and IEEE addresses. New events use canonical IDs when available and counts/reason codes before registration. Do not export vendor identity into core logs.
- Do not pass unknown errors straight to a handler: URL, schema, protocol, and authentication errors can embed secrets or rejected payloads. At those boundaries, use a fixed `error_code` plus safe operation/stage metadata. For schema failures log the fixed validation class, not the raw validation error's instance value. Allowlist reviewed local error text; do not attempt generic regex redaction of arbitrary error strings.
- Audit existing Error/Warn sites in the touched runtime paths as part of adoption; Debug is not an escape hatch for sensitive output. Bootstrap diagnostics must not repeat invalid configured values.

## 3. Event ownership and emission points

### Process, core, and dependency lifecycle (D2)

| Event | Level | Owner and meaning |
| --- | --- | --- |
| `process.starting` | Info | `cmd/*`, after logger construction, before config load |
| `process.config_loaded` | Info | `cmd/*`, after successful load/validation; no config dump or secret path |
| `process.stopping` | Info | App Run, when cancellation/error initiates teardown; reason code distinguishes cancellation from failure |
| `process.stopped` | Info | `cmd/*`, after Run returns nil and deferred cleanup has run; means process execution ended, not proof every cleanup operation succeeded |
| `process.failed` | Error | `cmd/*`, one fatal record for config/Run failure, safe classification and stage |
| `core.startup_stage_completed` | Info | Core Run after `database_migrated`, `active_commands_interrupted`, `jetstream_provisioned`, `nats_servers_started`, `observation_consumer_started` |
| `core.http_listening` | Info | After the HTTP socket is successfully bound, with `http_addr` |
| `core.readiness_changed` | Info / Warn | Supervisor: first evaluation and subsequent ready/reason changes; ready is Info, unready is Warn |
| `core.lease_expiry_resumed` | Info | Supervisor once when recovery grace expires and expiry polling resumes |
| `dependency.connected` | Info | Connection owner after initial connection establishment |
| `dependency.disconnected` | Warn | Connection owner once when a live connection is lost unexpectedly |
| `dependency.reconnected` | Info | Connection owner once transport reconnects, not a claim of restored Adapter health |
| `dependency.retrying` | Warn then Debug | Retry-loop owner: first failed attempt Warn; further attempts Debug with attempt/delay |
| `dependency.recovered` | Info | Retry-loop owner once the operation that was failing actually succeeds |
| `dependency.closed` | Debug / Error | Expected close during teardown Debug; unexpected terminal connection close Error with `reason_code=unexpected_close` |
| `core.observations_pruned` | Debug | Successful existing hourly prune; no invented count and no additional prune loop |

Do not log HTTP listening before `ListenAndServe` runs: acquire a `net.Listener` explicitly, then log and call `server.Serve(listener)` using existing shutdown/error handling. A bind failure must never produce `core.http_listening`. Preserve ownership/closure of the listener on every path.

Core readiness is dependency readiness exactly as `/readyz` defines it; it does not assert that every Entity is available. Emit the first readiness sample even if false. Add `readinessObserved bool` and `readinessReason string` to `healthSupervisor` to suppress repeats, plus `leaseExpiryPaused bool` to emit resumption once. State changes stay in the existing single poll loop; do not mutate logging state inside HTTP readiness requests. Use fixed reason codes (`sqlite_unavailable`, `nats_disconnected`, `jetstream_unavailable`, `observation_consumer_inactive`, `readiness_check_failed`) while preserving the existing checker interface. Concrete runtime readiness failures may carry these codes in a private typed error; unknown checker errors map to `readiness_check_failed`, never by matching error strings. No additional polling loop.

Readiness events include `status=ready|not_ready`, `reason_code` on failure, and `lease_expiry_grace_ms=15000` on becoming ready. Socket listening and dependency readiness are separate evidence.

NATS callbacks belong in `connectCoreNATS` and `sdk/adapter.Connect`, which already create connections. They report existing connection behavior without changing reconnection policy or supervision. An unexpected terminal close is logged at Error even when current Run supervision stays alive or treats `ErrClosed` as a normal return; this spec introduces no new connection-to-supervisor failure channel. Classify expected closure using lifecycle context or state safely under concurrency. Do not duplicate loss/recovery events in both callbacks and an operation retry loop for the same connection; operation-specific retries may still identify the blocked operation.

Add `process.cleanup_failed` at Warn around currently ignored cleanup errors (including deferred session Close and Core drains/close operations), with a fixed `stage`. Preserve current return/exit semantics and any primary Run error; do not turn this logging adoption into a shutdown refactor. The SDK's existing release failure diagnostic becomes `adapter.session_release_failed` at Warn. Do not log successful cleanup from a defer merely because cleanup was attempted.

Startup interruption logs only that `InterruptActiveCommands` succeeded: its current interface returns no affected-row count. Do not claim zero interruptions or enumerate interrupted Commands. Existing Command history remains the source for individual `interrupted/core_restarted` records.

### Adapter session and external system (D3)

| Event | Level | Owner and meaning |
| --- | --- | --- |
| `adapter.session_claimed` | Info | SDK after Core acknowledges the runtime claim; include Adapter/runtime IDs |
| `adapter.registration_completed` | Info | SDK after an accepted registration, with `device_id` and `entity_count`; include `entity_id` for a single-Entity result |
| `adapter.registration_mapping` | Debug | SDK per returned mapping, with canonical Device/Entity IDs and bounded validated Entity key |
| `adapter.registration_rejected` | Warn | SDK permanent rejection, existing rejection code; transient attempts use retry events |
| `adapter.commands_listening` | Info | SDK after Command subscription and required flush succeed; does not mean external system is healthy |
| `adapter.health_reported` | Info / Warn | SDK after acknowledged heartbeat evidence changes status/reason; healthy/initial unknown Info, unhealthy Warn; repeated successful heartbeats silent at Info |
| `adapter.session_fenced` | Warn | SDK detects runtime fencing; include runtime identity and code, then retain current termination behavior |
| `adapter.session_released` | Info | SDK after successful graceful release, not merely local Close invocation |
| `adapter.reconcile_completed` | Info | Z2M after route activation/reconciliation; available aggregate counts, no per-Entity Info dump |
| `adapter.upstream_ready` | Info | HA after subscription/snapshot/buffer reconciliation; Z2M after its existing bridge/config/inventory health conditions hold; once per recovery |
| `simulator.initialized` | Info | Simulator Run after registration and Initialize, with configured scenario and canonical Entity ID |

Expose startup waits through the existing retry boundaries for session claim, registration, Entity availability acknowledgement (`sdk/adapter/availability.go:reportAvailabilityBatch`), upstream connection, and upstream subscription/snapshot acquisition. Ordinary successful availability reports stay silent at Info; a blocked report emits retry/recovery evidence even when NATS remains connected. Successful transport reconnect alone must not emit `adapter.upstream_ready`. Adapter health logs describe acknowledged reported evidence. Protect remembered acknowledged health status/reason with the same concurrency discipline as session health, and emit outside locks.

Z2M reconciliation summaries describe only successfully activated work. Keep existing isolated-device warnings but sanitize their metadata. Unsupported inventory is not necessarily a failure; a supported-device count of zero must be explicit in the summary. Do not add a logger to the simulator library just to duplicate SDK events: its existing Run layer has all required initialization context.

### Commands and Observations (D4)

| Event | Level | Owner and meaning |
| --- | --- | --- |
| `command.created` | Info | Devices Service after `CreateCommand` commits, including immediately terminal pre-dispatch records |
| `command.dispatched` | Debug | Service immediately before sending to the existing runtime route; an attempt, not confirmed delivery |
| `command.received` | Info | SDK after wire/routing/deadline validation, before invoking the handler |
| `command.accepted` | Info | SDK after acceptance response publication succeeds; include command/correlation/Entity IDs |
| `command.rejected` | Warn | SDK after rejection response publication succeeds, safe typed reason (never upstream free-text response) |
| `command.completed` | Info / Warn | Service, after a known durable terminal result: satisfied Info; expected rejected/unavailable/disabled/unhealthy/timeout Warn |
| `command.execution_failed` | Error | Service for unexpected execution/persistence failure; never infer durable status from an error alone |
| `observation.published` | Debug | SDK after JetStream publish acknowledgement, ordinary and command-linked observations |
| `observation.projected` | Debug | Core Observation consumer after committed applied/unchanged/duplicate/rejected result; include disposition and rejection code |
| `observation.invalid` | Warn | Core consumer for permanent wire-invalid input, safe size/validation class; existing acknowledge behavior unchanged |
| `observation.processing_failed` | Error | Core consumer for projection/metadata/acknowledgement failure, with stage distinguishing commit from ack |
| `observation.clock_skew` | Warn | Preserve existing clock-skew diagnosis and safe timestamps |

A command-linked `observation.published` includes `command_id`, `correlation_id`, and `observation_id`. The consumer includes those IDs when present in the validated envelope. Untrusted IDs from malformed wire input are not promoted to normal identity fields.

`command.completed` includes command/correlation/Entity/Adapter IDs, runtime ID if assigned, operation, durable status, optional failure code, elapsed duration, and the satisfying `observation_id` when applicable. No parameters or State value. The SDK logs acceptance; the core does not emit a second `command.accepted` for the same command.

The Service owns the terminal summary, not the HTTP handler, command sender, Observation projector, or SQLite repository. This prevents missing completions after HTTP cancellation and double reporting by both projection and command waiting. Separate layers may record distinct events, but must not log-and-return the same generic error at every stack frame. Returning fatal errors is the executable's responsibility; swallowed/retried errors are diagnosed where recovery decisions occur.

### Command implementation contract

Add an optional concrete logger to the existing dependencies, preserving constructor callers and signatures:

```diff
--- a/internal/modules/devices/service.go
+++ b/internal/modules/devices/service.go
@@
 type Dependencies struct {
+    Logger           *slog.Logger
     Now              func() time.Time
@@
 type Service struct {
+    logger       *slog.Logger
     stores       Stores
```

`NewService` sets `service.logger` from `Dependencies.Logger`, falling back to `slog.Default()` for compatibility. Core assembly passes a child logger with `component=devices`. No repository logger is introduced.

Do not obtain terminal status by adding a diagnostic database read. Carry the already-known committed status through the existing private result:

```diff
--- a/internal/modules/devices/command.go
+++ b/internal/modules/devices/command.go
@@
 type commandOutcome struct {
     result CommandResult
     err    error
+    // Empty status means no durable terminal outcome was established here.
+    status CommandStatus
+    failureCode CommandFailureCode
 }
```

Private helper owned by `internal/modules/devices/command_logging.go`:

```go
func (service *Service) logCommandOutcome(
    ctx context.Context,
    command CommandRecord,
    outcome commandOutcome,
    startedAt time.Time,
)
```

Rules for filling and logging the result:

1. Immediately terminal `CreateCommand` results use their returned durable status/failure code and emit a single summary before returning. Creation failures have no `command.created` event.
2. The asynchronous goroutine delivers the known durable outcome on the buffered `completed` channel before terminal logging, then logs the returned outcome exactly once. The committed result must never wait for the terminal log emission, so a stalled synchronous logger cannot block the HTTP-facing return. Never defer terminal logging on the outer HTTP-facing `ExecuteCommand` method.
3. Receiving the existing waiter result establishes `satisfied`; use its Observation ID. This applies in all `ErrCommandTerminal` race paths too.
4. Successful `CompleteCommand` establishes its supplied status and code. Failed completion does not establish that status. Empty status emits `command.execution_failed`, not a fabricated completed record.
5. An established `internal_failure` emits one `command.execution_failed` with that durable status; it is not additionally logged as `command.completed`.
6. An accepted response or `MarkCommandAccepted` success alone never emits terminal success.
7. Cancellation does not change existing deadlines, durable outcomes, dispatch, waiter ordering, or restart behavior. Logging failures must not influence these decisions.

A crash between commit and log can lose a record. This design promises one terminal emission per normally finishing in-memory lifecycle, not exactly-once delivery across crashes. SQLite history resolves gaps.

## 4. Retry and noise policy

Use small local retry-episode state inside existing loops, not a shared limiter or new timers:

- First retryable failure: Warn with operation/dependency, fixed code, attempt, and next delay.
- Further attempts in the same episode: Debug. A distinct failure class may produce another Warn; identical failures must not flood Info/Warn.
- Eventual success: one Info recovery event with attempts and elapsed time; reset episode state.
- Permanent failure: terminate/report according to existing behavior, no invented retry.
- Cancellation: no final error merely because a blocked retry was canceled.

Info output grows with actual Commands, registrations, reconciliations, and transitions, not sensor update rate or five-second heartbeat frequency. To determine current health after a period of silence, use `/readyz` and Adapter/Entity reads; logs describe historical transitions.

For Debug investigations, restart the affected process with `--log-level debug`. Adapter restarts create new runtime IDs and may wait for prior leases.

## 5. Project layout and ownership

Paths below describe implementation ownership. Existing tests next to touched behavior are extended rather than replaced.

```text
cmd/
├── hearthd/main.go                         # modify, flags, root logger, fatal lifecycle
├── hearth-simulator/main.go                # modify, same
├── hearth-adapter-homeassistant/main.go    # modify, same
└── hearth-adapter-zigbee2mqtt/main.go       # modify, same
internal/
├── platform/logging/
│   ├── logger.go                          # new, LogOptions, NewApplicationLogger
│   └── logger_test.go                     # new, formats, thresholds, safe option errors
├── app/
│   ├── hearthd/
│   │   ├── run.go                         # modify, startup stages, listener, connection events, injection
│   │   ├── server.go                      # modify, private coded readiness error, unchanged Check interface
│   │   ├── health_supervisor.go           # modify, readiness transitions and grace resumption
│   │   └── logging_integration_test.go    # new, lifecycle/Command event behavior using existing harness
│   ├── simulator/run.go                   # modify, initialization and teardown evidence
│   ├── homeassistant/run.go               # modify, lifecycle; remove duplicate registration log
│   └── zigbee2mqtt/run.go                  # modify, lifecycle
├── modules/devices/
│   ├── service.go                         # modify, injected concrete logger
│   ├── command.go                         # modify, committed outcome tracking and emission points
│   ├── command_logging.go                 # new, private command summary helper
│   ├── command_test.go                    # modify, cancellation, persistence, race-path event assertions
│   └── nats/
│       ├── observation.go                 # modify, safe structured dispositions/errors
│       ├── observation_test.go            # modify, disposition, ack, and sensitive-data assertions
│       └── requestreply.go                # modify, common safe request failure diagnostics
└── adapters/
    ├── homeassistant/adapter.go            # modify, retry/recovery and upstream-ready evidence
    └── zigbee2mqtt/
        ├── adapter.go                     # modify, recovery episode diagnostics
        ├── connection.go                  # modify, sanitized bridge/connect diagnostics
        ├── reconcile.go                   # modify, activated reconciliation summary
        ├── runtime_commands.go            # modify, sanitize existing failure diagnostics
        ├── runtime_observations.go        # modify, sanitize existing failure diagnostics
        ├── observation.go                 # modify, sanitize existing payload diagnostics
        └── availability.go                # modify, sanitize existing malformed evidence diagnostics
sdk/adapter/
├── session.go                             # modify, connection hooks and command events
├── lifecycle.go                           # modify, claims, registration, health, release, retry episodes
├── availability.go                        # modify, blocked availability acknowledgement retry/recovery
├── command_evidence.go                    # modify, acknowledged publication event
└── logging_test.go                        # new, SDK fields, transition suppression, payload safety
README.md                                  # modify, examples and link to logging guide
docs/logging.md                            # new, developer conventions and operator workflow
specs/application-logging.md               # new now, implementation contract
```

Also migrate existing diagnostic sites in `internal/modules/devices/nats/{session,registration,availability,enablement,owned_mappings}.go` to event/component/safety rules without changing their protocol behavior. Extend existing app/adapter lifecycle tests at the owning paths for new events. No generated files, schema definitions, repository interfaces, public HTTP models, or SDK method signatures change.

The private readiness error in `internal/app/hearthd/server.go` has this shape (D2):

```go
type readinessCheckError struct {
    reasonCode string
    err error
}

func (err *readinessCheckError) Error() string
func (err *readinessCheckError) Unwrap() error
```

It preserves current error text/wrapping for existing consumers while providing a fixed safe reason for logs through `errors.As`. It is not a public health model or new response field.

## 6. Acceptance criteria and test strategy (D5)

Test structured records using a concurrency-safe recorder/handler or locked writer. Do not read a `bytes.Buffer` while goroutines write to it. Match event and relevant attributes, not timestamp, PID, full line order across goroutines, or human prose. Use existing synchronization/harness waits rather than arbitrary sleeps. Requirements below are the test oracles.

| Test | Protected behavior / defect it must detect |
| --- | --- |
| Logger option matrix | All four levels and two formats; default hides Debug; invalid options produce safe nonzero bootstrap failure before dependency creation |
| Record structure | Text/JSON equivalent fields; JSON parses per line; no duplicate root keys; required app/component/event/pid present for executable-created loggers |
| Execution context preservation | A recording handler observes a test context value from the originating operation at transport and asynchronous Command emission sites, including after HTTP cancellation; adoption review confirms all new/migrated sites use context-aware slog methods without replacing available operation context with a background context; no OTel dependency required |
| Startup failure | Busy HTTP port never emits listening; failed migration/provision never emits that completed stage; fatal diagnostic contains failed stage |
| Readiness sequence | First false, repeated false, changed failure reason, true, repeated true, grace expiry: only required transitions and one expiry-resumed event; polling/expiry semantics unchanged |
| All four application lifecycles | Config load, process identity, app-specific startup evidence, normal cancellation without Error, stopped only after Run/defers return; injected ignored cleanup failure emits Warn without changing existing exit behavior |
| NATS/upstream recovery | One disconnect/retry episode warning for identical failures, debug attempts, actual recovery and later upstream-ready evidence; reconnect does not falsely report health |
| SDK registration/health | Canonical mappings discoverable, acknowledged status/reason changes logged, repeated heartbeats silent at Info, rejection/fencing codes preserved |
| Happy Command end to end | Same command/correlation IDs across core creation, SDK receipt/acceptance, linked publication at Debug, and one satisfied core outcome with Observation ID |
| HTTP cancellation | Disconnect caller after durable creation; eventual outcome still logs once and matches persisted history |
| Terminal races and failure matrix | Satisfaction racing timeout wins exactly as before; immediate disabled/unhealthy and rejected/unavailable/timeout get correct summaries; failed persistence never claims completion |
| Observation dispositions | Applied/unchanged/duplicate/rejected visible at Debug; no routine per-Observation Info; malformed input Warn and acknowledged; transient failure Error and eligible for redelivery |
| Sensitive input sentinels | Tokens in URL/error/config; State/parameters in validation errors; malicious protocol body: sentinel absent from every level/format while stage/code and safe IDs remain useful |
| Restart recovery | Successful startup interruption stage emitted, no redispatch and no fabricated per-command outcome/count |

Extend `internal/modules/devices/nats/observation_test.go`, `internal/app/hearthd/simulator_matrix_integration_test.go`, `recovery_integration_test.go`, `health_supervisor_test.go`, and existing app/SDK/adapter tests where they already establish these behaviors. Keep existing persistence, deadline, ack/redelivery, and runtime-fencing assertions; new log assertions must not replace them.

Validation uses repository mise tasks, preferably `mise run validate`. Review generated/formatting changes; logging must not require changing generated artifacts. No physical-device interaction is needed for this implementation gate.

### Operator acceptance exercise

Run local NATS and the simulator using the normal workflow; save stderr separately:

```sh
mise run nats
# Separate terminals; existing config files required:
go run ./cmd/hearthd --config configs/hearthd.yaml --log-format json 2>core.log
go run ./cmd/hearth-simulator --config configs/simulator.yaml --log-format json 2>simulator.log
```

1. Find `core.http_listening`, a ready `core.readiness_changed`, and `simulator.initialized` with the canonical Entity ID.
2. Send one valid Command with the existing HTTP API. Find its `command.created` ID and search both files for that ID.
3. Verify SDK receipt/acceptance and exactly one core satisfied outcome with the linked Observation ID. The database/API outcome must agree; acceptance alone is insufficient.
4. Run the existing rejection and timeout scenarios and verify terminal codes identify the cause without payloads.
5. Use existing automated recovery tests to demonstrate connection loss/recovery ordering.
6. Repeat with Debug to inspect Observation publication/projection by ID, and with default text to verify terminal readability.

`docs/logging.md` includes a short event lookup table, examples of filtering JSON by `event`/`command_id`, the safety rules, and the reminder that silence is not current-health evidence. Log captures may still contain household identity metadata and should not be committed.

## 7. Deliverables and rollout

| Deliverable | Effort | Depends on | Completion gate |
| --- | --- | --- | --- |
| D1: Shared setup and four executable flags | M | none | Option/format tests and unchanged YAML loading |
| D2: Core/process lifecycle and readiness | M | D1 | Stage, listener failure, readiness/grace, normal shutdown tests |
| D3: SDK and Adapter progress/recovery | L | D1 | Session/registration/upstream recovery tests and bounded Info output |
| D4: Command outcomes and Observation diagnostics | L | D1 | Cancellation/race/failure matrix matches durable history |
| D5: Cross-app verification and logging guide | M | D2, D3, D4 | Safety tests, operator exercise, `mise run validate` |

D3 and D4 are independent after D1. Each deliverable includes its local tests and log migration for its paths. Do not call the rollout complete until all four applications adopt the pattern.

## Risks and mitigations

| Risk | Mitigation |
| --- | --- |
| Logs claim success before commit or hide failed cleanup | Emit at the owning successful boundary with durable outcome metadata; cleanup warnings stay distinct from process termination; negative tests for persistence/listener/cleanup failures |
| Async command and connection callbacks introduce races or duplicates | Existing ownership loops, synchronized state only where needed, single terminal emission site, race-enabled tests |
| Errors leak credentials or household payloads | Allowlisted metadata and fixed codes at unsafe boundaries; sentinel tests cover error strings, not only direct fields |

## Review status

This spec covers startup and readiness, end-to-end Command activity, and failure and recovery for terminal and container output across all four applications. The end-to-end implementation request authorized the defaults, event granularity, and safety/noise trade-offs described here.
