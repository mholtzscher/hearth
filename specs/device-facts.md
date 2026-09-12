# Device Facts over Core NATS

**Status:** Implemented. D1–D3 landed with the implementation; D4 operator/developer documentation and downstream exact-name updates land with this change.
**Baseline:** `9bfee97`.
**Effort:** XL across four deliverables. This is a prerequisite for [Entity Event automations](entity-event-automations.md).

## 1. Problem and purpose

Hearth durably verifies device activity inside the `devices` module, but downstream consumers have no supported live interface for learning what Core committed.

Introduce **Device Facts**: versioned Core NATS messages published only after the devices-owned SQLite transaction establishing an accepted Observation, accepted Entity Event or Command status transition commits.

```text
Adapter input / Core Command lifecycle
                 ↓
        devices SQLite transaction
                 ↓ commit
       devices.DeviceFactSink
                 ↓
  ephemeral Core NATS publication
                 ↓
 live internal and external subscribers
```

They are a live, at-most-once notification surface, not another history or execution log; the durable SQLite record and HTTP read API remain authoritative.

Automations consume Core-verified accepted Entity Events through this surface, so their admission never couples to devices transactions and never replays missed events.

## 2. Decisions

- Core publishes on `hearth.v1.core.fact.>` using plain NATS pub/sub, not JetStream. Facts are externally supported, language-neutral contracts under `contracts/v1`.
- Subjects are Entity-first:

  ```text
  hearth.v1.core.fact.entity.<entity_id>.<family>.<variant>
  ```

- Each publication receives a new `fct_<UUIDv7>` envelope identity; the durable source identity remains in `data` and in `causation_id`.
- Accepted first-seen Observations publish `applied` or `unchanged`. Accepted first-seen Entity Events publish a fact whose variant is the event name. Rejected, duplicate and identity-conflict inputs publish nothing.
- Every actual durable Command status transition publishes a fact, including startup interruption. A command inserted directly in a terminal status publishes that terminal status only.
- Publication occurs after commit, never inside a transaction, and a failed publication never changes the committed operation's result.
- No acknowledgement, retry, reconnect buffering, outbox, offset, replay or Core-owned fact retention exists. One bounded in-memory dispatcher queue isolates committed device work from NATS write latency and drops rather than carrying facts across connection generations.
- Accepted Observation and Entity Event facts are gated by continuously connected NATS generations and epochs, so JetStream backlog never becomes live facts.
- Schemas expose canonical Entity and source-record data, not Adapter/runtime identities, bindings, fingerprints, receive-order counters or raw rejected values.

## 3. Scope

This spec owns:

- Device Fact vocabulary, domain projections and three strict external wire schemas;
- Core-originated subject construction and parsing;
- post-commit emission hooks in the devices service, including exact Command-transition reporting from persistence;
- a best-effort, non-buffering Core NATS fact publisher with generation-fenced connection-epoch freshness;
- application assembly, readiness, lifecycle, logging and operator documentation;
- integration tests proving post-commit, live-only external delivery.

**Non-goals:** durable facts, replay, offsets and delivery acknowledgements, or any Core-owned fact storage; subscriber registration APIs, a Go subscriber SDK and an HTTP fact endpoint; signing and authorization implementation; automation implementation; facts for rejected input, Adapter health, Entity availability, enablement, registration or Device-level Entities; global ordering; configurable freshness. The bounded volatile dispatcher queue is transport isolation, not durable delivery.

An external subscriber may independently persist received facts, but Hearth owns no behavior or compatibility guarantee for that downstream store beyond the published v1 wire contract.

## 4. Domain language and guarantees

Add this term to `CONTEXT.md` during implementation:

**Device Fact**:
One Core-verified statement published after the devices transaction establishing an accepted Observation, accepted Entity Event or durable Command status transition commits. It reaches only live Core NATS subscribers and is never acknowledged, retried, replayed or stored by Hearth. A missing fact proves nothing about the underlying activity: the durable record and HTTP read API remain authoritative, and a fact reports what Core recorded, not physical truth.
_Avoid_: Entity Event, Observation, event stream, event sourcing, change log

The external guarantee is:

> If a qualifying devices transition commits while its input is inside the current live connection epoch and the fact publishing connection accepts the publication, Core emits one schema-valid fact. Any subscriber, connection or process failure may lose that fact permanently, and Hearth never recreates it from history.

This is at-most-once notification, not exactly-once delivery. "One fact" describes Core emission for one transition, not receipt by every subscriber.

## 5. Subject contract

### 5.1 Grammar

```text
hearth.v1.core.fact.entity.<entity_id>.<family>.<variant>
```

The subject has exactly eight tokens:

| Position | Token | Rule |
|---:|---|---|
| 1 to 4 | `hearth.v1.core.fact` | fixed v1 Core Fact namespace |
| 5 | `entity` | fixed scope; leaves room for future non-Entity facts |
| 6 | `<entity_id>` | canonical `ent_<UUIDv7>` |
| 7 | `<family>` | `observation`, `entity-event` or `command` |
| 8 | `<variant>` | family-specific closed token or event-name slug |

