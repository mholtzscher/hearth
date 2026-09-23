# Fact-driven automations

**Status:** Implementation-ready; approved for task breakdown on 2026-09-13.
**Supersedes:** [Entity Event automations](entity-event-automations.md).
**Baseline:** `1f9a2b0`; Device Facts are implemented. Do not restore the automation module removed in `423addb` wholesale.
**Effort:** XL, split into four ordered deliverables.
**Follow-on:** [Automation Conditions](automation-conditions.md) specifies optional current-State Conditions, explicit manual bypass, and condition-blocked automatic/manual Skips. It amends §3.1's definition field list, §3.4–§3.5's admission contracts, §4's current-State lookup exclusion (history lookups remain excluded), and the `Skip`/`SkipReason` and manual-admission type/interface listings below. The original implementation baseline is retained here; use the follow-on spec for those changed contracts.
**Follow-on:** [Observation Trigger transitions](observation-trigger-transitions.md) adds optional comparisons against the State immediately preceding an accepted Observation, carried as immutable evidence on that Observation Fact. It amends §3.2, §4's new-Fact-schema exclusion, and the Observation Fact/Trigger/history contracts while preserving admission and execution semantics.

## 1. Problem statement

**Who:** A Hearth household operator using the runtime HTTP API.

**What:** Core records and publishes accepted Observation and Entity Event evidence as durable Device Facts, but nothing can yet turn those facts into Commands.

**Why it matters:** Hearth cannot replace basic household behavior until an operator can define, execute, and inspect reliable fact-driven automations. Logs alone are insufficient: definitions, admission decisions, Run snapshots, Step outcomes, and skips must remain truthful after edits and restarts.

**Evidence:** `specs/device-facts.md` is implemented; `HEARTH_DEVICE_FACTS_V1` publishes both accepted fact families. `CONTEXT.md` already defined Automation, Trigger, Step, and Run, while `internal/modules/automations/` is absent and `docs/architecture.md` listed automation semantics as undecided before this decision.

## 2. Proposed solution

Add a cohesive `automations` product module with its own domain model, SQLite repository, Huma transport, and NATS Device Fact consumer. The module owns definition management, typed single-Fact matching, atomic admission, immutable Run snapshots, ordered Command execution, skips, history, and retention. `devices` continues to own Entities, support, Commands, and Device Fact publication.

One named durable JetStream consumer reads both Device Fact families. On its first creation it starts at the current stream tail. Thereafter it resumes from its acknowledgement floor, but a matching Fact whose envelope `emitted_at` is more than 30 seconds old records `stale_fact` skips instead of executing delayed Commands. The consumer acknowledges only after the automation admission transaction commits. It never executes a Command in the callback.

The HTTP API manages durable definitions and starts manual Runs. Automatic and manual Runs share one persisted execution path. A Run executes a snapshotted ordered sequence of static typed Commands and stops at the first failure. Unfinished Runs are marked interrupted on restart and are never replayed.

## 3. Scope and semantic contract

### 3.1 Automation definitions

- A definition has a name, explicit enabled state, 1–32 identified Triggers, and 1–32 identified ordered Steps.
- Trigger IDs and Step IDs are subject-safe slugs and unique within their respective lists.
- Triggers combine with OR. If one Fact matches several Triggers in one Automation, it still creates one outcome and records all matching Trigger IDs.
- Enablement controls only fact-triggered admission. An operator may manually start a disabled Automation.
- Create and full replacement validate every current Entity, Entity Event name, Operation, static parameter object, JSON Pointer, operator, and comparison operand atomically.
- Replacement and deletion require the caller's expected revision. Revision starts at 1 and increments by one on replacement.
- Deleting a definition does not interrupt an active snapshotted Run. Retained history remains queryable by the former Automation ID.
- Definition creation, replacement, and enablement do not scan Device Fact history. They affect ordinary evaluations that begin after their transaction commits; the redelivery caveat in §6.7 applies.

### 3.2 Typed Triggers

An **Entity Event Trigger** matches one Entity Event Fact by exact Entity ID and exact event name.

An **Observation Trigger** matches one Observation Fact by:

1. exact Entity ID;
2. a non-empty set containing `applied`, `unchanged`, or both; and
3. zero to eight comparisons against that Fact's `data.value`, all of which must match.

The transition follow-on additionally permits zero to eight `previous_comparisons` against optional `data.previous_value`; see that spec for precedence, compatibility, and evidence rules.

Each comparison uses an RFC 6901 JSON Pointer relative to `data.value`. The empty pointer selects the whole value. A pointer is at most 256 UTF-8 bytes and must use valid `~0` and `~1` escaping. Arrays use canonical non-negative decimal indices without leading zeroes except `0`; `-` is invalid for reads.

Operators are closed to `eq`, `ne`, `lt`, `lte`, `gt`, and `gte`:

- `eq` and `ne` accept any JSON operand. Values must have the same JSON type; JSON numbers compare by mathematical value, object member order is irrelevant, and array order is significant.
- ordering operators require both selected value and operand to be finite JSON numbers and compare them without binary floating-point loss.
- a missing pointer, invalid runtime array index, or incompatible runtime type makes the comparison false, including `ne`; it is not an admission error.
- comparisons never read another Fact, admission-time State, Entity metadata, history, time, or a Command outcome. The transition follow-on permits comparisons against the projection-time predecessor carried by the same Fact.

### 3.3 Steps and Commands

- A Step stores one exact Entity ID, Operation name, and normalized static JSON parameter object.
- Steps execute sequentially. The next Step starts only after the prior Command reaches the Operation's required terminal outcome.
- `satisfied` and `dispatched` are successful Step outcomes. Every Step must succeed for the Run to succeed.
- The first failed or interrupted Step stops the Run. Later Steps remain `not_attempted`.
- The automation engine does not retry, branch, compensate, roll back physical effects, or interpolate Fact values into parameters.
- Save-time validation does not promise execution-time eligibility. Normal Command validation still handles support changes, disablement, health, availability, identity conflicts, deadlines, and upstream outcomes.

