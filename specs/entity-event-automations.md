# Entity Event automations

**Status:** Deferred design; the Device Facts foundation defined by `specs/device-facts.md` is implemented, and this spec now names its exact Entity Event fact DTO, subject, schema, stream and lifecycle seams plus the automations consumer's own durability policy. Automations remain unimplemented and this spec is not implementation-ready until the completeness review passes.
**Baseline:** `d9760b8`. Do not restore the automation module removed in `423addb` wholesale.
**Effort:** XL after the Device Facts foundation, across four remaining deliverables.

## 1. Purpose and scope

Build one useful automation path on Core-verified facts:

```text
Adapter Entity Event
        ↓
implemented durable Entity Event ingestion and history (devices)
        ↓ accepted Device Fact queued in the devices transaction
        ↓ relay publishes to HEARTH_DEVICE_FACTS_V1 (JetStream)
exact Trigger match → atomically record Run or skip
                                      ↓
                         existing Entity Commands
```

This spec owns:

- strict HTTP/SQLite Automation definitions;
- Entity Event Triggers and manual invocation;
- exact Trigger matching and bounded Run admission;
- ordered Steps executed through existing Entity Commands;
- immutable Run snapshots, recorded skips and truthful execution history;
- automation durable fact consumer, subscription, lifecycle and application assembly after Device Facts exist.

This spec does **not** own Entity Event identity, registration, wire ingestion, validation, duplicate detection, history or retention. Those are implemented by `devices` and specified in [Entity Events](entity-events.md).

**Non-goals:** State or Observation Triggers, Command-outcome Triggers, timers or schedules, conditions, branching, retries, compensation, cancellation, replay, history-scan catch-up, deletion, configurable history retention, manual idempotency keys, definition-count quotas, and a privileged event-injection endpoint.

Physical Entity Event mappings are separately owned by Adapters. The simulator and Zigbee2MQTT already publish Entity Events; this slice only consumes accepted facts about them.

## 2. Implemented Entity Event foundation

The following foundation is already present and must be reused rather than recreated:

- `hearth.enumevent/v1` is a stateless, non-commandable Entity type whose support lists 1–64 named events.
- `sdk/adapter.Session.PublishEntityEvent` publishes one stable `evt_` identity through JetStream and retries the same encoded report until PubAck, caller cancellation or termination.
- `HEARTH_ENTITY_EVENTS_V1` durably retains Adapter reports for the devices consumer.
- `devices.Service.RecordEntityEvent` records each first-seen wire-valid report and its accepted or rejected disposition in one devices-owned transaction.
- The `entity_events` table owns report identity, fingerprint conflict detection, processing-time validation evidence and fixed 30-day history retention.
- `GET /v1/entities/{entity_id}/events` exposes accepted and rejected per-Entity history.
- `received_at` is JetStream storage time; `recorded_at` is Core's first-record time. Event age is not an ingestion rejection rule.
- Duplicate and changed-input handling are devices concerns. Automations add no parallel event receipt, fingerprint, expiry or cleanup table.

An accepted Entity Event changes no State or Command. It only establishes a durable Core fact that the report passed runtime, Entity ownership, enablement and supported-name checks at processing time.

## 3. Implemented Device Facts foundation

`specs/device-facts.md` defines and has landed a shared **Device Fact** publishing seam.

The foundation owns all cross-module and external delivery decisions, including:

- versioned, strict contracts for accepted Observations and accepted Entity Events;
- an external Core-originated NATS subject namespace distinct from Adapter input subjects;
- a transactional outbox row written in the same devices-owned SQLite transaction that establishes the fact, so a fact and its evidence commit atomically and an outbox failure rolls the evidence back;
- one JetStream stream, `HEARTH_DEVICE_FACTS_V1`, retaining facts for seven days or one GiB (DiscardOld), with no Core-owned consumer and no subject transform;
- a single relay that publishes pending facts oldest-first, waits for a broker `PubAck` naming that stream and deletes the outbox row only after the acknowledgement;
- at-least-once publication from the outbox with broker deduplication bounded by the stream's two-hour duplicate window, so a retry outside the window can store the same fact twice;
- subject/payload agreement, canonical IDs, timestamps, correlation, restored trace propagation and safe logging;
- exact ordering, duplicate, poison, eviction, startup, shutdown and publish-failure semantics;
- the rule that durable HTTP/SQLite records remain authoritative and a fact reports only what Core recorded;
- application wiring and documentation for trusted external subscribers and durable consumers;
- transactional enqueue hooks for both agreed fact families, even though this automation slice consumes only accepted Entity Event facts.

The implemented `devices.EntityEventFact` DTO, queued by the devices transaction and published from `HEARTH_DEVICE_FACTS_V1`, gives automations:

```go
type EntityEventFact struct {
    ID            devices.DeviceFactID // stable fct_ identity, also the published Nats-Msg-Id
    EventID       devices.EntityEventID
    EntityID      devices.EntityID
    Name          devices.EntityEventName
    CorrelationID devices.CorrelationID
    ReportedAt    time.Time // SDK emitted_at
    ReceivedAt    time.Time // JetStream storage time
    RecordedAt    time.Time // Core first-record time
    CreatedAt     time.Time // Core commit time, published as emitted_at
    Trace         devices.DeviceFactTraceContext
}
```

Its strict wire schema is `urn:hearth:schema:entity-event-fact:v1`, whose `data` object is exactly `{event_id, entity_id, name, reported_at, received_at, recorded_at}`; `correlation_id` stays in the envelope, `id` is the stable `fct_` fact identity, and `emitted_at` is Core's commit time stored at enqueue rather than publication time.

The Entity Event fact contract guarantees:

1. only a first-seen `devices.EntityEventOutcomeAccepted` transition queues a fact;
2. rejected events, duplicates and identity conflicts queue no fact;
3. the fact and the recorded event commit in one devices transaction, so an identity-mint or insert failure rolls the event back and inbound redelivery retries both together;
4. a broker outage, a relay restart or a Core restart delays publication but never discards a queued fact, and a retry reuses the same identity, subject and payload bytes;
5. a fact stored in `HEARTH_DEVICE_FACTS_V1` stays readable until the stream's seven-day or one-GiB bound evicts it, so an automation with a durable consumer can recover a fact published while it was down;
6. a retry outside the two-hour duplicate window can store the same fact twice, so automations must stay idempotent;
7. the publisher never turns a devices ingestion failure into an accepted fact;
8. Core provisions the stream and no consumer, and no automation reads Entity Event history to discover work.

These guarantees make missed automatic execution an explicit consumer-policy choice. An automation that owns a durable consumer and acknowledges only after its admission transaction commits does not miss a broker-stored fact; an automation that keeps a live-only subscription can. Either way a historical Entity Event without a corresponding Automation Run is truthful.

This file now names the implemented Entity Event fact contract exactly: the durable DTO is `devices.EntityEventFact`, the Core JetStream stream is `devicesnats.DeviceFactStreamName` (`HEARTH_DEVICE_FACTS_V1`), the Core subject wildcard is `hearth.v1.core.fact.entity.*.entity-event.>` and the strict wire schema is `urn:hearth:schema:entity-event-fact:v1`. No automation implementation should invent those details independently.

## 4. Ownership and module seams

- `devices` owns Entity identity, Entity Event ingestion and history, Command creation and outcomes, and the transactional Device Fact enqueue that writes the durable outbox row.
- `automations` owns definitions, its durable subscription to accepted Entity Event facts, matching, Runs, skips, Steps and execution history.
- `contracts/v1` and `internal/contracts/v1/natswire` own the language-neutral Device Fact schemas and subject mechanics defined by the Device Facts spec.
- `internal/modules/automations/nats` owns schema-validated mapping from the external fact contract into the automation domain.
- `internal/app/hearthd` assembles both modules, the shared SQLite database and NATS connection, and owns startup/readiness/drain ordering.

