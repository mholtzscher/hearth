# Device Facts over durable JetStream

**Status:** Implemented for the Observation and Entity Event families. The owning devices transaction queues each fact in a transactional outbox; one relay publishes pending facts oldest-first into `HEARTH_DEVICE_FACTS_V1` and deletes each row only after the broker acknowledges it; every consumer chooses its own delivery and recovery policy.
**Baseline:** `d9760b8`.
**Effort:** XL across four deliverables. This is a prerequisite for [Entity Event automations](entity-event-automations.md).

## 1. Problem and purpose

Hearth durably verifies device activity inside the `devices` module. Downstream consumers need a supported interface for learning what Core committed that neither reads Core's database nor loses every fact to one broker outage or process restart.

Introduce **Device Facts**: versioned Core messages queued by the same devices-owned SQLite transaction that establishes an accepted Observation or an accepted Entity Event, published oldest-first by one relay into one bounded JetStream stream, and read by consumers that own their own delivery and recovery policy.

```text
              Adapter input
                   ↓
       devices SQLite transaction
       ├── durable evidence (observation / entity event)
       └── device_facts_outbox row      (one atomic commit)
                   ↓ commit
           DeviceFactRelay
                   ↓ JetStream publish, PubAck required
        HEARTH_DEVICE_FACTS_V1  (Limits/File, 7d / 1 GiB)
                   ↓                    ↓
     pending row deleted        consumer-owned policy:
     only after PubAck          named durable resume, or
                                new DeliverNew tail
```

The outbox makes Core's recording durable: a fact survives a relay restart, a broker outage and a Core process exit, and the relay republishes it with the same identity, subject and payload bytes. The stream gives consumers a bounded retention window. Durable SQLite evidence and the HTTP read APIs remain authoritative, and a fact reports what Core recorded, not physical truth.

Publication is **at-least-once from the outbox**, with broker-side deduplication that is bounded by the stream's duplicate window. A retry whose PubAck or row delete was lost is collapsed by the broker only while that window lasts; after it, the same fact is stored again. Every consumer must therefore stay idempotent on the stable fact identity.

Entity Event automations consume accepted Entity Events through this surface and choose durable recovery, so a short Core outage no longer silently drops a Trigger.

Command lifecycle is deliberately **not** a fact family. Commands remain authoritative in the SQLite `commands` table and the HTTP Command read API, and no Command Fact exists: there is no Command schema, subject, family token, outbox row, relay path, transition evidence, transition-stripe ordering or startup-interruption publication. Observations are not Command lifecycle substitutes, because an Observation reports accepted State evidence from an Adapter rather than a Command status transition, and a stateless (`dispatched`) or failure (`rejected`, `adapter_unhealthy`, `entity_unavailable`, `outcome_timeout`, `entity_disabled`, `internal_failure`, `interrupted`) Command outcome produces no accepted Observation, so those outcomes have no fact at all. A consumer that needs Command lifecycle must read durable HTTP/SQLite history.

## 2. Decisions

- Facts are externally supported, language-neutral contracts under `contracts/v1`, published under `hearth.v1.core.fact.>`.
- Delivery is durable and broker-mediated. One relay publishes pending outbox rows into the one JetStream stream `HEARTH_DEVICE_FACTS_V1`; a row is deleted only after a `PubAck` naming that stream. The same publish is also delivered to any connected plain Core NATS subscriber, which stays live-only and can neither acknowledge nor recover a fact.
- The outbox row is written **inside** the devices transaction that commits the evidence, so a fact and the evidence it reports are one atomic unit and an outbox failure rolls the evidence back.
- Subjects stay Entity-first:

  ```text
  hearth.v1.core.fact.entity.<entity_id>.<family>.<variant>
  ```

- Each queued fact receives one stable `fct_<UUIDv7>` identity at enqueue time. That identity is the durable row key, the published envelope `id` and the published `Nats-Msg-Id`; the durable source identity stays in `data` and in `causation_id`.
- Accepted first-seen Observations queue `applied` or `unchanged`. Accepted first-seen Entity Events queue a fact whose variant is the event name. Rejected, duplicate and identity-conflict inputs queue nothing, and no Command lifecycle transition queues anything.
- `emitted_at` is Core's fact **creation/commit time**, stored in the outbox row at enqueue. A fact published late still reports when Core committed it, and a retried publish reuses the same value.
- The inbound W3C `traceparent` and `tracestate` are persisted with the pending fact and restored as headers on publication.
- The stream is `LimitsPolicy` + `FileStorage`, subject filter `hearth.v1.core.fact.>`, `MaxAge` seven days, `MaxBytes` one GiB, `MaxMsgs`/`MaxMsgsPerSubject`/`MaxMsgSize` unlimited, `DiscardOld`, a two-hour duplicate window, and no subject transform. Core provisions the stream and **no consumer**.
- One shared Core NATS connection carries subscriptions, request/reply, JetStream ingestion and fact publication. There is no dedicated fact connection, no connection epoch or freshness fence, no volatile queue, no reconnect-buffer distinction and no freshness suppression.
- Schemas expose canonical Entity and source-record data, not Adapter/runtime identities, bindings, fingerprints, receive-order counters or raw rejected values.

## 3. Scope

This spec owns:

- Device Fact vocabulary, the outbox row, the domain projections and two strict external wire schemas;
- Core-originated subject construction and parsing;
- transactional enqueue of a pending fact inside the devices transaction for accepted Observations and accepted Entity Events;
- the one Device Fact JetStream stream, its exact configuration and its validation;
- the single relay that publishes pending facts oldest-first, waits for a PubAck, deletes only what the broker acknowledged and retries or faults honestly;
- application assembly, readiness, lifecycle and logging;
- consumer-owned delivery/recovery policy guidance for internal and external readers;
- integration tests proving transactional enqueue, oldest-first durable publication and consumer-owned recovery.

