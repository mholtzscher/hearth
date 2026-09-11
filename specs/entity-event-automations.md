# Entity Event automations: first working slice

**Status:** Deferred design notes; blocked by [Entity Events](entity-events.md), which will be implemented first. Not implementation-ready.

> **Superseded foundation:** `entity-events.md` now owns the event type, SDK/wire delivery, durable ingestion and history. The one-shot request/reply, no-retry/no-buffer publication, two-second event expiry, read-only event acceptance and automation-owned ephemeral receipt proposals below are retained as historical design notes, **not instructions to implement**. The “add no event table/store” instruction is superseded by the foundation's `entity_events` table. Its `received_at` is JetStream storage time, replacing this draft's Core-callback interpretation. Revise these sections and the D1/D3 breakdown after the Entity Events foundation; preserve the separate requirement that future automations must not catch up missed triggers merely because event history does.
**Baseline:** `378080b`; do not restore the deliberately removed automation module wholesale.
**Effort:** XL overall, reduced scope across four deliverables. This remains a cross-module feature, not a small endpoint change.

## 1. Scope and simplification

Build one useful path:

```text
Simulated live press → validate source/name → atomically record Run or skip
                                                        ↓
                                            existing Entity Commands
```

Agreed requirements: entity-event Triggers, HTTP/SQLite definitions, manual invocation, ordered Steps, one active Run per Automation, recorded busy skips, no offline catch-up. Physical-device mappings, State conditions, timers and schedules are deferred.

**Removed from the earlier draft:** independently browsable Entity Event history, entity-event write repository, two-stage durable acceptance/handoff, readiness generations, a new readiness controller, startup outcome reconstruction, manual idempotency keys, separate Run/Trigger Decision/event-history API surfaces, configurable history retention, and definition-count quotas.

**Retained:** canonical event-source Entities and generated validation, immutable Run snapshots, verified Command links, duplicate protection, bounded concurrency, safe shutdown, and integration tests. These protect the first use case rather than a hypothetical future platform.

## 2. Ownership and the one durable admission

- `devices` owns Entity identity, event support and source validation. Entity Events are not Observations and never update State or satisfy Commands.
- `automations` owns definitions, matching, Runs, skips, execution history and its small duplicate-receipt table.
- The device NATS transport decodes live requests. Assembly injects the automation service as its receiver; no app-owned event bus or handoff coordinator is needed.
- `automations.ReceiveEntityEvent` asks devices to validate the source, then performs **one automation-owned write transaction**. Receipt, matching outcomes, snapshots and initial Step rows commit together. Acknowledgement follows that commit; Commands run separately.

Source validation uses one consistent read of current Entity support/enablement, owner, active runtime and Adapter health. Its meaning is **valid at that read**, not a guarantee that these facts remain unchanged until dispatch. A later source disablement or re-registration does not retroactively retract this input. Definition matching uses the revisions read in the admission transaction. Target Operations are revalidated by normal Command execution.

This is a deliberate, simple ordering contract: no nested transactions, cross-module SQL writes, source locks spanning execution, or attempt to make all device metadata and automation edits one global transaction. Hearth's single SQLite connection makes calling another module's repository inside a held transaction unsafe.

## 3. Event model and generated type

Add `hearth.enumevent/v1`: a stateless, non-commandable Entity with multiple supported names:

```json
{"state":{},"operations":{},"events":{"names":["single_press","double_press"]}}
```

Names are unique slugs, `^[a-z0-9][a-z0-9_-]{0,62}$`, with 1–64 names. Each press has a separate `evt_<UUIDv7>` identity even when its name repeats. No arbitrary event payload, source-time model, last-event pseudo-State, or new Device-kind taxonomy.

Extend the existing generator, not a handwritten parallel type system:

- Manifest flag `event_source` defaults false; true requires `stateless:true`, no Operations, and the fixed required support shape `events.names`.
- The generator verifies this schema structure and emits a typed name selector, codecs, conformance tests, descriptor helper and event builder. No generic selector/predicate language.
- Registration's outer support schema allows optional `events`. Existing type schemas remain closed and still reject it. Preserve the field through SDK/NATS DTO mappings.
- Core catalog exposes supported names through the generated selector. Existing stateless Observation rejection in `sqlite_observations.go:classifyObservation` remains `invalid_value`; event Entities have `state:null` and no Command handlers or Observation builders.