Concrete subjects:

```text
hearth.v1.core.fact.entity.ent_<uuidv7>.observation.applied
hearth.v1.core.fact.entity.ent_<uuidv7>.observation.unchanged
hearth.v1.core.fact.entity.ent_<uuidv7>.entity-event.single_press
hearth.v1.core.fact.entity.ent_<uuidv7>.command.requested
hearth.v1.core.fact.entity.ent_<uuidv7>.command.satisfied
hearth.v1.core.fact.entity.ent_<uuidv7>.command.interrupted
```

Useful subscriptions:

```text
hearth.v1.core.fact.>                              # every Device Fact
hearth.v1.core.fact.entity.<entity_id>.>           # one Entity
hearth.v1.core.fact.entity.*.observation.applied   # State-changing Observations
hearth.v1.core.fact.entity.*.command.satisfied     # satisfied Commands
```

Any family or variant can be pinned the same way; `hearth.v1.core.fact.entity.*.entity-event.>` selects every accepted Entity Event. Subject and payload Entity, family and variant must agree, and subscribers must reject disagreement.

### 5.2 Subject types

Add to `internal/contracts/v1/natswire/subjects.go`:

```go
type DeviceFactFamily string

const (
    DeviceFactFamilyObservation DeviceFactFamily = "observation"
    DeviceFactFamilyEntityEvent DeviceFactFamily = "entity-event"
    DeviceFactFamilyCommand     DeviceFactFamily = "command"
)

type DeviceFactRoute struct {
    EntityID string
    Family   DeviceFactFamily
    Variant  string
}

func DeviceFactWildcard() string
func EntityDeviceFactsWildcard(entityID string) (string, error)
func DeviceFactFamilyWildcard(family DeviceFactFamily) (string, error)
func ObservationFactSubject(entityID string, disposition string) (string, error)
func EntityEventFactSubject(entityID string, name string) (string, error)
func CommandFactSubject(entityID string, status string) (string, error)
func ParseDeviceFactSubject(subject string) (DeviceFactRoute, error)
```

Builders reject noncanonical Entity IDs and invalid family variants, and each validates its exact variant set: Observation variants are `applied` and `unchanged`, Command variants are the eleven `CommandStatus` values, and Entity Event variants satisfy the implemented event-name slug pattern. The parser rejects any subject whose tokens do not round-trip.

`natswire` remains domain-neutral and imports no `devices` package. Its closed string constants mirror the schemas and are conformance-tested against devices values.

## 6. Wire schemas

Add `fct_id` to `contracts/v1/common.schema.json`:

```json
{"type":"string","pattern":"^fct_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"}
```

Each fact schema constrains its own `causation_id` directly to the durable source ID type (`obs_id`, `evt_id` or `cmd_id`). `fct_id` stays out of the common `causation_id` union because facts do not cause existing inbound contracts.

All three schemas use the standard envelope fields:

```json
{
  "id":"fct_<uuidv7>",
  "schema":"urn:hearth:schema:<family>-fact:v1",
  "emitted_at":"<Core publication time>",
  "correlation_id":"cor_<uuidv7>",
  "causation_id":"<durable source ID>",
  "data":{}
}
```

Envelope semantics:

- `id` identifies this one ephemeral publication, not the durable record, and `correlation_id` is copied from the accepted report or Command record.
- `emitted_at` is Core publication time from the fact publisher's clock, and `causation_id` identifies the durable Observation, Entity Event or Command transition source.
- W3C trace headers continue the context that caused Core to process the transition. `Nats-Msg-Id` is absent because no Core-owned stream or broker deduplication applies.

### 6.1 Observation fact

File: `contracts/v1/observation-fact.schema.json`  
Schema ID: `urn:hearth:schema:observation-fact:v1`  
Causation: `obs_id`

```json
{
  "id":"fct_…",
  "schema":"urn:hearth:schema:observation-fact:v1",
  "emitted_at":"…Z",
  "correlation_id":"cor_…",
  "causation_id":"obs_…",
  "data":{
    "observation_id":"obs_…",
    "entity_id":"ent_…",
    "disposition":"applied",
    "value":{"on":true},
    "adapter_received_at":"…Z",
    "source_updated_at":"…Z",
    "observed_at":"…Z"
  }
}
```

`disposition` is `applied` or `unchanged`, so the schema cannot represent rejected or duplicate dispositions. `value` is the normalized canonical State JSON committed for the accepted Observation, and `source_updated_at` is optional.

### 6.2 Entity event fact

File: `contracts/v1/entity-event-fact.schema.json`  
Schema ID: `urn:hearth:schema:entity-event-fact:v1`  
Causation: `evt_id`

The envelope is the standard one above, with `data`:

```json
{
  "event_id":"evt_…",
  "entity_id":"ent_…",
  "name":"single_press",
  "reported_at":"<SDK emitted_at>",
  "received_at":"<JetStream storage time>",
  "recorded_at":"<Core first-record time>"
}
```

`reported_at` deliberately differs from envelope `emitted_at`: it is the Adapter SDK's publication time copied from the durable event record. The schema represents accepted events only, so it carries no disposition field.