**Non-goals:** a Core-owned fact consumer, replay or catch-up endpoints, subscriber registration APIs, a subscriber SDK, an HTTP fact endpoint, signing and authorization implementation, automation implementation, facts for rejected input, Command lifecycle transitions, Adapter health, Entity availability, enablement, registration or Device-level Entities, and configurable retention or duplicate windows. Core never interprets a subscriber's delivery position.

## 4. Domain language and guarantees

`CONTEXT.md` carries this term:

> **Device Fact**:
> One Core-verified statement Core durably queues in the same devices transaction that establishes an accepted Observation or accepted Entity Event, then publishes to a bounded JetStream stream, so it has exactly two sources: accepted Observation evidence and accepted Entity Events. Publication is at-least-once from that durable queue with broker-side deduplication bounded by the stream's duplicate window, so a consumer may see a duplicate and must stay idempotent; a fact can still be evicted by the stream's age or size bound, and a consumer that chooses no durable recovery policy can still miss facts published while it was absent. A fact reports what Core recorded, not physical truth, and Command status transitions, including startup interruption, publish no fact: durable HTTP/SQLite Command history is authoritative and Observations are not its substitute.
> _Avoid_: Entity Event, Observation, Command, Command lifecycle, event sourcing, change log

The two guarantees Core owns:

> **Atomic enqueue.** A qualifying devices transition commits exactly one pending fact together with its evidence, or commits neither. An outbox identity-mint or insert failure rolls the evidence back, so JetStream redelivers the inbound report and retries the evidence and its fact together.

> **Eventual publication from the outbox.** Once a pending row commits, the relay republishes it until the broker acknowledges it into `HEARTH_DEVICE_FACTS_V1`, at which point the row is deleted. A failed publish, a missing acknowledgement, an unexpected stream or a failed delete keeps the row and retries. Only a deterministic poison row stops the relay, and it preserves the row and fails readiness rather than discarding evidence.

Publication is not exactly-once. A retry after the duplicate window has elapsed is stored again, and any consumer may see a duplicate; consumers stay idempotent on the fact identity. Retention is bounded: the stream evicts the oldest facts when it reaches seven days or one GiB, whichever comes first.

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
| 7 | `<family>` | `observation` or `entity-event` |
| 8 | `<variant>` | family-specific closed token or event-name slug |

Concrete subjects:

```text
hearth.v1.core.fact.entity.ent_<uuidv7>.observation.applied
hearth.v1.core.fact.entity.ent_<uuidv7>.observation.unchanged
hearth.v1.core.fact.entity.ent_<uuidv7>.entity-event.single_press
```

The stream stores every subject matched by `hearth.v1.core.fact.>`, which is also `natswire.DeviceFactWildcard()`. Useful reader subscriptions:

```text
hearth.v1.core.fact.>                              # every Device Fact
hearth.v1.core.fact.entity.<entity_id>.>           # one Entity
hearth.v1.core.fact.entity.*.observation.applied   # State-changing Observations
hearth.v1.core.fact.entity.*.entity-event.>        # every accepted Entity Event
```

Any family or variant can be pinned the same way. Subject and payload Entity, family and variant must agree, and subscribers must reject disagreement.

### 5.2 Subject types

`internal/contracts/v1/natswire/subjects.go` exposes:

```go
type DeviceFactFamily string

const (
    DeviceFactFamilyObservation DeviceFactFamily = "observation"
    DeviceFactFamilyEntityEvent DeviceFactFamily = "entity-event"
)

const (
    ObservationFactApplied   = "applied"
    ObservationFactUnchanged = "unchanged"
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
func ParseDeviceFactSubject(subject string) (DeviceFactRoute, error)
```

Builders reject noncanonical Entity IDs and invalid family variants, and each validates its exact variant set: Observation variants are `applied` and `unchanged`, and Entity Event variants satisfy the implemented event-name slug pattern. The parser rejects any subject whose tokens do not round-trip, and it rejects a subject transform because the stream is provisioned without one.

`natswire` remains domain-neutral and imports no `devices` package. Its closed string constants mirror the schemas and the devices values and are conformance-tested against them.

## 6. Wire schemas

Add `fct_id` to `contracts/v1/common.schema.json`:

```json
{"type":"string","pattern":"^fct_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"}
```

Each fact schema constrains its own `causation_id` directly to the durable source ID type (`obs_id` or `evt_id`). `fct_id` stays out of the common `causation_id` union because facts do not cause existing inbound contracts.

Both schemas use the standard envelope fields:

```json
{
  "id":"fct_<uuidv7>",
  "schema":"urn:hearth:schema:<family>-fact:v1",
  "emitted_at":"<Core fact creation time>",
  "correlation_id":"cor_<uuidv7>",
  "causation_id":"<durable source ID>",
  "data":{}
}
```

Envelope semantics:

- `id` is the stable fact identity minted at enqueue. It is the outbox row key, the envelope identity and the published `Nats-Msg-Id`, so the broker's duplicate window can collapse a retry of the same row.
- `correlation_id` is copied from the accepted report.
- `emitted_at` is Core's fact creation/commit time, stored once at enqueue and reused byte-for-byte by every retry of that row.
- `causation_id` identifies the durable Observation or Entity Event source.
- W3C trace headers continue the context that caused Core to process the transition. The persisted `traceparent` and `tracestate` are restored on every (re)publication, including a retry after a restart.

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