### 3.4 Automatic admission

For one valid Device Fact, admission evaluates current enabled definitions and records at most one outcome per matching Automation:

1. Group all matching Trigger IDs by Automation.
2. Visit matching Automations by Automation ID for deterministic behavior.
3. If a durable `(fact_id, automation_id)` receipt already exists, report a duplicate and schedule no worker; return the retained outcome as well when history has not yet been pruned.
4. If `now - emitted_at > 30s`, record `stale_fact`.
5. Otherwise, if that Automation already has a running Run, record `automation_busy`.
6. Otherwise, atomically create a running Run, immutable definition snapshot, Fact summary, matched Trigger IDs, and all initial Step rows.
7. Commit all outcomes for the Fact in one automation-owned SQLite transaction.
8. Register workers only after commit.

Freshness precedes the busy check, so an old Fact always explains itself as `stale_fact`. The same Fact may start one Run in each of many matching Automations. There is deliberately no process-wide Run cap or worker queue; cross-Automation fan-out is an accepted resource risk.

Disabled and unmatched Automations write no history. A Skip is not a Run and never executes later.

### 3.5 Manual admission

- `POST /v1/automations/{automation_id}/runs` starts a new Run from the current definition snapshot, even when disabled.
- Each accepted POST creates a distinct Run. There is no manual idempotency key; clients must not automatically retry an ambiguous request.
- A manual start while that Automation has a running Run returns `409 automation_busy` and writes no Skip.
- Admission first checks both the automation gate and `AutomationDevices.CommandAdmissionOpen`. If either is already closed, return `503 admission_unavailable` and create no Run.
- Shutdown closes automation admission before Command admission. If Command admission races closed after the preflight and after the Run commits, execution records `interrupted/core_stopping`; the cross-module gates do not claim an atomic check-and-admit operation.
- Manual Runs have source `manual`, no Device Fact summary, and no matched Trigger IDs.

### 3.6 Execution and restart

- Before invoking a Step, persist `running`, `started_at`, and newly reserved Command and Correlation IDs.
- Call `devices.Service.ExecuteCommand` outside every automation transaction with a process-owned context detached from the HTTP request and NATS callback.
- A `devices.CommandExecutionError` proves a Command was created. Read that Command and verify its ID, correlation, Entity, Operation, and parameters before exposing a link or adopting its durable status.
- A verified terminal Command determines the Step's terminal outcome. A missing, unverifiable, or nonterminal Command after `CommandExecutionError` is an executor fault: persist the Step and Run as `interrupted/executor_fault` when possible, expose no unverified link, close automation admission, and start no later Step. The separately owned Command may still reach its own terminal outcome.
- If a Step-start, Step-completion, or Run-completion write cannot be durably committed, do not invent progress or retry the Command. Release the worker, close automation admission as an executor fault, and leave startup interruption to classify any still-running durable row after restart.
- Never expose a merely reserved Command ID as a public link.
- On startup, preserve terminal Steps; mark running Steps and Runs `interrupted/core_restarted`; leave later Steps `not_attempted`; never redispatch or infer success from Command history.
- During drain, a Run may finish its current Command. If Command admission closes before its next Step, mark that Step and Run `interrupted/core_stopping` with no Command link.

## 4. Non-goals

- Browser UI.
- Cron, schedules, timers, sunrise/sunset, multi-Fact conditions, State/history lookups, Command-outcome Triggers, expression languages, or Fact-to-Command interpolation.
- Parallel Steps, per-Step retry policy, delay Steps, branching, compensation, cancellation, or resumable Runs.
- Definition import/export, bulk mutation, per-Automation retention, quotas, a global Run cap, or a durable execution queue.
- Manual-run idempotency keys.
- Replaying retained Facts after definition edits or intentionally scanning Observation/Entity Event history.
- Authentication or authorization changes; the API retains Hearth's current deployment boundary.
- Except for the additive optional predecessor evidence defined by the transition follow-on: new Device Fact schemas, subjects, streams, outbox behavior, or publisher changes.

## 5. Ownership and dependencies

- `devices` owns Entity identity/support, Observation and Entity Event evidence, Device Fact publication, Command creation, and Command outcomes.
- `automations` owns definitions, matching, admission, Runs, Steps, Skips, execution history, and history pruning.
- `automations/api` owns HTTP DTOs, Huma registration, pagination cursors, and domain-to-HTTP error mapping.
- `automations/nats` owns its consumer resource, strict Device Fact wire decoding, subject/payload checks, trace continuation, ack disposition, and mapping into automation-owned inputs.
- `internal/app/hearthd` owns concrete construction, shared SQLite/NATS dependencies, startup/readiness/shutdown ordering, and hourly maintenance.
- Neither module reads the other's tables. No SQLite transaction calls another module, publishes NATS, or waits for a Command.

Dependency direction stays acyclic:

```text
contracts/v1 ← natswire ← devices/nats ← hearthd → automations/nats → automations
                               ↑                         ↓
                            devices ← AutomationDevices ┘
```

`automations/nats` accepts the Device Fact stream name from `hearthd`; it does not import `devices/nats` merely to obtain a constant. The small durable-consumer loop remains module-owned in this slice rather than prematurely extracting the unexported devices transport helper.

## 6. Types

### 6.1 Domain types

Owned by new files under `internal/modules/automations/`:

