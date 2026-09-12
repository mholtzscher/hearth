# Entity Event automations

**Status:** Deferred design; the Device Facts foundation defined by `specs/device-facts.md` is implemented, and this spec now names its exact Entity Event fact DTO, subject, schema and lifecycle seams. Automations remain unimplemented and this spec is not implementation-ready until the completeness review passes.
**Baseline:** `9bfee97`. Do not restore the automation module removed in `423addb` wholesale.
**Effort:** XL after the Device Facts foundation, across four remaining deliverables.

## 1. Purpose and scope

Build one useful automation path on Core-verified facts:

```text
Adapter Entity Event
        ↓
implemented durable Entity Event ingestion and history (devices)
        ↓ accepted, live Device Fact over Core NATS
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
- automation subscription, lifecycle and application assembly after Device Facts exist.

This spec does **not** own Entity Event identity, registration, wire ingestion, validation, duplicate detection, history or retention. Those are implemented by `devices` and specified in [Entity Events](entity-events.md).

**Non-goals:** State or Observation Triggers, Command-outcome Triggers, timers or schedules, conditions, branching, retries, compensation, cancellation, replay, catch-up, deletion, configurable history retention, manual idempotency keys, definition-count quotas, and a privileged event-injection endpoint.

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
- publication only after the devices-owned SQLite transaction establishing the fact commits;
- plain Core NATS pub/sub, not JetStream: no acknowledgement, retry, persistence, offset, replay or catch-up;
- subject/payload agreement, canonical IDs, timestamps, correlation, trace propagation and safe logging;
- exact duplicate, ordering, startup, reconnect, shutdown, slow-consumer and publish-failure semantics;
- a freshness boundary that suppresses accepted Observation and Entity Event records drained from JetStream backlog;
- no reconnect buffering that can publish a fact acquired while the fact transport was disconnected;
- the rule that durable HTTP/SQLite records remain authoritative and a missing Device Fact proves nothing;
- application wiring and documentation for trusted external subscribers;
- emission hooks for both agreed fact families, even though this automation slice consumes only accepted Entity Event facts.

The implemented `devices.EntityEventFact` DTO, delivered by `devices.DeviceFactSink.EntityEventAccepted`, gives automations:

```go
type EntityEventFact struct {
    EventID       devices.EntityEventID
    EntityID      devices.EntityID
    Name          devices.EntityEventName
    CorrelationID devices.CorrelationID
    ReportedAt    time.Time // SDK emitted_at
    ReceivedAt    time.Time // JetStream storage time
    RecordedAt    time.Time // Core first-record time
}
```

Its strict wire schema is `urn:hearth:schema:entity-event-fact:v1`, whose `data` object is exactly `{event_id, entity_id, name, reported_at, received_at, recorded_at}`; `correlation_id` stays in the envelope.

The Entity Event fact contract guarantees:

1. only a first-seen `devices.EntityEventOutcomeAccepted` transition is eligible for publication;
2. rejected events, duplicates and identity conflicts publish no accepted fact;
3. publication occurs after the devices transaction commits and never for a report consumed from a startup or reconnect backlog;
4. input acquired while the fact transport is disconnected is not buffered into a later fact;
5. subscribers receive only facts published while subscribed;
6. a crash or NATS failure between commit and publication may permanently lose the fact;
7. the publisher never turns a devices ingestion failure into an accepted fact;
8. Core provisions no durable fact stream and automations create no replay consumer.

These guarantees make missed automatic execution an explicit at-most-once limitation. A historical Entity Event without a corresponding Automation Run is truthful and expected after downtime or a publish/subscriber failure.

This file now names the implemented Entity Event fact contract exactly: the post-commit seam is `devices.DeviceFactSink.EntityEventAccepted`, the DTO is `devices.EntityEventFact`, the Core NATS wildcard is `hearth.v1.core.fact.entity.*.entity-event.>` and the strict wire schema is `urn:hearth:schema:entity-event-fact:v1`. No automation implementation should invent those details independently.

## 4. Ownership and module seams

- `devices` owns Entity identity, Entity Event ingestion and history, Command creation and outcomes, and post-commit Device Fact production.
- `automations` owns definitions, subscriptions to accepted Entity Event facts, matching, Runs, skips, Steps and execution history.
- `contracts/v1` and `internal/contracts/v1/natswire` own the language-neutral Device Fact schemas and subject mechanics defined by the Device Facts spec.
- `internal/modules/automations/nats` owns schema-validated mapping from the external fact contract into the automation domain.
- `internal/app/hearthd` assembles both modules, the shared SQLite database and NATS connection, and owns startup/readiness/drain ordering.

The dependency direction remains acyclic:

```text
contracts/v1 ← natswire ← devices/nats ← hearthd → automations/nats → automations
                               ↑                         ↓
                            devices ← AutomationDevices ┘