`disposition` is `applied` or `unchanged`, so the schema cannot represent rejected or duplicate dispositions. `value` is the normalized canonical State JSON committed for the accepted Observation, `source_updated_at` is optional, and `observed_at` is JetStream storage time.

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

### 6.3 Compatibility

The strict schemas and `hearth.v1` subject prefix are one external v1 contract. Adding a family or a new major subject or schema is compatible. Adding a property, variant or enum value to an existing strict v1 schema is not assumed compatible; make an explicit versioned change.

## 7. Domain types, the outbox and the notifier

`internal/modules/devices/device_facts.go` defines:

```go
type DeviceFactID string     // fct_<UUIDv7>; the durable row key and message identity
type DeviceFactFamily string // observation | entity-event

type DeviceFactTraceContext struct {
    Traceparent string // at most 128 printable ASCII bytes
    Tracestate  string // at most 512 printable ASCII bytes
}
func (DeviceFactTraceContext) Validate() error // bounded size, printable ASCII

type ObservationFact struct {
    ID                DeviceFactID
    ObservationID     ObservationID
    EntityID          EntityID
    Disposition       ObservationDisposition // applied | unchanged
    Value             Value                  // normalized State JSON committed with the Observation
    CorrelationID     CorrelationID
    AdapterReceivedAt time.Time
    SourceUpdatedAt   *time.Time
    ObservedAt        time.Time // JetStream storage time
    CreatedAt         time.Time // Core commit time, published as emitted_at
    Trace             DeviceFactTraceContext
}

type EntityEventFact struct {
    ID            DeviceFactID
    EventID       EntityEventID
    EntityID      EntityID
    Name          EntityEventName
    CorrelationID CorrelationID
    ReportedAt    time.Time // SDK emitted_at
    ReceivedAt    time.Time // JetStream storage time
    RecordedAt    time.Time // Core first-record time
    CreatedAt     time.Time // Core commit time, published as emitted_at
    Trace         DeviceFactTraceContext
}

// DeviceFact is exactly one typed pending fact.
type DeviceFact interface {
    DeviceFactFamily() DeviceFactFamily
    deviceFact()
}

type PendingDeviceFact struct {
    Sequence int64 // durable enqueue order, oldest first
    Fact     DeviceFact
}
```

The unexported `deviceFact()` method seals the family set, so a pending row always carries one of the two canonical projections and never an opaque payload or a third family. `NewDeviceFactID` and `ParseDeviceFactID` live beside the other canonical ID constructors in `internal/modules/devices/ids.go`.

Two narrow seams keep the dependency direction one-way:

```go
// DeviceFactOutbox is the durable pending set the relay drains. It is
// implemented by the devices SQLite repository.
type DeviceFactOutbox interface {
    ListPendingDeviceFacts(ctx context.Context, limit int) ([]PendingDeviceFact, error)
    DeleteDeviceFact(ctx context.Context, factID DeviceFactID) error
}

// DeviceFactNotifier is the one nonblocking wake hint devices needs. It never
// performs I/O, never blocks, never returns an error and may lose a hint: the
// relay also polls the outbox, so a lost hint costs latency, never a fact.
type DeviceFactNotifier interface {
    NotifyPendingDeviceFacts()
}
```

Extend `devices.Dependencies` with the notifier only:

```diff
 type Dependencies struct {
     Logger           *slog.Logger
     Now              func() time.Time
+    DeviceFacts      DeviceFactNotifier
     NewDeviceID      func() (DeviceID, error)
```

A nil notifier is a no-op, so focused devices tests and non-NATS assembly need no transport setup. The service stores the notifier privately, calls it only after repository success, and never imports a transport package. `devices` depends on nothing about JetStream, streams, subjects or consumer policy.

`DeleteDeviceFact` rejects a noncanonical identity and treats an already-deleted fact as success, so a relay cannot fail on work another drain consumed. `ListPendingDeviceFacts` rejects a non-positive limit with `ErrInvalidDeviceFactLimit`.

## 8. Transactional enqueue

The outbox row is written **inside** the transaction that commits the evidence. There is no post-commit emission hook and no in-memory fact queue: the committed row is the pending fact.

SQLite table `device_facts_outbox`:

| Column | Role |
|---|---|
| `enqueue_order` | `INTEGER PRIMARY KEY AUTOINCREMENT`; the durable oldest-first publication order |
| `fact_id` | unique canonical `fct_` identity, minted inside the transaction |
| `family` | `observation` or `entity-event` |
| `entity_id` | canonical `ent_` identity carried in the subject and payload |
| `variant` | Observation disposition (`applied`/`unchanged`) or Entity Event name |
| `source_id` | durable `obs_`/`evt_` identity published as `causation_id` |
| `correlation_id` | canonical `cor_` copied from the accepted report |
| `created_at` | Core fact creation/commit time, published as `emitted_at` |
| `traceparent`, `tracestate` | persisted inbound W3C trace context, size- and printability-checked |
| `value_json`, `adapter_received_at`, `source_updated_at`, `observed_at` | Observation-only committed evidence |
| `reported_at`, `received_at`, `recorded_at` | Entity Event-only committed evidence |

`CHECK` constraints enforce one family per row, so an Observation row leaves every Entity Event column `NULL` and the reverse. The table has no foreign keys, like `entity_events`: a pending fact must survive runtime, ownership, descriptor and retained-history pruning. The table holds **only** the unpublished set and is empty whenever the relay has caught up; it is not fact retention.

### 8.1 Observations

`internal/modules/devices/model.go` carries the inbound correlation and trace on the in-memory input; `devices/nats.domainObservation` maps them from the envelope headers. `Service.ProjectObservation` validates the correlation and trace, and `sqlite_observations.go` calls `queueAcceptedObservationDeviceFact` as the last write before `tx.Commit()`:

