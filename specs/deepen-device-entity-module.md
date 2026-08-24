# Deepen the Device / Entity module — implementation spec

- **Status:** Ready for task breakdown
- **Type:** Refactoring
- **Effort:** XL (5–9 engineering days, 70% confidence)
- **Approved design:** 2026-08-24

## Problem definition

**Who:** Hearth maintainers changing Device, Entity, Observation, State, and Command behavior.

**What:** The current `devices.Service` depends on an eight-method `Repository` interface whose only production adapter is `SQLiteRepository`. Registration, State, and Command tests must implement unrelated persistence methods, while the most important rules remain split between `Service`, repository adapters, and application assembly.

**Why it matters:** Recent first-light work repeatedly changes this path. The current seam reduces locality, encourages tests to reproduce persistence behavior, and makes one Device / Entity change require navigation through shallow modules and `hearthd` mapping code.

**Evidence:**

- `registration_test.go` and HTTP transport tests each implement seven irrelevant persistence methods.
- `command_test.go` contains an in-memory repository that reimplements Command transitions and linked Observation satisfaction.
- `hearthd/run.go` knows Device / Entity mapping, startup interruption, receipt pruning, and NATS Command translation.
- Observation receipt insertion, State projection, and linked Command satisfaction are correctly atomic today, but that invariant spans behavior and repository files.
- The affected test area contains 53 top-level tests across 20 files; focused race runs currently pass.

**Cost of not solving:** Every new Device / Entity behavior widens the broad repository seam or adds more duplicated test behavior. The module remains harder to change and harder for maintainers and coding agents to navigate.

## Constraints

1. One cohesive Device / Entity product module continues to own identity, reconciliation, State projection/read, and Command behavior.
2. A linked Observation receipt, State projection, and matching Command satisfaction commit in one SQLite transaction.
3. Application assembly owns the shared `*sql.DB`, migration execution, connection policy, and close order.
4. The Device / Entity module owns its SQLite implementation, startup recovery, receipt pruning, and in-memory Command work.
5. SQLite is local-substitutable; no persistence interface is introduced.
6. Command delivery is a real seam with a NATS production adapter and an in-memory test adapter.
7. `New` restores database invariants before NATS connection; `Run` installs Command delivery and owns operational lifetime, maintenance, and shutdown.
8. Product methods become operational when `Run` starts. Calls that race startup wait for `Run` or their own context cancellation.
9. Graceful and ungraceful shutdown use one recovery rule: unfinished Commands remain active until the next `New` marks them interrupted. ADR-0012 remains unchanged.
10. The implementation stays in one Go package. Registration, State, and Command behavior are deep methods on one concrete root type, not shallow delegated structs.
11. Domain-aware NATS mapping moves beside the Device / Entity module; domain-neutral NATS mechanics remain in `internal/platform/nats`.
12. Catalog overrides, clocks, IDs, deadlines, schedules, and race controls remain private test inputs.
13. Implementation is delivered as ordered green commits in one pull request.

## Compatibility policy

Compatibility changes were permitted during planning, but discovery found no concrete need outside internal Go callers.

| Surface | Policy |
|---|---|
| Internal Go callers | Breaking changes are expected. |
| HTTP | Preserve paths, bodies, operation IDs, OpenAPI schemas, status mappings, and stable errors. |
| NATS wire | Preserve subjects, schemas, envelopes, headers, correlation/causation, deadlines, and acknowledgement behavior. |
| SQLite | Preserve the existing schema, query sources, data, and migration history. |
| Public adapter SDK | Preserve exported types and Session behavior. |
| Configuration | Preserve all YAML fields and defaults. |

Any change to a preserved surface is scope expansion and requires re-estimation.

## Solution-space analysis

| Option | Depth | Locality | Cost | Decision |
|---|---|---|---|---|
| Test helper only | Leaves the broad production interface; removes panic-method boilerplate only | Low improvement | S | Rejected: fails the behavior-locality and navigation goals. |
| Narrow persistence interfaces | Smaller test adapters, but several hypothetical production seams | Medium | L | Rejected: SQLite has one implementation and tests can use it directly. |
| Separate Registration, State, and Command packages | Strong directory separation | Poor for atomic State/Command work; catalog and transactions leak across seams | XL | Rejected: contradicts the cohesive-module decision and weakens locality. |
| Concrete deep module | Small caller interface; SQLite and lifecycle hidden | Highest | XL | **Selected.** |
| Broad future-facing module | Adds history, pagination, detached Command operations | Speculative and shallow for absent callers | XL+ | Rejected by YAGNI. |

## Proposed solution

Replace `Service`, `Repository`, and `SQLiteRepository` with one concrete `devices.Module`. Application assembly first passes an already-open, migrated `*sql.DB` and logger to `New`, which constructs the built-in Entity-type catalog, performs startup interruption, and prunes expired Observation receipts before any NATS connection attempt. After NATS connects, application assembly passes the Command delivery adapter to `Run`; `Run` makes product methods operational and owns hourly pruning and module shutdown.

`Register`, `ReceiveObservation`, `GetEntity`, and `ExecuteCommand` are implemented directly on `*Module` in behavior-owned files. Those methods use sqlc packages and SQLite transactions internally. There is no repository adapter between behavior and SQLite.

HTTP and NATS adapters declare narrow consumer-owned Go interfaces. Domain-aware NATS translation moves from `hearthd/run.go` to `internal/modules/devices/nats`; validation, subjects, acknowledgement, request/reply, and tracing stay in `internal/platform/nats`.

## Implementation contract

### Domain types

Owner: `internal/modules/devices/model.go`.

```diff
diff --git a/internal/modules/devices/model.go b/internal/modules/devices/model.go
@@
 type Observation struct {
     ID                ObservationID
     EntityID          EntityID
     Value             Value
     AdapterReceivedAt time.Time
     SourceUpdatedAt   *time.Time
     RefreshForCommand *CommandID
 }
+
+// ReceivedObservation combines Adapter ownership and the Core-assigned
+// durable receive time with one Adapter-reported Observation.
+type ReceivedObservation struct {
+    AdapterID   string
+    Observation Observation
+    ObservedAt  time.Time
+}
@@
-type ProjectionResult struct {
-    Disposition      ObservationDisposition
-    State            *State
-    Rejection        *ObservationRejection
-    SatisfiedCommand *CommandResult
+// ObservationReceipt is returned only after the receipt transaction commits.
+// State and Command effects remain behind the module interface.
+type ObservationReceipt struct {
+    Disposition ObservationDisposition
+    Rejection   *ObservationRejection
 }
@@
-type CommandRequest struct {
+type CommandDispatch struct {
     ID            CommandID
     CorrelationID CorrelationID
     EntityID      EntityID
     OperationName OperationName
     Parameters    CommandParameters
     Deadline      time.Time
 }
```

Constraints:

- `ReceivedObservation.AdapterID` is a subject-safe Adapter instance slug.
- `ReceivedObservation.ObservedAt` is the trusted JetStream server timestamp, normalized to UTC.
- `ObservationReceipt` contains only durable disposition and rejection information. Callers observe current State through `GetEntity` and Command satisfaction through `ExecuteCommand`.
- Persistence-only `CommandRecord`, `CommandCompletion`, status/failure types, registration write parameters, and private projection details move to behavior-owned files and become unexported where no caller needs them.
- Byte slices and pointer timestamps are copied at the module seam as they are today.

No HTTP, NATS JSON, SDK, or SQLite schema type changes are required.

Final persistence-only names and owners are explicit:

- `registerBindingParams` in `registration_sqlite.go`.
- `observationProjection` in `observation_sqlite.go`, containing the public receipt plus private projected State and satisfied Command result used before return/notification.
- `commandRecord`, `commandCompletion`, `commandStatus`, and `commandFailureCode` in `command_sqlite.go`.
- `sqliteDBTX` and shared time/null conversion helpers in `sqlite.go`.

No persistence-only type is exported or crosses the Device / Entity module seam.

ID helper compatibility is explicit: `NewDeviceID`, `NewEntityID`, `NewObservationID`, `NewCommandID`, `NewCorrelationID`, and all five corresponding `Parse*ID` functions remain exported with unchanged signatures. Production module controls call the Device, Entity, Command, and Correlation generators; Observation IDs remain ingress-owned. Existing ID tests and integration callers do not migrate to private names.

### Module type and construction

Owner: `internal/modules/devices/module.go`.

```diff
diff --git a/internal/modules/devices/service.go b/internal/modules/devices/module.go
similarity index 35%
rename from internal/modules/devices/service.go
rename to internal/modules/devices/module.go
@@
-type Dependencies struct {
-    Now              func() time.Time
-    NewDeviceID      func() (DeviceID, error)
-    NewEntityID      func() (EntityID, error)
-    NewCommandID     func() (CommandID, error)
-    NewCorrelationID func() (CorrelationID, error)
-}
-
-type Service struct {
-    repository   Repository
-    sender       CommandSender
-    catalog      *TypeCatalog
-    dependencies Dependencies
-    waiters      commandWaiters
+type Module struct {
+    database  *sql.DB
+    delivery  CommandDelivery // installed exactly once by Run
+    catalog   *typeCatalog
+    logger    *slog.Logger
+    controls  moduleControls
+    waiters   commandWaiters
+    lifecycle moduleLifecycle
 }
@@
-func NewService(repository Repository, sender CommandSender, catalog *TypeCatalog, dependencies Dependencies) *Service
+func New(
+    ctx context.Context,
+    database *sql.DB,
+    logger *slog.Logger,
+) (*Module, error)
+
+func newModule(
+    ctx context.Context,
+    database *sql.DB,
+    logger *slog.Logger,
+    controls moduleControls,
+) (*Module, error)
+
+func (module *Module) Run(
+    ctx context.Context,
+    delivery CommandDelivery,
+) error
```

`New` invariants and side effects:

1. Reject a nil database.
2. Default a nil logger to `slog.Default()`.
3. Obtain a complete, validated `productionModuleControls()` value: UTC `time.Now`, UUIDv7 ID generators, a built-in catalog factory, 192-hour receipt retention, one-hour maintenance schedule, existing persistence timeout, and production deadline construction.
4. Invoke the catalog factory exactly once; production returns the built-in catalog and tests may return a private test catalog.
5. In one serializable startup transaction, mark persisted `requested` and `accepted` Commands `interrupted` with `core_restarted`, then delete expired Observation receipts while preserving the receipt referenced by current State.
6. Commit startup interruption and initial pruning together. If either operation fails, roll back both and return an error; a later `New` retries the same idempotent transaction and never redispatches a Command.
7. Return only after the startup transaction commits successfully.
8. Do not open, migrate, reconfigure, or close the database.
9. Start no goroutine and require no NATS dependency.

`Run` invariants and side effects:

1. Reject a nil delivery adapter before claiming the one-shot lifecycle.
2. Claim the module exactly once, install delivery, transition `constructed → running`, and release calls waiting for operational readiness.
3. A product-method call in `constructed` state waits for running state, its own context cancellation, or terminal module stop. Calls in `running` state proceed; calls in `stopping` or `stopped` state return `ErrModuleStopped`.
4. Own the hourly maintenance schedule. A prune failure is logged and retried at the next tick; it does not terminate `Run`.
5. On `ctx` cancellation, cancel the private module work context with cause `ErrModuleStopped` regardless of the caller context's cause, atomically transition to `stopping`, and reject new work.
6. Wait until tracked database operations, post-commit notifications, and Command lifecycles stop, transition to `stopped`, then return `nil` for normal cancellation.
7. Shutdown cancellation must not persist `adapter_unavailable`, `internal_failure`, `outcome_timeout`, or `interrupted` solely because the module stopped.
8. Unfinished `requested` and `accepted` records remain active for the next `New` to interrupt.
9. After `Run` returns, product methods return `ErrModuleStopped`; the instance cannot restart.
10. A repeated or concurrent non-nil `Run` call returns `ErrModuleAlreadyRun` and does not install another delivery adapter or schedule.
11. Application assembly closes NATS and the shared database only after `Run` returns.

Lifecycle states and work tokens:

```go
type moduleState uint8

const (
    moduleConstructed moduleState = iota
    moduleRunning
    moduleStopping
    moduleStopped
)
```

- `beginWork`, transition to stopping, and `WaitGroup.Add` execute under one lifecycle mutex, preventing `Add` from racing `Wait`.
- Every product method acquires one work token before database access.
- `Register`, `ReceiveObservation`, and `GetEntity` release their token on return.
- `ExecuteCommand` transfers its existing token to the detached Command lifecycle after the `requested` commit; only that lifecycle releases it. If durable creation fails, the caller releases it. No post-commit goroutine performs a second `Add`.
- An Observation work token covers transaction commit and post-commit waiter publication, so `Run` cannot return between those events.

Cancellation and terminal-write linearization:

Use four distinct contexts:

1. `requestContext`: caller values and cancellation combined with module cancellation before durable creation.
2. `workContext`: derived from `context.WithoutCancel(callerCtx)`, preserving caller values while remaining cancelable by module stop.
3. `deadlineContext`: derived from `workContext` through `withDeadline`; used only for delivery and outcome waiting.
4. `writeContext`: derived from `workContext` with `persistenceTimeout`; used for acceptance, failure, timeout, and terminal reconciliation writes so deadline expiry permits an outcome write while module stop still cancels it.

Additional rules:

- `writeContext` never derives from an already-expired `deadlineContext`.
- A committed terminal database write is the winner. If satisfaction or another terminal update commits before stopping wins, retain and return that result.
- If module cancellation prevents or rolls back a terminal update first, leave the Command active and return `*CommandExecutionError` wrapping `ErrModuleStopped`.
- A satisfaction commit that precedes stopping must be published to its waiter before the Command lifecycle may return stopped.
- Any terminal-state read performed during an open transaction uses that transaction's sqlc handle, never an independent `*sql.DB` query on the single SQLite connection.

