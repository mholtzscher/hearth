# Device Events: durable ingestion and history

**Status:** Draft for review; implement this foundation before automations after approval.
**Baseline:** `378080b`.
**Effort:** XL overall, four bounded deliverables. Reuse existing device infrastructure; no new product module or event framework.

## 1. Purpose and scope

Hearth can record that something happened to an Entity without pretending it is State. A simulated button publishes two `single_press` events; both remain inspectable, including when Core was temporarily offline.

User decisions:

- Device Events are a separate foundational feature, implemented before automations.
- Events published to NATS while Core is offline should survive for history when Core returns.
- The first slice includes a minimal paginated per-Entity history API, not a dashboard.

```text
Adapter SDK → dedicated JetStream stream → devices consumer → SQLite
                                                              ↓
                                               GET /v1/entities/{id}/events
```

This spec owns the event Entity type, wire schema, SDK publication, durable ingestion, history and simulator proof. It supersedes the one-shot event transport and ephemeral event-receipt proposals in [the deferred automation draft](device-event-automations.md).

**Non-goals:** Triggers, Runs, automation hooks, replay execution, a generic event bus, event sourcing of other domain models, arbitrary event payloads, a global event search API, a dashboard, configurable retention, physical-device mappings, and Adapter disk outboxes.

Historical ingestion and automatic execution are different policies. Preserving an old press does **not** authorize running an automation for it. Future automation design must distinguish fresh accepted input from history/backlog; this feature makes no live-trigger delivery guarantee.

## 2. Existing foundations and ownership

The cohesive `devices` module already owns identity, registration, runtime fencing, enablement, State/Observation persistence and Command history. Add Device Events there, not under automations or platform.

Reuse established patterns:

- `sdk/adapter/command_evidence.go`: stable publication identity, JetStream acknowledgement and same-message transient retry.
- `internal/modules/devices/nats/jetstream.go` and `observation.go`: resource provisioning/validation, durable consumption, acknowledgement after SQLite commit.
- `sqlite_observations.go`: first-seen identity and transactional runtime/owner/enablement checks, preserving rejection evidence.
- `entity_state_history.go` and `api/entity_state_history.go`: parent existence, keyset paging, explicit transport models.
- `entitytypes/` and `internal/cmd/entitytypegen/`: schema/manifest-owned Entity behavior and generated SDK facades.
- `internal/app/hearthd/run.go`: assembly, readiness, maintenance and dependency-safe shutdown.

No generated SQL or NATS types cross the devices service interface. App assembly still owns connections and process lifecycle. Do not refactor Observations into a new general “event processing” abstraction as a prerequisite.

## 3. What a Device Event means

A **Device Event** is one named occurrence reported by an Adapter for an Entity. Its identity distinguishes reports, not names: two `single_press` reports with different IDs are two events; redelivery of one identity is not another press. Acceptance establishes valid reported input under Core's processing-time rules, not physical truth or causation.

Introduce **`hearth.enumevent/v1`**, stateless and non-commandable, with this support shape:

```json
{"state":{},"operations":{},"events":{"names":["single_press","double_press"]}}
```

Names are unique slugs (`^[a-z0-9][a-z0-9_-]{0,62}$`), 1–64 entries. One Entity may represent several buttons/gestures through distinct names. No last-event State, sequence counter disguised as State, implicit Operation, or extra Device kind.

Generated implementation:

- Add optional manifest boolean `event_source` (default false). True requires `stateless:true`, an empty Operations map, and required `support.events.names` with the shape above.
- Verify the fixed schema structure during generation; emit typed support validation, name membership, descriptor/event builders and conformance examples. No generic selector DSL or handwritten branch for the new type in Core.
- Allow optional `events` in the outer registration support schema and preserve it through descriptor DTOs. Existing per-type support schemas remain closed and reject it on other types.
- Extend the catalog with an optional generated supported-name selector. A type without it cannot emit accepted Device Events, even if it is otherwise stateless.
- Existing `classifyObservation` rejects all stateless Observations as `invalid_value`. Preserve that behavior. The new facade generates neither Observation builders nor Command handlers, and Entity reads return `state:null`.