1. eligibility is checked first: only `DispositionApplied` and `DispositionUnchanged` queue a row — a rejected outcome and a duplicate that never reached this transaction mint no identity and insert no row;
2. `created_at` is Core's transaction time and becomes `emitted_at`, so a fact published late still reports when Core committed it;
3. the fact identity is minted and validated before it reaches SQLite, so a defective generator fails the transaction that would have carried it instead of persisting an unpublishable row.

A mint, validation or insert failure returns an error and rolls the evidence back, so the inbound JetStream report is not acknowledged and both the evidence and its fact are retried together.

After the repository returns a committed result, `ProjectObservation` notifies the existing in-memory Command waiter first and then calls `NotifyPendingDeviceFacts` only when `result.PendingFactID != nil`. Transport work can therefore never delay authoritative Command completion. An Observation that satisfies a Command still queues exactly its own Observation fact; the Command's `satisfied` status queues nothing.

### 8.2 Entity events

`EntityEventRecordResult` gains `PendingFactID *DeviceFactID`, set only for a first-seen accepted row. `SQLiteRepository.RecordEntityEvent` calls `queueAcceptedEntityEventDeviceFact` before `tx.Commit()`, reusing the committed `recorded_at` as `created_at` so the fact reports exactly Core's record time. Duplicates and identity conflicts return an existing outcome and queue nothing; rejected first-seen rows queue nothing.

`Service.RecordEntityEvent` validates the trace and then notifies the relay only when `result.PendingFactID != nil`.

### 8.3 Trace capture

`deviceFactTraceFromHeaders` reads exactly `traceparent` and `tracestate` from the inbound message and nothing else, so an arbitrary inbound header can never reach SQLite. Capture is bounded and sanitizing rather than rejecting: a value outside `DeviceFactTraceContext.Validate`'s size and printable-ASCII bound is dropped for that report instead of failing it. A malformed trace costs trace continuity, never the report.

### 8.4 Commands

No Command path touches the outbox. `CommandLedger` keeps its transition methods returning only their existing error results, and `TestCommandLifecycleQueuesNoDeviceFact` pins the absence of a Command row. Command status, including startup interruption, is observable only through durable SQLite and HTTP Command history.

## 9. Durable JetStream stream

`internal/modules/devices/nats/device_fact_stream.go` owns one stream:

```go
const (
    DeviceFactStreamName            = "HEARTH_DEVICE_FACTS_V1"
    DeviceFactStreamMaxAge          = 7 * 24 * time.Hour
    DeviceFactStreamMaxBytes  int64 = 1 << 30
    DeviceFactStreamDuplicateWindow = 2 * time.Hour
)
```

Live configuration:

| Setting | Value |
|---|---|
| Subjects | `hearth.v1.core.fact.>` (`natswire.DeviceFactWildcard()`) |
| Storage / retention | `FileStorage` / `LimitsPolicy` |
| `MaxAge` | 7 days |
| `MaxBytes` | 1 GiB |
| `MaxMsgs`, `MaxMsgsPerSubject`, `MaxMsgSize` | `-1` (unlimited) |
| `Discard` | `DiscardOld` |
| `Duplicates` | 2 hours |
| `SubjectTransform` | none |
| Consumers created by Core | none |

`ProvisionDeviceFactStream(ctx, js)` creates the stream when it is absent and then validates the live configuration; `ValidateDeviceFactStream(ctx, js)` only validates. Validation fails on any name, subject, storage, retention, limit, discard, duplicate-window, `NoAck` or `SubjectTransform` mismatch.

A subject transform is rejected outright rather than tolerated. It would rewrite a canonical fact subject before the broker stores it while the publish call still returned a successful `PubAck` for the original subject; the relay would then delete the outbox row even though no consumer could ever read that fact under the canonical subject it published.

Core provisions **no consumer** for the fact stream. Every reader that needs a position, an ack floor or a filter owns its own consumer, so Core never pins a delivery policy or an acknowledgement floor for a reader it does not have. `ProvisionDeviceFactStream` is idempotent and leaves the stream with zero consumers.

## 10. The relay

`internal/modules/devices/nats/device_fact_relay.go` implements the single publisher. `DeviceFactRelay` implements `devices.DeviceFactNotifier`, reads `devices.DeviceFactOutbox`, and owns exactly one worker goroutine.

```go
const (
    DeviceFactRelayBatchSize      = 64
    DeviceFactRelayPollInterval   = 5 * time.Second
    DeviceFactRelayRetryBackoff   = time.Second
    DeviceFactPublishTimeout      = 5 * time.Second
)

func StartDeviceFactRelay(
    js jetstream.JetStream,
    outbox devices.DeviceFactOutbox,
    validator *contractsv1.Validator,
    logger *slog.Logger,
) (*DeviceFactRelay, error)

func (relay *DeviceFactRelay) NotifyPendingDeviceFacts()
func (relay *DeviceFactRelay) Active() bool
func (relay *DeviceFactRelay) Closed() <-chan struct{}
func (relay *DeviceFactRelay) Drain(ctx context.Context) error
```

The relay publishes over the shared Core NATS connection's JetStream context exactly as `js.PublishMsg` does: one `PublishMsg` per pending row, waiting for the broker's `PubAck`. It creates and modifies nothing.

### 10.1 Publication pass

One pass:

1. `ListPendingDeviceFacts(ctx, 64)` returns the oldest pending rows in durable enqueue order;
2. each row is mapped to its stable strict message and published with `js.PublishMsg`;
3. a `PubAck` naming `HEARTH_DEVICE_FACTS_V1` deletes that row;
4. the whole batch is published before the next read, so the worker always restarts from the oldest pending row.

