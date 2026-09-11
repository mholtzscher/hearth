# Entity Events: durable ingestion and history

**Status:** Draft for review. Implement this foundation before automations after approval.
**Baseline:** `378080b`.
**Effort:** XL across four deliverables. Reuse the existing devices module and infrastructure.

## 1. Purpose and scope

Hearth records named occurrences without treating them as State. Broker-acknowledged reports survive Core outages and remain inspectable through a paginated per-Entity history API. This foundation precedes automations.

```text
Adapter SDK → dedicated JetStream stream → devices consumer → SQLite
                                                              ↓
                                               GET /v1/entities/{id}/events
```

This spec owns the event Entity type, wire and SDK contracts, durable ingestion, history, and simulator proof. The [deferred automation draft](entity-event-automations.md) depends on it.

**Non-goals:** automation execution or replay, a generic event bus or event sourcing, arbitrary payloads, global search or a dashboard, configurable retention, physical-device mappings, and Adapter disk outboxes.

Historical ingestion does not authorize automatic execution. Future automations must distinguish fresh input from backlog. This feature makes no live-trigger delivery guarantee.

## 2. Existing foundations and ownership

The `devices` module owns identity, registration, runtime fencing, enablement, State and Observation persistence, Command history, and Entity Events.

Reuse these established patterns:

- `sdk/adapter/command_evidence.go` for stable identity, PubAck, and same-message retry.
- `internal/modules/devices/nats/jetstream.go` and `observation.go` for resources, durable consumption, and commit-before-ack handling.
- `sqlite_observations.go` for first-seen identity, rejection evidence, and transactional runtime, owner, and enablement checks.

Generated SQL and NATS types stay behind the devices service interface. App assembly owns connections and process lifecycle. Entity Events require no general Observation-processing abstraction.

## 3. What an Entity Event means

An **Entity Event** is one named occurrence reported by an Adapter for an Entity. IDs distinguish reports: two `single_press` IDs are two events, while redelivery of one ID is the same event. Acceptance means the report passed Core's processing-time rules, not that it proves physical truth or causation.

Introduce **`hearth.enumevent/v1`**, stateless and non-commandable, with this support shape:

```json
{"state":{},"operations":{},"events":{"names":["single_press","double_press"]}}
```

Names are unique slugs (`^[a-z0-9][a-z0-9_-]{0,62}$`), with 1 to 64 entries. One Entity may represent several buttons or gestures through distinct names. The type has no State or Operations and adds no Device kind.

Generated implementation:

- Add optional manifest boolean `event_source` (default false). True requires `stateless:true`, an empty Operations map, and required `support.events.names` with the shape above.
- Validate the fixed schema during generation. Emit typed support and name validation, descriptor and event builders, conformance examples, and the catalog selector. Core has no handwritten event-type branch or selector DSL.
- Allow optional `events` in the outer registration support schema and preserve it through descriptor DTOs. Per-type schemas stay closed; only event-source types allow it.
- Only types with a generated supported-name selector can emit accepted Entity Events.
- Preserve `classifyObservation` behavior: all stateless Observations are `invalid_value`. The new facade generates no Observation builders or Command handlers, and Entity reads return `state:null`.

During implementation, update the glossary definitions of Entity Event, Entity, Entity support, and Entity enablement for event sources, supported names, and disabled-source rejection. Keep Observation and State unchanged, and distinguish Entity Events from the outbound `enumaction.trigger` Operation.

## 4. Wire and SDK contract

One new schema, **`urn:hearth:schema:entity-event:v1`**, in `contracts/v1/entity-event.schema.json`:

```json
{
  "id":"evt_<uuidv7>",
  "schema":"urn:hearth:schema:entity-event:v1",
  "emitted_at":"<UTC RFC3339Nano>",
  "correlation_id":"cor_<uuidv7>",
  "data":{"entity_id":"ent_<uuidv7>","name":"single_press"}
}
```

The strict envelope omits `causation_id`. Add `evt_id` to common ID definitions and embed and register the schema, but leave it out of the causation union because this slice produces no message caused by an event. Entity Events have no Command link, arbitrary payload, or source-time field. `emitted_at` is SDK publication time, not claimed physical occurrence time.