Proposed glossary changes at implementation: define Device Event, widen Entity to include event sources, and widen Entity support to include supported event names. Keep Observation and State meanings unchanged; distinguish an inbound Device Event from the outbound `enumaction.trigger` Operation. No automation glossary changes are required for this foundation.

## 4. Wire and SDK contract

One new schema, **`urn:hearth:schema:device-event:v1`**, in `contracts/v1/device-event.schema.json`:

```json
{
  "id":"evt_<uuidv7>",
  "schema":"urn:hearth:schema:device-event:v1",
  "emitted_at":"<UTC RFC3339Nano>",
  "correlation_id":"cor_<uuidv7>",
  "data":{"entity_id":"ent_<uuidv7>","name":"single_press"}
}
```

The shared envelope is strict; `causation_id` is absent for Device Events. Add `evt_id` to common ID definitions and embed/register the schema, but do not add it to the common causation union: this slice has no Hearth message caused by an event or event request/response exchange. There is no Hearth request/response pair, no `expires_at`, no Command link, and no arbitrary payload or source-time fields in this first version. `emitted_at` is SDK publication time, not claimed physical occurrence time.

Subject: `hearth.v1.adapter.<adapter>.runtime.<runtime>.device-event.<entity_id>`. The SDK sets `Nats-Msg-Id` to the envelope event ID and preserves W3C trace headers. Core verifies subject/payload Entity agreement and MsgId/envelope identity before persistence.

New `sdk/adapter/device_events.go`:

```go
// DeviceEventID identifies one report across all publication retries.
type DeviceEventID string

type DeviceEvent struct {
    EntityID string
    Name     string
}

// PublishDeviceEvent waits for JetStream storage acknowledgement, not Core acceptance.
func (s *Session) PublishDeviceEvent(context.Context, DeviceEvent) (DeviceEventID, error)
```

The generated `sdk/adapter/enumeventv1` facade adds `DeviceEventInput{EntityID string; Support Support; Name string}` and `NewDeviceEvent(DeviceEventInput) (adapter.DeviceEvent, error)` for typed support/name checks. Generic Session publication still validates the envelope; only Core owns current support validation.

Publication mints `evt_` and `cor_` UUIDv7 values once, encodes once, and retries transient JetStream failures with the same ID, bytes, subject, MsgId and trace metadata. Reuse the existing publication context/session cancellation pattern. Retry until PubAck, caller cancellation/deadline, a permanent error, or Session termination. Return a minted ID on subsequent errors for diagnosis. A caller must not rebuild the same report with a new identity after an ambiguous failure.

**Keep existing NATS reconnect buffering and Session lifecycle behavior.** This replaces the automation draft's no-buffer/no-retry rule; there is no new two-second expiry, heartbeat policy or SDK offline queue. Acknowledged events survive an Adapter exit within stream limits. Unacknowledged work is not protected against Adapter process death.

### Core-offline guarantee and its limits

A previously connected/registered SDK Session can publish to an already-provisioned JetStream stream without a running Core consumer. Existing transient heartbeat failures retry in the Session lifecycle context; there is no fixed number of missed heartbeat ticks that automatically ends a Session. A permanent/fenced outcome or explicit close still terminates it.

The guarantee is specifically **broker-acknowledged input while NATS/JetStream remains available**, within the retention limits below. Initial claim/registration still needs Core. When NATS itself is unavailable, the SDK can retry while alive, but this is not a disk-backed outbox or a guarantee through Adapter/NATS process loss. If Core is absent longer than broker retention/capacity permits, older input can be discarded.

## 5. Dedicated durable stream and consumer

Follow the Observation resource pattern, with independent subjects/resources so button history cannot be mistaken for State:

| Setting | Device Event value |
|---|---|
| Stream | `HEARTH_DEVICE_EVENTS_V1` |
| Subject filter | `hearth.v1.adapter.*.runtime.*.device-event.*` |
| Storage/retention | FileStorage, LimitsPolicy, DiscardOld, PubAck enabled |
| Limits | 7 days or 1 GiB, whichever is reached first; max message size 4 KiB |
| Consumer | `hearthd-device-events-v1`, durable |
| Delivery | DeliverAll, ReplayInstant, explicit acknowledgement |
| Retry/order | AckWait 30s, unlimited redelivery, MaxAckPending 1 |