Changes to existing domain/catalog types (D1):

```diff
 // internal/modules/devices/model.go
 type ObservationID string
+type EntityEventID string
+type EntityEventName string
 type CommandID string

 // internal/modules/devices/catalog.go
 type EntityTypeDefinition struct {
     id               EntityTypeID
     stateless        bool
+    eventNames       func(EntitySupport) ([]EntityEventName, error)
```

New `internal/modules/devices/entity_events.go` contract (D1):

```go
// EntityEventInput is decoded input; only ValidateEntityEvent establishes source eligibility.
type EntityEventInput struct {
    ID            EntityEventID
    AdapterID     string
    RuntimeID     RuntimeID
    EntityID      EntityID
    Name          EntityEventName
    CorrelationID CorrelationID
    EmittedAt     time.Time // SDK envelope time
    ReceivedAt    time.Time // Core callback time
}

// ValidatedEntityEvent captures a point-in-time validation, not durable acceptance.
type ValidatedEntityEvent struct { Event EntityEventInput }

type EntityEventReceipt struct {
    ID         EntityEventID
    ReceivedAt time.Time
    Duplicate  bool
}

func (s *Service) ValidateEntityEvent(context.Context, EntityEventInput) (ValidatedEntityEvent, error)
func (s *Service) ValidateEntityEventTrigger(context.Context, EntityID, EntityEventName) error
```

`ValidateEntityEvent` checks canonical identities, active owning runtime, Entity enablement, healthy Adapter and supported name. Entity availability is diagnostic, not a second input gate. Use a single source-read query through the existing devices read store; add no event table/store. `ValidateEntityEventTrigger` is the save-time check: only Entity existence and supported name, not enablement/health. Both are read-only.

## 4. Live delivery, not a recovery protocol

Use one-shot Core NATS request/reply:

```text
hearth.v1.adapter.<adapter>.runtime.<runtime>.entity-event.<entity_id>
```

Request schema `urn:hearth:schema:entity-event-request:v1` uses the shared envelope: `evt_` ID, SDK-minted `emitted_at`, required correlation ID, no causation ID. Data is only `{"entity_id":"ent_…","name":"single_press"}`. Subject and payload Entity must agree.

Response schema `urn:hearth:schema:entity-event-response:v1` uses `rep_` ID, matching correlation and `causation_id` equal to the request ID; add `evt_id` to the common causation union. Accepted data is `{status:"accepted", event_id, received_at, duplicate}`. Rejected data is `{status:"rejected", event_id, error:{code}}`.

**Accepted means the automation admission transaction committed**, including the no-match case. It does not mean a Run succeeded. Timeout/internal error is ambiguous and is not permission to resend a press.

New SDK data types in `sdk/adapter/entity_events.go` (D1), publication method in D3:

```go
type EntityEventID string
type EntityEvent struct { EntityID string; Name string }

// PublishEntityEvent makes one attempt and never retries a missed press.
func (s *Session) PublishEntityEvent(context.Context, EntityEvent) (EntityEventID, error)
```

Generated `sdk/adapter/enumeventv1.NewEntityEvent` takes `{EntityID string; Support Support; Name string}` and returns a validated `adapter.EntityEvent`. The Session mints identity/time once, performs one `RequestMsgWithContext`, and returns the ID even if a prepared publication fails.

Simple freshness policy, with no admission generations:

- One request deadline: earlier of caller deadline and SDK now + two seconds. Adapters publish newly acquired live input; they must not queue, reconstruct or retry missed presses.
- Core accepts envelope age in `[-1 second, 2 seconds)`; future skew beyond one second and age at least two seconds reject. Recheck expiry immediately before recording admission outcomes. The admission deadline derives from envelope time, not a second wire deadline field.
- This bounds transport freshness and requires reasonably synchronized SDK/Core clocks. It cannot prove physical press time. An unexpired in-flight request spanning a brief disruption may still be admitted; no historical catch-up is promised or implemented.
- Disable SDK reconnect buffering with **`ReconnectBufSize(-1)`**, verified against pinned `nats.go v1.53.1`; zero restores its default buffer. Events never use retry wrappers. Keep existing non-event retries, adding `ErrReconnectBufExceeded` to their transient classifications; Observation retries preserve ID, payload, MsgId and existing deadline.
- Use existing dependency readiness to reject new input with `core_unavailable` while unready. On drain, close automation admission before closing device Command admission. No new controller, connection epoch or readiness-supervisor behavior.
- Event callbacks perform short validation/admission work synchronously, not Command execution. Set pending limits to 64 messages/256 KiB and cap input at 4 KiB before decoding. Overflow may drop input; it never creates a work queue. These are safety constants, not new operator configuration.