Subject: `hearth.v1.adapter.<adapter>.runtime.<runtime>.entity-event.<entity_id>`. The SDK sets `Nats-Msg-Id` to the envelope event ID and preserves W3C trace headers. Core verifies subject/payload Entity agreement and MsgId/envelope identity before persistence.

New `sdk/adapter/entity_events.go`:

```go
// EntityEventID identifies one report across all publication retries.
type EntityEventID string

type EntityEvent struct {
    EntityID string
    Name     string
}

// PublishEntityEvent waits for JetStream storage acknowledgement, not Core acceptance.
func (s *Session) PublishEntityEvent(context.Context, EntityEvent) (EntityEventID, error)
```

The generated `sdk/adapter/enumeventv1` facade adds `EntityEventInput{EntityID string; Support Support; Name string}` and `NewEntityEvent(EntityEventInput) (adapter.EntityEvent, error)` for typed support/name checks. Generic Session publication still validates the envelope; only Core owns current support validation.

Publication mints `evt_` and `cor_` UUIDv7 values once and encodes once. Transient JetStream retries reuse the ID, bytes, subject, MsgId, and trace metadata until PubAck, caller cancellation or deadline, a permanent error, or Session termination. Return the minted ID with later errors. After an ambiguous failure, the caller must not assign the report a new identity. Reuse existing publication context, Session cancellation, reconnect buffering, and lifecycle behavior.

### Core-offline guarantee and its limits

A connected, registered Session can publish to an existing stream without a Core consumer. Transient heartbeat failures retry in the Session lifecycle context, with no fixed missed-tick cutoff. A permanent or fenced outcome or explicit close still terminates the Session.

The guarantee covers **broker-acknowledged input while NATS and JetStream remain available** within the limits below. Initial claim and registration need Core. During a NATS outage, the SDK retries only while its Adapter process lives. Broker-acknowledged input survives Adapter exit within stream limits. Unacknowledged work is not protected against Adapter process death. The guarantee does not cover NATS process loss. Stream limits can discard old input during a long Core outage.

## 5. Dedicated durable stream and consumer

Follow the Observation resource pattern, with independent subjects/resources so button history cannot be mistaken for State:

| Setting | Entity Event value |
|---|---|
| Stream | `HEARTH_ENTITY_EVENTS_V1` |
| Subject filter | `hearth.v1.adapter.*.runtime.*.entity-event.*` |
| Storage/retention | FileStorage, LimitsPolicy, DiscardOld, PubAck enabled |
| Limits | 7 days or 1 GiB, whichever is reached first; max message size 4 KiB |
| Consumer | `hearthd-entity-events-v1`, durable |
| Delivery | DeliverAll, ReplayInstant, explicit acknowledgement |
| Retry/order | AckWait 30s, unlimited redelivery, one maximum pending acknowledgement |

Match the Observation stream's explicit unconstrained count and per-subject limits. Provision absent resources and reject incompatible existing configuration. Add no replay tasks or stream administration API.

One pending slot is safe because Core resolves every deterministic per-report failure before it can occupy that slot. A report whose persisted event-source descriptor Core cannot interpret is terminated, which releases the slot immediately, and a report that only storage failed to record is negatively acknowledged with a bounded delay. A report that storage cannot record may hold the sole slot until that retry, which is acceptable: while storage is unavailable no other report can be safely persisted either, and the reports queued behind it remain in the stream until storage recovers.

`EntityEventConsumer` has `Active`, `Stop`, `Drain`, and `Closed` lifecycle methods. It validates the route and envelope, reads the JetStream timestamp, records the event, then acknowledges it or resolves a record failure as either a termination or a delayed negative acknowledgement. It calls no synchronous subscribers, Commands, or automations.