### Private implementation controls

Owners: `moduleControls` and `moduleTicker` live in `internal/modules/devices/module.go`; `sqliteDBTX` lives in `internal/modules/devices/sqlite.go`. All are visible only to same-package tests through `newModule`.

```go
type sqliteDBTX interface {
    ExecContext(context.Context, string, ...any) (sql.Result, error)
    PrepareContext(context.Context, string) (*sql.Stmt, error)
    QueryContext(context.Context, string, ...any) (*sql.Rows, error)
    QueryRowContext(context.Context, string, ...any) *sql.Row
}

type moduleTicker interface {
    C() <-chan time.Time
    Stop()
}

type moduleControls struct {
    now                    func() time.Time
    newDeviceID            func() (DeviceID, error)
    newEntityID            func() (EntityID, error)
    newCommandID           func() (CommandID, error)
    newCorrelationID       func() (CorrelationID, error)
    newCatalog             func() (*typeCatalog, error)
    withDeadline           func(context.Context, time.Time) (context.Context, context.CancelFunc)
    newTicker              func(time.Duration) moduleTicker
    pruneReceipts          func(context.Context, sqliteDBTX, time.Time) error
    receiptRetention       time.Duration
    pruneInterval          time.Duration
    persistenceTimeout     time.Duration
    beforeObservationCommit func()
    afterObservationCommit  func()
}
```

Rules:

- `productionModuleControls() moduleControls` returns the complete production value. `newModule` rejects an incomplete control set; only `beforeObservationCommit` and `afterObservationCommit` may be nil.
- Production callers cannot override these controls.
- `withDeadline` preserves parent cancellation and reports `context.DeadlineExceeded` for deadline expiry.
- `newTicker` allows deterministic maintenance ticks without real time.
- `pruneReceipts` defaults to the real SQLite implementation and accepts either the startup transaction or module database; tests may return a one-shot fault and then delegate to the real implementation to verify rollback, logging, and retry without reproducing pruning behavior.
- `beforeObservationCommit`, when non-nil, runs after all transaction writes and immediately before `Commit`, allowing deterministic cancellation/rollback tests.
- `afterObservationCommit`, when non-nil, runs immediately after successful commit, before waiter notification, and before the Observation work token is released.
- Do not add exported options or a general clock interface.

### Command delivery seam

Owner: `internal/modules/devices/command.go`.

```diff
diff --git a/internal/modules/devices/repository.go b/internal/modules/devices/command.go
@@
-type CommandSender interface {
-    Send(context.Context, string, CommandRequest) (CommandAcceptance, error)
+type CommandDelivery interface {
+    Deliver(
+        context.Context,
+        string,
+        CommandDispatch,
+    ) (CommandAcceptance, error)
 }
```

The module owns this interface because it consumes Command delivery. Two adapters justify the seam:

- `internal/modules/devices/nats.CommandDelivery` in production.
- An in-memory function adapter in module tests.

Delivery behavior:

- The module commits `requested` before calling `Deliver`.
- Delivery occurs exactly once and is never queued, retried by the module, or replayed.
- `Accepted: false` maps to upstream rejection.
- `ErrAdapterUnavailable` classifies no responder, disconnect, draining, or pre-acceptance request expiry.
- Any other delivery error maps to internal Command failure unless module shutdown caused it.
- Context values and trace context from the initiating caller survive caller cancellation via `context.WithoutCancel`, but module shutdown and the absolute Command deadline still cancel delivery.

### Stable errors

Owner: `internal/modules/devices/errors.go`.

