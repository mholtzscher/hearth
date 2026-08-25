# Deepen the Device / Entity module — implementation spec

- **Status:** Ready for task breakdown
- **Type:** Refactoring
- **Effort:** XL (5–9 engineering days, 70% confidence)
- **Approved design:** 2026-08-24

## Problem and decision

Hearth maintainers changing Device, Entity, Observation, State, and Command behavior currently work through an eight-method `devices.Repository` whose only production adapter is `SQLiteRepository`. Registration, State, and Command tests implement unrelated persistence methods, while important rules span `Service`, repository adapters, and application assembly.

Evidence:

- Registration and HTTP transport tests each implement seven irrelevant persistence methods.
- `command_test.go` reimplements Command transitions and linked Observation satisfaction in memory.
- `hearthd/run.go` owns Device / Entity mapping, startup interruption, receipt pruning, and NATS Command translation.
- Receipt insertion, State projection, and linked Command satisfaction are atomic today, but the invariant spans behavior and repository files.
- The affected area has 53 top-level tests across 20 files; focused race runs pass.

Without this refactor, new behavior will keep widening the persistence seam or duplicating it in tests. The approved design deepens the existing concrete `devices.Service` and removes `Repository` and `SQLiteRepository`: callers learn a small interface while SQLite transactions, lifecycle, recovery, maintenance, and in-memory Command work stay local to the module.

### Ownership and seams

1. One Device / Entity product module owns identity, reconciliation, State projection/read, and Command behavior.
2. A linked Observation receipt, State projection, and matching Command satisfaction commit in one SQLite transaction.
3. Application assembly owns the shared `*sql.DB`, migrations, connection policy, close order, and process configuration.
4. The module owns its SQLite implementation, startup recovery, receipt pruning, and in-memory Command work. SQLite is local-substitutable, so there is no persistence interface.
5. The implementation remains one Go package. `Register`, `ReceiveObservation`, `GetEntity`, and `ExecuteCommand` are direct methods on one concrete root type, not delegated behavior structs.
6. `New` restores database invariants before NATS connection. `Run` installs Command delivery, makes product methods operational, and owns maintenance and shutdown.
7. Calls racing startup wait for `Run` or their own context cancellation.
8. Graceful and ungraceful shutdown use one recovery rule: unfinished Commands remain active until the next `New` marks them interrupted. ADR-0012 is unchanged.
9. Command delivery is a real seam: the module owns the consumed interface, with NATS and in-memory adapters.
10. HTTP and NATS ingress adapters own their narrow consumed interfaces. Domain-aware NATS mapping lives in `internal/modules/devices/nats`; domain-neutral NATS mechanics remain in `internal/platform/nats`.
11. Catalog overrides, clocks, IDs, deadlines, schedules, and race controls are private test inputs.
12. Delivery is one pull request composed of the ordered green commits below.

### Key design choices

| Chosen design | Rationale |
|---|---|
| Concrete SQLite implementation | There is one production implementation, and migrated SQLite is the local test substitute. |
| One concrete `devices.Service` | Atomic State/Command behavior and shared catalog rules need locality. |
| Direct root behavior methods | Delegating structs would add shallow, hypothetical seams. |
| Explicit `Run` | Application assembly retains clear lifetime and database close ordering. |
| Recovery-time interruption | One rule covers graceful and ungraceful termination while preserving ADR-0012. |
| Receipt summary | Ingress needs durable disposition, not internal State or waiter details. |
| Consumer-owned transport interfaces | Production and test adapters make these seams real and narrow. |
| Preserved external contracts | No external redesign increases module depth in this refactor. |

## Compatibility and scope

Internal Go callers may break. Every other established surface is preserved:

| Surface | Contract |
|---|---|
| HTTP | Paths, bodies, operation IDs, OpenAPI schemas, status mappings, and stable errors. |
| NATS wire | Subjects, schemas, envelopes, headers, correlation/causation, deadlines, and acknowledgement behavior. |
| SQLite | Existing schema, query sources, data, and migration history. |
| Public adapter SDK | Exported types and Session behavior. |
| Configuration | All YAML fields and defaults. |

Changing a preserved surface expands scope and requires re-estimation. This refactor adds no Device or Entity product behavior, runtime Entity-type loading or extension, persistence backend, generic transaction abstraction, cross-module transaction, HTTP/NATS feature, SDK change, NATS wire-type deduplication, or authentication, availability, deployment, or automation semantics.

## Implementation contract

### Domain types

Owner: `internal/modules/devices/model.go`.

```go
type Observation struct {
    ID                ObservationID
    EntityID          EntityID
    Value             Value
    AdapterReceivedAt time.Time
    SourceUpdatedAt   *time.Time
    RefreshForCommand *CommandID
}

// ReceivedObservation combines Adapter ownership and the Core-assigned
// durable receive time with one Adapter-reported Observation.
type ReceivedObservation struct {
    AdapterID   string
    Observation Observation
    ObservedAt  time.Time
}

// ObservationReceipt is returned only after the receipt transaction commits.
// State and Command effects remain behind the module interface.
type ObservationReceipt struct {
    Disposition ObservationDisposition
    Rejection   *ObservationRejection
}

type CommandDispatch struct {
    ID            CommandID
    CorrelationID CorrelationID
    EntityID      EntityID
    OperationName OperationName
    Parameters    CommandParameters
    Deadline      time.Time
}
```

Contracts:

- `ReceivedObservation.AdapterID` is a subject-safe Adapter instance slug.
- `ReceivedObservation.ObservedAt` is the trusted JetStream server timestamp normalized to UTC.
- `ObservationReceipt` contains only durable disposition and rejection information. Callers observe State through `GetEntity` and Command satisfaction through `ExecuteCommand`.
- Byte slices and pointer timestamps are copied at the module seam as they are today.
- No HTTP, NATS JSON, SDK, or SQLite schema type changes are required.

Persistence-only types are unexported and do not cross the module seam:

- `registerBindingParams` in `registration_sqlite.go`.
- `observationProjection` in `observation_sqlite.go`, containing the public receipt plus the projected State and satisfied Command result needed before return and notification.
- `commandRecord`, `commandCompletion`, `commandStatus`, and `commandFailureCode` in `command_sqlite.go`.
- `sqliteDBTX` and shared time/null conversion helpers in `sqlite.go`.

`NewDeviceID`, `NewEntityID`, `NewObservationID`, `NewCommandID`, `NewCorrelationID`, and the five corresponding `Parse*ID` functions remain exported with unchanged signatures. Production controls generate Device, Entity, Command, and Correlation IDs; Observation IDs remain ingress-owned. Existing ID tests and integration callers continue to use the exported helpers.

### Service construction and lifecycle

Owner: `internal/modules/devices/service.go`.

```go
type Service struct {
    database  *sql.DB
    delivery  CommandDelivery // installed exactly once by Run
    catalog   *typeCatalog
    logger    *slog.Logger
    controls  serviceControls
    waiters   commandWaiters
    lifecycle serviceLifecycle
}

func New(
    ctx context.Context,
    database *sql.DB,
    logger *slog.Logger,
) (*Service, error)

func newService(
    ctx context.Context,
    database *sql.DB,
    logger *slog.Logger,
    controls serviceControls,
) (*Service, error)

func (service *Service) Run(
    ctx context.Context,
    delivery CommandDelivery,
) error
```

`New`:

1. Rejects a nil database and defaults a nil logger to `slog.Default()`.
2. Obtains a complete `productionServiceControls()` value: UTC `time.Now`, UUIDv7 ID generators, built-in catalog factory, 192-hour receipt retention, one-hour maintenance interval, existing persistence timeout, and production deadline construction.
3. Invokes the catalog factory exactly once. Production returns the built-in catalog; tests may return a private test catalog.
4. In one serializable transaction, marks persisted `requested` and `accepted` Commands `interrupted` with `core_restarted`, then deletes expired Observation receipts while preserving the receipt referenced by current State.
5. Commits interruption and pruning together. Failure rolls back both and returns an error; a later `New` retries the idempotent transaction and never redispatches a Command.
6. Returns only after the startup transaction commits.
7. Does not open, migrate, reconfigure, or close the database, start a goroutine, or require NATS.

`Run`:

1. Rejects a nil adapter with `ErrCommandDeliveryRequired` before claiming the one-shot lifecycle.
2. Claims the module exactly once, installs delivery, transitions `constructed → running`, and releases calls waiting for operational readiness.
3. Runs hourly receipt pruning. A prune failure is logged and retried on the next tick without terminating `Run`.
4. On `ctx` cancellation, cancels the private module work context with cause `ErrServiceStopped`, regardless of the caller context's cause; atomically transitions to `stopping`; and rejects new work.
5. Waits for tracked database operations, post-commit notifications, and Command lifecycles, then transitions to `stopped` and returns `nil` for normal cancellation.
6. Does not persist `adapter_unavailable`, `internal_failure`, `outcome_timeout`, or `interrupted` solely because the module stopped. Unfinished `requested` and `accepted` records remain active for the next `New`.
7. Cannot restart. A repeated or concurrent non-nil call returns `ErrServiceAlreadyRun` without installing another adapter or schedule.
8. Requires application assembly to close NATS and the database only after it returns.

Lifecycle states:

```go
type serviceState uint8

const (
    serviceConstructed serviceState = iota
    serviceRunning
    serviceStopping
    serviceStopped
)
```

A product method in `constructed` waits for `running`, its context cancellation, or terminal module stop. Calls in `running` proceed; calls beginning in `stopping` or `stopped` return `ErrServiceStopped` without writes. Calls after `Run` returns also return `ErrServiceStopped`.

Work-token rules:

- `beginWork`, transition to stopping, and `WaitGroup.Add` execute under one lifecycle mutex so `Add` cannot race `Wait`.
- Every product method acquires one token before database access.
- `Register`, `ReceiveObservation`, and `GetEntity` release their token on return.
- After the `requested` commit, `ExecuteCommand` transfers its token to the detached Command lifecycle; only that lifecycle releases it. The caller releases it if durable creation fails. No post-commit goroutine performs another `Add`.
- An Observation token covers transaction commit and post-commit waiter publication, so shutdown cannot complete between them.

Command cancellation uses four contexts:

1. `requestContext`: caller values and cancellation combined with module cancellation before durable creation.
2. `workContext`: derived from `context.WithoutCancel(callerCtx)`, preserving caller values and canceled only by module stop.
3. `deadlineContext`: derived from `workContext` through `withDeadline`, used only for delivery and outcome waiting.
4. `writeContext`: derived from `workContext` with `persistenceTimeout`, used for acceptance, failure, timeout, and terminal reconciliation writes. It allows a terminal write after deadline expiry while remaining cancelable by module stop, and never derives from an expired `deadlineContext`.