Rejections: `invalid_event`, `event_expired`, `clock_skew`, `unknown_entity`, `wrong_adapter`, `runtime_fenced`, `entity_disabled`, `adapter_unhealthy`, `unsupported_event`, `event_id_conflict`, `core_unavailable`, `internal_error`. Undecodable input or unsafe request identities are discarded/logged instead of echoed. Runtime fencing retains normal SDK termination behavior. Log safe IDs/reasons, not raw payloads, parameters or full subjects.

## 5. Definitions, matching and admission

Definition JSON is strict, defaults `enabled:false`, and permits 0–32 Triggers and 1–32 ordered Steps. Zero Triggers means manual-only. Trigger/Step IDs are unique slugs within their arrays. Name length is 1–200; total body is at most 64 KiB. Keep revision-checked replacement to avoid silently overwriting another edit.

```json
{
  "name":"Button turns on light", "enabled":true,
  "triggers":[{"id":"press","kind":"entity_event","entity_id":"ent_<button>","event_name":"single_press"}],
  "steps":[{"id":"light_on","entity_id":"ent_<power>","operation":"set","parameters":{"value":true}}]
}
```

Validate source names and call existing `ValidateCommand` at save, persisting normalized parameters. Disabled/unavailable targets can be saved. Edits or disablement affect future admission, not a Run's immutable snapshot.

In one automation transaction:

1. Reject expired input. Look up its internal receipt by event ID. Identical fingerprint returns the original receipt without rematching; different content returns `event_id_conflict`.
2. Read current enabled definitions and match exact `(entity_id,event_name)`. Group all matching Trigger IDs per Automation; visit Automations by ID for deterministic capacity decisions.
3. For each match, admit one Run or record one skip. Busy reason is `automation_busy`; the global bound is 16 active Runs, with excess matches skipped as `run_capacity`. Never queue skips. The active-Run partial unique index backs up the busy check; count/global capacity and writes share this transaction.
4. Write the event receipt even when nothing matches, plus every Run snapshot/initial Step row or skip, and commit them together. Schedule workers only after commit.

The receipt is **not event history**: just event ID, immutable-input fingerprint, first Core receipt time and `expires_at = emitted_at + 2 seconds`. Unexpired identical input returns `duplicate:true` with the original receipt time; expired input returns `event_expired` before receipt lookup, never an accepted duplicate or a new match. Hash a canonical encoding of Adapter/runtime/Entity/name/correlation/envelope-time fields (not whitespace or Core arrival time). It suppresses even an unmatched duplicate after definition edits. Each admission deletes at most 256 expired receipts; expiry is checked before duplicate lookup, so deleting old receipts cannot revive old input. No receipt API, payload archive, separate retention setting or replay consumer.

If validation or the admission transaction fails, acknowledge no success and start no worker. If a commit's outcome is uncertain, or a committed Run cannot get its worker, stop new automation admission and surface an executor fault; restart interrupts any recorded work. Do not add rollback classification frameworks, repair loops or replay to resolve this rare case.

Manual invocation uses the same Run admission rules but ignores definition enablement. It has no event, receipt or matched Trigger IDs. Each successful POST creates a new Run: **manual idempotency keys are deferred**, like the existing direct-Command API. Clients must not automatically retry an ambiguous manual request; inspect history first.

## 6. Execution, truthful history and restart

Reuse `devices.ExecuteCommand` without a special automation bypass:

- Before each Step, persist its `running` status, start time and freshly reserved Command/correlation IDs. Use `devices.NewCommandID` / `devices.NewCorrelationID` (`cmd_<UUIDv7>` / `cor_<UUIDv7>`), with equivalent constructors injected for tests. Only then call devices outside the transaction, using a process-owned context detached from the HTTP/NATS caller.
- Wait for the existing Operation outcome. `satisfied` and `dispatched` are distinct successful Step statuses; Run success requires all Steps successful. Preexisting matching State is not evidence of a new Command's success.
- Stop on the first known failure; later Steps remain `not_attempted`. No retry, compensation or rollback of earlier physical effects.
- Known precreation errors (`ErrInvalidCommand`, `ErrEntityNotFound`, `ErrCommandIDConflict`) become `invalid_command`, `entity_not_found`, `command_id_conflict`; they never acquire a Command link. `CommandExecutionError` proves creation, not the eventual outcome: verify the owned Command record and use its durable status/failure code. Unknown existence/outcome faults admission rather than inventing a terminal result.
- Bare `devices.ErrCommandUnavailable` during drain maps to Step `interrupted/core_stopping`, with no Command link; it is not a failed Operation or executor fault. Outside drain it is an unexpected admission inconsistency, not evidence that shutdown occurred.
- A public Command link is exposed only after checking reserved Command ID, correlation ID, Entity and Operation. Never adopt a collision or an unrelated record. Already failed-before-creation Steps are excluded from later lookup.

Startup recovery is deliberately **not execution reconciliation**: run existing `InterruptActiveCommands`, mark all still-running automation Steps/Runs `interrupted/core_restarted`, leave untouched Steps `not_attempted`, and preserve already recorded terminal Steps. Do not infer a successful Run from Command history, resume Steps, or process receipts. History may read an interrupted Step's verified Command link without rewriting that Step's status: “Run interrupted; its Command satisfied” is a truthful possible result.

Drain order: stop new automation admission/Steps → stop device Command admission → join automation workers → join device Command workers → drain Observation/health/DB dependencies. In-progress Commands keep their normal deadlines and Observation access. A remaining sequence becomes `interrupted/core_stopping`; an already attempted last Step may finish normally. A Step losing the race to device admission is interrupted with no created Command. An executor fault is distinct from ordinary dependency unavailability and remains closed until restart.

## 7. Minimal types, storage and API

New domain types in `internal/modules/automations/automation_model.go`; strings below denote closed named status/code types in implementation:

```go
type AutomationID string     // aut_<UUIDv7>
type AutomationRunID string  // arn_<UUIDv7>; run_ already means Adapter runtime
type AutomationSkipID string // ask_<UUIDv7>
type EntityEventTrigger struct { ID, Kind string; EntityID devices.EntityID; EventName devices.EntityEventName }
type AutomationStep struct { ID string; EntityID devices.EntityID; Operation devices.OperationName; Parameters devices.CommandParameters }
type AutomationDefinition struct { Name string; Enabled bool; Triggers []EntityEventTrigger; Steps []AutomationStep }
type AutomationRecord struct { ID AutomationID; Revision int64; Definition AutomationDefinition; CreatedAt, UpdatedAt time.Time }
type AutomationEventSummary struct { ID devices.EntityEventID; EntityID devices.EntityID; Name devices.EntityEventName; ReceivedAt time.Time }

type AutomationStepAttempt struct {
    Position int
    StepID, Status string // not_attempted | running | satisfied | dispatched | failed | interrupted
    ReservedCommandID *devices.CommandID       // private; not proof a Command exists
    ReservedCorrelationID *devices.CorrelationID // private; paired with reserved Command ID
    FailureCode *string
    StartedAt, CompletedAt *time.Time
}
type AutomationRun struct {
    ID AutomationRunID
    AutomationID AutomationID
    Revision int64
    Snapshot AutomationDefinition
    Event *AutomationEventSummary // nil for manual
    MatchedTriggerIDs []string     // empty for manual
    Status string // running | succeeded | failed | interrupted
    FailureCode *string
    StartedAt time.Time
    CompletedAt *time.Time
    Steps []AutomationStepAttempt
}
type AutomationSkip struct {
    ID AutomationSkipID
    AutomationID AutomationID
    Revision int64
    Event AutomationEventSummary
    MatchedTriggerIDs []string
    Reason string // automation_busy | run_capacity
    SkippedAt time.Time
}
type AutomationHistoryEntry struct {
    Kind string // run | skip
    Run *AutomationRun
    Skip *AutomationSkip // exactly one alternative, matching Kind
}
```

