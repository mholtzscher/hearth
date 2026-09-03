# Asynchronous command evidence and the Zigbee2MQTT runtime coordinator

**Status:** Draft for review
**Type:** Refactoring feature plan
**Effort:** XL, 4 to 7 focused days at 65% confidence
**Date:** 2026-09-02
**Baseline:** branch `implement-z2m` at `a42bc53`
**Depends on:** `zigbee2mqtt-adapter.md`

## Problem

The Adapter SDK links an Observation to a Command through two hidden requirements. The Adapter sets `Observation.RefreshForCommand` to a raw Command ID, then passes the original Command handler context to `Session.PublishObservation`. The SDK reads correlation metadata from that context.

The SDK cancels the context when the handler returns. An Adapter that waits for an external report must therefore keep the handler alive until it publishes the report or reaches the Command deadline.

This is awkward for Zigbee2MQTT. Core invokes handlers concurrently, MQTT State arrives on another goroutine, and Zigbee2MQTT does not include Hearth Command IDs in State reports. The current implementation shares routes, connections, matchers, and Command state between these goroutines. It coordinates them with a broad mutex, a route-lifecycle RW mutex, per-IEEE locks, and matcher channels. Route teardown can also wait behind a blocked MQTT publication.

The wire model is already right. Core receives acceptance separately from outcome evidence, and Observation publication waits for JetStream acknowledgement. This change keeps those rules while removing the handler-context dependency and giving Zigbee Command state one owner.

## Decision and scope

Make a direct SDK source break:

- `Responder.Accept` returns a `CommandEvidence` capability after it publishes acceptance.
- The capability publishes Observations linked to that accepted Command without the handler context.
- Public SDK Observation types no longer accept a raw Command link.
- The wire field `refresh_for_command_id`, envelope causation, NATS subjects, schemas, Core behavior, and persistence stay unchanged.

Add one private Adapter runtime coordinator to Zigbee2MQTT. It owns routes, MQTT generations, per-IEEE queues, Command attempts, matchers, deadlines, and State disposition. `HandleCommand` returns after `/set` receives PUBACK and `Accept` succeeds. The coordinator continues `/get`, State matching, linked publication, and queue cleanup.

Blocking MQTT and JetStream calls run in tracked effect goroutines. The coordinator only changes state and handles completion events. `mqttRelay` keeps its mutex because Paho callbacks, queue consumption, and closure remain concurrent.

This specification supersedes the Command-runtime sections of `specs/zigbee2mqtt-adapter.md`, including its mutex-protected route snapshot, context locks, handler-owned matcher lifecycle, and original-context publication rule. Its discovery, identity, registration, health, availability, State normalization, MQTT, and user-visible Command rules still apply.

These product rules remain fixed:

- Acceptance does not satisfy a Command. A fresh linked Observation must match the Entity-type outcome policy.
- The link says Hearth observed the requested outcome. It does not claim causation.
- Every accepted Command triggers `/get`, including no-op Commands.
- Core may overlap Commands. Zigbee2MQTT serializes them per IEEE Device because its protocol cannot correlate them. Different IEEE Devices may proceed concurrently.
- Queue time consumes the existing absolute deadline. The Adapter does not persist or replay Command work.
- Core receive order still determines canonical State.
- Availability remains advisory when Adapter health permits dispatch.
- Runtime fencing, Entity enablement, and Core's transactional Command and Observation rules remain authoritative.
- The coordinator holds only ephemeral state rebuilt from Core mappings and Zigbee2MQTT inventory.

No Core, wire, schema, database, HTTP, configuration, discovery, or State-normalization changes belong in this work. Home Assistant and simulator only migrate to the new SDK interface. They do not need asynchronous runtimes.

## Deliverables

| Deliverable | Effort | Depends on |
|---|---:|---|
| D1. Add the SDK Command-evidence capability and update generated Observation types | L | None |
| D2. Migrate Home Assistant, simulator, test adapters, and direct SDK callers | L | D1 |
| D3. Replace Zigbee shared Command state with the runtime coordinator | XL | D1 |
| D4. Run integration and mutation tests, record the decision, and update superseded docs | L | D2, D3 |

## SDK contract

### Public types

Owner: `sdk/adapter/types.go`.