### 6.3 Command fact

File: `contracts/v1/command-fact.schema.json`  
Schema ID: `urn:hearth:schema:command-fact:v1`  
Causation: `cmd_id`

The envelope is the standard one above, with `data`:

```json
{
  "command_id":"cmd_…",
  "entity_id":"ent_…",
  "operation":"set",
  "parameters":{"value":true},
  "status":"satisfied",
  "requested_at":"…Z",
  "deadline_at":"…Z",
  "accepted_at":"…Z",
  "completed_at":"…Z",
  "outcome_observation_id":"obs_…"
}
```

The schema enumerates:

```text
requested | accepted | satisfied | dispatched | rejected |
adapter_unhealthy | entity_unavailable | outcome_timeout |
entity_disabled | internal_failure | interrupted
```

It mirrors the `commands` table invariants:

- `requested` and `accepted` are nonterminal and omit `completed_at`, `failure_code` and `outcome_observation_id`;
- `satisfied` requires `completed_at` and `outcome_observation_id` and omits `failure_code`;
- `dispatched` requires `completed_at` and omits outcome evidence and failure;
- failure statuses require `completed_at` and their matching `failure_code`;
- `interrupted` requires `completed_at` and `failure_code:core_restarted`;
- `accepted_at` is optional because satisfaction can race acceptance persistence under the existing Command contract.

Command facts include normalized parameters already exposed by Command history. They omit Adapter and runtime identity.

### 6.4 Compatibility

The strict schemas and `hearth.v1` subject prefix are one external v1 contract. Adding a family or a new major subject or schema is compatible. Adding a property, variant or enum value to an existing strict v1 schema is not assumed compatible; make an explicit versioned change.

## 7. Domain types and sink interface

New file `internal/modules/devices/device_facts.go`:

```go
type DeviceFactID string // fct_<UUIDv7>

type ObservationFact struct {
    ObservationID     ObservationID
    EntityID          EntityID
    Disposition       ObservationDisposition // applied | unchanged
    Value             Value
    CorrelationID     CorrelationID
    AdapterReceivedAt time.Time
    SourceUpdatedAt   *time.Time
    ObservedAt        time.Time // JetStream storage time
}

type EntityEventFact struct {
    EventID       EntityEventID
    EntityID      EntityID
    Name          EntityEventName
    CorrelationID CorrelationID
    ReportedAt    time.Time // SDK emitted_at
    ReceivedAt    time.Time // JetStream storage time
    RecordedAt    time.Time // Core first-record time
}

type CommandFact struct {
    Record CommandRecord
}

// DeviceFactSink receives only facts whose owning SQLite transition committed.
// Implementations own transport validation, freshness, logging and delivery.
// They must never return an error, block on NATS I/O, retry, durably retain a
// fact, or make a committed devices operation depend on publication.
type DeviceFactSink interface {
    ObservationAccepted(context.Context, ObservationFact)
    EntityEventAccepted(context.Context, EntityEventFact)
    CommandTransitioned(context.Context, CommandFact)
}
```

The three methods make invalid family and type combinations unrepresentable and state the devices-owned eligibility rule at each call site. A nil sink is a no-op, so focused devices tests and non-NATS assembly need no transport setup. The service stores the sink privately and calls it only after repository success: repositories never publish, and transport code never decides whether a rejection is a fact.

Extend `devices.Dependencies`:

```diff
 type Dependencies struct {
     Logger           *slog.Logger
     Now              func() time.Time
+    DeviceFacts      DeviceFactSink
     NewDeviceID      func() (DeviceID, error)
```

Add `NewDeviceFactID` and `ParseDeviceFactID` beside existing canonical ID constructors in `internal/modules/devices/ids.go`.

## 8. Post-commit emission

### 8.1 Observations

Extend the in-memory Observation input with the wire correlation; do not add a persistence column because facts are never reconstructed:

```diff
 type Observation struct {
     ID                ObservationID
     EntityID          EntityID
     Value             Value
+    CorrelationID     CorrelationID
     AdapterReceivedAt time.Time
```

`devices/nats.domainObservation` maps `envelope.CorrelationID`. `Service.ProjectObservation` validates and passes it through the transaction input.

After `stores.Observations.ProjectObservation` returns successfully:

1. notify the existing in-memory Command waiter first, so transport work cannot delay authoritative Command completion;
2. enqueue `ObservationFact` for `applied` and `unchanged` only, using `ProjectionResult.State.Value` as the normalized value, and enqueue nothing for `rejected` or `duplicate`;
3. if the same transaction satisfied a Command, enqueue the Observation fact before the satisfied Command fact.

Add the durable satisfied Command record alongside the existing waiter result:

```diff
 type ProjectionResult struct {
     Disposition      ObservationDisposition
     State            *State
     Rejection        *ObservationRejection
     SatisfiedCommand *CommandResult
+    SatisfiedCommandRecord *CommandRecord
 }
```

The SQLite projection constructs both values from the one committed transition. `SatisfiedCommand` continues serving the waiter; `SatisfiedCommandRecord` is the external fact projection.

### 8.2 Entity events