```go
type AutomationID string     // aut_<UUIDv7>
type RunID string  // arn_<UUIDv7>; run_ remains Adapter runtime
type SkipID string // ask_<UUIDv7>

type TriggerID string // subject-safe slug, 1–63 bytes
type StepID string    // subject-safe slug, 1–63 bytes

type TriggerKind string
const (
    TriggerKindObservation TriggerKind = "observation"
    TriggerKindEntityEvent TriggerKind = "entity_event"
)

type ComparisonOperator string
const (
    ComparisonEqual              ComparisonOperator = "eq"
    ComparisonNotEqual           ComparisonOperator = "ne"
    ComparisonLessThan           ComparisonOperator = "lt"
    ComparisonLessThanOrEqual    ComparisonOperator = "lte"
    ComparisonGreaterThan        ComparisonOperator = "gt"
    ComparisonGreaterThanOrEqual ComparisonOperator = "gte"
)

type ObservationComparison struct {
    Pointer  string          // RFC 6901, relative to data.value; empty selects root
    Operator ComparisonOperator
    Operand  json.RawMessage // exactly one normalized JSON value
}

type ObservationTrigger struct {
    EntityID    devices.EntityID
    Dispositions []devices.ObservationDisposition // 1–2 unique: applied, unchanged
    Comparisons []ObservationComparison           // 0–8, all must match
}

type EntityEventTrigger struct {
    EntityID  devices.EntityID
    EventName devices.EntityEventName
}

type Trigger struct {
    ID          TriggerID
    Kind        TriggerKind
    Observation *ObservationTrigger // set iff KindObservation
    EntityEvent *EntityEventTrigger // set iff KindEntityEvent
}

type Step struct {
    ID            StepID
    EntityID      devices.EntityID
    OperationName devices.OperationName
    Parameters    devices.CommandParameters // normalized static JSON object
}

type Definition struct {
    Name     string // 1–200 runes, trimmed; not unique
    Enabled  bool
    Triggers []Trigger // 1–32, IDs unique
    Steps    []Step    // 1–32, IDs unique and execution ordered
}

type Record struct {
    ID         AutomationID
    Revision   int64 // >=1
    Definition Definition
    CreatedAt  time.Time
    UpdatedAt  time.Time
}
```

The definition's strict JSON representation uses a discriminator and family-specific object, never nullable fields:

```json
{
  "name": "Button turns on light",
  "enabled": true,
  "triggers": [
    {
      "id": "single_press",
      "kind": "entity_event",
      "entity_id": "ent_<button>",
      "event_name": "single_press"
    },
    {
      "id": "occupied_and_warm",
      "kind": "observation",
      "entity_id": "ent_<sensor>",
      "dispositions": ["applied"],
      "comparisons": [
        {"value_pointer": "/temperature", "operator": "gt", "operand": 20},
        {"value_pointer": "/occupied", "operator": "eq", "operand": true}
      ]
    }
  ],
  "steps": [
    {
      "id": "light_on",
      "entity_id": "ent_<light>",
      "operation": "set",
      "parameters": {"value": true}
    }
  ]
}
```

Unknown JSON fields are rejected. Encoded definition JSON is at most 64 KiB after normalization.

### 6.2 Fact input and history

```go
type DeviceFactFamily string
const (
    DeviceFactObservation DeviceFactFamily = "observation"
    DeviceFactEntityEvent DeviceFactFamily = "entity_event"
)

type ObservationFact struct {
    FactID        devices.DeviceFactID
    ObservationID devices.ObservationID
    EntityID      devices.EntityID
    Disposition   devices.ObservationDisposition
    Value         devices.Value
    EmittedAt     time.Time
}

type EntityEventFact struct {
    FactID    devices.DeviceFactID
    EventID   devices.EntityEventID
    EntityID  devices.EntityID
    Name      devices.EntityEventName
    EmittedAt time.Time
}

type DeviceFact struct {
    Family      DeviceFactFamily
    Observation *ObservationFact // exactly one family payload is set
    EntityEvent *EntityEventFact
}

type DeviceFactSummary struct {
    FactID        devices.DeviceFactID
    Family        DeviceFactFamily
    EntityID      devices.EntityID
    Variant       string // observation disposition or Entity Event name
    CausationID   string // obs_ or evt_
    ObservationValue devices.Value // nil for Entity Event
    EmittedAt     time.Time
}

type AdmissionOutcome struct {
    MatchedAutomations int
    StartedRuns        int
    RecordedSkips      int
    DuplicateOutcomes  int
}
```

History copies the normalized Observation value so a retained outcome remains explainable after the seven-day Device Fact stream and Observation history are pruned.

### 6.3 Runs, Steps, and Skips

```go
type RunSource string
const (
    RunSourceDeviceFact RunSource = "device_fact"
    RunSourceManual     RunSource = "manual"
)

type RunStatus string
const (
    RunRunning     RunStatus = "running"
    RunSucceeded   RunStatus = "succeeded"
    RunFailed      RunStatus = "failed"
    RunInterrupted RunStatus = "interrupted"
)

type StepStatus string
const (
    StepNotAttempted StepStatus = "not_attempted"
    StepRunning      StepStatus = "running"
    StepSatisfied    StepStatus = "satisfied"
    StepDispatched   StepStatus = "dispatched"
    StepFailed       StepStatus = "failed"
    StepInterrupted  StepStatus = "interrupted"
)

type StepAttempt struct {
    Position              int // zero-based, immutable
    StepID                StepID
    Status                StepStatus
    ReservedCommandID     *devices.CommandID
    ReservedCorrelationID *devices.CorrelationID
    VerifiedCommandID     *devices.CommandID // only after ownership verification
    FailureCode           *string
    StartedAt             *time.Time
    CompletedAt           *time.Time
}

type Run struct {
    ID                RunID
    AutomationID      AutomationID
    AutomationName    string
    Revision          int64
    Snapshot          Definition
    Source            RunSource
    Fact              *DeviceFactSummary // non-nil iff source=device_fact
    MatchedTriggerIDs []TriggerID        // empty iff source=manual
    Status            RunStatus
    FailureCode       *string
    StartedAt         time.Time
    CompletedAt       *time.Time
    Steps             []StepAttempt
}

type SkipReason string
const (
    SkipBusy      SkipReason = "automation_busy"
    SkipStaleFact SkipReason = "stale_fact"
)

type Skip struct {
    ID              SkipID
    AutomationID    AutomationID
    AutomationName  string
    Revision        int64
    Fact            DeviceFactSummary
    MatchedTriggers []Trigger // immutable matching Trigger snapshots
    Reason          SkipReason
    SkippedAt       time.Time
}
```