```diff
diff --git a/sdk/adapter/types.go b/sdk/adapter/types.go
@@
 type Observation struct {
     EntityID          string          `json:"entity_id"`
     Value             json.RawMessage `json:"value"`
     AdapterReceivedAt string          `json:"adapter_received_at"`
     SourceUpdatedAt   *string         `json:"source_updated_at,omitempty"`
-    RefreshForCommand *string         `json:"refresh_for_command_id,omitempty"`
 }
+
+// CommandEvidence publishes Observations linked to one accepted Command.
+type CommandEvidence interface {
+    PublishObservation(context.Context, Observation) (ObservationID, error)
+}
@@
 type Responder interface {
-    Accept() error
+    Accept() (CommandEvidence, error)
     Reject(message string) error
     RejectUnavailable(message string) error
 }
```

`CommandEvidence` is an interface so Adapter tests can provide fakes. Production returns a private immutable implementation. `Command.ID` and `Command.CorrelationID` remain available for diagnostics, but neither grants publication authority.

### Evidence semantics

Owner: `sdk/adapter/session.go` and new `sdk/adapter/command_evidence.go`.

1. `Accept` publishes the current accepted response before returning evidence.
2. A successful response consumes the one-shot responder and returns non-nil evidence.
3. A failed response returns `nil, err` without consuming the responder. The caller may retry.
4. Any response after a successful response returns `ErrAlreadyResponded`. Repeated `Accept` returns nil evidence with that error. Rejection never returns evidence.
5. Evidence stores the Command ID, correlation ID, Entity ID, runtime, absolute deadline, and trace context.
6. Evidence rejects another Entity with `ValidationError` before publication.
7. One capability may publish zero, one, or several Observations before its deadline. Home Assistant needs more than one when a stale refresh precedes the matching refresh.
8. The context passed to `CommandEvidence.PublishObservation` may shorten the call. It cannot extend the Command deadline and need not be the handler context.
9. Handler return does not cancel evidence. The Command deadline, Session closure, or fencing does.
10. Each publication creates one Observation ID and retries the same encoded envelope and ID until JetStream acknowledges it, the effective context ends, or the Session fails.
11. Evidence injects the accepted Command's correlation, causation, and trace metadata. It does not take those values from the caller context.
12. A call started after the deadline returns `context.DeadlineExceeded` without publishing.

The SDK must combine caller cancellation with the stored Command deadline. It may use `context.WithoutCancel` to detach stored trace data from handler return, but it must not let that extend the deadline.

### Wire encoding

The public `Observation` type no longer mirrors the complete wire payload. The SDK keeps the link private:

```go
type wireObservation struct {
    EntityID          string          `json:"entity_id"`
    Value             json.RawMessage `json:"value"`
    AdapterReceivedAt string          `json:"adapter_received_at"`
    SourceUpdatedAt   *string         `json:"source_updated_at,omitempty"`
    RefreshForCommand *string         `json:"refresh_for_command_id,omitempty"`
}

type observationLink struct {
    commandID     string
    correlationID string
    entityID      string
    deadline      time.Time
    traceContext  trace.SpanContext
}
```

`Session.PublishObservation` encodes `wireObservation` without a link. `commandEvidence.PublishObservation` calls the same private publisher with an `observationLink`. The shared publisher still owns validation, envelope creation, W3C propagation, stable retry identity, JetStream acknowledgement, fencing checks, and error classification.

Keep these structs private. The public Observation types must not regain a raw Command ID.

### Generated Observation types

Owner: `internal/cmd/entitytypegen/render_sdk.go`.

```diff
diff --git a/internal/cmd/entitytypegen/render_sdk.go b/internal/cmd/entitytypegen/render_sdk.go
@@ generated ObservationInput
 type ObservationInput struct {
     EntityID          string
     Support           Support
     State             State
     AdapterReceivedAt time.Time
     SourceUpdatedAt   *time.Time
-    RefreshForCommand *string
 }
```

Generated `NewObservation` functions build ordinary Observation values. The evidence capability adds Command linkage during publication. Regenerate every checked-in facade.

`ServeCommands` still invokes handlers concurrently. Typed routing, parameter decoding, response payloads, ordinary Observation durability, and Entity-type outcome policies do not change.

## Caller migration

Home Assistant captures evidence from `Accept` and uses it for each refresh tied to that Command. Remove the private `refreshForCommand *string` parameters. Subscription and startup State still use `Session.PublishObservation`. Its handler may remain blocked until its current refresh flow finishes.