A committed terminal database write wins. A terminal result committed before stopping is retained and returned. If module cancellation first prevents or rolls back the terminal update, the Command remains active and the caller receives `*CommandExecutionError` wrapping `ErrServiceStopped`. A satisfaction commit preceding stop is published before its lifecycle may return stopped. Reads inside an open transaction use that transaction's sqlc handle, never an independent `*sql.DB` query on the single SQLite connection.

### Private implementation controls

Owners: `serviceControls` and `serviceTicker` in `service.go`; `sqliteDBTX` in `sqlite.go`. Same-package tests access them only through `newService`.

```go
type sqliteDBTX interface {
    ExecContext(context.Context, string, ...any) (sql.Result, error)
    PrepareContext(context.Context, string) (*sql.Stmt, error)
    QueryContext(context.Context, string, ...any) (*sql.Rows, error)
    QueryRowContext(context.Context, string, ...any) *sql.Row
}

type serviceTicker interface {
    C() <-chan time.Time
    Stop()
}

type serviceControls struct {
    now                     func() time.Time
    newDeviceID             func() (DeviceID, error)
    newEntityID             func() (EntityID, error)
    newCommandID            func() (CommandID, error)
    newCorrelationID        func() (CorrelationID, error)
    newCatalog              func() (*typeCatalog, error)
    withDeadline            func(context.Context, time.Time) (context.Context, context.CancelFunc)
    newTicker               func(time.Duration) serviceTicker
    pruneReceipts           func(context.Context, sqliteDBTX, time.Time) error
    receiptRetention        time.Duration
    pruneInterval           time.Duration
    persistenceTimeout      time.Duration
    beforeObservationCommit func()
    afterObservationCommit  func()
}
```

`productionServiceControls()` returns the complete production value. `newService` rejects an incomplete set; only the before/after Observation hooks may be nil. Production callers cannot override controls.

- `withDeadline` preserves parent cancellation and reports `context.DeadlineExceeded` for deadline expiry.
- `newTicker` provides deterministic maintenance ticks.
- `pruneReceipts` defaults to the real SQLite implementation and accepts either the startup transaction or module database. Tests may inject a one-shot fault and then delegate to the real implementation.
- `beforeObservationCommit` runs after transaction writes and immediately before `Commit`.
- `afterObservationCommit` runs immediately after successful commit, before waiter notification and token release.

No exported options or general clock interface are added.

### Stable errors

Owner: `internal/modules/devices/errors.go`.

```go
var (
    ErrServiceStopped          = errors.New("Device / Entity service stopped")
    ErrServiceAlreadyRun       = errors.New("Device / Entity service Run already called")
    ErrCommandDeliveryRequired = errors.New("Command delivery is required")
)

// Existing errors remain stable:
// ErrEntityNotFound, ErrInvalidCommand, ErrAdapterUnavailable,
// ErrUpstreamRejected, ErrOutcomeTimeout.

type CommandExecutionError struct {
    CommandID CommandID
    Err       error
}

func (*CommandExecutionError) Error() string
func (*CommandExecutionError) Unwrap() error
```

`Run` with nil delivery returns `ErrCommandDeliveryRequired` without consuming the one-shot claim. Repeated non-nil `Run` calls return `ErrServiceAlreadyRun`. A caller waiting on a durably created Command receives `&CommandExecutionError{CommandID: id, Err: ErrServiceStopped}` when shutdown wins; Command execution failures retain the pointer form and existing `errors.As` behavior. Caller cancellation after durable creation remains distinct: the caller may receive its request-context error while the Command lifecycle continues. A transaction canceled by module shutdown rolls back and returns `ErrServiceStopped`. Registration rejection codes and HTTP mappings remain unchanged.

### Registration

Owners: behavior in `internal/modules/devices/registration.go`; SQLite in `registration_sqlite.go`.

```go
func (service *Service) Register(
    ctx context.Context,
    adapterID string,
    registration Registration,
) (Binding, error)
```

One serializable transaction performs, in order: Binding lookup; external Device identity check; Device create/update; Binding create/update; Entity mapping lookup; immutable Entity-type check; external Entity identity check; Entity create/update; mapping create/update; and commit before returning canonical IDs.

The module validates and normalizes support before writing, copies caller input, preserves canonical IDs on unambiguous re-registration, and rolls back all writes on conflict. SQL moves from `sqlite_repository.go` to `registration_sqlite.go`; no Registration repository type remains.

### Observation and State

Owners: behavior in `internal/modules/devices/observation.go`; SQLite in `observation_sqlite.go`; periodic orchestration in `maintenance.go`.

```go
func (service *Service) ReceiveObservation(
    ctx context.Context,
    received ReceivedObservation,
) (ObservationReceipt, error)

func (service *Service) GetEntity(
    ctx context.Context,
    id EntityID,
) (EntityView, error)
```

One serializable transaction performs, in order: exact Observation-ID duplicate lookup; Entity and owning Adapter lookup; support and value validation; durable receipt insertion and `receive_order` assignment; State upsert for applied and unchanged values; optional linked Command evaluation and satisfaction; and commit.

Behavior:

- A duplicate returns `DispositionDuplicate` without reevaluating ownership, support, value, State, or Command.
- Unknown Entity, wrong owner, and invalid value commit rejected receipts and return nil error.
- Same-value Observations advance State evidence and timestamps.
- Current State follows receive order, never timestamps.
- Linked Command satisfaction commits atomically with receipt and State.
- Waiter notification occurs strictly after commit.
- Service stop before commit rolls back. Commit is the linearization point: stop after commit preserves State and Command effects, and tracked notification completes before shutdown.
- Receipt expiry is `ObservedAt + 192h`.
- Pruning compares timestamps chronologically and preserves the receipt referenced by current State.