The dependency direction remains acyclic:

```text
contracts/v1 ← natswire ← devices/nats ← hearthd → automations/nats → automations
                               ↑                         ↓
                            devices ← AutomationDevices ┘
```

`devices` never imports `automations`, and the only runtime handoff between the two modules is the fact stream: the automations consumer reads only accepted facts from `HEARTH_DEVICE_FACTS_V1`, never Adapter-originated Entity Event subjects, and never reads Entity Event history to discover work. Neither module reads the other's tables.

No SQLite transaction may call another module, publish NATS, or wait for a Command. Automation admission is one automation-owned transaction over an already verified fact, and the broker, not a transaction, hands the fact to it. Command execution begins only after that transaction commits.

## 5. Definitions and save-time validation

Definition JSON is strict, defaults `enabled:false`, and permits 0–32 Triggers and 1–32 ordered Steps. Zero Triggers means manual-only. Trigger and Step IDs are unique slugs within their arrays. Name length is 1–200 and the total body is at most 64 KiB. Replacement remains revision-checked.

```json
{
  "name":"Button turns on light",
  "enabled":true,
  "triggers":[
    {"id":"press","kind":"entity_event","entity_id":"ent_<button>","event_name":"single_press"}
  ],
  "steps":[
    {"id":"light_on","entity_id":"ent_<power>","operation":"set","parameters":{"value":true}}
  ]
}
```

At create and replacement:

- validate each Trigger's Entity exists and currently supports its event name;
- do not require the source Entity to be enabled, available or owned by a healthy Adapter at save time;
- validate every Step through `devices.Service.ValidateCommand` and persist its normalized parameters;
- allow currently disabled or unavailable targets to be saved;
- reject unknown fields, duplicate IDs, unsupported Trigger names and invalid Step parameters atomically.

Add one read-only devices seam because the implemented ingestion method is deliberately not a save-time validator:

```go
func (service *Service) ValidateEntityEventTrigger(
    context.Context,
    EntityID,
    EntityEventName,
) error
```

It reads the current Entity and calls the existing catalog support selector. It checks existence and supported name only and performs no write.

Edits and disablement affect future admission. Existing Run and skip history retains the definition revision and matching explanation that existed when recorded.

## 6. Fact subscription and automatic admission

`internal/modules/automations/nats` creates or opens exactly one named durable JetStream consumer on `devicesnats.DeviceFactStreamName` (`HEARTH_DEVICE_FACTS_V1`) filtered to the accepted Entity Event fact wildcard `hearth.v1.core.fact.entity.*.entity-event.>`, and validates each message against `urn:hearth:schema:entity-event-fact:v1`. It does not create one consumer or subscription per Trigger: matching belongs to the automation transaction, so definition edits require no broker churn.

The automations consumer **must choose its recovery policy explicitly**, and the recommended policy is durable resume, because an automation that should not miss a Trigger cannot rely on a live connection:

1. it creates its own named durable consumer (for example name and durable `hearthd-automation-entity-event-facts-v1`) with `DeliverPolicy: jetstream.DeliverAllPolicy`, `AckPolicy: jetstream.AckExplicitPolicy` and the Entity Event filter subject, so the broker tracks its acknowledgement floor and the consumer resumes there after a restart;
2. it reports active only after the subscription is live, and Core's Device Fact stream provisioning creates no consumer for it;
3. it bounds its own in-flight work; the durable stream, not a private recovery queue, is the only recovery surface, and a fact older than the stream's seven-day or one-GiB bound can be evicted and then never redelivered;
4. it validates the strict `urn:hearth:schema:entity-event-fact:v1` schema, `entity-event` subject shape, canonical `fct_`/`evt_`/`ent_` identities, `Nats-Msg-Id` equal to the envelope `id`, and subject/payload agreement;
5. it maps one accepted `devices.EntityEventFact` to the automation-owned `automations.EntityEventFact`;
6. it calls `AutomationService.ReceiveEntityEventFact` synchronously for short admission work only;
7. it acknowledges a fact only after the admission transaction commits, so a crash before commit leaves the fact unacknowledged and redelivered; a fact matching no Automation commits nothing and is acknowledged; a transient admission failure is negatively acknowledged so the fact is redelivered;
8. it terminates a fact that can never be valid (schema, identity or subject/payload failure) as a permanent error, logs it once and never lets one malformed fact block the consumer;
9. it never executes Commands in the consumer callback.