Extend first-seen results with Core record time:

```diff
 type EntityEventRecordResult struct {
     Outcome   EntityEventRecordOutcome
     Rejection *EntityEventRejection
+    RecordedAt time.Time // set only for first-seen accepted or rejected rows
 }
```

`SQLiteRepository.RecordEntityEvent` returns the same `recordedAt` it wrote in its transaction; duplicates and identity conflicts leave it zero. `Service.RecordEntityEvent` publishes only when `Outcome == EntityEventOutcomeAccepted`, using the trusted input event, the JetStream `receivedAt` parameter and the result `RecordedAt`.

### 8.3 Commands

Every real durable status transition produces one fact. Persistence must report whether a transition actually changed the row:

```go
type CommandTransition struct {
    Record  CommandRecord
    Changed bool
}
```

Change the ledger interface:

```diff
 type CommandLedger interface {
     CreateCommand(context.Context, CommandRecord) (CommandRecord, error)
-    MarkCommandAccepted(context.Context, CommandID, time.Time) error
-    CompleteCommand(context.Context, CommandCompletion) error
-    InterruptActiveCommands(context.Context, time.Time) error
+    MarkCommandAccepted(context.Context, CommandID, time.Time) (CommandTransition, error)
+    CompleteCommand(context.Context, CommandCompletion) (CommandTransition, error)
+    InterruptActiveCommands(context.Context, time.Time) ([]CommandRecord, error)
 }
```

Rules:

- `CreateCommand` returns the committed row. Publish exactly its persisted status. Immediate `entity_disabled` or `adapter_unhealthy` insertion emits one terminal fact and never invents a preceding `requested` transition.
- `MarkCommandAccepted` returns `Changed:true` only for `requested → accepted`. An already accepted or already satisfied row returns `Changed:false` without error. Other terminal states preserve their current error classification.
- `CompleteCommand` returns `Changed:true` only when it commits a terminal transition. Repeating the identical completion returns `Changed:false`; a conflicting completion retains `ErrCommandTerminal`.
- Observation-driven satisfaction returns the completed `CommandRecord` through `ProjectionResult.SatisfiedCommandRecord` and does not also call `CompleteCommand`.
- `InterruptActiveCommands` updates active rows atomically and returns every post-transition record; repeating the call returns an empty slice.

Publish only transitions that report `Changed`.

A fixed set of striped per-Command transition mutexes, owned by `devices.Service`, keeps facts for one Command in durable transition order. Every post-creation transition path (`MarkCommandAccepted`, completion, failure, timeout, and Observation projection carrying `RefreshForCommand`) locks the stripe before entering the transaction and holds it through fact enqueue; Command creation enqueues before spawning its worker. Hash collisions may conservatively serialize unrelated Commands, but cannot deadlock because each path acquires at most one stripe, and it acquires it before SQLite.

### 8.4 Startup interruption

Active Commands are interrupted even when later NATS startup fails:

1. `InterruptActiveCommands` commits before either NATS connection opens, and Core retains the returned records in memory;
2. once both connections and the dispatcher are up, Core enqueues one `interrupted` fact per retained record;
3. if either connection cannot start, Core startup fails as today. The committed interruptions remain authoritative and their facts are lost permanently, because a later startup's interruption call returns no rows.

## 9. Connection epochs and freshness

Plain NATS prevents subscriber replay, but the existing Observation and Entity Event JetStream consumers use `DeliverAll`. Without an additional gate, Core restart or reconnect backlog would be published as apparently live Device Facts.

### 9.1 Two Core connections

Keep the existing shared Core connection for request/reply and JetStream ingestion. Add a dedicated fact publication connection using the same configured NATS URL:

```go
natsgo.Name("hearthd-device-facts")
natsgo.MaxReconnects(-1)
natsgo.ReconnectWait(natsReconnectWait)
natsgo.ReconnectBufSize(-1)
natsgo.FlusherTimeout(coreNATSWriteTimeout)
```

`ReconnectBufSize(-1)` is required by pinned `nats.go v1.53.1`; zero restores the default buffer. A fact published while this connection is reconnecting returns an error and is dropped instead of being delivered later. `FlusherTimeout` is bounded to one second on both Core connections so the synchronous generation check, shutdown close and dispatcher join cannot wait on either connection's mutex for the client's one-minute default. The existing shared connection keeps its reconnect buffering and retry behavior.

The publisher waits for no PubAck and calls no `Flush` per message. `PublishMsg` success means the client accepted the live publication, not that any subscriber received it.

### 9.2 Generation-fenced epoch tracker

NATS lifecycle callbacks are asynchronous and may run after subscriptions resume. Callback time alone is therefore not a safe freshness fence. Add a concurrency-safe `DeviceFactEpochs` that records both the connection's observed reconnect generation and its UTC epoch:

```go
type NATSConnectionGeneration struct {
    Reconnects uint64
}

type DeviceFactEpochs struct {
    // private mutex; per-connection connectivity, generation and UTC epoch
}

func (epochs *DeviceFactEpochs) IngestConnected(generation NATSConnectionGeneration, at time.Time)
func (epochs *DeviceFactEpochs) IngestDisconnected()
func (epochs *DeviceFactEpochs) PublishConnected(generation NATSConnectionGeneration, at time.Time)
func (epochs *DeviceFactEpochs) PublishDisconnected()
func (epochs *DeviceFactEpochs) LiveSince(
    ingestGeneration NATSConnectionGeneration,
    publishGeneration NATSConnectionGeneration,
) (time.Time, bool)
```

The transport derives `NATSConnectionGeneration.Reconnects` synchronously from `connection.Stats().Reconnects`. `LiveSince` returns true only when both connections report `IsConnected()`, both supplied current reconnect counts equal the generations established by their initial-connect or reconnect handlers, and both stored epochs are nonzero; it then returns the later stored epoch. A reconnect count mismatch closes the live window conservatively even if the asynchronous reconnect callback has not run yet.

Initial successful connects establish generation zero, and each reconnect handler reads the connection's current stats and establishes that generation with the callback's Core time, before facts may flow for that generation. Disconnect callbacks also close the window for diagnostics, but correctness does not depend on their delivery preceding reconnection.

### 9.3 Eligibility

The NATS dispatcher uses two stages of eligibility:

```text
caller/enqueue stage:
  read only the epoch tracker's cached snapshot
  never call nats.Conn methods
  for Observation/Event require receive time >= cached LiveSince
  capture both cached generations in the queued item

worker/publication stage:
  require both connections currently connected
  require connection.Stats().Reconnects to match the queued generations
  require receive time >= the current established LiveSince
```

The caller stage is a conservative fast filter; correctness belongs to the worker stage. A delayed disconnect callback can only queue work from a stale snapshot, which the worker's synchronous connection and generation check drops. A delayed reconnect callback leaves the cached generation old, so new work queues under the old generation and is dropped, or waits until the callback establishes the new one.

Observation `ObservedAt` and Entity Event `ReceivedAt` are JetStream storage times; Adapter clocks and SDK `emitted_at` never decide fact freshness. Startup interruption facts capture both initial generations after both connections establish their epochs, and other Command transitions originate inside Core and have no JetStream receive time.

There is no skew tolerance, because a tolerance would deliberately admit some backlog after a short outage. Hearth assumes the Core and NATS clocks used for connection and JetStream storage evidence are synchronized; if they are not, the conservative failure is a missing fact while durable history remains correct. Log a safe clock-skew diagnostic when a newly processed report is suppressed because its receive time precedes the current epoch.

The generation and epoch gates suppress backlog retained while Core was stopped or while either connection was disconnected, work queued ahead of an epoch boundary and processed after reconnect, and facts still waiting in the local dispatcher when either connection generation changes. They never expire a live report merely because processing is slow: a report stored after the current live epoch remains eligible even when an earlier JetStream backlog delays its processing.

## 10. Bounded NATS dispatcher

New `internal/modules/devices/nats/device_fact_dispatcher.go` implements `devices.DeviceFactSink` and owns one publisher worker.

```go
const (
    DeviceFactMaxMessageBytes = 64 * 1024
    DeviceFactPendingMessages = 256
    DeviceFactPendingBytes    = 4 * 1024 * 1024
)

type DeviceFactDispatcher struct {
    connection *natsgo.Conn
    validator  *contractsv1.Validator
    epochs     *DeviceFactEpochs
    logger     *slog.Logger
    now        func() time.Time
    newFactID  func() (devices.DeviceFactID, error)
    // private bounded queue, queued-byte count, lifecycle state and worker
}

func StartDeviceFactDispatcher(
    connection *natsgo.Conn,
    validator *contractsv1.Validator,
    epochs *DeviceFactEpochs,
    logger *slog.Logger,
) (*DeviceFactDispatcher, error)
func (dispatcher *DeviceFactDispatcher) Active() bool
func (dispatcher *DeviceFactDispatcher) StopAdmission()
func (dispatcher *DeviceFactDispatcher) Drain(context.Context) error
func (dispatcher *DeviceFactDispatcher) Closed() <-chan struct{}
```

Each sink method performs bounded CPU work on the caller and never calls `nats.Conn`:

1. read the epoch tracker's independently synchronized cached snapshot and apply the enqueue-stage eligibility from §9.3;
2. mint one `fct_` ID and capture both cached reconnect generations;
3. map the typed devices value to its private wire DTO, build the family-specific strict envelope with Core publication time, source correlation and source ID causation, and derive the exact Entity/family/variant subject;
4. validate and encode with `natswire.Encode`;
5. reject an encoded message over 64 KiB and inject W3C trace headers;
6. enqueue without waiting if both message-count and byte limits permit; otherwise log and drop.

One worker dequeues FIFO and, immediately before each single `connection.PublishMsg` call, rechecks the worker-stage eligibility from §9.3. It drops stale queued work, never holds the epoch mutex across a NATS call, and never retries. A single worker preserves enqueue order for facts that are actually published.