```

`devices` never imports `automations`. The automation subscriber never consumes Adapter-originated Entity Event subjects and never reads event history to discover work.

No SQLite transaction may call another module, publish NATS, or wait for a Command. Automation admission is one automation-owned transaction over an already verified fact. Command execution begins only after that transaction commits.

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

`internal/modules/automations/nats` subscribes once to the accepted Entity Event fact wildcard `hearth.v1.core.fact.entity.*.entity-event.>` and validates each message against `urn:hearth:schema:entity-event-fact:v1`. It does not create one subscription per Trigger: matching belongs to the automation transaction, so definition edits require no NATS subscription churn.

The subscriber:

1. creates a plain Core NATS subscription and flushes it before reporting active;
2. sets subscriber pending limits to 64 messages and 256 KiB; overflow drops the live fact, reports a safe slow-consumer diagnostic and never creates a recovery queue;
3. validates the strict `urn:hearth:schema:entity-event-fact:v1` schema, `entity-event` subject shape, canonical `fct_`/`evt_`/`ent_` identities and subject/payload agreement;
4. maps one accepted `devices.EntityEventFact` to the automation-owned `automations.EntityEventFact`;
5. calls `AutomationService.ReceiveEntityEventFact` synchronously for short admission work only;
6. logs and drops malformed facts or admission failures without retrying them;
7. never executes Commands in the NATS callback.

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

No timestamp freshness or expiry rule exists in automations. Live-only delivery is a Device Facts transport guarantee. Automations must not query Entity Event history on startup, reconnect, definition edits or subscriber recovery.

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

Preserve the Device Facts foundation's startup ordering, including interruption of active Commands into durable history before either NATS connection attempt. Insert automation work at these points:

1. open/migrate SQLite and interrupt active Commands into durable history as required by Device Facts;
2. interrupt active Automation Runs before either NATS connection attempt;
3. establish the shared and dedicated Device Fact connections, `devicesnats.DeviceFactEpochs` and `devicesnats.DeviceFactDispatcher`;
4. construct the devices and automation services;
5. provision JetStream resources and start request/reply transports;
6. start and flush the automation Entity Event fact subscription;
7. start durable Observation and Entity Event consumers;
8. expose HTTP and readiness.

Starting the plain fact subscription before durable consumers prevents Hearth's own automation subscriber from missing fresh facts produced during process startup. The Device Facts generation/epoch gate suppresses retained backlog. External subscribers retain the documented Core NATS at-most-once semantics and may miss any fact published before they subscribe.

Readiness requires the automation fact subscription to be active and automation admission to be open, in addition to existing device requirements. It never waits for an Entity Event backlog and adds no Device Fact resource check because plain Core NATS has no resource.

Drain order:

1. stop/drain the automation fact subscription and close new automation admission;
2. close device Command admission;
3. join automation workers;
4. join device Command workers;
5. drain Entity Event and Observation consumers while dependencies remain available and the Device Fact dispatcher still accepts facts;
6. stop Device Fact admission and drain `DeviceFactDispatcher` within the foundation's five-second deadline;
7. drain transports, both NATS connections and SQLite.

A Run admitted before drain may finish its current Command. It starts no later Step after Command admission closes; such a Step becomes `interrupted/core_stopping` without a Command link.

An automation executor fault closes admission until restart. Ordinary fact loss or the absence of an external subscriber is not a readiness failure.

## 10. Deliverables

The Device Facts foundation is a separate prerequisite and is not a deliverable of this spec.

| ID | Outcome | Effort | Depends on | Acceptance |
|---|---|---|---|---|
| D1 | Definitions, save-time validation, manual Runs and history API | L | Device Facts foundation implemented | A1–A3 |
| D2 | Sequential Step execution, verified Command links and restart/drain behavior | L | D1 | A4–A5 |
| D3 | Entity Event fact subscriber and atomic Run/skip admission | L | Device Facts implementation, D1 | A6–A8 |
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
└── nats/entity_event_facts.go              # new — plain NATS subscriber and wire mapping [D3]
internal/platform/db/migrations/00001_initial.sql
                                               # modify — three automation tables [D1]
internal/app/hearthd/run.go / server.go         # modify — assembly/readiness/drain [D2,D3]
internal/app/hearthd/entity_event_automation_integration_test.go
                                               # new — whole slice [D4]
sqlc.yaml / mise.toml                          # modify — automation generation [D1]
README.md / CONTEXT.md / docs/{architecture,logging}.md
                                               # modify — semantics and operator usage [D4]
```