Redelivery is expected and benign: admission is idempotent on the durable source event, so the unique `(event_id, automation_id)` constraint below makes a redelivered or duplicated fact produce at most one outcome per Automation. A consumer that instead chooses a live-only subscription accepts that facts published while it was absent are missed; that is a legitimate choice only for advisory consumers, never for the recommended automation path.

The automation-owned domain type mapped from the validated `devices.EntityEventFact` fact is:

```go
type EntityEventFact struct {
    EventID    devices.EntityEventID
    EntityID   devices.EntityID
    Name       devices.EntityEventName
    ReportedAt time.Time // SDK emitted_at
    ReceivedAt time.Time
    RecordedAt time.Time
}

type EntityEventFactReceiver interface {
    ReceiveEntityEventFact(context.Context, EntityEventFact) (AdmissionOutcome, error)
}

type AdmissionOutcome struct {
    MatchedAutomations int
    StartedRuns        int
    RecordedSkips      int
}
```

In one automation-owned transaction, `ReceiveEntityEventFact`:

1. reads current enabled definitions matching exact `(entity_id,event_name)`;
2. groups all matching Trigger IDs per Automation;
3. visits matching Automations by ID for deterministic capacity decisions;
4. records one Run or one skip per matching Automation;
5. commits each Run snapshot, event summary, matching Trigger IDs and initial Step rows atomically;
6. registers workers only after commit.

The same Event may match several Automations. Several matching Triggers in one Automation produce one outcome carrying all matching Trigger IDs.

Only one Run may be active per Automation. A second match records an `automation_busy` skip. At most 16 Runs may be active globally; excess matches record `run_capacity` skips. Skips never queue.

Use a unique `(event_id, automation_id)` constraint on event-backed history as defense against accidental duplicate fact delivery or duplicate local subscription. A conflicting insert reads and returns the existing outcome without scheduling another worker. Unmatched facts write nothing and need no receipt.

No timestamp freshness or expiry rule exists in automations. Whether a fact is recoverable is the automations consumer's own durability choice, not a property of the payload, and the recommendation above is durable resume. Automations must not query Entity Event history on startup, reconnect, definition edits or consumer recovery: recovery is the broker redelivering unacknowledged facts, never a history scan.

Manual invocation uses the same busy and global-capacity rules but ignores definition enablement. It has no Event or matched Trigger IDs. Every successful POST creates a new Run; manual idempotency keys remain deferred. Clients must not automatically retry an ambiguous manual request.

## 7. Execution and truthful history

Reuse `devices.Service.ExecuteCommand` without an automation bypass:

- Before each Step, persist `running`, start time and freshly reserved Command/correlation IDs.
- Generate IDs through `devices.NewCommandID` and `devices.NewCorrelationID`, with constructors injected for deterministic tests.
- Call devices outside the automation transaction using a process-owned context detached from the HTTP or NATS caller.
- Wait for the existing Operation outcome before starting the next Step.
- `satisfied` and `dispatched` are distinct successful Step statuses; all Steps must succeed for the Run to succeed.
- Existing matching State is never evidence of a new Command's success.
- Stop after the first known failure. Later Steps remain `not_attempted`; do not retry, compensate or roll back earlier physical effects.