Constructors and repository decoders reject impossible combinations, unknown statuses/reasons, zero timestamps, malformed IDs, a manual Run with Fact data, a fact-backed Run without Fact data, and a Skip without matched Triggers.

### 6.4 Configuration

Focused shape for `internal/app/hearthd/config.go`:

```diff
 type Config struct {
     HouseholdTimezone string `yaml:"household_timezone"`
     HTTPAddr          string `yaml:"http_addr"`
     NATSURL           string `yaml:"nats_url"`
     SQLitePath        string `yaml:"sqlite_path"`
     ObservationRetention time.Duration `yaml:"observation_retention"`
+    AutomationHistoryRetention time.Duration `yaml:"automation_history_retention"`
 }
+
+const DefaultAutomationHistoryRetention = 30 * 24 * time.Hour
+const MinimumAutomationHistoryRetention = 8 * 24 * time.Hour
+const FactMaximumAge = 30 * time.Second
```

`AutomationHistoryRetention == 0` selects 30 days; non-zero values below eight days fail config validation. Fact maximum age is deliberately a fixed semantic constant, not operator configuration. `configs/hearthd.example.yaml` documents the retention key.

## 7. Persistence

Add an `automations` sqlc target with queries owned by the module. Generated types never cross the repository seam. Edit the single development migration `00001_initial.sql`; Hearth has no deployment compatibility requirement.

### 7.1 Tables

| Table | Required invariants |
|---|---|
| `automations` | `id` PK; revision >=1; normalized strict definition JSON; created/updated UTC timestamps. Replacement and deletion compare expected revision atomically. |
| `automation_history` | ID PK (`arn_` Run or `ask_` Skip); Automation ID and copied name without an FK to `automations`; kind; revision; recorded time; nullable Fact summary; Run-only definition snapshot/source/status/failure/completion; Skip-only matched Trigger snapshots/reason. Partial unique running Run per Automation. Partial unique `(fact_id, automation_id)` for fact-backed outcomes while retained. |
| `automation_run_steps` | `(run_id, position)` PK; FK to history with cascade; Step ID/status/failure/timestamps; reserved identity pair and nullable verified Command ID; parent must decode as a Run. |
| `automation_fact_receipts` | `(fact_id, automation_id)` PK for every matched outcome; outcome kind and original history ID; written atomically with the Run/Skip and retained after history pruning. No row for an unmatched Automation or unmatched Fact. |

The absence of an FK from history to definitions is intentional: definitions may be hard-deleted while active Runs and retained history survive. There is no FK to `observations`, `entity_events`, or `commands`; their retention and ownership differ. Copied snapshots, matched Trigger snapshots, and Fact summaries are immutable explanation, not executable work.

Matched Fact receipts are retained indefinitely so a late outbox republication cannot execute or record the same `(fact_id, automation_id)` again after its history entry is pruned. Unmatched Facts still write nothing. Consequently, a crash after an unmatched evaluation but before broker acknowledgement can redeliver that Fact after a definition edit and evaluate it against the newer definition. This bounded at-least-once race is accepted to avoid a durable write for every published Fact.

### 7.2 Transaction boundaries

- Create, replace, and delete each own one short SQLite transaction.
- One Device Fact admission owns one transaction covering every matching Automation's receipt, Run/Skip decision, immutable explanation, and initial Step rows.
- A worker is registered only after that transaction commits. Commit failure schedules nothing.
- Step start, Step completion, and Run completion are separate short writes around the external Command call.
- Retention deletes terminal Runs and Skips in bounded batches; running Runs are never pruned.
- No transaction invokes `AutomationDevices`, NATS, Huma, or a worker wait.

## 8. Interfaces

### 8.1 Devices-facing consumer seam

The interface is defined by `automations`, its consumer:

```go
type AutomationDevices interface {
    ValidateObservationTrigger(context.Context, devices.EntityID) error
    ValidateEntityEventTrigger(context.Context, devices.EntityID, devices.EntityEventName) error
    ValidateCommand(context.Context, devices.CommandInput) (devices.CommandParameters, error)
    CommandAdmissionOpen() bool
    ExecuteCommand(context.Context, devices.CommandInput) (devices.CommandResult, error)
    GetCommand(context.Context, devices.CommandID) (devices.CommandRecord, error)
}
```

Add the two read-only validation methods to `devices.Service`. Observation validation requires an existing stateful Entity. Entity Event validation requires an existing event-source Entity currently supporting the exact name. Neither check requires enablement, availability, or a healthy owner. Comparison pointer existence cannot be proven against future values; save-time validation covers pointer syntax and operand/operator compatibility instead.

### 8.2 Repository and service

```go
type Repository interface {
    CreateAutomation(context.Context, Definition) (Record, error)
    GetAutomation(context.Context, AutomationID) (Record, error)
    ListAutomations(context.Context, ListAutomationsParams) (Page[Record], error)
    ReplaceAutomation(context.Context, AutomationID, int64, Definition) (Record, error)
    DeleteAutomation(context.Context, AutomationID, int64) error
    AdmitDeviceFact(context.Context, DeviceFact, time.Time) (AdmissionResult, error)
    AdmitManualRun(context.Context, AutomationID, time.Time) (Run, error)
    MarkStepRunning(context.Context, StepStart) error
    CompleteStep(context.Context, StepCompletion) error
    CompleteRun(context.Context, RunCompletion) error
    GetHistoryEntry(context.Context, AutomationID, string) (HistoryEntry, error)
    ListHistory(context.Context, ListHistoryParams) (Page[HistorySummary], error)
    InterruptActiveRuns(context.Context, time.Time, string) error
    DeleteHistoryBefore(context.Context, time.Time, int) (int64, error)
}

type Dependencies struct {
    Logger           *slog.Logger
    Now              func() time.Time
    NewAutomationID  func() (AutomationID, error)
    NewRunID         func() (RunID, error)
    NewSkipID        func() (SkipID, error)
    NewCommandID     func() (devices.CommandID, error)
    NewCorrelationID func() (devices.CorrelationID, error)
}

func NewService(
    repository Repository,
    devices AutomationDevices,
    dependencies Dependencies,
) *Service

func (service *Service) CreateAutomation(context.Context, Definition) (Record, error)
func (service *Service) GetAutomation(context.Context, AutomationID) (Record, error)
func (service *Service) ListAutomations(context.Context, ListAutomationsParams) (Page[Record], error)
func (service *Service) ReplaceAutomation(context.Context, AutomationID, int64, Definition) (Record, error)
func (service *Service) DeleteAutomation(context.Context, AutomationID, int64) error
func (service *Service) StartManualRun(context.Context, AutomationID) (Run, error)
func (service *Service) ReceiveDeviceFact(context.Context, DeviceFact) (AdmissionOutcome, error)
func (service *Service) GetHistoryEntry(context.Context, AutomationID, string) (HistoryEntry, error)
func (service *Service) ListHistory(context.Context, ListHistoryParams) (Page[HistorySummary], error)
func (service *Service) StopAdmission()
func (service *Service) AdmissionOpen() bool
func (service *Service) Drain(context.Context) error
func (service *Service) InterruptActiveRuns(context.Context, time.Time) error
func (service *Service) PruneHistory(ctx context.Context, now time.Time) error
```