Do not list or modify Device Fact contracts, subjects, publishers or device fact emission hooks here; those belong to `specs/device-facts.md` and must land first.

## 11. Acceptance tests

- **A1 — Definitions:** strict schema limits, save-time event-name and Command validation, parameter normalization, zero-Trigger manual-only definitions and revision conflicts work through real SQLite and HTTP.
- **A2 — History:** Run snapshots and matching Trigger IDs remain immutable after edits; Run and skip alternatives satisfy table and constructor invariants; parent-scoped pagination is newest-first and excludes private reserved IDs.
- **A3 — Manual admission:** each manual POST creates a new Run, ignores definition enablement, enforces busy/global capacity, and reports asynchronous failures only in history.
- **A4 — Execution:** two Steps run in order and the second never starts until the first reaches its required outcome. First failure prevents later Steps. Existing matching State is insufficient.
- **A5 — Truthful lifecycle:** caller disconnect does not cancel an admitted Run; precreation failures expose no Command link; collisions cannot adopt unrelated Commands; restart and drain interrupt without replay or invented success.
- **A6 — Fact validation:** embedded NATS tests prove malformed schema, unsafe identity, wrong subject family and subject/payload mismatch never reach admission. Receiver failure is logged once and never retried.
- **A7 — Atomic admission:** injected failures before commit leave no partial Run, skip or Step rows and schedule no worker. One event produces at most one outcome per matching Automation; multiple matching Triggers produce one grouped outcome.
- **A8 — Busy and capacity:** deterministic concurrent facts produce one active Run plus `automation_busy` skips, never exceed 16 active Runs, and record `run_capacity` for excess matches. Accidental duplicate fact delivery does not schedule duplicate work.
- **A9 — No catch-up:** with Core or the automation subscriber absent, broker-acknowledged Entity Events later appear in Entity Event history but create no Run or skip. Starting or editing definitions never scans history. A subsequent live accepted fact creates exactly one outcome.
- **A10 — Whole slice:** real SDK registration/publication, implemented Entity Event ingestion, external Core NATS Device Fact delivery, HTTP Automation definition/history, embedded NATS and real SQLite demonstrate simulated press → successful light Command, busy skip and restart interruption. `mise run validate` passes.

Use injected clocks and synchronization barriers, real SQLite for transaction and constraint claims, and embedded NATS for fact subscriber behavior. Do not use sleeps as correctness oracles.

## 12. Risks and follow-up

Accepted limitations:

- a durable accepted Entity Event can exist without an Automation outcome because Core NATS facts are live and at-most-once;
- a crash may occur after automation admission commits but before a worker starts, leaving restart to mark the Run interrupted;
- source validity is the devices processing-time verdict carried by the accepted fact; automations do not revalidate it at admission;
- targets may change after definition save and are revalidated by normal Command execution;
- manual POSTs are not idempotent;
- Automation history grows until a separate retention feature exists;
- external fact publishers are trusted according to the NATS deployment boundary defined by the Device Facts spec.

The Device Facts foundation has landed and this file now carries its exact `devices.EntityEventFact` DTO, `hearth.v1.core.fact.entity.*.entity-event.>` wildcard, `urn:hearth:schema:entity-event-fact:v1` schema and `DeviceFactEpochs`/`DeviceFactDispatcher` lifecycle seams; run the completeness review before marking this design implementation-ready.

On automation implementation, update `CONTEXT.md` to define Entity Event Trigger and Automation Skip, permit zero Triggers for manual-only Automations, and remove the obsolete scheduled `Occurrence` wording. Keep Entity Event distinct from Trigger and from the outbound `enumaction.trigger` Operation.