The queue exists only to keep a slow socket write off authoritative devices paths; it is not a recovery buffer, and a queued item cannot survive dispatcher restart, Core restart or either NATS connection generation change. `StopAdmission` rejects new work. `Drain` publishes currently eligible queued work until empty or its context expires; on timeout, app assembly closes the dedicated fact connection to unblock a stalled write, drops the remainder and joins the worker.

Errors are owned by the sink and never returned to devices. Invalid internal facts, a zero clock, and ID generation, subject, size or encoding failures log `device_fact.not_published` with `stage` and a fixed `error_code`; disconnected, draining and reconnect-buffer errors log one safe `device_fact.not_published` diagnostic and drop the fact; queue overflow logs `device_fact.not_published` with `error_code=fact_queue_full` and the current bounded counts; epoch or generation suppression logs `device_fact.suppressed` at debug with family, safe source ID and reason `not_live`, `generation_changed` or `before_epoch`. Logs never include State values, Command parameters, raw envelopes or full subjects.

A slow subscriber cannot back-pressure the publisher; NATS owns subscriber pending limits and disconnect behavior.

## 11. Ordering, duplicates and failure semantics

### 11.1 Ordering

- Observation facts preserve committed Observation consumer order, and Entity Event facts preserve committed Entity Event consumer order, because each durable consumer processes one pending message at a time.
- An Observation fact is enqueued before a satisfied Command fact committed by the same transaction.
- Command creation is enqueued before its worker can persist acceptance or completion, and the striped per-Command sequencing from §8.3 covers persistence through enqueue, so published facts for one Command follow durable transition order, including an Observation-driven satisfaction race.
- The single dispatcher worker publishes eligible queued messages FIFO. Drops may create gaps but never reorder the facts that remain in one dispatcher generation.
- No global durable order exists across families, Entities or Core restarts. Fact IDs are identities, not sequence numbers, so consumers must not sort by UUID or envelope timestamp to invent a total order.

### 11.2 Duplicates

Core sets no `Nats-Msg-Id` and promises no broker deduplication. A subscriber can receive duplicates if it creates duplicate subscriptions, or if another publisher violates the trust boundary. Consumers that cause side effects must stay idempotent using the durable source ID and the relevant variant.

### 11.3 Loss windows

A fact is permanently lost when no subscriber is present, when the publisher connection is unavailable, reconnecting or draining, when Core crashes after SQLite commit and before publication, when ID generation, schema mapping, encoding or publication fails, when a subscriber exceeds its own pending limits or disconnects, or when epoch freshness suppresses backlog. None of these failures rolls back, reclassifies or retries the durable source record. HTTP history remains the recovery and diagnostic surface, but consumers must not turn an HTTP recovery read into automatic catch-up unless a separate future feature explicitly permits it.

## 12. Assembly, readiness and lifecycle

Startup order:

1. open and migrate SQLite;
2. interrupt active Commands and retain the transitioned records;
3. connect the shared ingest/request connection and the dedicated no-buffer fact connection, attach one `DeviceFactEpochs` to both connections' lifecycle callbacks, and mark each connection's initial epoch;
4. start the bounded dispatcher;
5. construct the devices service with the fact sink;
6. enqueue the retained startup interruption facts;
7. provision JetStream resources and start request/reply transports;
8. start the Observation and Entity Event consumers;
9. expose HTTP and readiness.

Readiness requires both NATS connections connected and the dispatcher active, in addition to the existing SQLite, resource and consumer checks. It does not require a subscriber, a fact stream, an empty dispatcher queue or proof that a publication was received, and it does not prove that any external subscriber is present or keeping up. A later automation feature adds its own subscription and admission checks.

Shutdown keeps the fact connection available until fact-producing work stops:

1. close Command admission and join Command workers;
2. drain the Entity Event and Observation consumers while the dispatcher still accepts facts;
3. stop dispatcher admission and drain its bounded queue with a five-second deadline;
4. drain request/reply transports, the shared connection and the dedicated fact connection;
5. close SQLite.

If either connection disconnects during drain, the epoch closes and queued or later facts drop. If dispatcher drain times out, close the dedicated fact connection, discard the queue and join the worker before continuing.

## 13. External usage and security

The contracts are observable with any NATS client:

```sh
nats sub 'hearth.v1.core.fact.>'
nats sub 'hearth.v1.core.fact.entity.ent_<uuidv7>.>'
nats sub 'hearth.v1.core.fact.entity.*.entity-event.single_press'
```

Facts do not justify widening the current trusted-network deployment boundary. Until NATS authentication and authorization are implemented, anyone with broker access may forge a fact or read its canonical State values and Command parameters. Documentation must state that external consumers trust the broker boundary, not a cryptographic Core signature.

When permissions exist, expected policy is:

```text
Core:       publish hearth.v1.core.fact.>
Subscriber: subscribe hearth.v1.core.fact.>; publish denied
Adapter:    no publish or subscribe permission under hearth.v1.core.>
```

No Adapter SDK fact consumer is added. `sdk/adapter` remains the Adapter-facing interface; external consumers use the schemas and subject contract directly.

## 14. Deliverables