Leave unconstrained count/per-subject limits at the same explicit values used for Observations. Provision absent resources; reject incompatible existing configuration rather than silently changing retention/delivery policy. No retention-based replay tasks or stream administration API.

`DeviceEventConsumer` has the existing consumer lifecycle shape (`Active`, `Stop`, `Drain`, `Closed`). It decodes, validates the route/envelope, gets the JetStream timestamp, calls the devices recorder, then acknowledges. No synchronous downstream subscribers, commands or automation callbacks.

- Wire-invalid input, missing/mismatched MsgId, unexpected causation and route mismatch: acknowledge and log a safe permanent class. No SQLite row when trustworthy domain input cannot be formed; raw input remains only in the bounded stream.
- First-seen accepted/rejected input: acknowledge only after the SQLite transaction commits.
- Exact duplicate or identity conflict: acknowledge after the repository has established that result; do not mutate the existing row.
- Infrastructure/commit errors: leave unacknowledged for redelivery. Slow logs must not delay acknowledgement of a committed result.

Backlog is intentionally consumed. **Age is not a rejection reason.** Future SDK clock skew may be logged using the existing one-minute diagnostic threshold; it does not prevent recording or define ordering.

## 6. SQLite acceptance and rejection history

One new `device_events` table contains each first-seen, wire-valid report and its disposition. It is both history and the duplicate ledger—not two tables or an outbox.

In one devices-owned transaction:

1. Look up event ID. Identical immutable input is `duplicate`; changed input is `identity_conflict`. Preserve the first row in both cases.
2. Check active Adapter runtime using existing supervisor-owned fencing semantics, not an independent wall-clock lease check. Inactive/unknown runtime → `stale_runtime`.
3. Check Entity existence and owning Adapter → `unknown_entity` or `wrong_adapter`.
4. Check Entity enablement → `entity_disabled`. Events have no Command-linked exception.
5. Check current Entity event support/name → `unsupported_event` for a non-event type or unsupported name. Corrupt persisted descriptors/catalog errors are infrastructure failures, not evidence of a bad event.
6. Persist `accepted` if these checks pass, otherwise `rejected` with the first rejection code above. Commit once.

Adapter health and Entity availability do not separately gate historical input, matching the Observation distinction between reports and reachability. No write to State, Commands, bindings, enablement, health or availability occurs.

Validation uses **processing-time** runtime/ownership/enablement/support. If those changed while Core was offline, an old report can be durably recorded as rejected. Reprocessing the same identity never reclassifies it after a later enablement/support change. We do not reconstruct historical descriptors or claim that a rejected old report was invalid when physically emitted.

Store the bounded, wire-valid **reported name even for rejected events**, with an explicit rejected disposition. Unlike an arbitrary rejected State payload, this is only a schema-constrained label and is useful for diagnosing unsupported names. Do not log it as an unrestricted payload or mistake it for validated support membership.

Identity fingerprint: the raw 32 bytes of SHA-256 over UTF-8 `json.Marshal([]string{adapterID, string(runtimeID), string(entityID), string(name), emittedAt.UTC().Format(time.RFC3339Nano), string(correlationID)})`. IDs are already parsed canonical strings; array field order is fixed and the encoding contains no extra whitespace. Event ID is the lookup key. Ignore transport delivery count, Core receipt/recording time and trace headers. Same ID/different tuple delivered to Core logs `device_event.identity_conflict`; it does not overwrite or create a second event row. JetStream may suppress a same-MsgId copy before Core can compare its contents, so this is not a guarantee that every conflicting publication is logged. Deduplication protects report identity, not upstream duplicates that an Adapter incorrectly labels with two new IDs.

### Record shape and retention