Simulator captures evidence for successful and no-op scenarios. Timeout and interrupted scenarios accept without publishing evidence.

Test responders return fake evidence publishers. Adapter tests record ordinary and linked publications separately instead of reading `Observation.RefreshForCommand`. Tests that deliberately send invalid or late wire payloads use private wire fixtures rather than weakening the SDK type.

## Zigbee2MQTT runtime contract

### Ownership and process shape

Owner: new `internal/adapters/zigbee2mqtt/runtime.go`.

The runtime coordinator alone mutates:

- active routes, route revision, MQTT generation, and dispatchable connection;
- Command attempts and phases;
- per-IEEE FIFO queues;
- active matchers, claimed State, and deadline timers.

The connection loop owns `connectionSync`, inventory, availability evidence, pending MQTT messages, and its immutable `runtimeDevice` snapshot. The same serial loop already owns `knownMappings`. A route snapshot becomes immutable when reconciliation sends it to the coordinator.

Remove the broad Adapter mutex, `routeLifecycle`, `deviceLocks`, `contextLock`, shared route and matcher maps, and matcher channels. Keep the MQTT relay mutex, Paho `closeOnce`, SDK responder lock, and test-fake locks.

Owner: `internal/adapters/zigbee2mqtt/adapter.go`.

```diff
diff --git a/internal/adapters/zigbee2mqtt/adapter.go b/internal/adapters/zigbee2mqtt/adapter.go
@@
 type Adapter struct {
     session Session
     config  Config
     logger  *slog.Logger
     dialer  mqttDialer
-
-    routeLifecycle   sync.RWMutex
-    mutex            sync.Mutex
-    connection       mqttConnection
-    connectionCancel context.CancelCauseFunc
-    generation       uint64
-    healthy          bool
-    routeSerial      uint64
-    routes           map[string]commandRoute
-    devices          map[string]runtimeDevice
-    matchers         map[string]*commandMatcher
-    deviceLocks      map[string]*contextLock
+
+    runtimeEvents chan runtimeEvent
+    runtimeDone   chan struct{}

     knownMappings map[mappingKey]adapter.OwnedMapping
     knownOrder    []mappingKey
     retryDelay    func(time.Duration) time.Duration
 }
```

`Run` starts the MQTT reconnect loop and the runtime coordinator under one child context. A terminal error from either cancels and joins the other. Parent cancellation remains graceful. Application assembly still runs `Adapter.Run` beside `Session.ServeCommands`.

### Events

Events that can arrive late carry an attempt ID, route revision, or MQTT generation:

```go
type runtimeEvent interface{ runtimeEvent() }

type commandSubmitted struct {
    ctx       context.Context
    command   adapter.Command
    responder adapter.Responder
    result    chan error // buffer 1
}

type routeActivationResult struct {
    revision uint64
    err      error
}

type routesActivated struct {
    generation uint64
    connection mqttConnection
    disconnect context.CancelCauseFunc
    snapshot   routeSnapshot
    result     chan routeActivationResult // buffer 1
}

type routesInvalidated struct {
    generation uint64
    cause      error
    result     chan error // buffer 1
}

type stateCandidate struct {
    generation    uint64
    routeRevision uint64
    entityID      string
    state         decodedEntityState
    retained      bool
    receivedAt    time.Time
    result        chan stateDisposition // buffer 1
}

type setPublishFinished struct {
    attemptID uint64
    err       error
}

type getPublishFinished struct {
    attemptID uint64
    err       error
}

type linkedPublishFinished struct {
    attemptID uint64
    err       error
}

type fallbackPublishFinished struct {
    attemptID uint64
    err       error
}

type attemptDeadlineReached struct {
    attemptID uint64
}

type stateDisposition uint8

const (
    stateOrdinary stateDisposition = iota
    stateClaimed
)
```

Every event send and reply selects on its caller context or `runtimeDone`. Handler, timer, connection, and effect goroutines must not remain blocked after coordinator shutdown.

### State