```go
var (
    ErrModuleStopped          = errors.New("Device / Entity module stopped")
    ErrModuleAlreadyRun       = errors.New("Device / Entity module Run already called")
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

Rules:

- Product methods started after stopping begins return `ErrModuleStopped` without writes.
- `Run` with nil delivery returns `ErrCommandDeliveryRequired` without claiming the lifecycle; repeated non-nil `Run` returns `ErrModuleAlreadyRun`.
- A caller waiting on a durably created Command receives `&CommandExecutionError{CommandID: id, Err: ErrModuleStopped}` when module shutdown wins; Command execution failures always use the pointer form so existing `errors.As` behavior remains stable.
- Caller-request cancellation remains distinct: after durable creation, the caller may receive its request context error while the Command lifecycle continues.
- A transaction canceled by module shutdown rolls back and returns `ErrModuleStopped`.
- Registration rejection codes and existing HTTP error mappings remain unchanged.

### Registration behavior

Owner: `internal/modules/devices/registration.go`.

```diff
diff --git a/internal/modules/devices/registration.go b/internal/modules/devices/registration.go
@@
-func (service *Service) Register(
+func (module *Module) Register(
     ctx context.Context,
     adapterID string,
     registration Registration,
 ) (Binding, error)
```

Move the registration SQL and transaction implementation from `sqlite_repository.go` into `registration_sqlite.go`. Do not introduce a registration repository type.

One serializable transaction continues to own:

1. Binding lookup.
2. external Device identity check.
3. Device create/update.
4. Binding create/update.
5. Entity mapping lookup.
6. immutable Entity type check.
7. external Entity identity check.
8. Entity create/update.
9. mapping create/update.
10. commit before canonical IDs are returned.

The module validates and normalizes support before writing, copies caller input, preserves canonical IDs on unambiguous re-registration, and rolls back every write on conflict.

### Observation and State behavior

Owner: `internal/modules/devices/observation.go`.

```diff
diff --git a/internal/modules/devices/observation.go b/internal/modules/devices/observation.go
@@
-func (service *Service) ProjectObservation(
-    ctx context.Context,
-    adapterID string,
-    observation Observation,
-    observedAt time.Time,
-) (ProjectionResult, error)
+func (module *Module) ReceiveObservation(
+    ctx context.Context,
+    received ReceivedObservation,
+) (ObservationReceipt, error)
@@
-func (service *Service) GetEntity(ctx context.Context, id EntityID) (EntityView, error)
+func (module *Module) GetEntity(ctx context.Context, id EntityID) (EntityView, error)
```

Move entity reads, Observation persistence, State mapping, and linked Command satisfaction from `sqlite_observations.go` into `observation_sqlite.go`; move periodic pruning orchestration to `maintenance.go`. Do not introduce an Observation repository type.

One serializable Observation transaction continues to own:

1. Exact Observation-ID duplicate lookup.
2. Entity and owning Adapter instance lookup.
3. support and incoming value validation.
4. durable receipt insertion and `receive_order` assignment.
5. State upsert for both applied and unchanged values.
6. optional linked Command evaluation and satisfaction.
7. commit.

Required behavior:

- Duplicate returns `DispositionDuplicate` without reevaluating ownership, support, value, State, or Command.
- Unknown Entity, wrong owner, and invalid value commit rejected receipts and return nil error.
- Same-value Observations advance State evidence and timestamps.
- Current State follows receive order, never timestamps.
- Linked Command satisfaction commits atomically with receipt and State.
- Waiter notification occurs strictly after commit.
- Module shutdown before commit rolls back. A successful commit is the linearization point: shutdown after commit preserves the committed State/Command result and the tracked post-commit notification runs before shutdown completes.
- Receipt expiry remains `ObservedAt + 192h`.
- Pruning compares timestamps chronologically and preserves the receipt referenced by current State.

### Command behavior

Owner: `internal/modules/devices/command.go`.

```diff
diff --git a/internal/modules/devices/command.go b/internal/modules/devices/command.go
@@
-func (service *Service) ExecuteCommand(
+func (module *Module) ExecuteCommand(
     ctx context.Context,
     entityID EntityID,
     operationName OperationName,
     parameters CommandParameters,
 ) (CommandResult, error)
```

Move Command row mapping and write operations from `sqlite_repository.go` into `command_sqlite.go`. Keep linked satisfaction in the Observation transaction while calling private Command outcome helpers in the same package.

Required ordering:

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

Race invariants:

- Commands for one Entity overlap independently.
- An immediate linked Observation cannot be missed.
- `MarkCommandAccepted` may fill `accepted_at` after satisfaction but cannot regress status.
- SQLite status predicates serialize satisfaction against timeout and failure writers.
- If satisfaction committed first, failure/timeout/shutdown handling returns the satisfying waiter result after tracked post-commit notification.
- If another terminal update committed first, a late linked Observation may advance State but cannot change the terminal Command.
- If module cancellation prevents or rolls back the terminal update first, shutdown exits without a terminal write and leaves the record recoverable.

### Catalog visibility

Owners: `internal/modules/devices/catalog.go`, generated catalog output, and `internal/cmd/entitytypegen`.

The production constructor no longer accepts a catalog. Make catalog construction and authoring symbols private when no external caller remains:

```diff
-type TypeCatalog struct { ... }
-func NewTypeCatalog(...) (*TypeCatalog, error)
-func NewBuiltinTypeCatalog() (*TypeCatalog, error)
+type typeCatalog struct { ... }
+func newTypeCatalog(...) (*typeCatalog, error)
+func newBuiltinTypeCatalog() (*typeCatalog, error)
```

Apply equivalent private naming to `EntityTypeDefinition`, `OperationDefinition`, `ResolvedCommand`, `DefineEntityType`, and `DefineOperation`; discovery confirmed only same-package and generated use. Update generator templates and regenerate checked-in output. Catalog tests remain same-package tests through the private implementation.

### HTTP adapter interfaces

Owner: `internal/modules/devices/api/get_entity.go` and `command.go`.

```diff
diff --git a/internal/modules/devices/api/get_entity.go b/internal/modules/devices/api/get_entity.go
@@
-type Handler struct {
-    devices *devices.Service
+type EntityReader interface {
+    GetEntity(context.Context, devices.EntityID) (devices.EntityView, error)
 }

-func Register(api huma.API, service *devices.Service)
+type CommandExecutor interface {
+    ExecuteCommand(
+        context.Context,
+        devices.EntityID,
+        devices.OperationName,
+        devices.CommandParameters,
+    ) (devices.CommandResult, error)
+}
+
+type Dependencies struct {
+    Entities EntityReader
+    Commands CommandExecutor
+}
+
+type Handler struct {
+    entities EntityReader
+    commands CommandExecutor
+}
+
+func Register(api huma.API, dependencies Dependencies)
```

The HTTP adapter owns these interfaces because it consumes the behavior. Production passes the same `*devices.Module` for both fields. Tests use narrow adapters and do not construct persistence.

Owner: `internal/app/hearthd/server.go`.

```diff
-func NewHTTPHandler(service *devices.Service, readiness ReadinessChecker) (http.Handler, huma.API)
+func NewHTTPHandler(
+    deviceDependencies devicesapi.Dependencies,
+    readiness ReadinessChecker,
+) (http.Handler, huma.API)
```

`NewHTTPHandler` passes `deviceDependencies` unchanged to `devicesapi.Register`; readiness behavior and HTTP contracts do not change.

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

Ownership rules:

- Registration mapping preserves accepted/rejected responses and rejection codes.
- Observation mapping parses typed IDs and timestamps, copies JSON values, invokes `ReceiveObservation`, and ignores the successful receipt summary.
- Command mapping converts domain dispatch to platform request and maps `platformnats.ErrCommandUnavailable` to `devices.ErrAdapterUnavailable`.
- This package never acknowledges JetStream messages, constructs subjects directly, validates schemas, or manipulates trace headers.
- `internal/platform/nats` continues to own validation, routing, acknowledgement, subjects, request/reply, correlation checks, and tracing.
- The root `devices` package never imports its `nats` child package; application assembly imports both and avoids a cycle.

### Registration ingress join

Owner: new `internal/platform/nats/registration_lifecycle.go`; `registration_server.go` remains unchanged and compatibility-guarded.

```go
func (server *RegistrationServer) Closed() <-chan struct{}
```

`Closed` calls the existing subscription's `StatusChanged(natsgo.SubscriptionClosed)` and returns a derived `<-chan struct{}` that closes when the status channel reports or closes. NATS closes a drained subscription only after queued callbacks finish, so a blocked Registration handler keeps `Closed` open. A nil/uninitialized server returns an already-closed channel, matching `ObservationConsumer.Closed`. Application assembly calls `Closed` once before `Drain`; the one small forwarding goroutine terminates when the subscription closes. Add a transport test proving the blocked-handler behavior. This is additive lifecycle coordination only; Registration wire handling is unchanged.

### Application assembly

Owner: `internal/app/hearthd/run.go` and `server.go`.

Target assembly order:

```text
Open shared database
→ Run migrations
→ devices.New                   # recovery and initial prune; no NATS dependency
→ Connect and provision NATS
→ Construct NATS Command delivery adapter
→ Launch devices.Module.Run(ctx, delivery)
→ Start Registration and Observation ingress
→ Register HTTP adapter
→ On every exit path, initiate ingress quiescing without awaiting handlers
→ Cancel the dedicated Module.Run context
→ Await Module.Run, ObservationConsumer.Closed, RegistrationServer.Closed, and HTTP shutdown
→ Drain/close NATS
→ Close shared database
```

Remove from `hearthd/run.go`:

- catalog construction;
- `NewSQLiteRepository`;
- direct startup interruption and pruning;
- `NewService`;
- `natsCommandSender`;
- `registrationHandler`;
- `observationHandler`;
- `domainObservation`;
- receipt-prune ticker;
- Device / Entity mapping helpers.

Application assembly retains process configuration, database migration/lifetime, NATS resource provisioning, HTTP server lifecycle, readiness, and shutdown ordering.

`Module.Run` uses a dedicated child context canceled explicitly by assembly, rather than relying on HTTP shutdown completion. On normal cancellation, HTTP serve failure, or any ingress startup failure after `Run` launches, cleanup must continue even after an earlier shutdown error: initiate HTTP listener shutdown and NATS ingress drains, cancel the module immediately so active handlers unblock, join the module plus both NATS `Closed` channels and HTTP shutdown, then drain NATS and close SQLite. The five-second HTTP shutdown timeout must not bypass the module join or database close ordering.

Deterministic application-lifecycle tests use one private control value; it is not a production interface:

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

`productionAppControls()` returns no-op startup checks, `server.ListenAndServe`, and a no-op observer. The private startup checks run only after `Module.Run` launches: one immediately before Registration startup and one after Registration succeeds but before Observation startup. Tests return sentinel errors there to exercise both partial-startup cleanup paths. `onShutdownStep` runs after each named action/join, allowing exact ordering assertions without substituting database or NATS behavior.

Application error semantics are stable:

```go
func joinRunErrors(primary error, cleanup ...error) error
```

- A startup-hook, ingress-start, NATS, or non-`http.ErrServerClosed` serve failure is the primary error.
- Parent-context cancellation is a normal shutdown signal and contributes no primary error.
- Cleanup always continues. Non-ignorable cleanup errors are appended in action order.
- `joinRunErrors` passes the primary first to `errors.Join`, followed by non-nil cleanup errors. The return is nil only when every input is nil; `errors.Is` must match every retained cause.
- Existing ignorable close errors, including `http.ErrServerClosed` and `natsgo.ErrConnectionClosed`, remain ignored.

`TestJoinRunErrors` fixes these rules, and lifecycle tests assert their injected startup/serve sentinel remains discoverable after cleanup.

## Project layout

```text
CONTEXT.md                                             # modify — canonical Registration term (already resolved)
docs/architecture.md                                  # modify — Module, SQLite ownership, lifecycle, adapter locality
specs/first-light.md                                  # modify — replace obsolete Service/repository interface contract
specs/unified-entity-support.md                        # modify — update obsolete Service/catalog terminology
specs/deepen-device-entity-module.md                   # new — this implementation contract

internal/modules/devices/
├── module.go                                          # move/modify — concrete Module, New, Run, lifecycle, private controls
├── errors.go                                          # new — stable module and Command execution errors
├── model.go                                           # modify — ReceivedObservation, receipt summary, delivery types
├── registration.go                                    # modify — Registration interface, validation, normalization
├── registration_sqlite.go                             # new — Registration-owned SQLite transaction
├── observation.go                                     # modify — Observation/State interface and validation
├── observation_sqlite.go                              # new — Observation-owned State/receipt/satisfaction transaction
├── command.go                                         # modify — Command lifecycle, waiters, delivery seam
├── command_sqlite.go                                  # new — Command-owned SQLite writes and row mapping
├── sqlite.go                                          # new — small shared SQLite time/null helpers; no adapter type
├── maintenance.go                                     # new — startup interruption and periodic pruning
├── catalog.go                                         # modify — private catalog implementation
├── catalog_test.go                                    # modify — renamed private catalog symbols
├── ids.go                                             # unchanged — exported New*/Parse* domain ID helpers remain
├── zz_generated_entitytypes.go                        # regenerate — private built-in catalog assembly
├── zz_generated_entitytypes_test.go                   # regenerate — private catalog conformance names
├── service.go                                         # delete after module move
├── repository.go                                      # delete — broad persistence interfaces
├── sqlite_repository.go                               # delete after behavior moves
├── sqlite_observations.go                             # delete after behavior moves
├── test_helpers_test.go                               # new — migrated SQLite and in-memory delivery fixtures
├── module_lifecycle_test.go                           # new — Run, shutdown, recovery, one-shot lifecycle
├── registration_test.go                               # modify — Module Registration behavior on migrated SQLite
├── registration_persistence_test.go                   # new — transaction assertions moved from SQLite repository tests
├── observation_test.go                                # new — Module Observation/State behavior on migrated SQLite
├── observation_persistence_test.go                    # new — transaction assertions moved from SQLite observation tests
├── command_test.go                                    # modify — real SQLite + in-memory Command delivery
├── command_persistence_test.go                        # new — Command row invariants moved from repository tests
├── sqlite_repository_test.go                          # delete after tests move by ownership
├── sqlite_observations_test.go                        # delete after tests move by ownership
├── api/
│   ├── get_entity.go                                  # modify — consumer-owned EntityReader
│   ├── command.go                                     # modify — consumer-owned CommandExecutor
│   ├── get_entity_test.go                             # modify — narrow transport adapter
│   └── command_test.go                                # modify — narrow transport adapter
└── nats/
    ├── command_delivery.go                            # new — NATS Command delivery adapter
    ├── command_delivery_test.go                       # new — mapping and error classification
    ├── registration.go                               # new — Registration wire/domain mapping
    ├── registration_test.go                          # new — accepted/rejected mapping
    ├── observation.go                                # new — Observation wire/domain mapping
    └── observation_test.go                           # new — IDs, times, JSON, receipt behavior

internal/app/hearthd/
├── run.go                                             # modify — compose Module and module-owned NATS adapters
├── server.go                                          # modify — pass HTTP adapter Dependencies
├── server_test.go                                     # modify — remove persistence test adapter
├── run_integration_test.go                            # modify — Module construction and assertions
├── run_lifecycle_test.go                              # new — failure cleanup and close-order controls
├── recovery_integration_test.go                       # modify — graceful stop and pre-NATS recovery
└── simulator_matrix_integration_test.go               # modify — remove exported repository access

internal/cmd/entitytypegen/
├── render_catalog.go                                  # modify — private generated catalog names
└── render_catalog_conformance.go                      # modify — private conformance names

internal/platform/nats/
├── registration_lifecycle.go                          # new — expose drain completion
├── registration_lifecycle_test.go                     # new — prove callback join
├── registration_server.go                             # unchanged and compatibility-guarded
└── registration_server_test.go                        # unchanged

scripts/
├── check-device-module-shape.sh                       # new — stale-symbol/deleted-file gate
└── check-device-module-compat.sh                      # new — PR-base preserved-path gate

devenv.nix                                             # modify — run static shape gate in devenv test
.github/workflows/check.yml                            # modify — fetch history and run PR compatibility gate

internal/platform/db/migrations/**                     # unchanged
internal/platform/db/queries/**                        # unchanged
internal/platform/db/sqlc/**                           # regenerate and require no semantic diff
configs/**                                              # unchanged
contracts/v1/**                                        # unchanged
sdk/adapter/**                                         # unchanged
```

Behavior-prefixed SQLite companion files keep SQL beside its owning behavior without recreating a cross-role repository module.

## Test strategy

### Module behavior tests

Use `platformdb.Open` with a unique migrated named-memory SQLite database such as `file:devices-test-<uuid>?mode=memory&cache=shared` for ordinary tests, retaining its owning connection for the test lifetime. This preserves the production foreign-key, busy-timeout, and `SetMaxOpenConns(1)` policy; SQLite necessarily reports `journal_mode=memory` for `mode=memory`, so existing `internal/platform/db` file-backed tests remain responsible for WAL policy. Use a temporary file-backed database for reopen/recovery tests. Use an in-memory `CommandDelivery`; no test adapter may implement Device / Entity persistence behavior.

D4 owns the shared fixtures in `test_helpers_test.go`:

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
func newDeviceTestModule(t *testing.T, controls moduleControls) (*Module, *sql.DB)
```

The delivery records one call then invokes the required `deliver` function. The database helper calls `platformdb.Open`, runs migrations, and registers cleanup. The module helper calls `newModule` with the supplied complete controls; tests needing production controls pass `productionModuleControls()` explicitly.

Rewrite existing behavior tests through the new module interface:

- Registration error classification, normalization, idempotence, descriptor updates, conflicts, concurrency, and atomic rollback.
- Observation applied/unchanged/duplicate/rejected behavior, receive ordering, durable receipts, pruning, and State reads.
- Command commit-before-delivery, acceptance, rejection, unavailable adapter, internal failure, outcome timeout, overlapping Commands, linked mismatch, and caller cancellation.

### Deterministic race tests

Cover without wall-clock sleeps:

1. Linked Observation commits before acceptance write.
2. Linked Observation beats adapter-unavailable, rejection, and internal delivery failures.
3. Observation and deadline races in both commit orders.
4. Opposite overlapping Commands complete in reverse order with independent waiters and final receive-order State.
5. Caller cancellation after durable creation does not stop lifecycle work.
6. Late notification after waiter removal does not block, panic, mutate another Command, or leak work.

### Lifecycle and constructor tests

Add:

1. `New` rejects a nil database, defaults a nil logger, starts no goroutine, and does not close or reconfigure the database.
2. `New` interrupts active records and never redispatches them, including when subsequent NATS connection fails.
3. `New` performs initial pruning before return.
4. Product methods called before `Run` wait for running state or caller-context cancellation.
5. `Run` rejects nil delivery without consuming the one-shot claim.
6. `Run` prunes on a private ticker event and retries after a captured logged failure.
7. Cancellation while delivery is blocked stops work without a terminal failure write.
8. Cancellation while an accepted Command waits for Observation leaves the record active.
9. Shutdown races independently with unavailable, rejected, internal-error, deadline, and satisfying outcomes; only a committed database outcome survives.
10. Observation cancellation before commit rolls back; cancellation after commit preserves State and publishes any satisfying result before shutdown completes.
11. `Register`, `ReceiveObservation`, `GetEntity`, and `ExecuteCommand` all reject work once stopping begins without writes.
12. `Run` waits for active database operations and transferred Command work tokens to stop.
13. Repeated or concurrent `Run` calls return `ErrModuleAlreadyRun` without another adapter or ticker.
14. A fresh `New` after graceful stop marks unfinished records interrupted.
15. Database close after `Run` returns produces no use-after-close errors.
16. Delivery occurs exactly once and retains a caller context value/trace span after caller cancellation.

Exact new/rewritten test ownership:

| Path | Required test |
|---|---|
| `module_lifecycle_test.go` | `TestNewRequiresDatabaseAndDefaultsLogger` |
| `module_lifecycle_test.go` | `TestNewRecoversAndPrunesAtomicallyBeforeNATS` |
| `module_lifecycle_test.go` | `TestRunRequiresDeliveryAndIsOneShot` |
| `module_lifecycle_test.go` | `TestRunRetriesPruning` |
| `module_lifecycle_test.go` | `TestRunStopsWorkAndWaitsForDatabaseUse` |
| `module_lifecycle_test.go` | `TestModuleMethodsWaitForRunAndRejectAfterStop` |
| `module_lifecycle_test.go` | `TestNewInterruptsCommandsLeftByGracefulStop` |
| `observation_test.go` | `TestReceiveObservationCancellationAroundCommit` with before/after-commit subtests |
| `command_test.go` | `TestExecuteCommandCommitsBeforeDeliveryAndHandlesAcceptanceRace` |
| `command_test.go` | `TestExecuteCommandReturnsSatisfiedWhenObservationWinsDeliveryFailureRace` |
| `command_test.go` | `TestExecuteCommandSerializesDeadlineAndObservation` with both commit orders |
| `command_test.go` | `TestExecuteCommandKeepsOverlappingCommandsIndependent` |
| `command_test.go` | `TestExecuteCommandContinuesAfterCallerCancellation` |
| `command_test.go` | `TestExecuteCommandShutdownRaceMatrix` |
| `command_test.go` | `TestExecuteCommandPreservesContextValuesAfterCallerCancellation` |
| `command_test.go` | `TestExecuteCommandLateNotificationAfterWaiterRemoval` |
| `internal/platform/nats/registration_lifecycle_test.go` | `TestRegistrationServerClosedWaitsForHandler` |
| `internal/app/hearthd/run_lifecycle_test.go` | `TestRunCleansUpAfterHTTPServeFailure` |
| `internal/app/hearthd/run_lifecycle_test.go` | `TestRunCleansUpAfterIngressStartupFailure` with Registration/Observation subtests |
| `internal/app/hearthd/run_lifecycle_test.go` | `TestRunJoinsModuleAndIngressBeforeClosingResources` |
| `internal/app/hearthd/run_lifecycle_test.go` | `TestJoinRunErrors` |
| `internal/app/hearthd/recovery_integration_test.go` | `TestGracefulStopDefersInterruptionUntilNextStartup` |
| `internal/app/hearthd/recovery_integration_test.go` | `TestRunRecoversCommandsBeforeNATSConnectionFailure` |

Focused commands:

```sh
go test -race ./internal/modules/devices -run 'Test(New|Run|ModuleMethods|ReceiveObservationCancellation|ExecuteCommand)'
go test -race -count=10 ./internal/modules/devices -run 'Test(ReceiveObservationCancellation|ExecuteCommand)'
go test -race ./internal/platform/nats -run TestRegistrationServerClosedWaitsForHandler
go test -race ./internal/app/hearthd -run 'Test(RunCleansUp|RunJoins|GracefulStop|RunRecovers)'
```

### Persistence invariant tests

Direct SQL inspection is allowed only for facts absent from the external interface:

- transaction rollback and row counts;
- normalized stored JSON;
- receipt expiration/pinning;
- monotonic Command transitions and terminal row fields;
- startup interruption timestamps/failure code;
- absence of writes after module shutdown.

These tests inspect the SQLite implementation; they do not replace it.

### Transport tests

- HTTP tests fake `EntityReader` or `CommandExecutor`, never persistence.
- D1 extends `TestRuntimeOpenAPIContract` before changing transport construction so its exact request, response, error, and nullability expectations characterize the pre-refactor contract.
- D1 adds characterization tests around the existing `hearthd` NATS mappings before relocation.
- Device NATS adapter tests preserve those exact mapping and error expectations after relocation.
- `internal/platform/nats` tests continue to cover schema validation, subjects, acknowledgement, tracing, request/reply, and Registration callback joining.
- Full `hearthd` NATS/HTTP/recovery/simulator integration tests remain.

### Static and compatibility gates

`scripts/check-device-module-shape.sh` is a zero-argument static gate run by `devenv test`. It must:

1. fail on root-module definitions or uses of `Service`, `NewService`, `Repository`, `RegistrationRepository`, `CommandLedger`, `SQLiteRepository`, `NewSQLiteRepository`, `CommandSender`, `CommandRequest`, `RegisterBindingParams`, `ProjectObservationParams`, `ProjectObservation`, `ProjectionResult`, `CommandRecord`, `CommandCompletion`, `CommandStatus`, `CommandFailureCode`, `devices.Dependencies`, `TypeCatalog`, `NewTypeCatalog`, `NewBuiltinTypeCatalog`, `EntityTypeDefinition`, `OperationDefinition`, `DefineEntityType`, `DefineOperation`, `ResolvedCommand`, or exported `ObservationReceiptRetention`;
2. require deletion of `service.go`, `repository.go`, `sqlite_repository.go`, and `sqlite_observations.go`;
3. fail when a root Device / Entity test implements any former persistence-interface method, regardless of fake type name;
4. permit `platformnats.CommandRequest` inside the child NATS adapter.

Required shape-script implementation:

```sh
#!/usr/bin/env bash
set -euo pipefail
root=internal/modules/devices
pattern='\b(NewService|Service|NewSQLiteRepository|SQLiteRepository|Repository|RegistrationRepository|CommandLedger|CommandSender|CommandRequest|RegisterBindingParams|ProjectObservationParams|ProjectObservation|ProjectionResult|CommandRecord|CommandCompletion|CommandStatus|CommandFailureCode|TypeCatalog|NewTypeCatalog|NewBuiltinTypeCatalog|EntityTypeDefinition|OperationDefinition|DefineEntityType|DefineOperation|ResolvedCommand|ObservationReceiptRetention)\b|devices\.Dependencies'
if rg -n --glob '*.go' --glob '!**/nats/**' "$pattern" "$root" internal/app/hearthd; then
  echo 'stale shallow module symbol found' >&2
  exit 1
fi
if rg -n '^type Dependencies struct' "$root"/*.go; then
  echo 'root Dependencies type must remain private' >&2
  exit 1
fi
persistence_method='^func \([^)]*\) (RegisterBinding|GetEntityView|ProjectObservation|CreateCommand|MarkCommandAccepted|CompleteCommand|InterruptActiveCommands|DeleteExpiredObservationReceipts)\('
if rg -n "$persistence_method" "$root"/*_test.go; then
  echo 'persistence test adapter method found' >&2
  exit 1
fi
for path in service.go repository.go sqlite_repository.go sqlite_observations.go; do
  test ! -e "$root/$path" || { echo "$root/$path still exists" >&2; exit 1; }
done
```

`scripts/check-device-module-compat.sh <base-sha>` compares the pull request with its base and fails on changes to:

- `contracts/v1`;
- `sdk/adapter`;
- `configs`;
- `internal/platform/db/migrations`;
- `internal/platform/db/queries`;
- wire/subject/codec/Command-client/Observation-consumer/JetStream/trace files in `internal/platform/nats`.

The new `registration_lifecycle.go` and its test are excluded because they add lifecycle joining; existing `registration_server.go` remains guarded.

Required compatibility-script implementation:

```sh
#!/usr/bin/env bash
set -euo pipefail
base="${1:?usage: check-device-module-compat.sh <base-sha>}"
git diff --exit-code "$base" -- \
  configs \
  contracts/v1 \
  sdk/adapter \
  internal/platform/db/migrations \
  internal/platform/db/queries \
  internal/platform/nats/codec.go \
  internal/platform/nats/wire.go \
  internal/platform/nats/subjects.go \
  internal/platform/nats/command_client.go \
  internal/platform/nats/observation_consumer.go \
  internal/platform/nats/registration_server.go \
  internal/platform/nats/jetstream.go \
  internal/platform/nats/trace.go
```

`devenv.nix` invokes `scripts/check-device-module-shape.sh` inside `hearth:test` and `enterTest`. `.github/workflows/check.yml` sets `fetch-depth: 0` and, for pull requests, runs:

```sh
scripts/check-device-module-compat.sh '${{ github.event.pull_request.base.sha }}'
```

Push checks still run the static gate through `devenv test`.

## Acceptance criteria

- [ ] `devices.New` accepts an app-owned migrated `*sql.DB` and logger, completes recovery before any NATS connection, and no caller constructs a repository or catalog.
- [ ] `devices.Module.Run` accepts the Command delivery adapter and makes the one-shot module operational.
- [ ] `devices.Module` exposes only `Run`, `Register`, `ReceiveObservation`, `GetEntity`, and `ExecuteCommand` as product behavior.
- [ ] `Repository`, `RegistrationRepository`, `CommandLedger`, `SQLiteRepository`, `NewSQLiteRepository`, `Service`, `NewService`, and root `devices.Dependencies` are absent.
- [ ] `ReceiveObservation` returns only disposition and rejection after commit.
- [ ] Observation receipt, State projection, and linked Command satisfaction remain one SQLite transaction.
- [ ] Startup recovery and initial pruning commit atomically before `New` returns; a prune failure rolls back interruption and a retry succeeds.
- [ ] `Run` owns periodic pruning, logs transient failures, stops all module work, and waits for database use to end.
- [ ] Graceful shutdown leaves unfinished Commands active; the next `New` marks them interrupted without redispatch.
- [ ] Module shutdown never persists a false adapter, timeout, internal, or interrupted outcome.
- [ ] HTTP and NATS adapters depend on narrow consumer-owned interfaces.
- [ ] Domain-aware NATS mapping lives in `internal/modules/devices/nats`; `hearthd` contains assembly only.
- [ ] `RegistrationServer.Closed` stays open until drained callbacks complete, and shutdown joins it before closing NATS or SQLite.
- [ ] No test adapter reimplements persistence behavior.
- [ ] Affected Command race and shutdown tests use private deterministic controls rather than wall-clock sleeps.
- [ ] HTTP behavior, NATS wire contracts, SQLite schema/query inputs, SDK behavior, and configuration remain unchanged.
- [ ] `docs/architecture.md`, `specs/first-light.md`, and `specs/unified-entity-support.md` describe the resulting interface and ownership accurately.
- [ ] `scripts/check-device-module-shape.sh` passes locally through `devenv test`.
- [ ] `scripts/check-device-module-compat.sh <base-sha>` passes in pull-request CI.
- [ ] Canonical validation passes:

  ```sh
  devenv test
  ```

## Ordered deliverables and green commits

| ID | Deliverable | Effort | Depends on |
|---|---|---:|---|
| D1 | Characterize exact HTTP/OpenAPI and existing `hearthd` NATS mappings without moving production code | M | — |
| D2 | Introduce `devices.Module`, private controls, atomic `New`, `Run(ctx, delivery)`, work-token transfer, and shutdown semantics while retaining temporary compatibility aliases | L | D1 |
| D3 | Move Registration, Observation/State, and Command SQLite implementation onto `Module`; retain temporary forwarding interfaces/adapters required by unmigrated tests and callers | L | D2 |
| D4 | Replace persistence-reimplementing tests with migrated SQLite, in-memory Command delivery, and deterministic lifecycle/race controls | L | D3 |
| D5 | Add Registration callback joining; relocate domain-aware NATS mapping; add narrow HTTP interfaces; migrate application/integration callers while temporary aliases remain | L | D2, D4 |
| D6 | Remove every temporary interface, adapter, alias, and forwarding path; privatize catalog construction; regenerate; add static/compatibility gates; prove no stale symbols | M | D5 |
| D7 | Update architecture/current specs and run PR compatibility/full validation gates | M | D6 |

Recommended commit sequence:

1. `test(devices): characterize module contracts and mappings`
2. `refactor(devices): add concrete module lifecycle`
3. `refactor(devices): absorb SQLite implementation`
4. `test(devices): replace persistence fakes`
5. `refactor(devices): relocate adapters and migrate callers`
6. `refactor(devices): remove shallow compatibility seams`
7. `docs(devices): record deep module ownership`

Every commit must compile and pass its focused tests. Temporary aliases or forwarding are allowed only between D2 and D5 and must not survive D6.

## Verification by stage

### D1

```sh
go test -race ./internal/modules/devices/api ./internal/app/hearthd ./internal/platform/nats
```

### D2–D4

```sh
go test -race ./internal/modules/devices/...
go test -race ./internal/app/hearthd
```

Run affected race tests repeatedly while stabilizing controls:

```sh
go test -race -count=10 ./internal/modules/devices/...
```

### D5–D7

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

Static and PR-level compatibility gates:

```sh
scripts/check-device-module-shape.sh
base="$(git merge-base HEAD origin/main)"
scripts/check-device-module-compat.sh "$base"
```

Extend `TestRuntimeOpenAPIContract` to compare the complete request, response, error, and nullability schemas for the two Entity paths, not only operation IDs and property presence. Existing contract and SDK compatibility tests remain required.

## Risks and mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---:|---:|---|
| Module shutdown races a terminal Command write and creates a false outcome | Medium | High | Use commit as the linearization point, cancel transactions with a distinct module cause, and test every terminal/shutdown ordering. |
| `WaitGroup.Add` races `Run` shutdown waiting | Medium | High | Register work under a lifecycle mutex before releasing the stopped-state check. |
| SQLite single-connection tests deadlock by querying during an open transaction | Medium | High | Gate at explicit commit points; never issue an independent query while the transaction holds the sole connection. |
| Deadline tests remain flaky | Medium | Medium | Inject private `withDeadline`; remove affected wall-clock sleeps and polling. |
| NATS mapping drifts during relocation | Low | High | Add characterization tests before moving; preserve platform NATS and contract tests unchanged. |
| Temporary compatibility code survives | Medium | Medium | Isolate it to D2–D5; D6 static gate rejects every old symbol and deleted file. |
| Direct SQLite logic creates oversized files | Medium | Medium | Keep public behavior first and SQLite details in fixed behavior-prefixed companions; never recreate a shared repository seam. |
| Catalog privatization breaks generation | Low | Medium | Change generator templates and checked-in output together; run generator check at every catalog stage. |
| Graceful stop leaves active records longer than expected | Low | Medium | Document that interruption is recovery-owned; test fresh `New` after graceful stop and no redispatch. |

## Trade-offs made

| Chose | Over | Because |
|---|---|---|
| Concrete SQLite implementation | Persistence interface | One production implementation exists; migrated SQLite is the local test substitute. |
| One concrete `devices.Module` | Separate Registration/State/Command packages | Atomic State/Command behavior and shared catalog rules need locality. |
| Root behavior methods | Delegating internal structs | Separate structs fail the deletion test and add hypothetical seams. |
| Explicit `Run` | Hidden constructor goroutine | Application assembly retains clear lifetime and database close ordering. |
| Recovery-time interruption | Shutdown-time interruption | One rule handles graceful and ungraceful termination and preserves ADR-0012. |
| Receipt summary | Full projection result | Ingress needs durable disposition, not internal State or waiter details. |
| Consumer-owned HTTP/NATS interfaces | Concrete transport dependencies | Production and test adapters make those seams real and narrow. |
| Preserve external contracts | Use broad permission to redesign | No contract change increases module depth for this refactor. |

## Non-goals

- New Device or Entity behavior.
- New HTTP endpoints, request/response shapes, or error codes.
- New NATS subjects, schemas, envelope fields, retention, or delivery semantics.
- SQLite schema, migration, or query-source redesign.
- A second persistence adapter or generic transaction abstraction.
- Cross-module transactions for future product modules.
- SDK changes.
- Runtime Entity-type loading or catalog extension.
- General NATS wire-type deduplication.
- Authentication, availability, deployment, or automation semantics.

## Documentation updates

### `CONTEXT.md`

Retain the newly resolved term:

> **Registration**: An Adapter instance request to establish or refresh a Binding from its Device and Entity descriptions. Repeated Registrations preserve unambiguous Canonical IDs and reject identity conflicts rather than guessing.

### `docs/architecture.md`

Record:

- application assembly owns the shared database and migrations;
- `devices.Module` owns Device / Entity SQLite implementation and transactions;
- `New` owns NATS-independent startup recovery and initial pruning;
- `Run` installs Command delivery, makes behavior operational, and owns periodic pruning and module shutdown;
- domain-aware HTTP/NATS mapping belongs beside the module;
- platform NATS remains domain-neutral mechanics;
- Command delivery is the only injected production port.

### Current implementation specs

Update obsolete `Service`, repository, `ProjectObservation`, and catalog-construction excerpts in:

- `specs/first-light.md`
- `specs/unified-entity-support.md`

Do not rewrite historical plans or accepted ADRs.

## Success metrics

1. Zero production persistence interfaces or adapters in `internal/modules/devices`.
2. Zero tests that reimplement Registration, State, receipt, or Command persistence behavior.
3. Zero stale references to removed `Service`/repository symbols.
4. All Device / Entity callers cross one concrete deep module or a narrow consumer-owned transport interface.
5. All existing external contract and integration tests pass unchanged in meaning.
6. Affected race tests pass ten repeated `-race` runs without sleep-based synchronization.
7. `hearthd/run.go` contains assembly and lifecycle only, with no Device / Entity wire mapping or receipt timer.

## Open questions

None. Compatibility expansion requires a separate decision and re-estimation.