| Column | Meaning |
|---|---|
| `receive_order INTEGER PRIMARY KEY AUTOINCREMENT` | Tie-free first-seen Core recording order, independent of Observation order. |
| `event_id TEXT UNIQUE NOT NULL` | Immutable `evt_` report identity. |
| `adapter_id`, `runtime_id`, `entity_id`, `correlation_id` | All `TEXT NOT NULL`, with no FKs. Retain the reported canonical runtime ID even when it has no Core row; do not replace it with NULL. Private except Entity/event IDs. |
| `name`, `fingerprint` | Bounded reported name and immutable-input fingerprint. |
| `disposition`, `rejection_code` | `accepted` with no rejection, or `rejected` with exactly one enumerated code. |
| `emitted_at` | SDK envelope time. Diagnostic, not ordering authority. |
| `received_at` | JetStream server storage time, not consumer callback time. |
| `recorded_at` | Core time of the first SQLite record, retained across redelivery. |

All name/disposition/timestamp columns are NOT NULL; `fingerprint` is a NOT NULL BLOB with a 32-byte length CHECK. `rejection_code` alone is nullable: CHECK accepted implies NULL, and rejected implies exactly one of the five enumerated codes. Add basic ID prefix/length and slug/name checks; wire/service parsers enforce full canonical UUIDv7 syntax. Use the existing fixed-width sortable UTC encoding; index `(entity_id,receive_order DESC)` for history and `(recorded_at,receive_order)` for pruning. Public history is newest-first by receive order, not an assertion of physical or cross-stream chronology.

**Fixed SQLite retention: 30 days from `recorded_at`.** That is comfortably longer than the seven-day stream window and keeps newly recovered history available for a full window after recording. There is no current-State anchor for an event. Add `DeviceEventHistoryRetention` as an internal constant, not a new Core config option.

Extend the existing hourly maintenance worker with one call to `Service.DeleteExpiredDeviceEvents`. That service method owns the batch loop: delete eligible events in batches of 500, one transaction per batch, until fewer than 500 were deleted or its context ends. Derive one cutoff per sweep, delete strictly older records, release the connection between batches, and never prune events at startup. This adds no independent timer/controller and does not change Observation retention semantics.

History/deduplication are bounded, not indefinite. Normal retained broker redelivery remains inside the longer SQLite window. Deleting the database, clock anomalies that defeat retention assumptions, or manually republishing archived envelopes outside the window are not an exactly-once guarantee. Event history is not executable work, even when deliberately re-read.

## 7. Concrete Core interfaces

Add event identity/name types to `internal/modules/devices/model.go` and canonical ID helpers in `ids.go`:

```diff
 type ObservationID string
+type DeviceEventID string
+type DeviceEventName string
 type CommandID string
```

Catalog extension in `internal/modules/devices/catalog.go`:

```diff
 type EntityTypeDefinition struct {
     id               EntityTypeID
     stateless        bool
+    eventNames       func(EntitySupport) ([]DeviceEventName, error)
```

`TypeCatalog.SupportsDeviceEvent(entity Entity, name DeviceEventName) (bool, error)` returns false/nil for a type without event support or an unsupported name; unknown persisted types/corrupt descriptors return an error. The generated selector validates/decodes support and returns an owned name slice. This keeps unsupported input distinct from infrastructure/catalog corruption.

New `internal/modules/devices/device_events.go` defines:

```go
type DeviceEvent struct {
    ID            DeviceEventID
    EntityID      EntityID
    Name          DeviceEventName
    CorrelationID CorrelationID
    EmittedAt     time.Time
}

type RecordDeviceEventParams struct {
    AdapterID  string
    RuntimeID  RuntimeID
    Event      DeviceEvent
    ReceivedAt time.Time // JetStream metadata timestamp
    Now        func() time.Time // called by persistence for first recording
}

type DeviceEventDisposition string // accepted | rejected (persisted only)
type DeviceEventRejection string   // stale_runtime | unknown_entity | wrong_adapter | entity_disabled | unsupported_event
type DeviceEventRecordOutcome string // accepted | rejected | duplicate | identity_conflict

type DeviceEventHistoryEntry struct {
    EventID      DeviceEventID
    EntityID     EntityID
    Name         DeviceEventName
    Disposition  DeviceEventDisposition
    Rejection    *DeviceEventRejection
    EmittedAt    time.Time
    ReceivedAt   time.Time
    RecordedAt   time.Time
    ReceiveOrder int64 // internal/cursor position only
}

type DeviceEventRecordResult struct {
    Outcome   DeviceEventRecordOutcome
    Rejection *DeviceEventRejection // only for a newly rejected event
}

type ListEntityDeviceEventsParams struct {
    EntityID          EntityID
    BeforeReceiveOrder *int64
    Limit             int
}

func (s *Service) RecordDeviceEvent(context.Context, string, RuntimeID, DeviceEvent, time.Time) (DeviceEventRecordResult, error)
func (s *Service) ListEntityDeviceEvents(context.Context, ListEntityDeviceEventsParams) (Page[DeviceEventHistoryEntry], error)
func (s *Service) DeleteExpiredDeviceEvents(context.Context, time.Time) error // argument is Core sweep time
```