- Acknowledge wire-invalid input, missing or mismatched MsgId, unexpected causation, and route mismatch. Log a safe permanent class. Create no SQLite row when Core cannot form trustworthy domain input; raw input remains only in the bounded stream.
- Acknowledge first-seen accepted or rejected input only after the SQLite transaction commits.
- Acknowledge a duplicate or identity conflict after the repository establishes the result, without changing the existing row.
- Never positively acknowledge a record or commit failure, and never write a partial row. Log a safe structured failure, then classify the failure:
  - A failure Core attributes to interpreting the Entity's persisted event-source descriptor—an unknown Entity Type or support that no longer satisfies its type's schema—is permanent and record-local. Report it as the devices-level `ErrEntityEventDescriptorCorrupt` descriptor error without exposing persisted descriptor bytes, terminate the report, and release its pending slot. Log a further safe `stage=term`, `error_code=term_failed` failure if termination itself fails. A terminated report gets no history row and is never redelivered to this consumer; the bounded stream remains its only raw evidence.
  - Every other record or commit failure is transient storage trouble. Send a delayed negative acknowledgement after `EntityEventRedeliveryDelay`, which equals `AckWait`. The delay bounds the retry cadence and returns the report for redelivery while keeping it repairable for as long as the stream retains it; nothing terminates or drops it. If the negative acknowledgement itself fails, log a further safe structured failure and let `AckWait` expire, which redelivers the report anyway.
- A transiently failed report holds the sole pending entry until its delayed redelivery. A report that commits on a later attempt records at that later Core time, so history follows Core recording order rather than stream order.
- A slow log must not delay acknowledgement of a committed result.

Backlog is consumed. **Age is not a rejection reason.** Core may log SDK clock skew using the existing one-minute diagnostic threshold, but skew does not prevent recording or define ordering.

## 6. SQLite acceptance and rejection history

One `entity_events` table stores each first-seen, wire-valid report and its disposition. The same row provides history and duplicate detection.

In one devices-owned transaction:

1. Look up the event ID. Identical immutable input returns `duplicate`; changed input returns `identity_conflict`. Preserve the first row in both cases.
2. Check the active Adapter runtime using the supervisor's fencing semantics, not an independent wall-clock lease check. An inactive or unknown runtime returns `stale_runtime`.
3. Check Entity existence and ownership. Return `unknown_entity` or `wrong_adapter`.
4. Check Entity enablement. A disabled Entity returns `entity_disabled`, with no Command-linked exception.
5. Check current Entity event support and name. A non-event type or unsupported name returns `unsupported_event`. Non-event classification wins before any support decoding, so a resolved non-event type returns `unsupported_event` even when its persisted support is malformed. An unknown persisted type, or a persisted event-source descriptor that no longer satisfies its schema, is not a rejection: Core reports it as `ErrEntityEventDescriptorCorrupt` and writes no row, because redelivering such a report can never succeed.
6. Persist `accepted` if all checks pass. Otherwise persist `rejected` with the first rejection code above. Commit once.

Adapter health and Entity availability do not gate historical input. Recording changes no State, Commands, bindings, enablement, health, or availability.

An old report may be rejected if runtime, ownership, enablement, or support changed while Core was offline. Reprocessing the same identity never reclassifies it after later metadata changes. Core does not reconstruct historical descriptors or claim that a rejected report was invalid when physically emitted.

Store the bounded, wire-valid reported name for accepted and rejected events. The schema-constrained label diagnoses unsupported names. Do not log it as an unrestricted payload or treat it as validated support membership.

The fingerprint is the raw 32-byte SHA-256 hash of UTF-8 `json.Marshal([]string{adapterID, string(runtimeID), string(entityID), string(name), emittedAt.UTC().Format(time.RFC3339Nano), string(correlationID)})`. Inputs are canonical strings, array order is fixed, and the encoding has no extra whitespace. Use the event ID as the lookup key. Exclude delivery count, Core receipt and recording times, and trace headers. A changed tuple for the same ID logs `entity_event.identity_conflict` without changing or adding a row. JetStream may suppress a duplicate MsgId before Core compares it, so Core cannot log every conflict.

### Record shape and retention