The wake hint only shortens latency. A lost hint, a restart or work committed while the worker was busy is always found by the five-second poll, because the outbox is authoritative.

### 10.2 Message mapping

`mapPendingDeviceFact` is a pure function of the stored row, so retrying a row reuses its identity, subject and payload byte-for-byte:

- the exact subject is derived from the canonical Entity, family and variant;
- `emitted_at` is the stored `created_at`, not the current clock;
- the payload is produced by `natswire.Encode` against the strict family schema, so an unmappable row is caught here and never published partially;
- headers are set explicitly:

| Header | Value |
|---|---|
| `Nats-Msg-Id` | the stable `fct_` fact identity |
| `Nats-Expected-Stream` | `HEARTH_DEVICE_FACTS_V1` |
| `traceparent` | the persisted inbound traceparent, when present |
| `tracestate` | the persisted inbound tracestate, when present |

`Nats-Expected-Stream` makes the broker reject a publication into any other stream, so an acknowledgement naming another stream means the fact is not where Core must delete it: the relay keeps the row and retries rather than losing it. A duplicate acknowledgement is a success, because the fact is already stored under the same `Nats-Msg-Id`.

### 10.3 Retry and poison

The relay delivers or faults; it never discards a row.

**Retryable (row preserved, fixed one-second backoff, then retried oldest-first).** A transient outbox read failure, a publish failure or timeout, a missing acknowledgement, an acknowledgement naming another stream, and a delete failure. Each retry logs `device_fact.retry` at warn with a `stage` and one fixed `error_code`:

| Stage | `error_code` |
|---|---|
| `list` | `list_failed` |
| `publish` | `publish_failed` |
| `ack` | `ack_missing` |
| `ack` | `unexpected_stream` |
| `delete` | `delete_failed` |

A retry reuses the same identity, subject and bytes, so the stream's duplicate window collapses a republish whose `PubAck` or delete was lost. Beyond that bounded window the republish is stored again as a duplicate, which is why consumers must stay idempotent.

**Poison (row preserved, relay faults).** A deterministic failure that will fail the same way on every attempt:

| Stage | `error_code` | Cause |
|---|---|---|
| `list` | `invalid_row` | stored bytes Core cannot decode (`devices.ErrInvalidDeviceFactRow`) |
| `map` | `fact_invalid` | a stored zero/missing field or empty payload |
| `map` | `subject_invalid` | noncanonical Entity, family or variant |
| `map` | `unknown_family` | a row carrying no known family |
| `encode` | `encode_failed` | strict schema validation failure |

A poison row is never retried, never rewritten and never deleted, because deleting a fact Core cannot represent would silently lose durable evidence. The relay records the fault, logs `device_fact.poison` at error with `stage`, `error_code`, `fact_id` and — when the row decoded far enough to have them — `family` and the safe source identity, and stops. `Active()` then reports false, so **readiness fails** instead of reporting a publisher that can no longer make progress. Facts queued behind the poison row remain in the outbox and are published only after the operator resolves the row; restarting Core re-reads the oldest row first and hits the same poison.

`Drain` is idempotent: it clears `Active()`, cancels an in-flight publication pass, joins the worker, and then publishes the remaining pending rows itself under the caller's context until the outbox is empty. On context expiry the remaining rows stay in the outbox for the next Core process instead of being discarded. Drain returns the recorded poison fault when one exists.

### 10.4 Identity and deduplication limits, stated honestly

- The durable row guarantees publication is attempted until acknowledged. A crash between commit and publish loses nothing.
- The broker collapses a repeated `Nats-Msg-Id` only inside the two-hour duplicate window. A retry outside it is stored again, so the stream can hold two stored messages with the same fact identity.
- The relay's retry span is unbounded: a broker outage or a Core restart can easily outlast the window.
- Therefore a consumer must treat a fact identity as an idempotency key and must never assume one stored message per durable fact.

## 11. Ordering, duplicates and retention

### 11.1 Ordering

- The outbox assigns `enqueue_order` inside the committing transaction, and the single worker publishes pending rows in that order. Facts that remain in the outbox keep their relative order.
- Publication is batched, but the worker never starts the next batch until the current one has been published and deleted, so no row is skipped and no row is reordered behind another.
- No document-level order exists across Core restarts or across a stream eviction boundary. Fact IDs are identities, not sequence numbers, so consumers must not sort by UUID or envelope timestamp to invent a total order.
- Command status transitions have no fact order at all, because no Command fact exists; their order is the durable order of the `commands` table and the HTTP history endpoints.

### 11.2 Duplicates

A consumer can receive duplicates because (a) the relay republishes a row whose acknowledgement or delete was lost and the retry arrives after the duplicate window, (b) two consumers overlap during a handover, or (c) another publisher violates the trust boundary. Consumers that cause side effects must stay idempotent on the fact identity and the relevant variant. A recommended key for an event-driven consumer is `(fact_id)` for at-most-once side effects, or `(event_id, owner_id)` when the consumer wants one outcome per durable source event.

### 11.3 Retention and eviction

The stream retains facts for at most seven days and one GiB and evicts the oldest first. A fact deleted from the outbox lives only in the stream, so a reader that falls further behind than the stream's age or size bound loses the evicted facts permanently. Retention is a fixed v1 bound, not a setting, and Core proxies no read of the stream on a consumer's behalf.

### 11.4 Loss windows

A fact can be permanently missed when:

- the stream evicts it before any consumer reaches it, either because no consumer existed within the seven-day or one-GiB bound or because a reader fell behind that bound;
- the relay is faulted on a poison row, so the fact was never published and later rows stay unpublished until an operator intervenes;
- a plain Core NATS subscriber was not connected at publication time, because such a subscription is live-only;
- a reader exceeds its own pending limits or disconnects under a policy that does not recover.

None of these failures rolls back, reclassifies or loses the durable source evidence. HTTP history remains the diagnostic surface. When a fact **is** stored, a consumer with a durable consumer can recover it inside the retention window; Core does not, and consumers must not turn an HTTP recovery read into automatic catch-up unless a separate future feature explicitly permits it.

## 12. Consumer-owned recovery policy

Core owns the stream and nothing else. Each reader chooses a policy that matches its own durability requirement, and Core never creates, resumes or deletes a reader's consumer.

Two policies cover the implemented use cases:

**Durable resume (recommended when a reader must not miss a fact).** Create one named durable consumer with an explicit ack policy; the broker tracks its acknowledgement floor, so a restart resumes from the last acknowledged fact instead of replaying or skipping.

```go
consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
    Name:          "cooking-events-v1",              // stable durable name
    Durable:       "cooking-events-v1",
    FilterSubject: natswire.DeviceFactWildcard(),     // or a narrower family/entity filter
    DeliverPolicy: jetstream.DeliverAllPolicy,        // resume from the stored ack floor
    AckPolicy:     jetstream.AckExplicitPolicy,
})
```

**Tail only (a reader that wants only future facts).** A new consumer with `DeliverNewPolicy` receives nothing that is already stored and everything published after it asked:

```go
consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
    Name:          "live-dashboard-v1",
    Durable:       "live-dashboard-v1",
    FilterSubject: natswire.DeviceFactWildcard(),
    DeliverPolicy: jetstream.DeliverNewPolicy,        // never sees retained history
    AckPolicy:     jetstream.AckExplicitPolicy,
})
```

Both policies retain consumer responsibilities:

- stay idempotent on the fact identity because retries beyond the duplicate window may duplicate a fact;
- use `AckExplicitPolicy` and acknowledge only after the side effect commits, so a crash mid-handler redelivers;
- bind a consumer name to one reader and one filter, because changing either on an existing durable consumer is a configuration mismatch;
- accept that an unacknowledged fact older than the stream's age or size bound can be evicted and then redelivered never;
- never assume Core filters, rewrites or reorders facts for it.

A plain Core NATS subscription (`nats sub ...`) is still supported and remains live-only: it receives only facts published while it is connected, and it can neither acknowledge nor recover a fact. Use it for observation and debugging; use a durable consumer when a miss is unacceptable.

Consumers that only need one family or one Entity should narrow `FilterSubject` to `hearth.v1.core.fact.entity.*.entity-event.>` or `hearth.v1.core.fact.entity.<entity_id>.>`; the subject grammar is stable and Core validates it.

## 13. Assembly, readiness and lifecycle

Startup order:

1. open and migrate SQLite;
2. interrupt active Commands so they remain durable `interrupted` history (no fact is queued);
3. connect the one shared Core NATS connection;
4. compile the wire schemas and create the JetStream context;
5. provision and validate `HEARTH_DEVICE_FACTS_V1`; a wrong stream configuration fails startup instead of accepting evidence nothing can publish;
6. start the Device Fact relay over the repository outbox;
7. construct the devices service with the relay as its `DeviceFactNotifier`;
8. provision the Observation and Entity Event JetStream resources and start request/reply transports;
9. start the Observation and Entity Event consumers;
10. expose HTTP and readiness.

Readiness requires:

- a responsive SQLite database;
- the shared NATS connection connected;
- `ValidateDeviceFactStream` to pass;
- `DeviceFactRelay.Active()` to be true;
- the Observation and Entity Event resources to validate and both consumers to be active.

Readiness never requires a subscriber, a fact consumer Core does not own, a nonempty outbox or proof that any publication was received. It does prove that the fact stream is configured correctly and that the relay can still make progress; a poison fault or the start of a drain turns readiness false.

Shutdown order:

1. close Command admission and join Command workers;
2. stop the health supervisor and shut down HTTP;
3. cancel the dependency context used by Command workers, health and hourly maintenance;
4. drain the Entity Event consumer and then the Observation consumer while the relay still publishes pending facts;
5. `DeviceFactRelay.Drain` publishes every pending fact for five seconds, then leaves any remainder in the outbox;
6. drain the request/reply transports and then the shared NATS connection;
7. close SQLite.

The shared connection outlives the relay so a drain can still publish what the consumers committed. The relay drains again on every exit path through the deferred cleanup, and `Drain` is idempotent, so a failed startup or an error exit never leaves a publication goroutine behind. If the drain deadline expires, the unpublished rows stay durable for the next Core process; that is a bound, not a discard.

## 14. External usage and security

The contracts are observable with any NATS client or JetStream-enabled client:

```sh
nats sub 'hearth.v1.core.fact.>'                              # live only
nats sub 'hearth.v1.core.fact.entity.ent_<uuidv7>.>'          # one Entity, live only
nats sub 'hearth.v1.core.fact.entity.*.entity-event.single_press'
```

For durable reads, create a consumer on `HEARTH_DEVICE_FACTS_V1` in the client's own library and language; see §12 for the two policies and their exact configuration values.

Facts do not justify widening the current trusted-network deployment boundary. Until NATS authentication and authorization are implemented, anyone with broker access may forge a fact, read its canonical State values, publish into `HEARTH_DEVICE_FACTS_V1` bypassing the relay, or purge or evict stream data. Documentation must state that external consumers trust the broker boundary, not a cryptographic Core signature.