`StopAdmission` closes admission without waiting. `Drain` closes admission and joins admitted Runs; cancellation stops waiting without canceling Commands or reopening admission.

`Service` owns admission gating and per-Automation active-worker registration. The database partial unique index is the final busy guard across racing HTTP and NATS calls. No global worker semaphore exists.

Domain sentinels include `ErrAutomationNotFound`, `ErrHistoryNotFound`, `ErrInvalidAutomation`, `ErrRevisionConflict`, `ErrAutomationBusy`, `ErrAdmissionUnavailable`, `ErrInvalidDeviceFact`, and `ErrExecutorFault`. Errors retain identity through wrapping and are mapped only at transport boundaries.

### 8.3 NATS consumer

```go
const DeviceFactConsumerName = "hearthd-automation-device-facts-v1"
const DeviceFactConsumerAckWait = 5 * time.Second
const DeviceFactConsumerNakDelay = 1 * time.Second
const DeviceFactConsumerMaxAckPending = 1
const DeviceFactAdmissionTimeout = 2 * time.Second

type DeviceFactReceiver interface {
    ReceiveDeviceFact(context.Context, automations.DeviceFact) (automations.AdmissionOutcome, error)
}

func ProvisionDeviceFactConsumer(
    context.Context,
    jetstream.JetStream,
    string, // Device Fact stream name supplied by hearthd
) (jetstream.Consumer, error)

func StartDeviceFactConsumer(
    context.Context,
    jetstream.Consumer,
    DeviceFactReceiver,
    *contractsv1.Validator,
    *slog.Logger,
) (*platformnats.Consumer, error)
```

The returned platform consumer supplies `Active`, `Stop`, `Drain(context.Context) error`, and `Closed`; see [Managed durable consumer](managed-consumer.md).

Expected JetStream consumer configuration:

- durable/name `hearthd-automation-device-facts-v1`;
- filter `natswire.DeviceFactWildcard()` (`hearth.v1.core.fact.>`);
- `DeliverNewPolicy` on first creation; an existing durable consumer resumes its ack floor;
- explicit ack, instant replay, `AckWait=5s`, `MaxAckPending=1`, unlimited redelivery, and one-second delayed negative acknowledgements;
- a two-second admission context so a live callback either commits before AckWait or negatively acknowledges before the broker creates concurrent delivery;
- no per-Trigger consumers and no consumer recreation on definition edits.

For each message, the transport:

1. restores trace context and opens a bounded operation context;
2. parses the Device Fact subject and accepts only Observation and Entity Event routes;
3. decodes against the route's exact embedded schema;
4. validates canonical IDs, family/variant, `Nats-Msg-Id == envelope.id`, exact `causation_id == data.observation_id|data.event_id`, and subject/payload agreement;
5. maps `emitted_at` plus the family payload into `automations.DeviceFact`;
6. calls `ReceiveDeviceFact` synchronously for admission only;
7. `Ack()`s after successful admission, including unmatched and duplicate outcomes;
8. `NakWithDelay()`s transient admission/storage failures;
9. `Term()`s deterministic malformed wire input and logs a safe fixed error code.

A consumer or executor fault closes automation admission and fails readiness until process restart. Command execution never runs in the consumer callback.

### 8.4 HTTP API

`automations/api` declares its own consumer-facing service interface and explicit request/response DTOs. Domain, sqlc, and NATS types do not become public transport models.

| Method and route | Contract |
|---|---|
| `POST /v1/automations` | Strict definition → `201`, record body, and `Location`. |
| `GET /v1/automations` | ID-ascending keyset page; summaries include complete current definition. |
| `GET /v1/automations/{automation_id}` | Current record or `404`. |
| `PUT /v1/automations/{automation_id}` | `{expected_revision, definition}` full replacement → `200`; stale revision `409`. |
| `DELETE /v1/automations/{automation_id}?expected_revision=N` | Hard-delete definition → `204`; stale revision `409`; active Run continues. |
| `POST /v1/automations/{automation_id}/runs` | Empty body → `202` Run summary and history `Location`; busy `409`; closed admission `503`. |
| `GET /v1/automations/{automation_id}/history` | Newest-first keyset page of Run and Skip summaries, including after definition deletion. |
| `GET /v1/automations/{automation_id}/history/{entry_id}` | Run snapshot and ordered Steps or Skip detail; parent mismatch `404`. |

Collections use Hearth's existing opaque, endpoint-scoped keyset conventions: default 50, limits 1–200, no totals, no snapshot guarantee. History key is `(recorded_at,id)` descending. Automation key is ID ascending. An empty history page for a deleted Automation ID is indistinguishable from an unknown ID and returns `200` with no items; a retained entry remains directly queryable.