| Column | Meaning |
|---|---|
| `receive_order INTEGER PRIMARY KEY AUTOINCREMENT` | First-seen Core recording order, independent of Observation order and without ties. |
| `event_id TEXT UNIQUE NOT NULL` | Immutable `evt_` report identity. |
| `adapter_id`, `runtime_id`, `entity_id`, `correlation_id` | `TEXT NOT NULL` without foreign keys. Keep the reported canonical runtime ID even when Core has no runtime row. Adapter, runtime, and correlation IDs are private. |
| `name`, `fingerprint` | Bounded reported name and immutable-input fingerprint. |
| `disposition`, `rejection_code` | `accepted` without a rejection, or `rejected` with one enumerated code. |
| `emitted_at` | SDK envelope time for diagnosis, not ordering. |
| `received_at` | JetStream storage time, not consumer callback time. |
| `recorded_at` | Core time of the first SQLite record, retained across redelivery. |

Name, disposition, and timestamp columns are NOT NULL. `fingerprint` is a NOT NULL BLOB with a 32-byte length CHECK. A CHECK allows a nullable `rejection_code` only for accepted rows and requires one of the five codes for rejected rows. Add basic ID prefix, length, slug, and name checks; wire and service parsers enforce canonical UUIDv7. Use the fixed-width sortable UTC encoding. Index `(entity_id,receive_order DESC)` for history and `(recorded_at,receive_order)` for pruning. Public history is newest first by receive order, not physical or cross-stream chronology.

**SQLite retains events for 30 days from `recorded_at`.** This exceeds the seven-day stream window, and events have no current-State anchor. Define internal constant `EntityEventHistoryRetention`; do not add Core configuration.

The existing hourly maintenance worker calls `Service.DeleteExpiredEntityEvents`. The service deletes eligible events in batches of 500, using one transaction per batch, until a batch deletes fewer than 500 rows or the context ends. Use one cutoff per sweep, delete records strictly older than it, and release the connection between batches. Never prune at startup. Do not add another timer or change Observation retention.

The SQLite row provides duplicate protection only while retained. Database loss, clock anomalies that invalidate retention assumptions, or publication of an archived envelope after pruning can produce another first-seen record. Reading history never executes work.

## 7. Concrete Core interfaces

Add event identity/name types to `internal/modules/devices/model.go` and canonical ID helpers in `ids.go`:

```diff
 type ObservationID string
+type EntityEventID string
+type EntityEventName string
 type CommandID string
```

Catalog extension in `internal/modules/devices/catalog.go`:

```diff
 type EntityTypeDefinition struct {
     id               EntityTypeID
     stateless        bool
+    eventNames       func(EntitySupport) ([]EntityEventName, error)
```

`TypeCatalog.SupportsEntityEvent(entity Entity, name EntityEventName) (bool, error)` returns false/nil for a type without event support or an unsupported name. The non-event check precedes support decoding, so a resolved non-event type returns false/nil even when its persisted support is malformed. An unknown persisted type and a corrupt event-source descriptor return errors, because the generated selector decodes and validates support only for a type that owns it. The selector returns an owned name slice and separates unsupported input from catalog failures. Every error it returns is a deterministic failure to interpret the persisted descriptor, which the repository reports as the permanent `ErrEntityEventDescriptorCorrupt` class.

New `internal/modules/devices/entity_events.go` defines:

```go
type EntityEvent struct {
    ID            EntityEventID
    EntityID      EntityID
    Name          EntityEventName
    CorrelationID CorrelationID
    EmittedAt     time.Time
}

type RecordEntityEventParams struct {
    AdapterID  string
    RuntimeID  RuntimeID
    Event      EntityEvent
    ReceivedAt time.Time // JetStream metadata timestamp
    Now        func() time.Time // called by persistence for first recording
}

type EntityEventDisposition string // accepted | rejected (persisted only)
type EntityEventRejection string   // stale_runtime | unknown_entity | wrong_adapter | entity_disabled | unsupported_event
type EntityEventRecordOutcome string // accepted | rejected | duplicate | identity_conflict

type EntityEventHistoryEntry struct {
    EventID      EntityEventID
    EntityID     EntityID
    Name         EntityEventName
    Disposition  EntityEventDisposition
    Rejection    *EntityEventRejection
    EmittedAt    time.Time
    ReceivedAt   time.Time
    RecordedAt   time.Time
    ReceiveOrder int64 // internal/cursor position only
}

type EntityEventRecordResult struct {
    Outcome   EntityEventRecordOutcome
    Rejection *EntityEventRejection // only for a newly rejected event
}

// ErrEntityEventDescriptorCorrupt is the permanent class for a persisted
// descriptor Core cannot interpret: an unknown Entity Type or event-source
// support that no longer satisfies its schema. The consumer terminates such a
// report instead of redelivering it.
var ErrEntityEventDescriptorCorrupt error

type EntityEventDescriptorError struct {
    EntityID EntityID
    TypeID   EntityTypeID
    cause    error // never rendered by Error
}

func (failure *EntityEventDescriptorError) Error() string   // fixed, safe message
func (failure *EntityEventDescriptorError) Unwrap() []error // sentinel class + catalog cause

type ListEntityEventsParams struct {
    EntityID          EntityID
    BeforeReceiveOrder *int64
    Limit             int
}

func (s *Service) RecordEntityEvent(context.Context, string, RuntimeID, EntityEvent, time.Time) (EntityEventRecordResult, error)
func (s *Service) ListEntityEvents(context.Context, ListEntityEventsParams) (Page[EntityEventHistoryEntry], error)
func (s *Service) DeleteExpiredEntityEvents(context.Context, time.Time) error // argument is Core sweep time
```

Use named constants for the closed outcome, disposition, and rejection values. The service supplies `Dependencies.Now`; transport cannot set `recorded_at`. SQLite computes the fingerprint from the validated tuple. Trusted methods reject zero or invalid arguments before writing, while transport filters permanent wire errors before calling them. `RecordEntityEvent` returns `ErrEntityEventDescriptorCorrupt` (as an `EntityEventDescriptorError`) only when the persisted descriptor cannot be interpreted; ordinary query, transaction, and commit failures return their own retryable errors.

Add the persistence capability to the existing `Stores`, implemented by the existing SQLite repository and existing devices sqlc package:

```diff
 type Stores struct {
     // existing fields unchanged
     Observations ObservationRepository
+    EntityEvents EntityEventRepository
 }
```

```go
type EntityEventRepository interface {
    RecordEntityEvent(context.Context, RecordEntityEventParams) (EntityEventRecordResult, error)
    ListEntityEvents(context.Context, ListEntityEventsParams) (Page[EntityEventHistoryEntry], error)
    DeleteEntityEventsBefore(context.Context, time.Time, int) (int64, error) // cutoff, batch size
}
```

Consumer-owned `EntityEventRecorder` exposes only the Service's `RecordEntityEvent` signature. HTTP calls the Service, which validates the parent and pagination before reading.

## 8. Minimal history API

**`GET /v1/entities/{entity_id}/events`**, operation ID `list-entity-events`:

```json
{
  "items":[
    {"event_id":"evt_…","entity_id":"ent_…","name":"single_press","disposition":"accepted",
     "emitted_at":"…Z","received_at":"…Z","recorded_at":"…Z"},
    {"event_id":"evt_…","entity_id":"ent_…","name":"double_press","disposition":"rejected",
     "rejection_code":"unsupported_event","emitted_at":"…Z","received_at":"…Z","recorded_at":"…Z"}
  ],
  "next_cursor":"<opaque position if another page exists>"
}
```

- Return accepted and rejected records without filters. This slice has no global or per-event detail endpoint.
- An existing Entity with no events returns 200 with `items:[]`, including non-event types. An unknown parent returns 404. Rejections for unknown Entity IDs stay private and have no browse route.
- Accept `limit` and `cursor`. Default to 50, allow 1 to 200, query `limit+1`, omit totals, and omit the last page's cursor.
- Use the unsigned, versioned base64url JSON cursor. Bind it to `entity_events`, the Entity ID, and an exclusive lower `receive_order`, with no filter field. State and event history reject each other's cursors. Both SQL queries use the same index. Pages are not snapshots; restart for newer input, and pruning may empty a continuation.
- Huma models map UTC timestamps and optional rejection codes. Never expose Adapter, runtime, or correlation IDs, fingerprint, receive order, or raw envelopes. Domain, schema, and page errors use existing 400 and 422 conventions; unexpected errors return 500.