Known precreation errors (`ErrInvalidCommand`, `ErrEntityNotFound`, `ErrCommandIDConflict`) become Step failures `invalid_command`, `entity_not_found` and `command_id_conflict` and acquire no public Command link.

`CommandExecutionError` proves a Command was created, not its eventual outcome. Read the owned Command and verify reserved Command ID, correlation ID, Entity and Operation before exposing a link or adopting its durable status. Never expose a merely reserved identity.

A bare `devices.ErrCommandUnavailable` during drain maps to `interrupted/core_stopping` with no Command link. Outside drain it is an executor fault because the Automation admitted work while Command admission was unexpectedly closed.

Startup recovery is not execution replay:

- preserve terminal Steps;
- mark still-running Steps and Runs `interrupted/core_restarted`;
- leave untouched later Steps `not_attempted`;
- never infer Run success from later Command history;
- never redispatch or resume Steps.

History may show an interrupted Step whose verified Command later satisfied. That is truthful: the Run was interrupted while the separately owned Command reached a terminal outcome.

## 8. Domain model, storage and API

New domain types in `internal/modules/automations/automation_model.go`:

```go
type AutomationID string     // aut_<UUIDv7>
type AutomationRunID string  // arn_<UUIDv7>; run_ identifies Adapter runtimes
type AutomationSkipID string // ask_<UUIDv7>

type EntityEventTrigger struct {
    ID        string
    Kind      string // entity_event
    EntityID  devices.EntityID
    EventName devices.EntityEventName
}

type AutomationStep struct {
    ID            string
    EntityID      devices.EntityID
    Operation     devices.OperationName
    Parameters    devices.CommandParameters
}

type AutomationDefinition struct {
    Name     string
    Enabled  bool
    Triggers []EntityEventTrigger
    Steps    []AutomationStep
}

type AutomationRecord struct {
    ID         AutomationID
    Revision   int64
    Definition AutomationDefinition
    CreatedAt  time.Time
    UpdatedAt  time.Time
}

type AutomationEventSummary struct {
    ID         devices.EntityEventID
    EntityID   devices.EntityID
    Name       devices.EntityEventName
    ReceivedAt time.Time // JetStream storage time
}

type AutomationStepAttempt struct {
    Position              int
    StepID                string
    Status                string // not_attempted | running | satisfied | dispatched | failed | interrupted
    ReservedCommandID     *devices.CommandID
    ReservedCorrelationID *devices.CorrelationID
    FailureCode           *string
    StartedAt             *time.Time
    CompletedAt           *time.Time
}

type AutomationRun struct {
    ID                AutomationRunID
    AutomationID      AutomationID
    Revision          int64
    Snapshot          AutomationDefinition
    Event             *AutomationEventSummary // nil for manual
    MatchedTriggerIDs []string                 // empty for manual
    Status            string                   // running | succeeded | failed | interrupted
    FailureCode       *string
    StartedAt         time.Time
    CompletedAt       *time.Time
    Steps             []AutomationStepAttempt
}

type AutomationSkip struct {
    ID                AutomationSkipID
    AutomationID      AutomationID
    Revision          int64
    Event             AutomationEventSummary
    MatchedTriggerIDs []string
    Reason            string // automation_busy | run_capacity
    SkippedAt         time.Time
}
```

A skip is not a Run. Store both alternatives in one feature-owned history table so list and detail ordering remain uniform.

Three automation-owned tables are added to `00001_initial.sql`:

| Table | Required invariants |
|---|---|
| `automations` | ID PK, revision starts at 1, normalized strict definition JSON, created/updated times; replacement is revision-checked atomically. |
| `automation_history` | ID PK (`arn_` for Run, `ask_` for skip), Automation FK, kind, revision, recorded time, nullable event summary, matching IDs; Run-only snapshot/status/failure/completion and skip-only reason; partial unique Automation ID for running Runs; unique `(event_id,automation_id)` for event-backed outcomes. |
| `automation_run_steps` | `(run_id,position)` PK, history FK, Step ID/status/failure/times and nullable reserved Command/correlation pair; repository validation proves the parent is a Run. |