### Command

Owners: lifecycle and delivery seam in `internal/modules/devices/command.go`; SQLite row mapping and writes in `command_sqlite.go`. Linked satisfaction remains inside the Observation transaction and calls private Command outcome helpers in the same package.

```go
type CommandDelivery interface {
    Deliver(
        context.Context,
        string,
        CommandDispatch,
    ) (CommandAcceptance, error)
}

func (service *Service) ExecuteCommand(
    ctx context.Context,
    entityID EntityID,
    operationName OperationName,
    parameters CommandParameters,
) (CommandResult, error)
```

The production adapter is `internal/modules/devices/nats.CommandDelivery`; module tests use an in-memory function adapter.

Execution order:

1. Validate Entity ID, operation name, and parameters.
2. Read the Entity and resolve current support, normalized parameters, immutable deadline, and outcome matcher.
3. Generate Command and correlation IDs privately.
4. Install the waiter before persistence.
5. Commit `requested` before dispatch.
6. Detach lifecycle from caller cancellation while preserving caller context values.
7. Bind lifecycle to module shutdown and the absolute Command deadline.
8. Deliver exactly once.
9. Persist acceptance or a terminal delivery failure.
10. Wait for a matching linked Observation or deadline.

Delivery is never queued, retried, or replayed. `Accepted: false` maps to upstream rejection. `ErrAdapterUnavailable` classifies no responder, disconnect, draining, or pre-acceptance request expiry. Any other delivery error maps to internal Command failure unless module shutdown caused it. Caller and trace values survive caller cancellation via `context.WithoutCancel`; module shutdown and the absolute deadline still cancel delivery.

Race invariants:

- Commands for one Entity overlap independently; an immediate linked Observation cannot be missed.
- `MarkCommandAccepted` may fill `accepted_at` after satisfaction but cannot regress status.
- SQLite status predicates serialize satisfaction against timeout and failure writers.
- If satisfaction commits first, failure, timeout, or shutdown handling returns the satisfying waiter result after tracked notification.
- If another terminal update commits first, a late linked Observation may advance State but cannot change the terminal Command.
- If module cancellation first prevents or rolls back the terminal update, shutdown writes no terminal outcome and leaves the record recoverable.

### Catalog visibility

Owners: `internal/modules/devices/catalog.go`, generated catalog output, and `internal/cmd/entitytypegen`.

```go
type typeCatalog struct { /* ... */ }
func newTypeCatalog(/* ... */) (*typeCatalog, error)
func newBuiltinTypeCatalog() (*typeCatalog, error)
```

`EntityTypeDefinition`, `OperationDefinition`, `ResolvedCommand`, `DefineEntityType`, and `DefineOperation` receive equivalent private names; discovery found only same-package and generated callers. Update generator templates and checked-in output together. Catalog tests remain same-package tests.

### HTTP adapters

Owners: `internal/modules/devices/api/get_entity.go`, `command.go`, and `internal/app/hearthd/server.go`.

```go
type EntityReader interface {
    GetEntity(context.Context, devices.EntityID) (devices.EntityView, error)
}

type CommandExecutor interface {
    ExecuteCommand(
        context.Context,
        devices.EntityID,
        devices.OperationName,
        devices.CommandParameters,
    ) (devices.CommandResult, error)
}

type Dependencies struct {
    Entities EntityReader
    Commands CommandExecutor
}

type Handler struct {
    entities EntityReader
    commands CommandExecutor
}

func Register(api huma.API, dependencies Dependencies)

func NewHTTPHandler(
    deviceDependencies devicesapi.Dependencies,
    readiness ReadinessChecker,
) (http.Handler, huma.API)
```

The HTTP adapter owns the interfaces it consumes. Production passes the same `*devices.Service` for both fields; tests use narrow adapters without persistence. `NewHTTPHandler` passes `deviceDependencies` unchanged to `devicesapi.Register`; readiness and HTTP contracts do not change.

### Domain-aware NATS adapters

Owner: new `internal/modules/devices/nats` package.

```go
package nats

import (
    "context"

    "github.com/mholtzscher/hearth/internal/modules/devices"
    platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
)

type Registrar interface {
    Register(context.Context, string, devices.Registration) (devices.Binding, error)
}

type ObservationReceiver interface {
    ReceiveObservation(
        context.Context,
        devices.ReceivedObservation,
    ) (devices.ObservationReceipt, error)
}

func RegistrationHandler(Registrar) platformnats.RegistrationHandler
func ObservationHandler(ObservationReceiver) platformnats.ObservationHandler

type CommandDelivery struct {
    client *platformnats.CommandClient
}

func NewCommandDelivery(*platformnats.CommandClient) *CommandDelivery

func (*CommandDelivery) Deliver(
    context.Context,
    string,
    devices.CommandDispatch,
) (devices.CommandAcceptance, error)
```

Mapping contracts:

- Registration preserves accepted/rejected responses and rejection codes.
- Observation parses typed IDs and timestamps, copies JSON values, calls `ReceiveObservation`, and ignores a successful receipt summary.
- Command converts domain dispatch to a platform request and maps `platformnats.ErrCommandUnavailable` to `devices.ErrAdapterUnavailable`.
- The package does not acknowledge JetStream messages, construct subjects, validate schemas, or manipulate trace headers.
- `internal/platform/nats` retains validation, routing, acknowledgement, subjects, request/reply, correlation checks, and tracing.
- The root `devices` package does not import its `nats` child; application assembly imports both.