A skip is not a Run. Store the alternatives in one feature-owned history table, with CHECK constraints and constructor/read validation; do not create a second “started Trigger Decision” record duplicating each Run. Keep source summary and matching Trigger IDs in both alternatives so later edits cannot erase their explanation.

Consuming seam, `automation_service.go`:

```go
type AutomationDevices interface {
    ValidateEntityEvent(context.Context, devices.EntityEventInput) (devices.ValidatedEntityEvent, error)
    ValidateEntityEventTrigger(context.Context, devices.EntityID, devices.EntityEventName) error
    ValidateCommand(context.Context, devices.CommandInput) (devices.CommandParameters, error)
    ExecuteCommand(context.Context, devices.CommandInput) (devices.CommandResult, error)
    GetCommand(context.Context, devices.CommandID) (devices.CommandRecord, error)
}
func NewAutomationService(*SQLiteAutomationRepository, AutomationDevices, AutomationDependencies) *AutomationService
func (s *AutomationService) ReceiveEntityEvent(context.Context, devices.EntityEventInput) (devices.EntityEventReceipt, error)
func (s *AutomationService) CreateAutomation(context.Context, AutomationDefinition) (AutomationRecord, error)
func (s *AutomationService) ReplaceAutomation(context.Context, AutomationID, int64, AutomationDefinition) (AutomationRecord, error)
func (s *AutomationService) StartManualRun(context.Context, AutomationID) (AutomationRun, error)
func (s *AutomationService) StopAutomationAdmission()
func (s *AutomationService) WaitAutomationRuns(context.Context) error
func (s *AutomationService) AutomationExecutionReady() bool
```

Dependencies are the clock, ID constructors and logger. Use the real SQLite repository for transaction tests and the consuming device interface for deterministic execution tests. Device NATS transport's `EntityEventReceiver` interface has exactly the `ReceiveEntityEvent` signature above. App injects the automation service and existing dependency-readiness checker; no devices → automations import.

Four automation-owned tables, in `00001_initial.sql` (recreate development DBs; no compatibility migration):

| Table | Required columns/invariants |
|---|---|
| `automations` | ID PK, revision starting at 1, strict normalized definition JSON, created/updated times. Revision-checked replacement is atomic. |
| `automation_event_receipts` | Event ID PK, fingerprint, received_at, expires_at. Internal duplicate suppression only; no FK from history, no executable work. |
| `automation_history` | ID PK (`arn_` or `ask_` according to kind), Automation FK, kind, revision, recorded_at, nullable event summary, matching IDs. Run-only snapshot/status/failure/completed_at; skip-only reason. Partial unique Automation ID where kind=run/status=running; unique `(event_id,automation_id)` when event_id is non-null. |
| `automation_run_steps` | `(run_id,position)` PK; `run_id` FK to `automation_history(id)`; Step ID/status/failure/times, nullable reserved Command/correlation pair. Repository write/read validation enforces a run-kind parent: the FK alone cannot filter by kind. No Command FK for a merely reserved identity and no rows for skips. |

Use existing fixed-width sortable UTC encoding. Check legal kind/field/status combinations, terminal timestamps and reserved-ID pairing. Match/receipt/capacity/snapshot writes share one transaction; worker registration is tracked so drain cannot miss a committed Run. Never hold a transaction while calling another module, NATS, or waiting for a Command.

Run/skip history is retained indefinitely in this first slice, like existing Command history. Configurable pruning is deferred, with SQLite growth an explicit operational limitation before broad household rollout. Only expired duplicate receipts receive the small admission-time cleanup above.

One execution-history API surface; standard module-owned Huma DTOs and RFC 9457 errors:

| Route | Result |
|---|---|
| `POST /v1/automations` | Definition → 201 record/Location. |
| `GET /v1/automations` | ID-ascending list. |
| `GET /v1/automations/{id}` | Definition/revision/times. |
| `PUT /v1/automations/{id}` | `{expected_revision,definition}` → updated record; stale revision=409. |
| `POST /v1/automations/{id}/runs` | Empty body → 202 Run summary and Location to history detail; busy=409, capacity/unready=503. |
| `GET /v1/automations/{id}/history` | Newest-first Run/skip summaries with `kind`, `id`, revision, recorded time, event/matched IDs and status or skip reason. |
| `GET /v1/automations/{id}/history/{entry_id}` | Run snapshot/ordered Steps/verified Command links, or skip detail. Parent mismatch=404. |