Public Command links appear only for verified Commands. Validation errors are `400` RFC 9457 problems without internal database or upstream text. Stable operation IDs and `Automations` tags are asserted through generated OpenAPI inspection tests.

## 9. Lifecycle and observability

### 9.1 Startup

Preserve existing startup ordering and add automations at these exact seams:

1. Validate config; open and migrate SQLite.
2. Interrupt active Commands, then interrupt active Automation Runs, before connecting to NATS.
3. Connect the shared Core NATS connection; compile contracts; provision/validate `HEARTH_DEVICE_FACTS_V1`; start the Device Fact relay.
4. Construct devices, then automations repository/service with the devices-facing seam.
5. Provision existing Observation/Entity Event resources and the automation Device Fact consumer.
6. Start existing request/reply transports and the shared history pruning worker after startup recovery. Its startup sweep runs in the background, independently of readiness.
7. Start the automation Device Fact consumer before starting the inbound Observation and Entity Event consumers.
8. Construct readiness and HTTP, then bind the listener. Pruning never gates serving.

A consumer created for the first time begins at the then-current tail. A previously created consumer resumes from its durable ack floor. Readiness requires its exact broker configuration, `Active()`, and open automation admission; it does not require an empty backlog.

### 9.2 Shutdown

1. Drain/stop the automation Device Fact consumer so no new automatic admission enters.
2. Close automation admission, then close device Command admission.
3. Wait for Automation workers, then device Command workers.
4. Cancel dependencies and join the history pruning worker before SQLite closes; shut down HTTP and the health supervisor according to the existing deadline behavior.
5. Drain inbound Entity Event and Observation consumers.
6. Drain the Device Fact relay within its existing deadline.
7. Drain request/reply transports, shared NATS, and SQLite.

The process-owned execution context outlives HTTP and NATS caller cancellation but ends before dependencies are destroyed.

### 9.3 Retention

`automation_history_retention` defaults to 30 days and must be at least eight days. App assembly injects the effective window through `Dependencies.HistoryRetention`; the module enforces the eight-day minimum and derives the cutoff from the UTC sweep time passed to `PruneHistory`. The app-owned shared worker performs one background startup pass, then another pass one hour after each preceding pass completes, without overlapping or replaying missed ticks. The service prunes terminal Runs and Skips strictly older than `now-retention` in batches of 500, rechecking cancellation between batches. Active Runs and matched-Fact receipts are never selected. A newly configured window takes effect on the restart's startup pass. The service returns failures without logging; the app logs one safe `core.automation_history_prune_failed` record per failed module pass and retries next hour without failing readiness.

### 9.4 Logging

Add stable events to `docs/logging.md`, including:

- `automation.created`, `automation.replaced`, `automation.deleted`;
- `automation.run_started`, `automation.run_completed`, `automation.run_interrupted`;
- `automation.skipped` with fixed `reason` (`automation_busy` or `stale_fact`);
- `automation.fact_invalid`, `automation.fact_processing_failed`;
- `automation.executor_fault`;
- `core.automation_history_prune_failed`.

Never log definition JSON, Fact values, Command parameters, full subjects, raw envelopes, or upstream error text. IDs, family, disposition/event name, revision, status, reason, and fixed error codes are safe structured attributes.

## 10. Project layout

```text
CONTEXT.md                                      # modify — sharpen Trigger and add typed Triggers/Automation Skip
specs/
├── automations.md                             # new — implementation-ready source of truth
└── entity-event-automations.md                # modify — mark superseded; retain historical rationale
docs/
├── adr/0021-drive-automations-from-device-facts.md # new — durable fact-consumer boundary and freshness choice
├── architecture.md                            # modify — move automation semantics out of Undecided
└── logging.md                                 # modify — stable automation event vocabulary
configs/hearthd.example.yaml                   # modify — automation history retention
sqlc.yaml                                      # modify — add automations dbqueries/dbsqlc target
mise.toml                                      # modify — generated-code check covers automations output
internal/platform/db/migrations/
└── 00001_initial.sql                          # modify — definitions, history, Steps, and matched-Fact receipts
internal/modules/devices/
└── automation_validation.go                   # new — read-only Trigger source validation methods
internal/modules/automations/
├── ids.go                                     # new — aut_/arn_/ask_ identity constructors/parsers
├── model.go                                   # new — definitions, Triggers, Runs, Steps, Skips, Fact inputs
├── errors.go                                  # new — stable domain error classes
├── definition.go                              # new — strict normalization and cross-module validation
├── automation-definition.schema.json          # new — embedded strict persisted-definition shape
├── comparison.go                              # new — RFC 6901 lookup and typed comparison semantics
├── admission.go                               # new — fact/manual admission and post-commit worker registration
├── execution.go                               # new — ordered Command execution and ownership verification
├── history.go                                 # new — immutable summaries/details and retention behavior
├── lifecycle.go                               # new — admission gate, worker join, restart interruption
├── repository.go                              # new — domain-oriented persistence seam and SQLite adapter
├── sqlite_repository.go                       # new — transaction setup and row decoding invariants
├── dbqueries/
│   └── automations.sql                        # new — sqlc source queries
├── dbsqlc/                                    # generated — module-private query implementation
├── api/
│   ├── register.go                            # new — eight Huma operations
│   ├── models.go                              # new — strict API input/output DTOs
│   ├── errors.go                              # new — RFC 9457 mapping
│   └── pagination.go                          # new — scoped opaque cursors
└── nats/
    ├── resources.go                           # new — exact durable consumer provision/validation
    ├── consumer.go                            # new — lifecycle, ack, retry, permanent rejection
    └── device_fact_mapping.go                 # new — strict two-family wire-to-domain mapping
internal/app/hearthd/
├── config.go                                  # modify — retention setting and validation
├── run.go                                     # modify — composition, startup, maintenance, and drain
├── http_handler.go / runtime_readiness.go     # modify — API registration and readiness dependencies
├── automation_integration_test.go             # new — whole fact-to-Command vertical slice
├── automation_recovery_integration_test.go    # new — durable resume, stale skip, restart interruption
└── automation_readiness_test.go                # new — consumer/admission health gates
```