### Registration ingress join

Owner: new `internal/platform/nats/registration_lifecycle.go`; `registration_server.go` remains unchanged and compatibility-guarded.

```go
func (server *RegistrationServer) Closed() <-chan struct{}
```

`Closed` calls the existing subscription's `StatusChanged(natsgo.SubscriptionClosed)` and returns a derived channel that closes when the status channel reports or closes. Because NATS closes a drained subscription only after queued callbacks finish, a blocked Registration handler keeps `Closed` open. A nil or uninitialized server returns an already-closed channel, matching `ObservationConsumer.Closed`.

Application assembly calls `Closed` once before `Drain`; the forwarding goroutine terminates when the subscription closes. A transport test proves blocked-handler behavior. Registration wire handling is unchanged.

### Application assembly

Owners: `internal/app/hearthd/run.go` and `server.go`.

```text
Open shared database
→ Run migrations
→ devices.New                   # recovery and initial prune; no NATS
→ Connect and provision NATS
→ Construct NATS Command delivery adapter
→ Launch devices.Service.Run(ctx, delivery)
→ Start Registration and Observation ingress
→ Register HTTP adapter
→ Initiate ingress quiescing without awaiting handlers
→ Cancel the dedicated Service.Run context
→ Join Service.Run, both ingress Closed channels, and HTTP shutdown
→ Drain/close NATS
→ Close shared database
```

Final assembly owns process configuration, database migration/lifetime, NATS provisioning, HTTP/readiness lifecycle, and shutdown ordering; Device / Entity mapping, catalog construction, startup recovery, pruning, and Command translation live in the module or its adapters.

`Service.Run` receives a dedicated child context canceled explicitly by assembly. After `Run` launches, normal cancellation and every HTTP or ingress failure path continue cleanup despite earlier errors: initiate HTTP shutdown and ingress drains, cancel the module immediately so active handlers unblock, complete all joins, then drain NATS and close SQLite. The five-second HTTP timeout cannot bypass module joining or database close order. Cleanup applies only to initialized resources on earlier startup failures.

Private deterministic application controls:

```go
type appShutdownStep uint8

const (
    shutdownIngressQuiesced appShutdownStep = iota + 1
    shutdownModuleCanceled
    shutdownModuleJoined
    shutdownIngressJoined
    shutdownNATSClosed
    shutdownDatabaseClosed
)

type appControls struct {
    beforeRegistrationStart func() error
    beforeObservationStart  func() error
    serveHTTP               func(*http.Server) error
    onShutdownStep          func(appShutdownStep)
}

func Run(ctx context.Context, config Config, logger *slog.Logger) error {
    return run(ctx, config, logger, productionAppControls())
}

func run(context.Context, Config, *slog.Logger, appControls) error
```

`productionAppControls()` returns no-op startup checks, `server.ListenAndServe`, and a no-op observer. The private checks run after `Service.Run` launches: one immediately before Registration startup and one after Registration succeeds but before Observation startup. Tests inject sentinel failures for both partial-startup paths. `onShutdownStep` runs after each named action or join so tests can assert order without replacing database or NATS behavior.

Application error semantics:

```go
func joinRunErrors(primary error, cleanup ...error) error
```

- A startup-hook, ingress-start, NATS, or non-`http.ErrServerClosed` serve failure is primary. Parent-context cancellation is normal and contributes no primary error.
- Cleanup always continues; non-ignorable cleanup errors are retained in action order.
- `joinRunErrors` passes primary first to `errors.Join`, followed by non-nil cleanup errors. It returns nil only when all inputs are nil, and `errors.Is` matches every retained cause.
- Existing ignorable close errors, including `http.ErrServerClosed` and `natsgo.ErrConnectionClosed`, remain ignored.

`TestJoinRunErrors` fixes these rules; lifecycle tests keep injected startup and serve sentinels discoverable after cleanup.

## Project layout