Use named constants for the closed outcome/disposition/rejection vocabularies. The service supplies `Dependencies.Now` to record persistence; no transport-supplied `recorded_at`. The SQLite repository computes the fingerprint from the validated tuple, not an unchecked client hash. Zero/invalid trusted-method arguments produce errors before writing; the transport filters permanent wire errors before calling this seam.

Add the persistence capability to the existing `Stores`, implemented by the existing SQLite repository and existing devices sqlc package:

```diff
 type Stores struct {
     // existing fields unchanged
     Observations ObservationRepository
+    DeviceEvents DeviceEventRepository
 }
```

```go
type DeviceEventRepository interface {
    RecordDeviceEvent(context.Context, RecordDeviceEventParams) (DeviceEventRecordResult, error)
    ListEntityDeviceEvents(context.Context, ListEntityDeviceEventsParams) (Page[DeviceEventHistoryEntry], error)
    DeleteDeviceEventsBefore(context.Context, time.Time, int) (int64, error) // cutoff, batch size
}
```

Consumer-owned `DeviceEventRecorder` exposes only the Service's `RecordDeviceEvent` signature. HTTP calls the Service, which validates parent/pagination before reading. No new automation interface, generic publisher/subscriber interface or outward notification is part of this feature.

## 8. Minimal history API

**`GET /v1/entities/{entity_id}/events`**, operation ID `list-entity-device-events`:

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

- Include accepted and rejected records, with no disposition filter in v1. No global endpoint or per-event detail endpoint is needed for this slice.
- Existing Entity with no events (including a non-event type): 200 with `items:[]`. Unknown parent: 404. Rejection rows for unknown Entity IDs remain internal diagnostics, consistent with Observation history; do not introduce a global browse endpoint to expose them.
- Use existing paging conventions: default 50, allowed 1–200, query parameters `limit` and `cursor`, `limit+1` internally, no totals, omit next cursor on the last page.
- Cursor is the existing unsigned versioned base64url JSON pattern, bound to resource `entity-device-events`, Entity ID, and exclusive lower `receive_order`; no filter field. State-history cursors are rejected here and event-history cursors rejected there. First/after-page SQL queries use the same index. No snapshot guarantee; newer input requires restarting pagination, and pruning can empty a continuation.
- Explicit Huma models map UTC timestamps and rejection optionality. Never expose Adapter/runtime/correlation IDs, fingerprint, internal receive order or raw envelopes. Domain/schema/page errors follow existing 400/422 conventions; unexpected errors=500.

This endpoint provides direct household debugging value before any Automation exists: “Was the report recorded, and if not accepted, why?” A missing row alone cannot prove no physical press occurred or distinguish all transport losses; wire-invalid/lost/expired-broker input can be absent.

## 9. Assembly, simulator and implementation layout

Core provisions both streams before starting transports, starts both durable consumers, and includes both resource configurations and consumer activity in existing readiness. Do not require backlog exhaustion for ready status. Adding the consumer extends the existing dependency check; it does not create a generation gate, change the health supervisor's policy, or gate ingestion on its own readiness.

Keep Observation/health dependencies alive for current Commands during drain. Drain/stop the event consumer before closing its DB/NATS dependencies, on normal shutdown and startup-error exits. Unprocessed/unacknowledged events remain durable for the next Core process. No startup event replay into Commands and no callback waits for a Command.