When permissions exist, expected policy is:

```text
Core:       publish hearth.v1.core.fact.>; consume HEARTH_DEVICE_FACTS_V1
Subscriber: subscribe hearth.v1.core.fact.> and read HEARTH_DEVICE_FACTS_V1; publish denied
Adapter:    no publish or subscribe permission under hearth.v1.core.>
```

No Adapter SDK consumer is added. `sdk/adapter` remains the Adapter-facing interface; external consumers use the schemas, the subject contract and the stream directly.

## 15. Deliverables

| ID | Outcome | Effort | Depends on | Acceptance |
|---|---|---|---|---|
| D1 | Device Fact vocabulary, `fct_` identity, two schemas, outbox row and subject builders/parsers | L | none | A1 to A3 |
| D2 | Transactional enqueue for accepted Observations and Entity Events with rollback | L | D1 | A4 to A6 |
| D3 | Device Fact stream, single relay, shared-connection publication, assembly, readiness and drain | L | D1,D2 | A7 to A10 |
| D4 | Consumer-owned recovery guidance, ADR, external usage and operator/developer documentation | L | D3 | A11 to A13 |

## 16. Project layout

```text
contracts/v1/
├── common.schema.json                    # fct_ identity
├── observation-fact.schema.json          # accepted Observation wire contract
├── entity-event-fact.schema.json         # accepted Entity Event wire contract
└── embed.go                              # schema IDs and registration
internal/contracts/v1/natswire/
└── subjects.go                           # Core Fact subjects/routes
internal/modules/devices/
├── model.go                              # Observation correlation and trace
├── ids.go                                # DeviceFactID constructors/parsers
├── service.go                            # DeviceFactNotifier dependency
├── device_facts.go                       # fact types, outbox/notifier seams, trace validation
├── observation.go                        # accepted Observation enqueue result
├── entity_events.go                      # accepted Entity Event enqueue result
├── repository.go                         # DeviceFactOutbox and DeviceFactNotifier
├── sqlite_repository.go                  # injected fact ID generator
├── sqlite_observations.go                # enqueue inside the projection transaction
├── sqlite_entity_events.go               # enqueue inside the recording transaction
├── sqlite_device_facts.go                # pending-row read/delete and family mapping
├── dbqueries/device_facts.sql            # outbox SQL
└── nats/
    ├── core_nats.go                      # bounded socket write for the shared connection
    ├── observation.go                    # Observation correlation/trace mapping
    ├── entity_event.go                   # Entity Event trace mapping
    ├── device_fact_trace.go              # bounded inbound trace capture
    ├── device_fact_stream.go             # stream provision/validation
    ├── device_fact_mapping.go            # stable row → wire message mapping and poison class
    └── device_fact_relay.go              # single durable publisher
internal/app/hearthd/
├── run.go                                # stream, relay, assembly and drain
└── server.go                             # shared-connection, stream and relay readiness
internal/platform/db/migrations/
└── 00001_initial.sql                     # device_facts_outbox table
README.md                                 # subscription recipe and durable consumer guidance
CONTEXT.md                                # Device Fact vocabulary
docs/
├── architecture.md                       # accepted delivery/lifecycle constraints
├── logging.md                            # safe relay diagnostics
└── adr/
    ├── 0019-device-facts-over-core-nats.md # superseded in part by 0020
    └── 0020-durable-device-facts-via-outbox.md # this decision
specs/entity-event-automations.md         # Entity Event fact subscriber recovery policy
```

No Command schema, Command subject, Command outbox row or Command sink method exists, because Command lifecycle is not a fact family.

## 17. Acceptance criteria

- **A1. Schemas.** Both strict schemas compile and are embedded. Valid fixtures pass; wrong `fct_` or source prefixes, unknown fields, illegal dispositions and causation mismatches fail.
- **A2. Subjects.** Builders and parser round-trip every family and variant, reject wrong token counts, wildcards, unsafe variants, unknown families and noncanonical Entity IDs, and reject subject and payload disagreement; the stream validation rejects a subject transform.
- **A3. Identity.** `NewDeviceFactID` mints canonical UUIDv7 values; parsing rejects other prefixes, UUID versions, variants, uppercase and malformed values. The identity is stable across every retry of one row.
- **A4. Observation facts.** A first-seen applied or unchanged Observation commits exactly one pending row carrying the normalized committed value, correlation, evidence timestamps and trace; rejected and duplicate Observations commit none.
- **A5. Entity event facts.** Only a first-seen accepted event commits a pending row; rejected, duplicate and identity-conflict outcomes commit none; `recorded_at` equals the value committed to SQLite and becomes `created_at`.
- **A6. Atomic enqueue.** An injected fact-identity or outbox-insert failure rolls the evidence back, so no evidence and no pending row commit and inbound redelivery retries both together. No Command status transition, including startup interruption, queues a row.
- **A7. Relay.** Each pending row maps to the exact subject and a schema-valid payload, reuses its identity, `emitted_at` and bytes across retries, injects the persisted trace headers, sets `Nats-Msg-Id` to the fact identity and `Nats-Expected-Stream` to the stream name, and is deleted only after an acknowledgement naming that stream.
- **A8. Retry and poison.** A transient list, publish, acknowledgement or delete failure keeps the row and retries. A deterministic corrupt or unmappable row keeps the row, logs `device_fact.poison`, sets `Active()` false and fails readiness.
- **A9. Stream.** Provisioning creates the exact configuration and no consumer; validation rejects a missing stream, a subject transform and any mismatched setting.
- **A10. Lifecycle.** Readiness requires the shared connection, the validated stream and an active relay, but no subscriber and no Core-owned consumer. Shutdown drains consumers before the relay, drains or safely abandons the outbox within five seconds, and leaks no goroutine.
- **A11. External vertical slice.** A real SDK Observation, Entity Event and HTTP Command lifecycle produce schema-valid Observation and Entity Event facts read from `HEARTH_DEVICE_FACTS_V1` while their authoritative HTTP and SQLite records agree; the Command lifecycle itself queues and publishes no fact.
- **A12. Consumer-owned recovery.** A named durable consumer that acknowledges part of the stream and returns must resume from its own acknowledgement floor, and a new `DeliverNew` consumer must see none of the retained stream and then every fact published after it asked; provisioning creates no consumer.
- **A13. Delivery.** The README subscription recipe works; the ADR, architecture, logging, glossary and automation spec state the durability, duplicate, retention and security semantics; `mise run validate` and generation checks pass.