```go
type runtimeCoordinator struct {
    generation    uint64
    routeRevision uint64
    connection    mqttConnection
    disconnect    context.CancelCauseFunc
    dispatchable  bool

    routes        map[string]commandRoute
    deviceQueues  map[string]*deviceCommandQueue
    matchers      map[string]*commandAttempt
    attempts      map[uint64]*commandAttempt
    nextAttemptID uint64
}

type deviceCommandQueue struct {
    active *commandAttempt
    queued []*commandAttempt
}

type commandAttempt struct {
    id            uint64
    generation    uint64
    routeRevision uint64
    command       adapter.Command
    responder     adapter.Responder
    handlerResult chan error

    route        commandRoute
    payload      []byte
    desired      desiredState
    deadline     time.Time
    dispatchedAt time.Time

    phase         commandPhase
    evidence      adapter.CommandEvidence
    claimed       *matchedState
    deadlineTimer *time.Timer

    mqttContext context.Context
    cancelMQTT  context.CancelFunc
}

type commandPhase uint8

const (
    commandQueued commandPhase = iota
    commandPublishingSet
    commandAwaitingEvidence
    commandPublishingLinked
    commandPublishingFallback
    commandTerminal
)
```

These definitions fix ownership and stale-event identity. Implementation may regroup fields without changing those rules. Use one `time.AfterFunc` or equivalent timer per attempt. Its callback only sends `attemptDeadlineReached`. Expected household volume does not justify a deadline heap.

### Submission and dispatch

`HandleCommand` sends `commandSubmitted`. It waits for the buffered result, its context, or coordinator shutdown and never reads routes directly.

The coordinator resolves the route and invokes the generated power or brightness handler to decode parameters. The typed closure builds the MQTT payload and normalized target. The coordinator then queues the attempt by IEEE address. Queues are FIFO by coordinator receipt. An expired queued attempt never reaches MQTT.

When a Device becomes idle, the coordinator:

1. installs the matcher and records `dispatchedAt` immediately before launch;
2. starts `/set` at QoS 1 in an effect goroutine;
3. continues handling State, deadlines, route changes, and other Device queues while waiting for PUBACK;
4. holds one eligible early match;
5. calls `Accept` after PUBACK and stores the returned evidence;
6. launches `/get` before completing the handler result;
7. lets `HandleCommand` return;
8. keeps the Device active until linked or fallback publication finishes, or until a terminal path with no claimed State finishes.

`/get` does not delay handler return or linked publication. The Adapter attempts it for every accepted Command, even when an early natural match already exists. `/get` failure is logged. It cannot retract acceptance or cause another response.

MQTT and Observation calls never run in the coordinator loop. `Responder.Accept` may run there because it only writes the existing NATS response and does not wait for outcome evidence.

### State matching and disposition

A State candidate can satisfy the active matcher only when it:

- has the same MQTT generation and route revision;
- is non-retained;
- belongs to the exact Entity and discovered property;
- decodes and normalizes to the requested value;
- arrived after `dispatchedAt` and no later than the Command deadline;
- reaches an attempt that has not claimed State or started linked publication.

The coordinator returns `stateClaimed` only after taking ownership of the report. The connection loop publishes every other valid report ordinarily, including sibling properties and nonmatching values.

A claimed report has one disposition:

```text
not claimed                      ordinary publication by the connection loop
claimed before linking starts    ordinary fallback if linking becomes impossible
linked publication started       linked publication only
```

Linked publication uses `CommandEvidence.PublishObservation`. Fallback uses `Session.PublishObservation` with the Adapter runtime context so an expired Command context does not discard valid State. The Device remains active until that publication finishes.

Use ordinary fallback when a claim exists and linking has not started before `/set` failure, failed acceptance, deadline, route replacement, or MQTT disconnect. Once linked JetStream publication starts, do not send an ordinary fallback. A lost acknowledgement makes persistence ambiguous, and a second payload could duplicate the report. The SDK retries the original linked envelope and Observation ID until its deadline.

A terminal Session or fencing error from linked or fallback publication stops the Adapter runtime. A linked deadline error releases the Device and lets Core time out.

### Routes, connection loss, and shutdown

Reconciliation builds an immutable `routeSnapshot`, sends `routesActivated`, and waits for its revision. The connection loop stores that revision beside its local Device snapshot and includes it with State candidates. Replacement invalidates attempts from the old revision before exposing new routes.

Activate routes before reporting healthy. This avoids a healthy Adapter with no local Command routes. If healthy reporting fails, invalidate the new routes before returning.