```text
CONTEXT.md                                             # modify — canonical Registration term already resolved
docs/architecture.md                                  # modify — module, SQLite, lifecycle, adapter locality
specs/first-light.md                                  # modify — resulting module interface
specs/unified-entity-support.md                        # modify — resulting module/catalog terminology
specs/deepen-device-entity-module.md                   # this contract

internal/modules/devices/
├── service.go                                         # modify — Service, New, Run, lifecycle, controls
├── errors.go                                          # new — lifecycle and Command execution errors
├── model.go                                           # modify — received Observation, receipt, dispatch
├── registration.go                                    # modify — Registration behavior
├── registration_sqlite.go                             # new — Registration transaction
├── observation.go                                     # modify — Observation/State behavior
├── observation_sqlite.go                              # new — receipt/State/satisfaction transaction
├── command.go                                         # modify — Command lifecycle, waiters, delivery seam
├── command_sqlite.go                                  # new — Command writes and row mapping
├── sqlite.go                                          # new — SQLite protocol and conversion helpers
├── maintenance.go                                     # new — startup recovery and pruning
├── catalog.go                                         # modify — private catalog
├── catalog_test.go                                    # modify — private names
├── ids.go                                             # unchanged — exported New*/Parse* helpers
├── zz_generated_entitytypes.go                        # regenerate — private catalog assembly
├── zz_generated_entitytypes_test.go                   # regenerate — private conformance names
├── repository.go                                      # delete
├── sqlite_repository.go                               # delete after behavior moves
├── sqlite_observations.go                             # delete after behavior moves
├── test_helpers_test.go                               # new — migrated SQLite and delivery fixtures
├── service_lifecycle_test.go                          # new — lifecycle/recovery
├── registration_test.go                               # modify — module behavior on SQLite
├── registration_persistence_test.go                   # new — transaction assertions
├── observation_test.go                                # new — Observation/State behavior
├── observation_persistence_test.go                    # new — transaction assertions
├── command_test.go                                    # modify — SQLite plus in-memory delivery
├── command_persistence_test.go                        # new — row invariants
├── sqlite_repository_test.go                          # delete after migration
├── sqlite_observations_test.go                        # delete after migration
├── api/
│   ├── get_entity.go                                  # modify — EntityReader
│   ├── command.go                                     # modify — CommandExecutor
│   ├── get_entity_test.go                             # modify — narrow adapter
│   └── command_test.go                                # modify — narrow adapter
└── nats/
    ├── command_delivery.go                            # new — delivery adapter
    ├── command_delivery_test.go                       # new — mapping/error classification
    ├── registration.go                               # new — wire/domain mapping
    ├── registration_test.go                          # new — response mapping
    ├── observation.go                                # new — wire/domain mapping
    └── observation_test.go                           # new — IDs, times, JSON, receipt

internal/app/hearthd/
├── run.go                                             # modify — assembly
├── server.go                                          # modify — HTTP dependencies
├── server_test.go                                     # modify — remove persistence adapter
├── run_integration_test.go                            # modify — Service construction
├── run_lifecycle_test.go                              # new — cleanup/ordering controls
├── recovery_integration_test.go                       # modify — stop and pre-NATS recovery
└── simulator_matrix_integration_test.go               # modify — remove repository access

internal/cmd/entitytypegen/
├── render_catalog.go                                  # modify — private names
└── render_catalog_conformance.go                      # modify — private names

internal/platform/nats/
├── registration_lifecycle.go                          # new — drain completion
├── registration_lifecycle_test.go                     # new — callback join
├── registration_server.go                             # unchanged/compatibility-guarded
└── registration_server_test.go                        # unchanged

scripts/
├── check-device-module-shape.sh                       # new — final-shape gate
└── check-device-module-compat.sh                      # new — preserved-surface gate

devenv.nix                                             # modify — include shape gate
.github/workflows/check.yml                            # modify — full history and PR compatibility gate
```

Behavior-prefixed SQLite companions keep SQL beside its owning behavior without recreating a cross-role repository module. Migrations, query sources, contracts, SDK, configuration, and guarded platform NATS files remain unchanged; generated sqlc output is regenerated only to prove no semantic diff.

## Test and verification contract

### Service fixtures

Ordinary module tests use `platformdb.Open` with a unique migrated named-memory SQLite database such as `file:devices-test-<uuid>?mode=memory&cache=shared`, retaining its owning connection for the test lifetime. This preserves production foreign keys, busy timeout, and `SetMaxOpenConns(1)`. SQLite reports `journal_mode=memory` for this mode, so existing file-backed platform database tests remain responsible for WAL policy. Reopen/recovery tests use a temporary file-backed database.

`test_helpers_test.go` owns:

```go
type commandDeliveryCall struct {
    AdapterID string
    Dispatch  CommandDispatch
}

type inMemoryCommandDelivery struct {
    calls   chan commandDeliveryCall
    deliver func(context.Context, string, CommandDispatch) (CommandAcceptance, error)
}

func (delivery *inMemoryCommandDelivery) Deliver(
    context.Context,
    string,
    CommandDispatch,
) (CommandAcceptance, error)

func openMigratedDeviceTestDB(t *testing.T) *sql.DB
func newDeviceTestService(t *testing.T, controls serviceControls) (*Service, *sql.DB)
```

Delivery records one call before invoking the required function. The database helper opens, migrates, and registers cleanup. The Service helper calls `newService` with complete controls; tests needing production behavior pass `productionServiceControls()` explicitly. No test adapter implements Device / Entity persistence behavior.

### Required coverage

Existing behavior tests are rewritten through `Service`:

- Registration classification, normalization, idempotence, descriptor updates, identity conflicts, concurrency, and atomic rollback.
- Observation applied/unchanged/duplicate/rejected behavior, receive ordering, durable receipts, pruning, and State reads.
- Command commit-before-delivery, acceptance, rejection, unavailable/internal failures, outcome timeout, overlapping Commands, linked mismatch, and caller cancellation.

Deterministic tests use private controls rather than sleeps or polling. Exact ownership:

| Path | Required test and coverage |
|---|---|
| `service_lifecycle_test.go` | `TestNewRequiresDatabaseAndDefaultsLogger` — nil database, logger default, no goroutine, and no database close/reconfiguration. |
| `service_lifecycle_test.go` | `TestNewRecoversAndPrunesAtomicallyBeforeNATS` — interruption, pruning, rollback/retry, no redispatch, and later NATS failure. |
| `service_lifecycle_test.go` | `TestRunRequiresDeliveryAndIsOneShot` — nil, repeated, and concurrent `Run`. |
| `service_lifecycle_test.go` | `TestRunRetriesPruning` — deterministic tick and logged transient failure. |
| `service_lifecycle_test.go` | `TestRunStopsWorkAndWaitsForDatabaseUse` — blocked delivery, accepted wait, database work, transferred tokens, and no false terminal write. |
| `service_lifecycle_test.go` | `TestServiceMethodsWaitForRunAndRejectAfterStop` — all four product methods, context cancellation, and no post-stop writes. |
| `service_lifecycle_test.go` | `TestNewInterruptsCommandsLeftByGracefulStop` — active record recovery without redispatch. |
| `observation_test.go` | `TestReceiveObservationCancellationAroundCommit` — rollback before commit and preserved State/notification after commit. |
| `command_test.go` | `TestExecuteCommandCommitsBeforeDeliveryAndHandlesAcceptanceRace` — immediate Observation and late acceptance. |
| `command_test.go` | `TestExecuteCommandReturnsSatisfiedWhenObservationWinsDeliveryFailureRace` — unavailable, rejection, and internal-error orderings. |
| `command_test.go` | `TestExecuteCommandSerializesDeadlineAndObservation` — both commit orders. |
| `command_test.go` | `TestExecuteCommandKeepsOverlappingCommandsIndependent` — reverse completion and receive-order State. |
| `command_test.go` | `TestExecuteCommandContinuesAfterCallerCancellation` — detached durable lifecycle. |
| `command_test.go` | `TestExecuteCommandShutdownRaceMatrix` — unavailable, rejected, internal, deadline, and satisfying outcomes; only committed outcomes survive. |
| `command_test.go` | `TestExecuteCommandPreservesContextValuesAfterCallerCancellation` — one delivery, caller value, and trace span. |
| `command_test.go` | `TestExecuteCommandLateNotificationAfterWaiterRemoval` — no block, panic, cross-Command mutation, or work leak. |
| `internal/platform/nats/registration_lifecycle_test.go` | `TestRegistrationServerClosedWaitsForHandler`. |
| `internal/app/hearthd/run_lifecycle_test.go` | `TestRunCleansUpAfterHTTPServeFailure`. |
| `internal/app/hearthd/run_lifecycle_test.go` | `TestRunCleansUpAfterIngressStartupFailure` with Registration and Observation subtests. |
| `internal/app/hearthd/run_lifecycle_test.go` | `TestRunJoinsModuleAndIngressBeforeClosingResources` — includes no database use after close. |
| `internal/app/hearthd/run_lifecycle_test.go` | `TestJoinRunErrors`. |
| `internal/app/hearthd/recovery_integration_test.go` | `TestGracefulStopDefersInterruptionUntilNextStartup`. |
| `internal/app/hearthd/recovery_integration_test.go` | `TestRunRecoversCommandsBeforeNATSConnectionFailure`. |

Direct SQL inspection is limited to facts absent from the module interface: rollback and row counts, normalized JSON, receipt expiry/pinning, monotonic Command transitions and terminal fields, startup interruption fields, and absence of post-stop writes. These tests inspect the real SQLite implementation rather than replace it.

Transport coverage:

- HTTP tests fake only `EntityReader` or `CommandExecutor`.
- Before transport construction changes, D1 extends `TestRuntimeOpenAPIContract` to characterize complete request, response, error, and nullability schemas for both Entity paths, and characterizes existing `hearthd` NATS mappings.
- Device NATS adapter tests preserve the characterized mapping and error behavior after relocation.
- Platform NATS tests retain schema, subject, acknowledgement, tracing, request/reply, and callback-join coverage.
- Full `hearthd` NATS, HTTP, recovery, and simulator integration tests remain.

### Static and compatibility gates

`scripts/check-device-module-shape.sh` is a zero-argument gate run by `devenv test`. In `internal/modules/devices` (excluding its `nats` child) and `internal/app/hearthd` Go files, it rejects:

- former constructors and seams: `NewService`, `Repository`, `RegistrationRepository`, `CommandLedger`, `SQLiteRepository`, `NewSQLiteRepository`, `CommandSender`, `CommandRequest`, and `devices.Dependencies`;
- former persistence/transfer names: `RegisterBindingParams`, `ProjectObservationParams`, `ProjectObservation`, `ProjectionResult`, `CommandRecord`, `CommandCompletion`, `CommandStatus`, `CommandFailureCode`, and exported `ObservationReceiptRetention`;
- exported catalog authoring names: `TypeCatalog`, `NewTypeCatalog`, `NewBuiltinTypeCatalog`, `EntityTypeDefinition`, `OperationDefinition`, `DefineEntityType`, `DefineOperation`, and `ResolvedCommand`;
- a root `Dependencies` type, any root test implementation of a former persistence method, and the undeleted files `repository.go`, `sqlite_repository.go`, or `sqlite_observations.go`.

The child NATS adapter is excluded so `platformnats.CommandRequest` remains valid.

`scripts/check-device-module-compat.sh <base-sha>` fails on pull-request changes to:

- `configs`, `contracts/v1`, `sdk/adapter`;
- `internal/platform/db/migrations` and `internal/platform/db/queries`;
- `internal/platform/nats/codec.go`, `wire.go`, `subjects.go`, `command_client.go`, `observation_consumer.go`, `registration_server.go`, `jetstream.go`, and `trace.go`.

The additive `registration_lifecycle.go` and its test are excluded. `devenv.nix` runs the shape gate in `hearth:test` and `enterTest`. Pull-request CI sets `fetch-depth: 0` and runs:

```sh
scripts/check-device-module-compat.sh '${{ github.event.pull_request.base.sha }}'
```

Push checks receive the shape gate through `devenv test`.

### Verification by stage

D1:

```sh
go test -race ./internal/modules/devices/api ./internal/app/hearthd ./internal/platform/nats
```

D2–D4:

```sh
go test -race ./internal/modules/devices/...
go test -race ./internal/app/hearthd
go test -race -count=10 ./internal/modules/devices/...
```