The endpoint shows whether Core recorded a report and why Core rejected it. A missing row does not prove that no physical press occurred because wire-invalid, lost, or broker-expired input can be absent.

## 9. Assembly, simulator and implementation layout

Core provisions both streams before transports, starts both consumers, and includes their configuration and activity in readiness. Readiness neither waits for backlog exhaustion nor gates ingestion.

Keep Observation and health dependencies alive for draining Commands. On shutdown or startup error, drain or stop the event consumer before its database and NATS dependencies. Unprocessed or unacknowledged events remain for the next Core process. Startup does not replay events into Commands, and callbacks do not wait for Commands.

Simulator scenario `entity-events` registers an `events` Entity beside its existing `power` Entity without changing existing scenarios or Binding keys. Use generated registration and event builders. Expose deterministic `EmitEntityEvent(ctx,name)` for tests. While the Session is connected, local stdin lines `single_press` and `double_press` publish synthetic reports. A cancellable reader does not queue input; it logs and drops input while the publisher is busy. EOF stops only input; shutdown joins workers. Do not add a privileged Core HTTP injection endpoint or claim physical-device validation.

| ID | Outcome | Effort | Depends on | Acceptance |
|---|---|---|---|---|
| D1 | Schema-backed event-source Entity and wire/SDK data contract | L | None | A1, A2 |
| D2 | SDK PubAck publication, dedicated consumer, and transactional SQLite event history | L | D1 | A3 to A6 |
| D3 | Per-Entity history API and fixed retention maintenance | M | D2 | A7, A8 |
| D4 | Simulator Core-offline recovery proof and operator documentation | L | D2, D3 | A9, A10 |

```text
contracts/v1/
├── entity-event.schema.json             # new, durable report schema [D1]
├── common.schema.json / embed.go        # modify, evt_ ID/schema registration [D1]
└── registration-request.schema.json     # modify, optional event support [D1]
entitytypes/
├── entitytype-manifest.schema.json      # modify, event_source flag [D1]
└── enumeventv1/                         # new, schemas/manifest/examples/generated type [D1]
sdk/adapter/
├── entity_events.go                     # new, public types [D1] and PubAck/retry publication [D2]
└── enumeventv1/                         # generated, typed descriptor/event builders [D1]
internal/
├── cmd/entitytypegen/                   # modify, event-source generation and fixtures [D1]
├── contracts/v1/natswire/subjects.go     # modify, runtime-scoped event subject helpers [D1]
├── modules/devices/
│   ├── model.go / ids.go / catalog.go   # modify, event IDs/name selector [D1]
│   ├── zz_generated_entitytypes*.go     # generated, event catalog/conformance [D1]
│   ├── entity_events.go                 # new, domain recording/retention [D2]
│   ├── entity_event_history.go          # new, parent/paging reads [D3]
│   ├── service.go / repository.go       # modify, event persistence capability [D2]
│   ├── sqlite_repository.go             # modify, event capability in SQLiteStores [D2]
│   ├── sqlite_entity_events.go          # new, disposition/dedup/history/prune [D2,D3]
│   ├── dbqueries/entity_events.sql      # new, insert/dedup/history/retention SQL [D2,D3]
│   ├── dbsqlc/                          # generated, existing devices SQL package [D2,D3]
│   ├── nats/entity_event.go             # new, EntityEventConsumer and DTO mapping [D2]
│   ├── nats/entity_event_resources.go   # new, stream/consumer provisioning [D2]
│   └── api/entity_events.go             # new, history endpoint/DTOs/cursor [D3]
├── platform/db/migrations/00001_initial.sql # modify, entity_events table/indexes [D2]
├── app/hearthd/run.go / server.go       # modify, consumer/readiness/drain/maintenance [D2,D3]
├── app/hearthd/entity_events_integration_test.go # new, SDK/NATS/DB/HTTP proof [D4]
└── adapters/simulator/ / app/simulator/ # modify, named event scenario [D4]
README.md / configs/ / CONTEXT.md / docs/{architecture,logging}.md
                                       # modify, guarantees and usage [D4]
```