History list excludes full snapshots/Steps; detail includes them. Public Step command link is `{command_id,status}` from a verified read, with full evidence available at the existing Command endpoint; private reserved identities/correlation never leak. Both collections use existing keyset conventions: default 50, limits 1–200, no totals; history key `(recorded_at,id)`, endpoint/parent-scoped opaque cursors, no snapshot guarantee. Unknown resources=404, semantic validation=400, body schema errors follow Huma 422, unexpected errors=500. Async failures belong in history, not the completed 202 request. No deletion/cancellation/replay APIs.

## 8. Four implementation slices

| ID | Outcome | Effort | Depends on | Acceptance |
|---|---|---|---|---|
| D1 | Registered event-source type and read-only source validation | L | — | A1–A2 |
| D2 | API definitions, manual Runs, sequential execution and unified history | L | D1 | A3–A5 |
| D3 | Live SDK event → one atomic automation admission → Run/skip | L | D1,D2 | A6–A9 |
| D4 | Simulator-to-light proof, failure tests and operator documentation | L | D3 | A10 |

Owning paths (tests colocate with behavior; generated outputs ship with their sources):

```text
entitytypes/
├── entitytype-manifest.schema.json       # modify — event_source flag [D1]
└── enumeventv1/                          # new — schemas/manifest/examples/generated type [D1]
contracts/v1/
├── registration-request.schema.json     # modify — optional event support [D1]
├── entity-event-{request,response}.schema.json # new — live envelope/receipt [D3]
└── common.schema.json / embed.go         # modify — event IDs/causation/schema registration [D3]
sdk/adapter/
├── entity_events.go                      # new — data types [D1], publication [D3]
├── enumeventv1/                          # generated — descriptor/event builders [D1]
└── session.go / lifecycle.go             # modify — no buffering; preserve non-event retries [D3]
internal/
├── cmd/entitytypegen/                    # modify — event generation/conformance fixtures [D1]
├── contracts/v1/natswire/subjects.go      # modify — event route helpers [D3]
├── modules/devices/
│   ├── model.go / ids.go / catalog.go    # modify — event types/parsers/catalog selector [D1]
│   ├── entity_events.go                  # new — read-only validation [D1]
│   ├── repository.go / sqlite_reads.go   # modify — single source-snapshot read [D1]
│   ├── dbqueries/entity_event_source.sql # new — source lookup, no event storage [D1]
│   ├── dbsqlc/ / zz_generated_entitytypes*.go # generated — source read/catalog [D1]
│   └── nats/entity_event.go              # new — bounded request/reply, injected receiver [D3]
├── modules/automations/
│   ├── automation_model.go              # new — definition/Run/skip types [D2]
│   ├── automation_definition.go / automation_definition.schema.json # new — strict definition validation [D2]
│   ├── automation_service.go            # new — management/admission/lifecycle [D2,D3]
│   ├── automation_execution.go          # new — Steps using existing Commands [D2]
│   ├── automation_events.go             # new — exact matching, receipts and skips [D3]
│   ├── automation_history.go            # new — summary/detail/verified links [D2]
│   ├── sqlite_automations.go / dbqueries/automations.sql # new — owned transactions [D2,D3]
│   ├── dbsqlc/                          # generated — owned SQL [D2,D3]
│   └── api/automations.go               # new — seven routes/DTOs/cursors [D2]
├── platform/db/migrations/00001_initial.sql # modify — automation tables [D2,D3]
├── app/hearthd/run.go / server.go        # modify — assembly/readiness/drain/routes [D2,D3]
├── app/hearthd/entity_event_automation_integration_test.go # new — whole slice [D4]
└── adapters/simulator/ / app/simulator/  # modify — event scenario/explicit emission [D4]
sqlc.yaml / mise.toml                    # modify — automation SQL generation/checks [D2]
README.md / configs/ / CONTEXT.md / docs/{architecture,logging}.md
                                        # modify at implementation — semantics/local usage [D4]
```