`automations` remains internally flat until independent change pressure justifies capability subpackages; only delivery adapters (`api`, `nats`) and generated persistence output are nested.

## 11. Deliverables

| ID | Outcome | Effort | Owning paths | Depends on | Acceptance |
|---|---|---:|---|---|---|
| **D1** | Durable definitions, typed Trigger validation/comparison, revisioned CRUD, schema, repository, generation, and glossary foundation | L | `internal/modules/{automations,devices}`, migration, `sqlc.yaml`, `mise.toml`, `CONTEXT.md` | Device Facts implemented | A1–A3 |
| **D2** | Manual admission, immutable Runs/Steps, sequential Command execution, interruption, history API, and pruning | L | `internal/modules/automations/{admission,execution,history,lifecycle,api}`, `internal/app/hearthd/config.go` | D1 | A4–A7 |
| **D3** | Both-family durable consumer, exact wire validation, atomic automatic Run/Skip admission, freshness, deduplication, and readiness | L | `internal/modules/automations/nats`, `internal/modules/automations/admission.go`, `internal/app/hearthd/{run,server}.go` | D1,D2 | A8–A12 |
| **D4** | Whole-system simulator proof, restart/drain/reconnect validation, logging/architecture/operator docs, and supersession cleanup | L | `internal/app/hearthd/*automation*_test.go`, `docs/`, `configs/`, `specs/entity-event-automations.md` | D2,D3 | A13–A15 |

## 12. Acceptance criteria

- [ ] **A1 — Strict definitions:** Real SQLite and HTTP tests prove 1–32 bounds, unique slug IDs, 64 KiB limit, unknown-field rejection, typed-family exclusivity, disposition bounds, 0–8 comparison bounds, pointer/operator/operand rules, and normalized static Command parameters. This protects atomic definition validity and fails if malformed or partially validated definitions commit.
- [ ] **A2 — Reference validation:** Create/replace rejects missing or wrong-kind Trigger Entities, unsupported Entity Event names, missing action Entities, unsupported Operations, and invalid parameters, while allowing currently disabled/unavailable Entities. This protects current-reference integrity and fails if save-time validation is bypassed or control eligibility is incorrectly required.
- [ ] **A3 — Revisioned definitions:** CRUD, keyset listing, expected-revision replacement/deletion, and hard deletion behave through Huma and real SQLite. This protects concurrent edits and fails on last-write-wins or definitions that survive successful deletion.
- [ ] **A4 — Manual admission:** Each successful POST creates a distinct snapshotted Run even when disabled; busy returns 409 without history; closed admission returns 503. This protects the explicitly non-idempotent manual contract and fails if callers share Runs or bypass busy/admission gates.
- [ ] **A5 — Ordered execution:** A two-Step Run never starts Step 2 before Step 1 reaches `satisfied` or `dispatched`; first failure leaves later Steps `not_attempted`; no engine retry occurs. This protects ordered effects and fails on overlap, retry, or continuation after failure.
- [ ] **A6 — Truthful Command links:** Reserved identities remain private; created Commands are linked only after ID/correlation/Entity/Operation/parameter verification; collisions cannot adopt unrelated Commands. This protects cross-module ownership and fails if reservation is mistaken for durable creation.
- [ ] **A7 — Interruption, deletion, and retention:** Caller disconnect does not cancel admitted work; deleting a definition lets an active Run continue and leaves history queryable; startup and drain mark unfinished work with the correct reason without replay; startup and hourly pruning remove only terminal history older than the configured cutoff and preserves matched-Fact receipts. This protects durable truth and fails on cascading deletion, invented success, redispatch, pruning active Runs, or lost deduplication.
- [ ] **A8 — Comparison semantics:** Independent examples and property tests cover RFC 6901 escapes/arrays, root selection, missing-path false behavior (including `ne`), JSON type equality, object order, array order, and exact decimal ordering without float loss. This protects typed matching and fails on lexical-number, float-rounding, or missing-path mistakes.
- [ ] **A9 — Fact transport validation:** Embedded NATS tests prove both exact schemas/routes reach admission, while malformed schema, unsafe identity, unknown family/variant, `causation_id` unequal to the payload Observation/Event ID, `Nats-Msg-Id` mismatch, and subject/payload mismatch terminate without admission. Transient storage failures redeliver. This protects the trust boundary and fails if malformed or mismatched Facts become actions.
- [ ] **A10 — Atomic admission:** Injected failures before commit leave no partial receipt, Run, Skip, or Step and schedule no worker. One Fact produces at most one outcome per matching Automation; multiple matching Triggers group into one outcome and Skips preserve immutable matching Trigger snapshots. A duplicate after history pruning is suppressed by its retained receipt. This protects transactional admission and fails on partial history, unexplained Skips, or duplicate work.
- [ ] **A11 — Busy, stale, and fan-out:** Deterministic concurrency produces one active Run plus `automation_busy` skips for the same Automation; Facts over 30 seconds use `emitted_at` and produce `stale_fact` before busy; one fresh Fact may start all matching non-busy Automations without a global cap. This protects the chosen contention/freshness contract and fails on queueing, source-clock use, or hidden throttling.
- [ ] **A12 — Consumer recovery:** First provisioning starts at the current tail; reconnect/restart resumes the durable ack floor; a five-second crash-window Fact is recovered; an older-than-30-second backlog Fact records per-Automation stale skips; duplicate broker storage creates no second outcome. This protects age-bounded durable recovery and fails on DeliverAll first-start replay, tail-jumping restarts, or duplicate execution.
- [ ] **A13 — Readiness and drain:** Readiness requires exact consumer resources, active consumption, and open automation/Command admission. Shutdown stops new fact admission before closing Command admission, joins automation workers before device workers, and tears down dependencies last. This protects lifecycle safety and fails on callbacks or workers outliving dependencies.
- [ ] **A14 — Whole vertical slice:** A real SDK simulator publishes an accepted Entity Event and an accepted Observation; the relay publishes both Device Facts; HTTP-created Automations match them; each runs a typed light Command; API history shows immutable matched Trigger IDs, Fact evidence, ordered Steps, and verified Command links. This protects the user-visible feature and fails if any producer→broker→admission→Command boundary is disconnected.
- [ ] **A15 — Repository validation:** `mise run validate` regenerates both sqlc packages, formats/tidies, passes race-enabled unit and integration tests, lint, vet, entity-type fixtures, and leaves only intended documentation/generated diffs. Logging tests prove payloads and parameters are absent.