Update required descriptor DTOs and route registration interfaces without rearranging unrelated modules. Tests share their behavior's deliverable. D1 includes SDK types needed by generated builders, so it compiles before D2. Use the existing devices sqlc stanza and generation-check task. Modify the initial migration and recreate development databases because Hearth has no deployments. Add no legacy migration. Serialize changes to shared migration and generation files.

## 10. Acceptance tests and review gates

- **A1. Generated contracts.** Support round-trips from SDK registration through Core Entity reads. Closed types reject event support, and generated catalog conformance proves a resolved non-event type rejects Events before decoding malformed unrelated support. The zero-Operation event type validates names, generates no Observation or Command facade, and changes no State or Command.
- **A2. Wire.** Test schema, route, ID, time, MsgId, causation, size, and subject/Entity checks. Logs omit raw envelopes and unsafe names. Generated and embedded tests cover the schema.
- **A3. Durable SDK.** A lost PubAck retries the same ID and payload. Caller or Session cancellation stops retries. PubAck does not claim Core recording or acceptance. Existing Session, Observation, and Command-evidence behavior remains unchanged.
- **A4. SQLite identity and disposition.** Different IDs create separate rows. Duplicates and changed-input conflicts preserve the first row, its timestamps, and its disposition. Test rejection precedence and health and availability exclusion. Only a persisted descriptor Core cannot interpret—an unknown Entity Type or malformed event-source support—gets the permanent `ErrEntityEventDescriptorCorrupt` class, its message exposes no descriptor bytes, and it writes no row; ordinary storage failures stay retryable. A transiently failed record is never positively acknowledged, writes no partial row, and is redelivered without limit.
- **A5. Commit before acknowledgement.** Failure before commit causes delayed redelivery without a partial row. Response or acknowledgement loss after commit still yields one row after restart and redelivery. Permanent wire errors and permanently uninterpretable descriptors are acknowledged or terminated respectively and cannot block valid input with one maximum pending acknowledgement.
- **A6. Backlog semantics.** Old valid input is recorded. Runtime takeover or narrowed support during downtime produces the specified rejection. Metadata changes never reclassify an ID. Events never enter State history or satisfy Commands.
- **A7. HTTP history.** Test dispositions, parent errors, empty results, keyset continuation, cursor scope, limits, timestamps, ordering under clock disagreement, and private fields through real SQLite and the Huma route.
- **A8. Retention.** Test the exact 30-day boundary, batching, startup behavior, absence of a State anchor, unchanged Observation pruning, retained duplicate evidence, non-execution, and both indexes.
- **A9. Core-offline vertical slice.** With NATS running, stop Core and obtain PubAcks for several simulator events. Restart Core and read each once through HTTP. Test heartbeat retry without a fixed missed-tick cutoff, consumer lifecycle and readiness, and drain durability.
- **A10. Delivery.** The documented simulator and history recipe works without automations or hardware. `mise run validate` and reproducible generation pass. Only the devices module is required; no automation type, table, transport hook, or replay path appears.

Use real SQLite and embedded NATS for durability and transaction claims, injected clocks for retention, and synchronization barriers for acknowledgement and drain races. SDK failure injection proves identity stability without real outages. Use the existing test framework.

## 11. Automation follow-up

Before implementing automations, revise the deferred spec to consume Core-accepted Entity Event facts. Define the fresh-trigger boundary independently from this history consumer and decide how Run admission relates to recorded events. This spec does not choose the downstream execution interface. Real Zigbee2MQTT mapping remains separately owned by `specs/zigbee2mqtt-adapter.md`; its freshness contract must distinguish actual Events from retained or cache-expanded values while preserving repeated equal occurrences.