Simulator scenario `device-events` registers an `events` Entity alongside its existing `power` Entity, preserving existing scenarios/Binding keys. Use generated registration/event builders. Expose deterministic `EmitDeviceEvent(ctx,name)` for tests; local stdin lines `single_press`/`double_press` generate explicit synthetic reports while the Session remains connected. A cancellable reader and bounded, non-queued handoff prevent a blocked publisher from accumulating unlimited reports; log/drop excess local input rather than adding an outbox. EOF stops only input, shutdown joins workers. No privileged Core HTTP injection endpoint or physical-device validation is claimed.

| ID | Outcome | Effort | Depends on | Acceptance |
|---|---|---|---|---|
| D1 | Schema-backed event-source Entity and wire/SDK data contract | L | — | A1–A2 |
| D2 | SDK PubAck publication, dedicated consumer and transactional SQLite event history | L | D1 | A3–A6 |
| D3 | Per-Entity history API and fixed retention maintenance | M | D2 | A7–A8 |
| D4 | Simulator Core-offline recovery proof and operator documentation | L | D2,D3 | A9–A10 |

```text
contracts/v1/
├── device-event.schema.json             # new — durable report schema [D1]
├── common.schema.json / embed.go        # modify — evt_ ID/schema registration [D1]
└── registration-request.schema.json     # modify — optional event support [D1]
entitytypes/
├── entitytype-manifest.schema.json      # modify — event_source flag [D1]
└── enumeventv1/                         # new — schemas/manifest/examples/generated type [D1]
sdk/adapter/
├── device_events.go                     # new — public data types [D1], PubAck/retry publication [D2]
└── enumeventv1/                         # generated — typed descriptor/event builders [D1]
internal/
├── cmd/entitytypegen/                   # modify — event-source generation and fixtures [D1]
├── contracts/v1/natswire/subjects.go     # modify — runtime-scoped event subject helpers [D1]
├── modules/devices/
│   ├── model.go / ids.go / catalog.go   # modify — event IDs/name selector [D1]
│   ├── zz_generated_entitytypes*.go     # generated — event catalog/conformance [D1]
│   ├── device_events.go                 # new — domain recording/retention [D2]
│   ├── device_event_history.go          # new — parent/paging reads [D3]
│   ├── service.go / repository.go       # modify — event persistence capability [D2]
│   ├── sqlite_repository.go             # modify — include event capability in SQLiteStores [D2]
│   ├── sqlite_device_events.go          # new — atomic disposition, dedup, history/prune [D2,D3]
│   ├── dbqueries/device_events.sql      # new — insert/dedup/history/retention SQL [D2,D3]
│   ├── dbsqlc/                          # generated — existing devices SQL package [D2,D3]
│   ├── nats/device_event.go             # new — DeviceEventConsumer and DTO mapping [D2]
│   ├── nats/device_event_resources.go   # new — provision/validate dedicated stream/consumer [D2]
│   └── api/device_events.go             # new — history endpoint/DTOs/cursor [D3]
├── platform/db/migrations/00001_initial.sql # modify — device_events table/indexes [D2]
├── app/hearthd/run.go / server.go       # modify — second consumer/readiness/drain/maintenance [D2,D3]
├── app/hearthd/device_events_integration_test.go # new — full SDK/NATS/DB/HTTP proof [D4]
└── adapters/simulator/ / app/simulator/ # modify — named event scenario [D4]
README.md / configs/ / CONTEXT.md / docs/{architecture,logging}.md
                                       # modify during implementation — guarantees and usage [D4]
```

Update explicit descriptor DTOs/route registration interfaces where required; no unrelated module rearrangement. Tests live with their behavior and share deliverable ownership. D1 includes SDK types needed by its generated builders so it compiles without D2 publication. Use the existing devices sqlc stanza and generation-check task: no new SQL package/configuration framework. Modify the initial migration and recreate development databases under Hearth's no-deployments policy; no legacy migration path. Keep shared migration/generation writers serialized.

## 10. Acceptance tests and review gates