Unhealthy transitions and connection teardown wait for `routesInvalidated` acknowledgement. Invalidation makes routes nondispatchable, cancels `/set` and `/get`, and rejects unaccepted queued or active handlers as unavailable when they can still receive a response. Accepted Commands get no second response. Held pre-link claims fall back ordinarily. Attempt, generation, and revision checks ignore stale completions and State.

A non-context `/set` failure rejects the unaccepted Command and invokes the stored connection cancellation cause. The existing reconnect loop owns recovery. Linked publication already in progress does not depend on MQTT and may finish before its evidence deadline.

The coordinator tracks every effect. Shutdown cancels their contexts, lets completion sends observe shutdown, and joins them before `Run` returns and application assembly closes the Session. It must not wait while an effect can finish only by sending to an event channel that nobody drains. Process shutdown does not promise fallback publication after the Session lifecycle ends.

### Error behavior

| Event | Command response | Claimed State | Runtime action |
|---|---|---|---|
| Route missing or replaced before `/set` | `RejectUnavailable` | Ordinary fallback if present | Do not publish `/set` |
| Queue or `/set` deadline | No late response | Ordinary fallback if present | Release Device; keep connection unless separately lost |
| Non-context `/set` failure | `RejectUnavailable` when possible | Ordinary fallback | Cancel connection and reconnect |
| Acceptance publication failure | Not accepted | Ordinary fallback | Keep MQTT connection |
| `/get` failure | Already accepted | Keep waiting for a natural match | Log only |
| Match deadline before linking | Already accepted | Ordinary fallback | Core times out |
| Linked publication fails after start | Already accepted | No ordinary duplicate | Core times out unless the envelope persisted |
| MQTT loss or route replacement | Reject only unaccepted attempts | Ordinary fallback before link start | Invalidate generation or revision |
| Terminal SDK or fencing error | No second response | Keep one disposition where possible | Stop Adapter runtime |

## Files

```text
sdk/adapter/
├── types.go                         # modify, public CommandEvidence and Responder contract
├── session.go                       # modify, shared ordinary and linked publication path
├── command_evidence.go              # new, capability lifetime and private link encoding
├── session_test.go                  # modify, evidence and responder tests
├── typed/handler_test.go            # modify, responder migration
├── powerv1/zz_generated_facade.go   # generate, ObservationInput without raw linkage
└── brightnessv1/zz_generated_facade.go # generate, ObservationInput without raw linkage
internal/cmd/entitytypegen/
├── render_sdk.go                    # modify, generate ordinary Observation inputs
└── main_test.go                     # modify, generated contract checks
internal/adapters/
├── homeassistant/
│   ├── adapter.go                   # modify, publish Command refresh through evidence
│   └── adapter_test.go              # modify, evidence fake and repeated publication
├── simulator/
│   ├── simulator.go                 # modify, publish Command refresh through evidence
│   └── simulator_test.go            # modify, evidence and no-evidence scenarios
└── zigbee2mqtt/
    ├── adapter.go                   # modify, runtime channels and supervision
    ├── runtime.go                   # new, state owner, queues, events, effects, cleanup
    ├── runtime_test.go              # new, state-machine and stale-event tests
    ├── command.go                   # modify, typed submission and MQTT translation
    ├── command_test.go              # modify, asynchronous evidence behavior
    ├── command_concurrency_test.go  # modify, Device queues and cross-Device overlap
    ├── connection.go                # modify, local generation and runtime events
    ├── connection_test.go           # modify, teardown and generation fencing
    ├── routes.go                    # remove, logic moves to runtime.go
    ├── observation.go               # modify, submit State candidates
    ├── reconcile.go                 # modify, acknowledged route activation
    ├── runtime_helpers_test.go      # modify, evidence and coordinator fakes
    └── mqtt_integration_test.go     # modify, blocked publish and disconnect
internal/app/
├── hearthd/
│   ├── run_integration_test.go      # modify, evidence capability
│   └── simulator_matrix_integration_test.go # modify, SDK callers and private wire fixtures
└── zigbee2mqtt/run_integration_test.go # modify, delayed evidence after handler return
docs/
├── architecture.md                  # modify, accepted SDK and runtime ownership
├── plans/0001-first-light.md        # modify, evidence terminology
└── adr/0018-return-command-evidence-capability.md # new, record the SDK decision
specs/
├── zigbee2mqtt-adapter.md           # modify, replace superseded Command sections
├── first-light.md                   # modify, SDK examples
├── unified-entity-support.md        # modify, generated Observation contract
├── adapter-health-and-entity-availability.md # modify, Responder signature
└── devices-nats-transport-ownership.md # modify, evidence capability references
```