There is no `automation_event_receipts` table and no FK from automation history to `entity_events`: Event history has fixed retention and may be pruned while Automation history remains. The copied event summary is immutable explanation, not an executable record.

Run/skip history is retained indefinitely in this slice, like Command history. SQLite growth is an explicit limitation until Automation retention is designed.

Service seam:

```go
type AutomationDevices interface {
    ValidateEntityEventTrigger(context.Context, devices.EntityID, devices.EntityEventName) error
    ValidateCommand(context.Context, devices.CommandInput) (devices.CommandParameters, error)
    ExecuteCommand(context.Context, devices.CommandInput) (devices.CommandResult, error)
    GetCommand(context.Context, devices.CommandID) (devices.CommandRecord, error)
}

func NewAutomationService(*SQLiteAutomationRepository, AutomationDevices, AutomationDependencies) *AutomationService
func (service *AutomationService) CreateAutomation(context.Context, AutomationDefinition) (AutomationRecord, error)
func (service *AutomationService) ReplaceAutomation(context.Context, AutomationID, int64, AutomationDefinition) (AutomationRecord, error)
func (service *AutomationService) StartManualRun(context.Context, AutomationID) (AutomationRun, error)
func (service *AutomationService) ReceiveEntityEventFact(context.Context, EntityEventFact) (AdmissionOutcome, error)
func (service *AutomationService) StopAdmission()
func (service *AutomationService) AdmissionOpen() bool
func (service *AutomationService) WaitRuns(context.Context) error
func (service *AutomationService) InterruptActiveRuns(context.Context, time.Time) error
```

HTTP routes use module-owned Huma DTOs and RFC 9457 errors:

| Route | Result |
|---|---|
| `POST /v1/automations` | Definition → 201 record and Location. |
| `GET /v1/automations` | ID-ascending list. |
| `GET /v1/automations/{id}` | Definition, revision and timestamps. |
| `PUT /v1/automations/{id}` | `{expected_revision,definition}` → replacement; stale revision is 409. |
| `POST /v1/automations/{id}/runs` | Empty body → 202 Run summary and history Location; busy is 409, capacity or closed admission is 503. |
| `GET /v1/automations/{id}/history` | Newest-first Run/skip summaries. |
| `GET /v1/automations/{id}/history/{entry_id}` | Run snapshot and ordered Steps with verified Command links, or skip detail; parent mismatch is 404. |

List responses omit full snapshots and Steps. Collections use existing keyset conventions: default 50, limits 1–200, no totals, history key `(recorded_at,id)`, endpoint- and parent-scoped opaque cursors, and no snapshot guarantee.

## 9. Lifecycle

Preserve the Device Facts foundation's startup ordering, including interruption of active Commands into durable history before the shared NATS connection attempt. Insert automation work at these points:

1. open/migrate SQLite and interrupt active Commands into durable history as required by Device Facts;
2. interrupt active Automation Runs before the shared NATS connection attempt;
3. connect the one shared Core NATS connection, provision and validate `HEARTH_DEVICE_FACTS_V1`, and start the `devicesnats.DeviceFactRelay` over the durable outbox;
4. construct the devices and automation services;
5. provision the remaining JetStream resources and start request/reply transports;
6. create and start the automations durable Entity Event fact consumer and report active once it is consuming;
7. start durable Observation and Entity Event consumers;
8. expose HTTP and readiness.

Starting the automations consumer before the durable Observation and Entity Event consumers means the broker holds its position while fresh facts are produced during startup, and a fact published after the consumer exists is retained by the stream whether or not the callback has started. A consumer created later still receives earlier facts, because its `DeliverAll` policy resumes from its stored acknowledgement floor. An external reader owns its own consumer and can miss a fact only in the ways §11 and §12 of the Device Facts spec describe.