- **A1 — Generated contracts:** new support round-trips SDK → registration wire → Core → Entity read. Closed existing types still reject event support; zero-Operation event type generates cleanly and validates supported/unsupported names. No event Observation/Command facade and no State/Command side effects.
- **A2 — Wire:** authoritative schema/route/ID/time/MsgId checks, unexpected causation, oversized input and subject/Entity mismatch behave as specified. Neither raw rejected envelopes nor unsafe names are logged. Generated/embedded contract tests cover the new schema.
- **A3 — Durable SDK:** a lost PubAck causes same-ID/same-payload retry, not a second report; caller/Session cancellation ends waits. PubAck is distinguishable from Core recording/acceptance. Existing Session, Observation and Command-evidence behavior is unchanged.
- **A4 — SQLite identity/disposition:** same name/different IDs creates two rows; duplicate ID creates none and never changes first timestamps/disposition; changed immutable input conflicts without overwriting. Runtime/unknown Entity/wrong owner/disabled/unsupported precedence persists rejection evidence. Health/availability alone do not reject. Corrupt stored descriptors and DB failures are not acknowledged as successful recording.
- **A5 — Commit-before-ack:** inject failure before commit and prove redelivery with no partial row; inject response/ack loss after commit and prove one row after restart/redelivery. A permanently wire-invalid event is acknowledged so it cannot block valid later input.
- **A6 — Backlog semantics:** old but valid input is recorded without a freshness rejection; runtime takeover or narrowed support during downtime yields the specified rejection, not silent loss or invented historical acceptance. Same identity stays first-seen after metadata changes. Event input never appears in State history or satisfies a Command.
- **A7 — HTTP history:** all dispositions, correct parent errors/empty collections, exclusive keyset continuation, endpoint/Entity cursor scope, limits, timestamp fields and private-field exclusion. History ordering follows receive order even when wall clocks tie or disagree. Use real SQLite and the Huma route, not only mocked repository rows.
- **A8 — Retention:** fixed 30 days from first `recorded_at`, exact cutoff boundary, batched removal, no startup prune, no State anchor, no alteration of Observation pruning. Duplicate evidence remains for ordinary retained stream redelivery, and pruning does not execute anything. Verify history/prune indexes with representative data.
- **A9 — Core-offline vertical slice:** initialize Core/stream and a registered SDK simulator, stop only Core while NATS stays up, obtain PubAck for multiple named events, restart Core and read each exactly once through HTTP. Cover transient heartbeat failures without assuming a fixed missed-heartbeat shutdown limit. Also test consumer lifecycle/readiness and preservation of unprocessed input on drain.
- **A10 — Delivery:** documented local simulator/history recipe works without automations or physical hardware; `mise run validate` passes; generation remains reproducible. Only the devices module is required. No automation type, table, transport hook or executable replay path is introduced.

Use real SQLite/embedded NATS for durability and transaction claims, injected clocks for retention, and synchronization barriers for acknowledgement/drain races. SDK failure injection can prove identity stability without waiting for real outages. Existing dependencies suffice; no new test framework is needed.

## 11. Trade-offs and automation follow-up

- **Dedicated durable Device Event stream rather than live request/reply:** solves Core-offline history, but retains/re-delivers old input. That is intentional and separate from future live-only Triggers.
- **One stored report with disposition rather than accepted-only history:** explains disabled, unsupported and stale-runtime reports without reconstructing the past or hiding them as transport drops.
- **Fixed bounded retention and one read endpoint rather than a history product:** enough for debugging; archive/search/UI/configuration can follow actual demand.
- **Existing generated event type rather than raw Adapter payloads:** preserves canonical ownership and discoverability; real Zigbee2MQTT event mapping remains separate work and must distinguish actual events from retained/cache-expanded/repeated upstream values.

Before implementing automations, revise its deferred spec to consume Core-accepted Device Event facts, define the fresh-trigger boundary independently from this history consumer, and decide how Run admission relates to recorded events. Do not reuse that draft's one-shot request/response schema, no-retry SDK method, two-second history expiry, or automation-owned ephemeral event receipts. This feature deliberately stops short of choosing the downstream execution seam.