Move immutable route-building types into `reconcile.go` or `command.go` when removing `routes.go`. Do not keep a pass-through file.

## Verification and acceptance

### SDK

Tests must prove:

- successful `Accept` returns evidence after response publication;
- failed `Accept` returns nil evidence and remains retryable;
- rejection and repeated response attempts provide no evidence;
- ordinary publication remains ordinary inside a handler context;
- evidence publishes after handler return and with a different caller context;
- the encoded Observation keeps the accepted Command's correlation, causation, trace, Entity, and runtime;
- caller cancellation shortens publication, while deadline, Session closure, and fencing stop new work;
- evidence rejects another Entity;
- one capability publishes several Observations with distinct IDs;
- transient retries keep one envelope and Observation ID;
- the linked envelope has equal `causation_id` and `refresh_for_command_id`.

The public `Observation` and generated `ObservationInput` must expose no raw link field. Existing wire schemas and Core code must remain unchanged.

### Zigbee2MQTT

Use deterministic fake effects and completion events instead of sleeps. Tests must prove:

- no blocking MQTT or Observation call runs in the coordinator loop;
- `HandleCommand` waits for `/set` PUBACK and acceptance, then returns before `/get`, matching State, or linked acknowledgement;
- the matcher exists before `/set` launch and can hold an early report;
- retained, pre-dispatch, stale-generation, stale-revision, wrong-Entity, wrong-property, wrong-value, and post-deadline reports stay ordinary;
- one report gets exactly one linked or ordinary disposition;
- same-IEEE Commands stay serialized until disposition completes, even after the first handler returns;
- different IEEE Devices publish concurrently;
- queued deadlines skip MQTT, and every terminal path releases the Device queue;
- a held report falls back once on every pre-link terminal path;
- route activation and invalidation acknowledgements order visibility;
- stale effect completions cannot change a recovered route or newer attempt;
- empty Device queues are removed;
- connection loss makes routes nondispatchable without waiting for `/set` PUBACK;
- shutdown unblocks handlers and joins effects.

Retain existing tests for no-op refresh, natural matches after `/get` failure, sibling State, brightness normalization, advisory offline availability, typed unavailable rejection, recovery, and MQTT generation matching.

The end-to-end Zigbee test must delay State until after handler return, then show that Core receives acceptance, receives one linked Observation with no ordinary duplicate, projects State, and satisfies the Command. It must also cover shutdown with a live coordinator effect.

### Required commands

- Run focused tests through the repository `mise` tasks and include race detection.
- Run `mise run mutation-test -- ./sdk/adapter`.
- Run `mise run mutation-test -- ./internal/adapters/zigbee2mqtt`.
- Run focused mutation tests for changed Home Assistant or simulator behavior when their tests gain new guarantees.
- Finish with `mise run validate` and include generated changes.

## Risks

| Risk | Mitigation |
|---|---|
| Early State arrives before PUBACK | Install the matcher first and keep the event loop free while `/set` waits |
| A deadline makes linked persistence ambiguous | Enforce the deadline and never send ordinary fallback after linked publication starts |
| Old effects mutate recovered state | Check attempt ID, route revision, and MQTT generation on every late event |
| Blocking work turns the coordinator into a global lock | Run MQTT and Observation calls as effects and test cross-IEEE progress |
| Shutdown leaks goroutines | Make sends shutdown-aware, cancel effects, drain safely, then join |

## Documentation and rollout

Land this as one incompatible repository change. Update all first-party callers and generated files together. There are no deployments that need a compatibility shim.

Add ADR 0018 for the evidence capability. Update `docs/architecture.md` and prior SDK examples. Replace the superseded Command sections in `specs/zigbee2mqtt-adapter.md` rather than leaving both mechanisms documented. `CONTEXT.md` and ADRs 0005, 0007, 0012, 0013, and 0017 remain correct.

## Open questions

None. The selected design is a breaking SDK replacement, a full Zigbee runtime coordinator, and ordinary fallback only before linked publication starts.