Readiness requires the automations fact consumer to be active and automation admission to be open, in addition to the device requirements: the shared NATS connection connected, the Device Fact stream validated, the relay active, and the Observation and Entity Event resources validated with both consumers active. It never waits for Entity Event backlog and never waits for a fact consumer Core does not own.

Drain order:

1. stop/drain the automations fact consumer and close new automation admission;
2. close device Command admission;
3. join automation workers;
4. join device Command workers;
5. drain Entity Event and Observation consumers while dependencies remain available and the Device Fact relay still publishes;
6. drain `devicesnats.DeviceFactRelay` within the foundation's five-second deadline, leaving any unpublished rows in the durable outbox for the next Core process;
7. drain transports, the shared NATS connection and SQLite.

A Run admitted before drain may finish its current Command. It starts no later Step after Command admission closes; such a Step becomes `interrupted/core_stopping` without a Command link.

An automation executor fault closes admission until restart. A fact that the stream's retention bound has already evicted, or the absence of an external reader, is not a readiness failure.

## 10. Deliverables

The Device Facts foundation is a separate prerequisite and is not a deliverable of this spec.

| ID | Outcome | Effort | Depends on | Acceptance |
|---|---|---|---|---|
| D1 | Definitions, save-time validation, manual Runs and history API | L | Device Facts foundation implemented | A1–A3 |
| D2 | Sequential Step execution, verified Command links and restart/drain behavior | L | D1 | A4–A5 |
| D3 | Entity Event fact durable consumer and atomic Run/skip admission | L | Device Facts implementation, D1 | A6–A8 |
| D4 | Simulator-to-light proof, operator documentation and full integration validation | L | D2,D3 | A9–A10 |

Owning paths:

```text
internal/modules/devices/
└── entity_events.go                         # modify — save-time ValidateEntityEventTrigger [D1]
internal/modules/automations/
├── automation_model.go                     # new — definitions, Runs, skips and Steps [D1]
├── automation_definition.go                # new — strict validation and normalization [D1]
├── automation_definition.schema.json       # new — stored definition contract [D1]
├── automation_service.go                   # new — management, admission and lifecycle [D1,D3]
├── automation_admission.go                 # new — exact fact matching and Run/skip transaction [D3]
├── automation_execution.go                 # new — ordered Commands [D2]
├── automation_history.go                   # new — summaries, detail and verified links [D1,D2]
├── sqlite_automations.go                   # new — repository and transaction ownership [D1,D3]
├── dbqueries/automations.sql               # new — definitions/history/admission SQL [D1,D3]
├── dbsqlc/                                 # generated — automation SQL package [D1,D3]
├── api/automations.go                      # new — seven routes and DTOs [D1]
└── nats/entity_event_facts.go              # new — durable JetStream consumer and wire mapping [D3]
internal/platform/db/migrations/00001_initial.sql
                                               # modify — three automation tables [D1]
internal/app/hearthd/run.go / server.go         # modify — assembly/readiness/drain [D2,D3]
internal/app/hearthd/entity_event_automation_integration_test.go
                                               # new — whole slice [D4]
sqlc.yaml / mise.toml                          # modify — automation generation [D1]
README.md / CONTEXT.md / docs/{architecture,logging}.md
                                               # modify — semantics and operator usage [D4]
```

Do not list or modify Device Fact contracts, subjects, stream, relay or transactional enqueue hooks here; those belong to `specs/device-facts.md` and must land first.

## 11. Acceptance tests