| ID | Outcome | Effort | Depends on | Acceptance |
|---|---|---|---|---|
| D1 | Device Fact vocabulary, `fct_` identity, three schemas and subject builders/parsers | L | none | A1 to A3 |
| D2 | Exact post-commit devices facts and Command transition evidence | L | D1 | A4 to A7 |
| D3 | No-buffer NATS publisher, connection epochs, assembly, readiness and drain | L | D1,D2 | A8 to A11 |
| D4 | External vertical slices, ADR and operator/developer documentation | L | D3 | A12 to A14 |

## 15. Project layout

```text
contracts/v1/
├── common.schema.json                    # modify [D1]: fct_ identity
├── observation-fact.schema.json          # new [D1]: accepted Observation wire contract
├── entity-event-fact.schema.json         # new [D1]: accepted Entity Event wire contract
├── command-fact.schema.json              # new [D1]: Command transition wire contract
└── embed.go                              # modify [D1]: schema IDs and registration
internal/contracts/v1/natswire/
└── subjects.go                           # modify [D1]: Core Fact subjects/routes
internal/modules/devices/
├── model.go                              # modify [D2]: Observation correlation and satisfied record
├── ids.go                                # modify [D1]: DeviceFactID constructors/parsers
├── repository.go                         # modify [D2]: CommandTransition ledger returns
├── service.go                            # modify [D2]: optional DeviceFactSink dependency
├── device_facts.go                       # new [D1,D2]: fact types and sink interface
├── observation.go                        # modify [D2]: accepted Observation and satisfaction emission
├── entity_events.go                      # modify [D2]: accepted Entity Event emission/record time
├── command.go                            # modify [D2]: Command transition emission
├── sqlite_observations.go                # modify [D2]: satisfied Command record result
├── sqlite_repository.go                  # modify [D2]: exact transition reporting/interruption rows
├── dbqueries/commands.sql                # modify [D2]: transition RETURNING queries
└── nats/
    ├── observation.go                    # modify [D2]: Observation correlation mapping
    ├── device_fact_epochs.go             # new [D3]: generation-fenced live epochs
    └── device_fact_dispatcher.go         # new [D3]: bounded queue, epoch gate and Core NATS publication
internal/app/hearthd/
├── run.go                                # modify [D3]: second connection, epochs, assembly and drain
└── server.go                             # modify [D3]: fact connection readiness
internal/platform/db/migrations/
└── 00001_initial.sql                     # unchanged: no new durable fact data
README.md                                 # modify [D4]: external subscription recipe and limitations
CONTEXT.md                                # modify [D4]: Device Fact vocabulary
docs/
├── architecture.md                       # modify [D4]: accepted delivery/lifecycle constraints
├── logging.md                            # modify [D4]: safe fact diagnostics
└── adr/0019-device-facts-over-core-nats.md # new [D4]: durable architectural decision
specs/entity-event-automations.md         # modified [D4]: exact Entity Event Fact subject, schema and DTO names
```

Generated `dbsqlc` output changes when Command queries change, but no migration or observation persistence column is added, and tests colocate with each owning path.

## 16. Acceptance criteria

- **A1. Schemas.** All three strict schemas compile and are embedded. Valid fixtures pass; wrong `fct_` or source prefixes, unknown fields, illegal dispositions or statuses, invalid Command field combinations and causation mismatches fail.
- **A2. Subjects.** Builders and parser round-trip every family and variant, reject wrong token counts, wildcards, unsafe variants, unknown families and noncanonical Entity IDs, and reject subject and payload disagreement.
- **A3. Identity.** `NewDeviceFactID` mints canonical UUIDv7 values; parsing rejects other prefixes, UUID versions, variants, uppercase and malformed values.
- **A4. Observation facts.** First-seen applied and unchanged Observations publish the normalized committed value exactly once, with correct correlation and timestamps; rejected and duplicate Observations publish none.
- **A5. Entity event facts.** Only first-seen accepted events publish; rejected, duplicate and identity-conflict outcomes publish none; `recorded_at` equals the value committed to SQLite.
- **A6. Command facts.** Requested, accepted, every normal terminal status and interrupted each publish exactly once per real transition. Immediate terminal creation emits no synthetic requested fact, and repeated or racing no-op transitions emit none.
- **A7. Commit ordering.** Injected repository or commit failures produce no fact. Observation-driven satisfaction enqueues Observation evidence before the Command transition, preserves existing waiter behavior, and a barrier-controlled acceptance/satisfaction race cannot publish statuses out of durable order.
- **A8. Dispatcher.** Each typed fact maps to the exact subject and a schema-valid payload, mints a unique `fct_` ID, carries source correlation and causation, injects trace headers, omits `Nats-Msg-Id`, and receives at most one plain publish attempt. Sink methods never call `nats.Conn`; a worker stalled while holding the NATS connection mutex cannot delay Command dispatch, Command waiter notification or durable consumer acknowledgement; queue overflow drops safely.
- **A9. Epochs.** Initial connection, ingest disconnect/reconnect and publisher disconnect/reconnect deterministically advance the combined live epoch. Reports before the boundary are suppressed and reports at or after it are eligible; Commands require both connections live; a reconnect-generation mismatch suppresses facts even when reconnect callbacks are deliberately delayed.
- **A10. No buffering.** With real `nats.go v1.53.1`, a fact publication during publisher reconnect fails and is never observed after reconnect, and existing shared-connection retry and buffering behavior stays unchanged.
- **A11. Lifecycle.** Readiness requires both connections and an active dispatcher but no subscriber. Shutdown keeps the dispatcher available through Command completion and durable-consumer drain, then drains or safely aborts its bounded queue without leaking a goroutine.
- **A12. External vertical slice.** A real SDK Observation, Entity Event and HTTP Command lifecycle produce schema-valid facts observable by a plain NATS subscriber while their authoritative HTTP and SQLite records agree.
- **A13. No catch-up.** Reports broker-acknowledged before Core startup or during either connection outage later enter durable history but emit no Device Fact, and a subsequent live report emits one; no fact stream or replay resource is provisioned.
- **A14. Delivery.** The README subscription recipe works; the ADR, architecture, logging and glossary state the at-most-once, loss and security semantics; `mise run validate` and generation checks pass.