D1 includes SDK/domain types required by its generated builders. D2 ships working manual-only execution before D3 transport exists. Update any explicit descriptor DTOs needed to preserve `events` in D1. Serialize changes to shared migration/generation inputs; do not add infrastructure to make the slice artificially parallelizable.

Simulator D4 registers an `events` Entity alongside `power` in scenario `entity-events`; existing scenarios and Binding keys stay unchanged. Tests call explicit `EmitEntityEvent(ctx,name)`, not a timer. Local scenario input accepts `single_press`/`double_press` lines while serving Commands; use an unbuffered reader-to-publisher handoff, dropping/logging while busy or disconnected, never retaining presses for recovery. EOF stops only input; ensure shutdown can close/join its reader. No privileged Core event-injection endpoint and no real-hardware claim.

## 9. Acceptance tests and remaining trade-offs

- **A1 — Type contract:** generated zero-Operation event type registers and round-trips supported names through SDK/wire/Core; existing types still reject event support. Event Entities never gain State or satisfy Commands; invalid schema shapes fail generation.
- **A2 — Source validation:** read-only validation rejects wrong owner/runtime, disabled source, unhealthy Adapter and unsupported name. Barrier-test source changes between validation and admission against the explicit point-in-time contract; no cross-module transaction is held.
- **A3 — Definitions/history:** save-time parameter normalization, revision conflict, immutable Run snapshots, manual-only definitions, parent-scoped pagination and Run/skip detail shapes. Editing a definition never rewrites existing history.
- **A4 — Command execution:** a two-Step Run waits for the first required outcome; failure prevents the second. Existing matching State is insufficient. Precreation failure/ID collision yields no false Command link; distinct Automations may still issue overlapping Commands as Hearth permits.
- **A5 — Lifecycle:** caller disconnect does not cancel an admitted Run; shutdown starts no later Steps and keeps Observations alive for current Commands. Crash after reservation, dispatch or Command completion interrupts the Run on restart without rewriting it to success or redispatching. Verified Command evidence remains separately inspectable.
- **A6 — Atomic admission:** injected failures before commit leave neither receipt nor partial Runs/skips/Steps and schedule no workers. Post-commit response loss never re-executes an identical event. Unmatched duplicates remain unmatched even after adding a definition; same-ID changed input conflicts; two different IDs with the same name are distinct presses.
- **A7 — Busy/capacity:** deterministic concurrent events produce one active Run plus recorded busy skips with all matching Trigger IDs. Global active count never exceeds 16. Multiple Triggers on one Automation yield one outcome, not multiple Runs. Expired events produce no new receipt/history or delayed execution.
- **A8 — Live NATS:** use real embedded NATS to prove absence/reconnect does not queue event requests, expiry boundaries are enforced, malformed subject/payload/identity/oversized input safely rejects or drops, and publication never retries. Ordinary Observations and Session recovery still retry correctly with buffering disabled and preserve their identities/deadlines.
- **A9 — No deadlock:** event admission, SQL and lifecycle locks never wait for Command outcomes. With the real single-connection SQLite DB, a linked Observation completes a Command while other events are admitted. Duplicate-receipt cleanup cannot revive expired input; restart never scans receipts for work.
- **A10 — Whole slice:** real SDK registration/publication + HTTP definition/history + embedded NATS/SQLite demonstrate simulated press → successful light Command, busy skip and restart interruption. README recipe works locally; `mise run validate` passes and generation checks cover both SQL packages and the new type.

Use injected clocks and synchronization barriers, real SQLite for atomicity/constraints and embedded NATS for transport behavior. No new test dependencies or sleeps-as-oracles. Keep ordinary size limits and concurrency bounds; do not add a general queue or scheduler to make tests easier.

Risks remain explicit: input before durable admission can be lost; a committed Run can be interrupted before dispatch; clocks bound transport age, not physical truth; source validation is point-in-time; manual POSTs are not idempotent; execution history grows until a later retention feature. These are smaller, understandable limitations instead of extra recovery subsystems.

On implementation, update glossary Entity/Entity support to include named event sources, permit zero Triggers for manual-only Automations, and define Entity Event and Automation Skip. Preserve the distinction from outbound `enumaction.trigger`. Remove the unused scheduled Occurrence wording and fix its dangling references together; do not rename a skip into an executed Run. No accepted architecture/glossary is changed merely by this draft revision.