D5–D7:

```sh
test -z "$(gofmt -l $(git ls-files --cached --others --exclude-standard -- '*.go'))"
go run ./internal/cmd/entitytypegen -root .
go run ./internal/cmd/entitytypegen -root . -check
sqlc generate
git diff --exit-code -- internal/platform/db/sqlc
test -z "$(git ls-files --others --exclude-standard -- internal/platform/db/sqlc)"
go test -race ./...
go vet ./...
devenv test
```

Static and pull-request compatibility checks:

```sh
scripts/check-device-module-shape.sh
base="$(git merge-base HEAD origin/main)"
scripts/check-device-module-compat.sh "$base"
```

Every stage remains green. Existing contract and SDK compatibility tests retain their meaning.

## Acceptance criteria

All normative contracts above are acceptance criteria. Completion additionally requires these observable gates:

- [ ] `devices.New` accepts the app-owned migrated database and logger, completes atomic recovery/pruning before NATS, and leaves database lifetime with assembly; `Service.Run` installs delivery once and exposes the operational method set `Run`, `Register`, `ReceiveObservation`, `GetEntity`, and `ExecuteCommand`.
- [ ] The final-shape gate proves the old constructor/repository/catalog seams, persistence fakes, and deleted files are absent.
- [ ] Receipt disposition/rejection is returned only after commit; receipt, State, and linked Command satisfaction remain atomic.
- [ ] Periodic pruning, work draining, shutdown linearization, recovery-time interruption, and no-redispatch/no-false-outcome behavior pass deterministic race tests.
- [ ] HTTP and NATS ingress use narrow consumer-owned interfaces; Command delivery uses the module-owned seam; domain-aware NATS mapping is module-local; `hearthd` contains assembly only.
- [ ] Registration and Observation ingress callbacks join before NATS or SQLite closes on every initialized failure path.
- [ ] Preserved HTTP, NATS, SQLite, SDK, configuration, ID-helper, and error contracts pass compatibility and integration tests.
- [ ] `docs/architecture.md`, `specs/first-light.md`, and `specs/unified-entity-support.md` describe the resulting ownership and interface.
- [ ] Canonical validation passes with `devenv test`; pull-request CI also passes `check-device-module-compat.sh` against its base.

## Ordered deliverables

| ID | Deliverable | Effort | Depends on | Green commit |
|---|---|---:|---|---|
| D1 | Characterize exact HTTP/OpenAPI and existing `hearthd` NATS mappings without moving production code. | M | — | `test(devices): characterize module contracts and mappings` |
| D2 | Deepen `devices.Service` with private controls, atomic `New`, `Run`, work-token transfer, and shutdown semantics with temporary compatibility aliases. | L | D1 | `refactor(devices): deepen service lifecycle` |
| D3 | Move Registration, Observation/State, and Command SQLite implementation onto `Service`, retaining temporary forwarding needed by unmigrated callers/tests. | L | D2 | `refactor(devices): absorb SQLite implementation` |
| D4 | Replace persistence fakes with migrated SQLite, in-memory delivery, and deterministic lifecycle/race controls. | L | D3 | `test(devices): replace persistence fakes` |
| D5 | Add Registration callback joining, relocate NATS mapping, add narrow HTTP interfaces, and migrate application/integration callers. | L | D2, D4 | `refactor(devices): relocate adapters and migrate callers` |
| D6 | Remove temporary seams, privatize/regenerate the catalog, add static/compatibility gates, and prove no stale symbols. | M | D5 | `refactor(devices): remove shallow compatibility seams` |
| D7 | Update current architecture/specs and run full compatibility and validation gates. | M | D6 | `docs(devices): record deep module ownership` |

Temporary aliases and forwarding are allowed only from D2 through D5 and must be gone in D6. Each commit compiles and passes its focused tests.

## Documentation updates

`CONTEXT.md` retains the approved term:

> **Registration**: An Adapter instance request to establish or refresh a Binding from its Device and Entity descriptions. Repeated Registrations preserve unambiguous Canonical IDs and reject identity conflicts rather than guessing.

`docs/architecture.md` records that assembly owns database lifetime and migrations; `devices.Service` owns Device / Entity SQLite behavior and transactions; `New` owns NATS-independent recovery/pruning; `Run` installs delivery and owns operation, maintenance, and shutdown; domain-aware adapters sit beside the module; platform NATS remains domain-neutral; and Command delivery is the only injected production port.

Update obsolete shallow-Service, repository, Observation projection, and catalog-construction excerpts in `specs/first-light.md` and `specs/unified-entity-support.md`. Do not rewrite historical plans or accepted ADRs.

## Implementation risks

| Risk | Required control |
|---|---|
| Shutdown races terminal writes or `WaitGroup` registration | Commit linearizes outcomes; lifecycle mutex serializes token registration and stopping; deterministic tests cover both orders. |
| Single-connection SQLite tests deadlock | Gate at commit hooks and query through the active transaction. |
| Deadline and notification tests flake | Inject private deadlines/tickers/hooks; use no sleeps or polling. |
| NATS or external contracts drift during relocation | Characterize first, guard preserved paths, and retain transport/integration tests. |
| Temporary compatibility code or stale generated names survive | D6 shape gate plus generator checks reject them. |
| Behavior files become a new monolith | Keep behavior-first files with fixed SQLite companions; do not recreate a shared repository seam. |

## Open questions

None. Compatibility expansion requires a separate decision and re-estimation.