## 17. Test strategy

| Layer | What | Approach |
|---|---|---|
| Contract | schemas, IDs, subject grammar | fixtures, table tests and existing NATS wire fuzz harness |
| Devices unit | fact eligibility and ordering | recording sink plus repository fault doubles |
| SQLite | exact Command transitions and event record time | real single-connection SQLite transaction tests |
| NATS transport | mapping, queue bounds, generations, epochs, no reconnect buffer, stalled writer and loss | embedded NATS, injected clock/IDs and lifecycle callbacks |
| Assembly | readiness, startup interruption and drain | existing hearthd integration harness and synchronization barriers |
| Vertical slice | SDK/HTTP → SQLite → external fact subscriber | embedded NATS and real SQLite; no sleeps as correctness oracles |

Mutation-resistant tests must fail if any accepted-only guard, transition `Changed` guard, post-commit placement, epoch comparison, reconnect-buffer option, subject/payload check or Command schema invariant is removed.

## 18. Risks and mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Commit-to-publish crash window loses a fact | missed notification or Automation Trigger | explicit at-most-once contract; durable HTTP history remains truthful; no automatic catch-up |
| Core/NATS clock disagreement suppresses a new report | missed fact | use only Core and NATS receive evidence, log safe skew diagnostics, document clock synchronization requirement |
| Separate fact connection and dispatcher increase lifecycle complexity | startup/readiness/drain defects | one app-owned generation-fenced epoch tracker, one bounded worker, focused reconnect and shutdown integration tests |
| Command transition races emit duplicates | external lifecycle becomes untruthful | repository returns `CommandTransition.Changed`; publish only changed transitions |
| External clients treat facts as authoritative history | incomplete downstream state | schemas and docs state notification semantics; HTTP and SQLite remain authoritative |
| Broker access permits fact forgery or disclosure | unintended actions or data exposure | preserve the trusted-network boundary and reserve the Core namespace for future publish ACLs |
| High Observation volume or stalled NATS writer fills the local queue | dropped facts | fixed message and byte bounds, nonblocking enqueue, safe overflow diagnostics; subscribers filter `observation.applied` |

## 19. Trade-offs

| Chose | Over | Because |
|---|---|---|
| External Core NATS facts | in-process callbacks | one contract serves automations and external consumers without devices importing either |
| Plain NATS, with no persisted fact state | JetStream, an outbox or publication metadata | live-only delivery is required, durability already lives in SQLite and the inbound streams, and facts are never reconstructed or replayed |
| Entity-first subjects | family-first subjects | the canonical Entity is the stable routing identity, and one Entity subscription covers every family |
| Variant in subject and payload | payload-only routing | NATS subscribers filter event names, dispositions and statuses without decoding unrelated messages |
| A new `fct_` identity | the source ID as envelope ID | each Command transition is a distinct message while source identity stays explicit |
| Three schemas and sink methods | one generic union | invalid family and data combinations stay unrepresentable, and consumers validate only their family |
| Every Command transition | terminal-only facts | external consumers receive the complete durable lifecycle instead of a selectively lossy projection |
| Combined connection epochs | a fixed age window | no arbitrary TTL and no Adapter-clock dependency, and slow live processing stays eligible while outage backlog is suppressed |
| A separate no-buffer connection | disabling shared buffering | fact reconnect behavior changes without regressing existing Command and request/reply recovery |
| One bounded dispatcher worker | synchronous NATS writes on devices paths | socket stalls cannot consume Command deadlines or delay durable acknowledgements, and the generation checks keep the queue from becoming recovery storage |

## 20. Success criteria

The foundation is complete when:

1. external clients can subscribe to stable, schema-valid Core facts without consuming Adapter input;
2. a fact publication failure cannot change durable device behavior, and only the fact connection itself can affect readiness;
3. Core and connection outages never replay retained Observation or Entity Event history as facts;
4. the Entity Event automation spec carries the implemented Entity Event Fact subject, schema and DTO names without any change to the Device Facts module.

No open implementation questions remain. Any expansion to additional fact families requires a separate reviewed change.