## 18. Test strategy

| Layer | What | Approach |
|---|---|---|
| Contract | schemas, IDs, subject grammar | fixtures, table tests and the existing NATS wire fuzz harness |
| Devices unit | enqueue eligibility, trace validation, notifier ordering | recording notifier plus repository fault doubles |
| SQLite | atomic enqueue, rollback, oldest-first read, corrupt-row class | real single-connection SQLite transaction tests |
| NATS transport | mapping stability, stream validation, retry, poison, durable resume, `DeliverNew`, drain | embedded NATS, injected clock/IDs, fake outbox and publication seam |
| Assembly | readiness, startup order and drain | existing hearthd integration harness and synchronization barriers |
| Vertical slice | SDK/HTTP → SQLite outbox → stream → external consumer | embedded NATS and real SQLite; no sleeps as correctness oracles |

Mutation-resistant tests must fail if any accepted-only guard, transactional-enqueue placement, oldest-first ordering, acknowledgement-naming-the-stream check, duplicate-window setting, `Nats-Msg-Id`, poison classification or trace restoration is removed.

## 19. Risks and trade-offs

| Risk | Impact | Mitigation |
|---|---|---|
| A poison row stops publication of later facts | delayed notification while readiness fails | preserve the row, fault readiness loudly, keep later facts durable in the outbox until an operator resolves the row |
| A retry outside the duplicate window stores the fact twice | duplicate consumer side effects | explicit two-hour window, stable identity as the idempotency key, documented consumer obligation |
| Stream eviction outruns a slow reader | permanently missed facts | fixed seven-day/one-GiB bound, documented as a hard limit, durable consumer guidance |
| Consumers assume Core recovers facts for them | silent missed triggers | Core owns no consumer; recovery policy is an explicit per-reader choice |
| Consumers expect Command lifecycle on the fact surface | missing external Command state | docs, schemas and glossary state that Command status transitions have no fact and durable HTTP/SQLite history is authoritative |
| External clients treat facts as authoritative history | incomplete downstream state | schemas and docs state that Core's record is authoritative and a fact is a report |
| Broker access permits fact forgery, stream purge or data disclosure | unintended actions or data exposure | preserve the trusted-network boundary and reserve the Core namespace and stream for future publish/consume ACLs |
| The outbox grows while the broker is unreachable | unbounded local growth | the relay keeps retrying and retains rows; document that a long outage grows the outbox until the broker returns |

## 20. Trade-offs

| Chose | Over | Because |
|---|---|---|
| A transactional outbox in the owning transaction | a post-commit in-memory emission hook | a fact and its evidence commit atomically, and a crash or broker outage between commit and publication cannot lose the fact |
| One JetStream stream with consumer-owned recovery | Core-owned fact consumers | Core never pins a delivery policy or ack floor for a reader it does not have, and each reader picks resume or tail |
| At-least-once from the outbox with a bounded broker duplicate window | a claim of exactly-once or a claim of at-most-once without recovery | both limits are stated honestly and consumers get an idempotency key that works |
| One shared Core NATS connection | a dedicated no-buffer publication connection with connection epochs | one connection means one broker reachability fact, no freshness fence, and no second lifecycle to reason about |
| Entity-first subjects | family-first subjects | the canonical Entity is the stable routing identity and one Entity subscription covers every family |
| Variant in the subject and the payload | payload-only routing | subscribers filter event names and dispositions without decoding unrelated messages |
| A stable `fct_` identity minted at enqueue | a new identity per publication attempt | the identity is the row key, the envelope ID and the broker deduplication key at once |
| Observation and Entity Event families only | a Command lifecycle family | accepted State and event evidence is Core-verified data, while Command status already has an authoritative durable HTTP/SQLite history that Observations cannot substitute for |
| Rejecting a subject transform | tolerating one | a transform would silently break the subject contract while the publish still succeeded and the outbox row was deleted |
| A fixed seven-day/one-GiB retention bound | a configurable retention policy | v1 has no deployments and one honest, documented bound is simpler than a setting nobody can tune yet |

## 21. Success criteria

The foundation is complete when:

1. external clients read stable, schema-valid facts from one stream without reading Core's database;
2. a fact and the evidence it reports commit atomically, and a publication failure never changes the committed devices result;
3. a broker outage, a relay restart or a Core restart delays publication but never discards a committed fact, and the retry reuses the same identity and bytes;
4. every reader states its own recovery policy, and Core neither requires a subscriber nor pins a consumer position;
5. duplicate, retention and eviction limits are documented and match the implementation;
6. Command lifecycle is exposed only through durable HTTP/SQLite history, with no Command fact, subject, schema or outbox row anywhere.

No open implementation questions remain. Any expansion to additional fact families, configurable retention or a Core-owned recovery reader requires a separate reviewed change.