- **A1 — Definitions:** strict schema limits, save-time event-name and Command validation, parameter normalization, zero-Trigger manual-only definitions and revision conflicts work through real SQLite and HTTP.
- **A2 — History:** Run snapshots and matching Trigger IDs remain immutable after edits; Run and skip alternatives satisfy table and constructor invariants; parent-scoped pagination is newest-first and excludes private reserved IDs.
- **A3 — Manual admission:** each manual POST creates a new Run, ignores definition enablement, enforces busy/global capacity, and reports asynchronous failures only in history.
- **A4 — Execution:** two Steps run in order and the second never starts until the first reaches its required outcome. First failure prevents later Steps. Existing matching State is insufficient.
- **A5 — Truthful lifecycle:** caller disconnect does not cancel an admitted Run; precreation failures expose no Command link; collisions cannot adopt unrelated Commands; restart and drain interrupt without replay or invented success.
- **A6 — Fact validation:** embedded NATS tests prove malformed schema, unsafe identity, wrong subject family, a `Nats-Msg-Id` mismatch and subject/payload mismatch never reach admission and are terminated instead of blocking the consumer. A committed admission is acknowledged; a transient admission failure is not, so the fact redelivers and `(event_id, automation_id)` idempotency absorbs it.
- **A7 — Atomic admission:** injected failures before commit leave no partial Run, skip or Step rows and schedule no worker. One event produces at most one outcome per matching Automation; multiple matching Triggers produce one grouped outcome.
- **A8 — Busy and capacity:** deterministic concurrent facts produce one active Run plus `automation_busy` skips, never exceed 16 active Runs, and record `run_capacity` for excess matches. Accidental duplicate fact delivery does not schedule duplicate work.
- **A9 — Durable recovery and idempotency:** with automations absent, accepted Entity Events appear in Entity Event history and their facts stay in `HEARTH_DEVICE_FACTS_V1`; on restart the automations durable consumer resumes from its acknowledgement floor and each retained fact produces at most one outcome per Automation, with a redelivered or duplicated fact producing no second Run or skip. Starting or editing definitions never scans history.
- **A10 — Whole slice:** real SDK registration/publication, implemented Entity Event ingestion, durable Device Fact delivery from `HEARTH_DEVICE_FACTS_V1`, HTTP Automation definition/history, embedded NATS and real SQLite demonstrate simulated press → successful light Command, busy skip and restart interruption. `mise run validate` passes.

Use injected clocks and synchronization barriers, real SQLite for transaction and constraint claims, and embedded NATS for fact consumer behavior. Do not use sleeps as correctness oracles.

## 12. Risks and follow-up

Accepted limitations:

- a durable accepted Entity Event can outlive its fact when the stream's seven-day or one-GiB bound evicts it before the automations durable consumer reaches it, or when an operator purges the stream; a fact republished after the two-hour duplicate window can also be delivered twice, which `(event_id, automation_id)` idempotency absorbs;
- a crash may occur after automation admission commits but before a worker starts, leaving restart to mark the Run interrupted;
- source validity is the devices processing-time verdict carried by the accepted fact; automations do not revalidate it at admission;
- targets may change after definition save and are revalidated by normal Command execution;
- manual POSTs are not idempotent;
- Automation history grows until a separate retention feature exists;
- external fact publishers are trusted according to the NATS deployment boundary defined by the Device Facts spec.

The Device Facts foundation has landed and this file now carries its exact `devices.EntityEventFact` DTO, `devicesnats.DeviceFactStreamName` (`HEARTH_DEVICE_FACTS_V1`) stream, `hearth.v1.core.fact.entity.*.entity-event.>` wildcard, `urn:hearth:schema:entity-event-fact:v1` schema, `devicesnats.DeviceFactRelay` lifecycle seam and this automation consumer's own durable recovery policy; run the completeness review before marking this design implementation-ready.

On automation implementation, update `CONTEXT.md` to define Entity Event Trigger and Automation Skip, permit zero Triggers for manual-only Automations, and remove the obsolete scheduled `Occurrence` wording. Keep Entity Event distinct from Trigger and from the outbound `enumaction.trigger` Operation.