## 13. Test strategy

| Layer | Behavior and plausible defect | Test approach / oracle |
|---|---|---|
| Pure unit | Trigger normalization, pointer parsing, comparison semantics, grouping | Table/property tests against RFC 6901 examples and the contract in §3.2; no production helper as oracle. |
| Service unit | Validation orchestration, manual busy, worker lifecycle, Step stopping | Inject domain-oriented repositories/devices, clocks, IDs, and synchronization barriers. Every test states the behavior protected and defect detected. |
| SQLite integration | Revision compare-and-swap, partial unique busy guard, `(fact_id,automation_id)` dedupe, atomic multi-Automation admission, deletion/history, pruning | Real temporary SQLite with the actual migration and generated sqlc; failure injection at transaction boundaries. |
| Huma transport | Eight operations, strict body/query validation, statuses, headers, cursors, Problem Details, operation IDs | `httptest` against module registration; derive expectations from §8.4 rather than service implementation. |
| Embedded NATS | Provision validation, DeliverNew first creation, durable resume, ack/nak/term, duplicate and stale behavior, both schemas | Real embedded JetStream and controlled clocks; inspect consumer ack floor and durable rows, never sleep as an oracle. |
| Application integration | Full Device Fact→Automation→Command flow, startup interruption, drain, readiness, retention | Real `hearthd.Run`, SQLite, embedded NATS, SDK simulator, HTTP API, and deterministic barriers. |
| Race/repetition | Concurrent Facts/manual starts and shutdown | Race detector plus barrier-controlled repeated tests; no timing-only assertions. |

Where practical, use isolated negative controls or mutation testing for comparison boundaries, revision checks, dedupe constraints, ack-after-commit, and Step ordering. Tests must fail when those faults are introduced, not merely execute lines.

## 14. Risks and mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---:|---:|---|
| No global Run cap allows one Fact to fan out across an unbounded definition set and exhaust process resources. | Medium | High | Explicitly document as accepted; retain one-Run-per-Automation; expose stable logs/readiness; revisit when observed load justifies a cap or queue. |
| A 30-second freshness bound suppresses legitimate actions after longer outages. | Medium | Medium | Record `stale_fact` per matching Automation with Fact identity/time; keep the value fixed and visible; revisit based on household evidence. |
| Manual HTTP retry executes duplicate physical effects. | Medium | High | Return `202` with Location promptly; document no automatic retries; add idempotency only when clients require it. |
| Snapshotting Observation values increases SQLite growth. | Medium | Medium | 30-day configurable pruning with eight-day minimum; bounded Fact transport; monitor database growth before adding per-Automation retention. |
| Static validation goes stale when Entity support changes. | High | Medium | Reuse normal Command execution-time validation and preserve exact failure history; definitions are not executable authority over devices. |
| Crash after unmatched evaluation but before ack may re-evaluate against a newer definition. | Low | Medium | Explicitly accept the at-least-once boundary; matched outcomes remain deduplicated without writing a receipt for every unmatched Fact. |
| Indefinitely retained matched-Fact receipts grow with every automatic outcome. | Medium | Low | Keep receipts compact and separate from history; measure growth before designing a safe expiry tied to stronger publisher guarantees. |
| Separate module-owned durable-consumer loops may drift. | Low | Medium | Keep exact resource/ack lifecycle tests; extract platform mechanics only after two implementations demonstrate a stable common seam. |

## 15. Trade-offs made

| Chose | Over | Because |
|---|---|---|
| Fact-driven module boundary | Reading devices tables/history | Device Facts are the implemented cross-module contract and preserve acyclic ownership. |
| Typed family Triggers | Generic payload matcher/expression language | Validation, API discoverability, and failure semantics remain bounded. |
| Same-Fact all-of comparisons | Boolean or multi-Fact conditions | Covers useful sensor cases without stateful evaluation. |
| Runtime API and SQLite definitions | Checked-in config or Go registration | The operator explicitly needs durable runtime CRUD and enablement. |
| Immutable Run snapshots | Reading latest definition per Step | Edits cannot rewrite admitted work or history. |
| Sequential no-retry Steps | Parallelism/retry policies | Command ordering and physical-effect truth remain simple. |
| Age-bounded durable resume | All-backlog execution or tail-jumping restarts | Recovers short crashes without executing stale household actions. |
| No global cap | Fixed capacity skips or queue | Operator chose unrestricted cross-Automation fan-out for the base. |
| Per-match history | Logs/latest-only outcome | Runs and skips must be inspectable after edits/deletion. |
| Hard-delete definitions, retain history | Soft delete or cascade | Current management remains simple while historical truth survives. |
| No unmatched Fact receipts | Write per Device Fact | Avoids Observation-volume write amplification while accepting a narrow redelivery/edit race. |

## 16. Success metrics

- An operator can create, inspect, replace, delete, enable/disable, and manually run an Automation entirely through `/v1`.
- Both published Device Fact families can drive real typed Commands without reading devices history.
- Broker duplicates, short process crashes, concurrent matches, and republication after history pruning never create more than one outcome per `(fact_id, automation_id)`.
- History explains every admitted Run, busy skip, and stale skip through immutable snapshots and Fact summaries.
- Restart and drain never replay unfinished Steps or claim physical success not established by Command outcomes.
- All acceptance criteria pass under `mise run validate`.

## 17. Open questions

None. Any expansion beyond this contract is a new scoped decision.
