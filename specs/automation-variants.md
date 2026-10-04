# Automation variants migration

Status: implemented and validated on 2026-10-04. Changes are uncommitted on `spec/automation-variants`.

Date: 2026-10-04. Baseline: `3af0bb4` on `main`. Working branch: `spec/automation-variants`.

## Decision

Represent Automation alternatives with module-owned, sealed Go interfaces and concrete variant types. Keep a small identity wrapper where every variant has the same identity. Keep domain values as data, with validation, matching, evaluation, execution, and persistence owned by their existing functions and components.

Apply this to definitions, Device Facts, admission provenance, Condition evidence and decisions, branch decisions, history results, and completion outcomes. Convert their transport representations to explicit variant DTOs and TypeScript discriminated unions.

The user selected a fresh development database cutover. The new implementation reads and writes only the new Automation representation. No legacy decoder or retained-data conversion is required. This specification does not authorize deleting a database.

A separate prepared-definition type or execution plan is a follow-up. This migration keeps existing normalization boundaries and tree interpretation.

## Motivation and evidence

The goals are easier extension and clearer code. Current variant containers distribute knowledge of unrelated fields across consumers:

- `definition.go`: `Step` contains Command fields, `Kind`, `If`, and `Choose`.
- `trigger.go`: `Trigger` has four optional payloads; each family check excludes every other payload.
- `conditions.go`: `Condition` mixes State predicates, Trigger predicates, lists of children, and a single child.
- `fact.go`: `DeviceFact` and `DeviceFactSummary` represent the same two fact families differently.
- `run.go`, `skip.go`, and `history_model.go`: provenance and history variants repeat discriminator-plus-optional-field combinations.
- `conditions.go` and `branch_decision.go`: evidence mixes fields with different meanings and presence requirements.
- `definition_codec.go`, `api/models.go`, `api/branching.go`, and `api/mcp_outputs.go` repeat broad wire containers.
- `execution.go`: `executeCommand` accepts a general `Step` although it can only execute a Command.

The module already uses a sealed `ConditionDecision` interface. Its implementation is still one private struct with optional fields; this migration gives its four modes distinct implementations.

`.golangci.yml` already enables `gochecksumtype` with `default-signifies-exhaustive: false`. Annotated variant interfaces can therefore make missing dispatch cases a lint failure. Go compilation alone does not provide that guarantee.

## Boundaries and behavioral constraints

The [module ownership document](../internal/modules/automations/README.md) and [validation boundaries](automation-validation-boundaries.md) remain the authority for transactions and input ownership. This migration changes representations, not Automation behavior.

Preserve these contracts:

1. An Automation has at most one active Run. Fact receipts, Run-or-Skip decisions, initial attempts, and schedule progress retain their current atomic boundaries.
2. Run snapshots and Trigger matches describe the admitted revision. Later definition edits do not change execution or retained evidence.
3. All defined arms receive save-time structural and Devices-reference checks. Admission reads only admission Condition references.
4. Each reached If reads fresh coherent State. Each reached Choose shares one coherent read and evaluation time across its immediate alternatives. Reached nested branches read again.
5. Condition trees evaluate all leaves without short-circuiting. Choose stops at its first true or unknown root. Unknown fails the Run without choosing a fallback.
6. Each branch decision commits before selected children execute. Unknown/error evidence and Run failure commit atomically.
7. Command positions follow the existing definition traversal: sequences left-to-right, Then before Else, Choose alternatives before Default. Branches consume no Command position. Unselected leaves stay `not_attempted`.
8. No execution retry, replay, compensation, or new shutdown behavior. Recovery interrupts active execution.
9. Manual admission can run disabled definitions. Bypass affects admission Conditions only. Manual Runs have an empty Trigger match set.
10. Observation ordering, held-state windows, schedule watermarks, no-backlog schedule behavior, freshness, retention, and pagination retain their current semantics.

Tree limits remain depth 8, at most 64 Step nodes, 1–32 Command leaves, 1–32 Triggers, at most 64 nodes per Condition root, at most 256 Condition nodes across a definition, and at most 64 KiB of raw and normalized definition JSON. Existing per-sequence, comparison, duration, and identifier bounds also remain enforced.

## Representation rules

### Sealed data variants

- Every variant interface has an unexported marker and a `//sumtype:decl` annotation.
- Use concrete value payloads consistently. Public input boundaries reject nil interfaces, pointer payloads including typed nils, and unexpected implementations. Value-receiver markers also let pointers satisfy the interface, so the marker alone is not the enforcement point.
- Supported variants are exactly the concrete values listed in this document. Embedding an interface does not make an external implementation supported.
- Type switches enumerate the complete family, including deliberately inapplicable cases. Their default handles invalid runtime representations and must not suppress exhaustiveness checks.
- Do not store a discriminator alongside a domain payload. Existing enum types may remain as derived labels for SQL, JSON, logs, comparisons, and lightweight projections.
- Do not add generic variant containers, registration maps, reflection-based dispatch, or a shared cross-module union package.
- Domain nodes have no `Execute`, repository, transport, or service dependencies.

### Validation and ownership

Sealed variants remove contradictory family fields. They do not validate identifiers, scalar values, references, nil interfaces, recursive bounds, or snapshot-relative evidence.

`DecodeDefinition`, `NormalizeDefinition`, and `NormalizeAndEncodeDefinition` continue to produce owned normalized copies. Clone slices, optional values, and JSON bytes recursively. Bound traversal before recursive copying; value payloads can still contain cyclic slices.

Canonical values are mutable Go data and remain subject to the documented unchanged-normalized-value contract. This migration does not claim deep immutability. Save-time Command parameter normalization must return or replace the enclosing value payload because a type assertion produces a value copy.

Repository operations accepting arbitrary domain values remain independent validation boundaries. Private workflow helpers consume established preparation rather than revalidating the entire definition. Retained codecs preserve current corruption checks and error classification.

## Domain types

Definitions below are implementation contracts. Imports, documentation comments, and repetitive marker methods are omitted from some examples. Every listed union receives the annotation and marker described above.

### Steps, Triggers, and Conditions

Owner: `definition.go`, `branching.go`, `trigger.go`, and `conditions.go`.

```diff
 type Step struct {
     ID            StepID
-    Kind          StepKind
-    EntityID      devices.EntityID
-    OperationName devices.OperationName
-    Parameters    devices.CommandParameters
-    If            *IfStep
-    Choose        *ChooseStep
+    Body          StepBody
 }

 type Trigger struct {
     ID          TriggerID
-    Kind        TriggerKind
-    Observation *ObservationTrigger
-    EntityEvent *EntityEventTrigger
-    HeldState   *HeldStateTrigger
-    Cron        *CronTrigger
+    Body        TriggerBody
 }

 type Condition struct {
     ID          ConditionID
-    Kind        ConditionKind
-    EntityState *EntityStateCondition
-    Trigger     *TriggerCondition
-    Children    []Condition
-    Child       *Condition
+    Body        ConditionBody
 }
```

```go
//sumtype:decl
type StepBody interface { isStepBody() }

type CommandStep struct {
    EntityID      devices.EntityID
    OperationName devices.OperationName
    Parameters    devices.CommandParameters
}

func (CommandStep) isStepBody() {}
func (IfStep) isStepBody() {}
func (ChooseStep) isStepBody() {}

//sumtype:decl
type TriggerBody interface { isTriggerBody() }

//sumtype:decl
type ConditionBody interface { isConditionBody() }

type AllCondition struct { Children []Condition }
type AnyCondition struct { Children []Condition }
type NotCondition struct { Child Condition }

type CommandLeaf struct {
    ID      StepID
    Command CommandStep
}
```

`IfStep`, `ChooseStep`, and `ChooseBranch` retain their current fields and recursive `[]Step` children. Nil Else/Default continues to mean omission; present empty arms remain invalid.

`TriggerBody` variants are the existing `ObservationTrigger`, `EntityEventTrigger`, `HeldStateTrigger`, and `CronTrigger`. Their existing fields remain. `CronTrigger.schedule` retains its existing private compiled value and unchanged-expression contract until the preparation follow-up.

`ConditionBody` variants are `EntityStateCondition`, `TriggerCondition`, `AllCondition`, `AnyCondition`, and `NotCondition`. State and Trigger predicates retain their existing fields. `Definition.Conditions` remains `*Condition` because omission is meaningful.

Trigger Conditions remain branch-only, including when nested inside groups. Preparation checks membership in the owning definition's Trigger set. Do not duplicate the entire Condition family for admission versus branch use.

### Device Facts and provenance

Owner: `fact.go` and new `admission_cause.go`.

```diff
-type DeviceFact struct {
-    Family      DeviceFactFamily
-    Observation *ObservationFact
-    EntityEvent *EntityEventFact
-}
+//sumtype:decl
+type DeviceFact interface { isDeviceFact() }
```

The existing `ObservationFact` and `EntityEventFact` implement the interface as values. They already contain the required common and family-specific evidence. Remove `DeviceFactSummary`; retained provenance uses an owned `DeviceFact` with the same evidence. This does not change device-owned NATS schemas or facts emitted by Devices.

```go
//sumtype:decl
type AdmissionCause interface { isAdmissionCause() }

type ManualCause struct{}
type DeviceFactCause struct { Fact DeviceFact }
type HeldStateCause struct { Evidence HeldStateEvidence }
type ScheduleCause struct{}
```

Replace `Source`, `Fact`, and `HeldState` fields on `Run`, `Skip`, and `HistorySummary` with `Cause AdmissionCause`. `Run.MatchedTriggerIDs` and `Skip.MatchedTriggers` retain their existing roles and are not duplicated inside Cause. Schedule cause records no new timestamp; existing admission times remain the evidence available today.

Provenance validation remains snapshot-relative: manual matches are empty, Fact matches belong to the Fact family, held-state evidence identifies the sole matched held-state Trigger, and schedule matches are nonempty cron matches. Validate live facts at service and repository receipt independently. Historical facts must be structurally sound but are not rejected for being old.

`AdmissionSkip` also uses `Cause AdmissionCause` instead of its parallel `Source`, `FactID`, `Family`, and `Variant` fields. Admission returns the already available owned evidence; logging performs no new reads. Log source, family, variant, and fact ID remain derived scalar labels. State values are not added to logs.

### Condition leaf evidence

Owner: new `condition_evidence.go`; evaluation remains in `conditions_evaluation.go`.

```diff
 type ConditionNodeResult struct {
     ID            ConditionID
-    Result        ConditionResult
-    Trigger       *TriggerConditionEvidence
-    UnknownReason *ConditionUnknownReason
-    SelectedValue json.RawMessage
-    ObservationID *devices.ObservationID
-    ObservedAt    *time.Time
+    Evidence      ConditionEvidence
 }
```

```go
//sumtype:decl
type ConditionEvidence interface { isConditionEvidence() }

type ObservationEvidence struct {
    ObservationID devices.ObservationID
    ObservedAt    time.Time
}

type KnownStateEvidence struct {
    Matched       bool
    Observation   ObservationEvidence
    SelectedValue json.RawMessage
}

type UnknownStateEvidence struct {
    Reason        ConditionUnknownReason
    Observation   *ObservationEvidence
    SelectedValue json.RawMessage
}

type TriggerMatchEvidence struct {
    MatchedTriggerIDs []TriggerID
}

func (node ConditionNodeResult) Result() ConditionResult
```

The result is derived: Known State uses `Matched`, Unknown State is unknown, and Trigger evidence is true exactly when its configured-order intersection is nonempty. `ConditionEvaluation` retains `EvaluatedAt`, the composed root `Result`, and ordered `Nodes`.

Unknown evidence keeps genuinely optional data. Missing Entity/State has no Observation or selection; missing pointer has an Observation but no selection; type mismatch has both; future/expired evidence has an Observation and may have a selection. These reason-specific constraints remain enforced. A nil selection means absent; bytes containing `null` mean a selected JSON null.

### Admission Condition decisions and summaries

Owner: `conditions.go`, `conditions_decision.go`, and `history_model.go`.

Retain `ConditionDecision` and its existing constructor and accessor signatures. Replace the single private `conditionDecision` implementation with four private value variants:

| Variant | Fields |
| --- | --- |
| `notConfiguredDecision` | none |
| `notEvaluatedDecision` | `snapshot Condition` |
| `bypassedDecision` | `snapshot Condition` |
| `evaluatedDecision` | `snapshot Condition; evaluation ConditionEvaluation` |

Mode and bypass are derived from the concrete variant. Accessors keep their existing ownership contract; sealing alone does not make the referenced trees immutable. Codecs validate freely constructed nested data at their existing enforcement points.

Replace `HistorySummary.ConditionMode`, `.ConditionResult`, and `.BypassRequested` with `ConditionSummary ConditionDecisionSummary`:

```go
//sumtype:decl
type ConditionDecisionSummary interface { isConditionDecisionSummary() }

type NotConfiguredSummary struct{}
type NotEvaluatedSummary struct{}
type BypassedSummary struct{}
type EvaluatedSummary struct { Result ConditionResult }
```

Summary mapping reads existing materialized summary columns. Listing must not decode complete snapshots or Condition evidence to construct these variants.

### Branch decisions

Owner: `branch_decision.go` and `branch_decision_validation.go`.

```diff
 type BranchDecision struct {
     Position         int
     StepID           StepID
-    Kind             StepKind
     EvaluatedAt      time.Time
-    Outcome          BranchOutcome
-    SelectedBranchID *BranchID
-    Evaluations      []BranchConditionEvaluation
-    FailureCode      *string
+    Body             BranchDecisionBody
 }
```

`BranchDecisionBody` has `IfDecision` and `ChooseDecision` value variants:

```go
//sumtype:decl
type BranchDecisionBody interface { isBranchDecisionBody() }
type IfDecision struct { Result IfDecisionResult }
type ChooseDecision struct { Result ChooseDecisionResult }

//sumtype:decl
type IfDecisionResult interface { isIfDecisionResult() }
//sumtype:decl
type ChooseDecisionResult interface { isChooseDecisionResult() }

type IfArm string // then, else, no_match
type ChooseFallbackArm string // default, no_match

type ChooseEvaluation struct {
    BranchID   BranchID
    Evaluation ConditionEvaluation
}
```

The concrete result variants and their complete fields are:

| Interface | Variant | Fields |
| --- | --- | --- |
| `IfDecisionResult` | `IfSelected` | `Arm IfArm; Evaluation ConditionEvaluation` |
| | `IfUnknown` | `Evaluation ConditionEvaluation` |
| | `IfError` | `FailureCode string` |
| `ChooseDecisionResult` | `ChooseSelected` | `BranchID BranchID; Evaluations []ChooseEvaluation` |
| | `ChooseFallback` | `Arm ChooseFallbackArm; Evaluations []ChooseEvaluation` |
| | `ChooseUnknown` | `Evaluations []ChooseEvaluation` |
| | `ChooseError` | `FailureCode string; Evaluations []ChooseEvaluation` |

These types distinguish evidence shape without making separate types for alternatives that carry identical fields. Unknown derives `branch_condition_unknown`; it cannot carry an unrelated error code. If errors contain no completed root. Choose errors may retain a completed false prefix only under the current error rules.

Retain all current snapshot-relative checks: exact Step kind, Else/Default presence, root result, configured-order prefixes, complete leaf order, evaluation times, selected branch, Trigger intersections, and unknown-reason validity. Scalar enums and concrete payloads do not establish those semantic relationships by themselves.

### Run and Step lifecycle records

Owner: `run.go`, new `run_outcome.go`, and new `step_outcome.go`.

Separate terminal outcomes from lifecycle state. This removes optional-success-evidence versus optional-failure-code tuples from completion inputs without changing SQL transitions.

```diff
 type StepCompletion struct {
     RunID             RunID
     Position          int
-    Status            StepStatus
-    VerifiedCommandID *devices.CommandID
-    FailureCode       *string
+    Outcome           StepOutcome
 }

 type RunCompletion struct {
     RunID       RunID
-    Status      RunStatus
-    FailureCode *string
+    Outcome     RunOutcome
 }
```

| Interface | Variants and fields |
| --- | --- |
| `StepOutcome` | `SatisfiedStep { VerifiedCommandID devices.CommandID }`; `DispatchedStep { VerifiedCommandID devices.CommandID }`; `FailedStep { FailureCode string; VerifiedCommandID *devices.CommandID }`; `InterruptedStep { FailureCode string; VerifiedCommandID *devices.CommandID }` |
| `RunOutcome` | `SucceededRun {}`; `FailedRun { FailureCode string }`; `InterruptedRun { FailureCode string }` |
| `RunState` | `RunningRun {}`; `CompletedRun { CompletedAt time.Time; Outcome RunOutcome }` |
| `StepAttemptState` | `NotAttemptedStep {}`; `RunningStep { StartedAt time.Time; Reservation CommandReservation }`; `CompletedStep { StartedAt time.Time; CompletedAt time.Time; Reservation *CommandReservation; Outcome StepOutcome }` |

`CommandReservation` has `CommandID devices.CommandID` and `CorrelationID devices.CorrelationID`. Optional reservation on a completed attempt preserves interruption before reservation. Verified Command identity and reserved identity remain different concepts.

The interfaces use `isStepOutcome()`, `isRunOutcome()`, `isRunState()`, and `isStepAttemptState()` respectively. They are annotated sealed families; all variants in the table have value-receiver markers.

Replace Run's `Status`, `FailureCode`, and `CompletedAt` with `State RunState`. `StartedAt` remains common. Replace StepAttempt's status, reservation, outcome, and time fields with `State StepAttemptState`; `Position` and `StepID` remain common. Derive existing status labels for SQL, logs, and transport.

Repository completion still verifies identifiers and legal transitions. Success requires a verified Command; failed/interrupted attempts may lack one. Existing interruption of an unstarted attempt establishes its recorded start and completion timestamps. No reserved identity becomes public through this refactor.

### History and admission results

Owner: `history_model.go` and `admission_results.go`.

```diff
-type HistoryEntry struct {
-    Kind HistoryKind
-    Run  *Run
-    Skip *Skip
-}
+//sumtype:decl
+type HistoryEntry interface { isHistoryEntry() }

-type ManualAdmissionResult struct {
-    Run  *Run
-    Skip *Skip
-}
+//sumtype:decl
+type ManualAdmissionResult interface { isManualAdmissionResult() }
```

`Run` and `Skip` value types implement both interfaces. Manual admission must return one valid result after commit or a nil result with an error. A typed Skip still becomes the current blocked-admission error after its evidence commits.

`HistorySummary` keeps its common identity, Automation, revision, recorded time, Cause, and Condition summary fields. Replace `Kind`, `Status`, and `Reason` with `Body HistorySummaryBody`:

- `RunHistorySummary { Status RunStatus }`
- `SkipHistorySummary { Reason SkipReason }`

`HistorySummaryBody` is an annotated interface with marker `isHistorySummaryBody()` implemented by these two value types.

The lightweight Run summary intentionally has only status, not a full RunState requiring extra evidence reads. Counts in `AdmissionOutcome`, ordinary optional input fields, and scalar enums are not variant containers and retain their current roles.

## Interfaces and workflows

Repository and transport service method names, endpoint paths, and transactional responsibilities remain. Their existing named parameter/result types acquire the definitions above. `AutomationDevices` continues to own live Devices access.

Focused signature changes:

```diff
-func CommandLeaves(steps []Step) []Step
+func CommandLeaves(steps []Step) []CommandLeaf

-func (service *Service) executeCommand(ctx context.Context, run Run, step Step, position int) bool
+func (service *Service) executeCommand(ctx context.Context, run Run, stepID StepID, command CommandStep, position int) bool

 func NewRunSnapshot(
     record Record,
     runID RunID,
-    source RunSource,
-    fact *DeviceFactSummary,
+    cause AdmissionCause,
     matchedTriggerIDs []TriggerID,
     conditionDecision ConditionDecision,
     admittedAt time.Time,
 ) Run
```

`NewRunSnapshot` consumes owned normalized definition data and already validated owned provenance, copies the Trigger-ID slice, allocates initial NotAttemptedStep states, and performs no I/O. All four admission workflows provide complete Cause values before constructing the Run; they do not patch provenance afterward.

Retain these explicit input boundaries:

```go
func DecodeDefinition(raw json.RawMessage) (Definition, error)
func NormalizeDefinition(definition Definition) (Definition, error)
func NormalizeAndEncodeDefinition(definition Definition) (Definition, json.RawMessage, error)
func ValidateDeviceFact(fact DeviceFact) error
func ValidateStepCompletion(completion StepCompletion) error
func ValidateRunCompletion(completion RunCompletion) error
func ValidateBranchDecisionWithPreparedSnapshot(
    decision BranchDecision, snapshot Definition, matchedTriggerIDs []TriggerID,
) error
```

Bad definitions/evidence keep `ErrInvalidAutomation` and existing definition issue paths. Invalid facts retain their current fact error class. Missing snapshot coverage remains `ConditionSnapshotRequiredError`; corrupt State remains the devices corruption class. Branch and Command faults retain current readiness-latching and interruption behavior. Nil or malformed variants produce these errors, not panics or fabricated empty success values.

Structural walks and execution walks remain distinct. Share child enumeration for whole-definition tasks where it eliminates duplicated traversal, while preserving per-boundary bounds. Execution switches on concrete Step bodies and calls typed Command/If/Choose helpers. It must not traverse or read State for unselected nested arms.

## JSON, HTTP, MCP, and frontend contract

### Encoding policy

Use explicit discriminators on wire objects and concrete per-variant DTOs. Go field names `Body`, `Evidence`, and `State` do not automatically become nested wire fields. Preserve compact readable documents rather than exposing the internal wrapper structure.

Domain types do not implement transport-oriented `MarshalJSON`. Module codecs own retained definition/evidence JSON; `api` owns HTTP/MCP read models and schema publication. Small private wire wrappers may contain a sealed payload with an explicit codec. A wrapper must not recreate the old many-optional-fields struct or store an unrestricted `any` payload as its domain model.

Decode the discriminator, select a concrete DTO, and validate the exact variant shape. Reject unknown, inapplicable, missing, and disallowed-null fields. Keep pre-schema depth bounds, JSON number precision, complete-document checks, and owned bytes. Required arrays emit `[]`, never `null`; omitted optional arms remain absent.

### New definition format

- Keep `name`, `enabled`, `triggers`, optional `conditions`, and `steps`.
- Require `kind` on every Step, including `command`.
- Keep the current flat fields for command/if/choose, all four Trigger types, and all five Condition types.
- Accept only `value_pointer`; remove the legacy `pointer` alias.
- Publish the definition schema as `urn:hearth:schema:automation-definition:v2`. The version belongs to the schema identifier, not a new required document property.
- Existing `/v1` HTTP paths and MCP tool names remain in this coordinated development cutover. Their descriptions and schemas advertise the new representation.

```json
{
  "id": "turn-on",
  "kind": "command",
  "entity_id": "ent_01900000-0000-7000-8000-000000000001",
  "operation": "set",
  "parameters": {"value": true}
}
```

### Provenance and retained Fact output

Run, Skip, and history summary JSON use one required `cause` property instead of top-level `source`, `fact`, and `held_state`:

| Cause | JSON fields |
| --- | --- |
| Manual | `kind: "manual"` |
| Device Fact | `kind: "device_fact"`, required `fact` |
| Held State | `kind: "held_state"`, required `evidence` with existing `trigger_id`, `started_at`, `due_at` |
| Schedule | `kind: "schedule"` |

Fact JSON has common `fact_id`, `family`, `entity_id`, and `emitted_at`. Observation adds `observation_id`, `disposition`, `value`, and optional `previous_value`. Entity Event adds `event_id` and `name`. Remove overloaded public `variant` and `causation_id` fields; derive their existing SQL/log values from the typed Fact. An absent previous value differs from JSON null.

This is an Automation management/history representation change only. Device Fact NATS envelopes and the devices-facing contracts remain owned by Devices.

### Evidence and outcomes

Condition leaf JSON gains `kind: "entity_state"` or `kind: "trigger"`:

- Known State emits `id`, `kind`, derived `result`, required `observation_id`, `observed_at`, and `selected_value`.
- Unknown State emits `id`, `kind`, `result: "unknown"`, required `unknown_reason`, and the reason-appropriate optional Observation/selection fields.
- Trigger emits `id`, `kind`, derived `result`, and required `matched_trigger_ids`, directly on the leaf rather than under an optional `trigger` object.

Condition decision JSON keeps `mode`, derived `bypass_requested`, and mode-specific `snapshot`/`evaluation`. New decoders reject a bypass flag inconsistent with mode. Summary JSON keeps its existing flattened Condition labels, derived from the summary variant.

Branch decision JSON keeps common `position`, `step_id`, `kind`, `evaluated_at`, and `outcome`. Its concrete DTO determines evaluations and optional selected-branch/failure fields. If errors emit an empty evaluations array; other If outcomes emit exactly one unlabelled evaluation. Choose evaluations retain required branch IDs and current prefix ordering. Unknown emits the derived fixed failure code.

Run/Step status, failure, public verified-command, and timestamp JSON fields retain their current meanings and presence rules, derived from lifecycle variants. History detail remains exactly one `{kind: "run", run: ...}` or `{kind: "skip", skip: ...}`. No private reservation data is serialized.

### Published schemas and clients

Add module-owned `automation-history.schema.json`, with ID `urn:hearth:schema:automation-history:v2`, for public Run, Skip, history entry, history summary, branch decision, Condition decision/evaluation, and Fact/Cause variants. It references the canonical definition schema; use `$defs` and `oneOf` with constant discriminators. Common identities and actual optional fields must be modeled precisely. Use this schema for public contract validation, not to weaken the current snapshot-relative domain validators. `history_schema.go` embeds it and exposes `func AutomationHistorySchema() json.RawMessage`, returning an owned copy.

Publish these schemas through Huma components with rebased references and through MCP input/output schemas. The MCP SDK's reflection handling cannot infer recursive unions reliably, and `json.RawMessage` can be mistaken for byte arrays. Use the existing exact-output mechanism with explicit schemas and encoded JSON; do not convert numbers through `float64` or publish unconstrained schemas in place of the new unions.

HTTP and MCP map a domain value through the same public encoding helpers. If framework wrappers require `any` or raw JSON, limit it to the transport adapter after explicit mapping and schema selection. Remove duplicated MCP field-by-field union DTOs. Full schemas must accompany wrapper outputs.

The shared MCP wrapper currently accepts an input-schema override but always derives output schemas. D5 includes the small corresponding output capability in `internal/mcpapi`:

```diff
 type Tool[I, O any] struct {
     InputSchema any
+    OutputSchema any // Optional success schema; nil preserves derived output.
     ExactOutput bool
 }

 type ToolWithRequest[I, O any] struct {
     InputSchema any
+    OutputSchema any // Same contract as Tool.OutputSchema.
     ExactOutput bool
 }

-func portableOutputSchema[O any]() any
+func portableOutputSchema[O any](override any) any
```

Forward the override through both registration paths and exact-output registration. An override replaces only the success schema; the wrapper still adds its structured `ToolError` branch, portable normalization, and object-root contract. Explicit output must be honored even when O is `any`; nil override keeps the current behavior. Invalid schemas fail registration. Successful exact output is validated with `UseNumber` and delivered as its original encoded bytes.

The Automation API owns assembling self-contained success schemas. Bundle definition/history `$defs` and rebase references to local fragments before passing an output override. The shared MCP wrapper then preserves those references when it nests success inside its error union, by rebasing the success document's local references to their new locations. The API does not depend on the wrapper's internal union layout. There must be no unresolved URN references or network schema fetches. Existing tools without overrides retain their current schemas and error behavior. Contract checks cover recursive references, valid success, structured failure, invalid output, and exact numeric/null preservation through both registration paths.

`web/src/api/types.ts` declares explicit variants for every public union. In particular, split the current broad device Trigger interface into Observation, Entity Event, and Held State types; include `previous_comparisons`. Command has `kind: "command"`, replacing `kind?: never`. Renderers narrow on explicit discriminators and use an exhaustive never check for impossible cases. No schema/code generator is introduced in this migration.

## Persistence and development cutover

Keep SQLite's relational columns and atomic queries. Nullable SQL columns are an adapter representation, not a reason to retain broad domain structs.

SQLite mapping constructs exact Cause, Fact, lifecycle, summary, and history variants. It derives existing discriminator/summary columns when writing. Definitions, immutable Run snapshots, matched Trigger snapshots, Condition documents, and branch-decision documents all use the new codecs.

The selected JSON changes preserve paths that SQL currently inspects: definition Trigger `kind`, matched Trigger IDs, Condition decision `mode`, and `bypass_requested`. The existing migration chain can create the required schema on a fresh database. No table rebuild or new migration is required by this design. In particular, preserve the schedule constraints in `00009_automation_schedules.sql` and branch-decision keys in `00010_automation_branch_decisions.sql`.

Readers reject contradictory rows before constructing a domain variant. Keep checks for row identity, terminal transitions, command positions, verified links, branch order, and retained snapshot-relative evidence. History listing continues to use materialized summary columns without full snapshot decoding.

Development rollout:

1. Land domain, codecs, persistence, HTTP/MCP, frontend, fixtures, and documentation together before using the new binary.
2. Start the new binary against a fresh development database path. Re-enter definitions using the new format.
3. Existing development databases and old exported definitions are unsupported inputs to the new Automation codecs. Do not reinterpret them, automatically reset them, or add a fallback decoder.
4. Update command examples, schema IDs, agent prompt examples, public fixtures, and repository test setup to the new format. Historical migration tests keep the historical shapes needed to test those migrations.
5. Returning to an older binary requires its matching development database and frontend. This work adds no mixed-version support.

No NATS stream recreation, device registration change, new configuration setting, external service, or deployed-environment access is required for the migration design.

## Project layout and ownership

Existing packages remain. New files group evidence or lifecycle types with independent consumers; variants do not become separate packages.

```text
internal/
├── modules/
│   ├── automations/
│   │   ├── definition.go, branching.go, trigger.go       # modify: definition variants and structural traversal
│   │   ├── definition_validation.go, conditions_validation.go # modify: boundary normalization
│   │   ├── definition_codec.go, conditions_codec.go      # modify: strict per-variant retained codecs
│   │   ├── automation-definition.schema.json            # modify: explicit command discriminator and schema v2
│   │   ├── automation-history.schema.json               # new: public history/evidence contract
│   │   ├── history_schema.go                            # new: embed and expose history schema
│   │   ├── fact.go, trigger_matching.go, schedule_matching.go # modify: typed Fact/Trigger dispatch
│   │   ├── admission_cause.go                           # new: provenance variants
│   │   ├── conditions.go, conditions_decision.go         # modify: Conditions and decision variants
│   │   ├── condition_evidence.go                        # new: leaf evidence and derived results
│   │   ├── conditions_evaluation.go, conditions_snapshot.go # modify: variant evaluation and reference reads
│   │   ├── conditions_decision_codec.go                 # modify: retained decision/evidence DTOs
│   │   ├── branch_decision.go, branch_decision_validation.go # modify: typed decisions and evidence validation
│   │   ├── branch_evaluation.go, branch_execution.go, execution.go # modify: typed interpretation and completion
│   │   ├── run.go, skip.go, history_model.go             # modify: lifecycle/provenance/history containers
│   │   ├── run_outcome.go, step_outcome.go               # new: terminal outcomes and lifecycle state variants
│   │   ├── admission_results.go, admission_logging.go    # modify: committed variants and scalar log mapping
│   │   ├── fact_processing.go, held_state_processing.go  # modify: fact/hold admission inputs and outputs
│   │   ├── manual_runs.go, schedule_processing.go        # modify: manual/schedule result handling
│   │   ├── repository.go, README.md                     # modify: representation and ownership contracts
│   │   ├── api/
│   │   │   ├── models.go, conditions.go, branching.go    # modify: concrete public DTOs and shared mapping
│   │   │   ├── register.go, mcp_definition_schema.go     # modify: publish canonical schemas
│   │   │   ├── mcp.go, mcp_outputs.go, mcp_resources.go  # modify: exact canonical output and result dispatch
│   │   │   └── *_test.go                                # modify: transport contract coverage
│   │   ├── nats/                                       # modify: Fact mapping at the existing wire boundary
│   │   ├── sqlite/
│   │   │   ├── definition_mapping.go, fact_mapping.go, trigger_mapping.go # modify: retained decoding
│   │   │   ├── admission.go, held_state.go, schedule.go  # modify: variant construction inside existing transactions
│   │   │   ├── history_mapping.go, schedule_history.go   # modify: strict row-to-variant mapping
│   │   │   ├── execution.go, branch_decisions.go         # modify: lifecycle writes and evidence reads
│   │   │   └── *_test.go                                # modify: repository contracts and corruption cases
│   │   └── *_test.go, testdata/                         # modify: typed fixtures and behavioral regressions
│   └── agent/                                          # modify: Automation examples and matching tests
├── mcpapi/
│   ├── tool.go, output_schema.go, exact_output.go        # modify: optional explicit success schemas
│   └── output_schema_test.go, exact_output_test.go       # modify: override, failure, and precision contracts
└── app/hearthd/automations*_test.go                      # modify: cross-transport and execution integration fixtures
web/src/
├── api/types.ts                                        # modify: explicit public unions
└── pages/
    ├── AutomationsPage.tsx, AutomationDetailPage.tsx     # modify: new command/cause/evidence contract
    ├── AutomationStepTree.tsx, automation-step-tree.ts   # modify: explicit discriminator traversal
    └── automation-fetch-fake.ts, *Automation*.test.tsx  # modify: fixtures and observable rendering behavior
docs/
├── architecture.md                                     # modify: accepted representation after implementation
├── automation-branching.md, automation-conditions.md    # modify: current operator examples
specs/
└── automation-variants.md                              # new: this migration contract
```

Callers found by the compiler belong to the deliverable for the changed family. Generated `sqlite/dbsqlc` files remain owned by `dbqueries/automations.sql` and `sqlc.yaml`; do not edit generated output manually. This design does not require query changes. Review any regeneration diff before including it.

## Deliverables

These are implementation order and ownership, not agent assignments. Changes may overlap in one compiling branch; finish the public cutover as one coherent release.

| ID | Outcome | Owner paths | Depends on | Effort | Acceptance |
| --- | --- | --- | --- | --- | --- |
| D1 | Definition variants, typed traversals, owned normalization, exhaustive dispatch | `definition.go`, `branching.go`, `trigger.go`, `conditions.go`, definition/Condition validation, Trigger/schedule matching and their callers | none | L | A1, A2, A3 |
| D2 | Typed Facts, all admission causes, Run/Skip/history/manual results, lifecycle outcomes | `fact.go`, `admission_cause.go`, `run.go`, `run_outcome.go`, `step_outcome.go`, `skip.go`, `history_model.go`, admission workflows/results/logging, `nats` | D1 | L | A1, A4, A5 |
| D3 | Typed leaf evidence, Condition decisions/summaries, branch decisions, and executor integration | `condition_evidence.go`, Condition evaluation/decision files, branch files, `execution.go` | D1, D2 | L | A1, A6, A7 |
| D4 | Canonical new codecs and schemas plus strict SQLite variant mapping | definition/Condition/branch codecs, both schemas, `history_schema.go`, `sqlite` mapping/admission/execution and repository contracts | D1, D2, D3 | L | A2, A4, A5, A6, A7, A8 |
| D5 | One HTTP/MCP public contract, shared MCP output-schema override, and complete TypeScript unions | `automations/api`, `internal/mcpapi/tool.go`, `output_schema.go`, `exact_output.go` and their contract tests, `web/src/api/types.ts`, Automation pages, `modules/agent` | D4 | L | A8, A9 |
| D6 | Migrated behavior-focused fixtures, cross-module validation, and development cutover documentation | affected existing tests/testdata, `app/hearthd`, module README, operator docs, architecture | D1–D5 | L | A1–A10 |

Total estimate: XL, approximately 5–8 focused engineering days. The breadth is in consumer and contract migration, not implementing marker methods. Re-estimate if the implementation adds a prepared plan, compatibility conversion, or code generation.

## Acceptance and validation

| ID | Behavior or boundary | Expected result |
| --- | --- | --- |
| A1 | Annotated unions and every production type switch | All supported variants are handled explicitly; adding a temporary variant makes lint report missing cases. No dispatch operation silently drops an unfamiliar variant. |
| A2 | Public decode, service save, direct repository write | Reject missing/wrong/null payloads, legacy command shape, `pointer` alias, cycles/oversized trees, invalid references and encoded size through their owning boundaries. Errors retain established classes. |
| A3 | Normalize then mutate original nested input, parameters, operands, or match slices | Normalized owned data is unchanged. Normalization preserves absent versus present arms and compiled cron matching behavior. |
| A4 | Automatic Fact, held-state, schedule, and manual admission through service and repository | Correct typed cause, match set, committed Run/Skip, idempotency and precedence; no Command before commit. Each source's existing behavior remains. |
| A5 | Command success, failure before verification, drain before reservation, active restart, invalid completion transition | Exact status/timestamps and permitted verified evidence survive persistence; no private identity leaks; interruption never invents Command success or replay. |
| A6 | State known/unknown, selected null, missing selection, Trigger intersections, all/any/not | Three-valued results and reason precedence match current semantics; every evaluated leaf is retained in order; manual Trigger matches remain empty. |
| A7 | Nested If/Choose execution and retained branch evidence | Stable Command positions, one coherent immediate Choose read, fresh nested reads, first unknown stops selection, decision-before-command, atomic failed decision, and strict snapshot-relative validation. |
| A8 | New definition/evidence codecs, fresh migrated SQLite, history list/detail | All variants round-trip with precision; JSON schema and runtime shape agree; malformed rows are rejected; lightweight listing does not decode full snapshots. Existing schedule SQL checks still hold. |
| A9 | HTTP, MCP tools/resources, shared output-schema overrides, and frontend | Equal public JSON meaning and precise published variants, including recursive definitions, selected/previous null and large numbers. Explicit command kind, new causes, and all history/evidence variants render correctly. Shared MCP success/failure validation and existing tools without overrides retain their contracts. |
| A10 | Full repository validation and diff review | Existing regressions pass; generation/format/module changes are understood; no mixed old/new fixtures or stale current examples remain. |

Reuse existing behavioral tests. Change their construction helpers and expected JSON. Add cases only where an existing test cannot distinguish a migration fault, especially ownership aliasing, nil/pointer boundary rejection, strict variant decoding, and HTTP/MCP schema parity. Do not create tests that merely invoke marker methods or mirror a type switch.

During implementation, use:

```sh
GO_PACKAGES='./internal/modules/automations/...' mise run --skip-deps test
GO_PACKAGES='./internal/modules/automations/... ./internal/modules/agent ./internal/mcpapi ./internal/app/hearthd' mise run --skip-deps lint
GO_PACKAGES='./internal/modules/automations/... ./internal/modules/agent ./internal/mcpapi ./internal/app/hearthd' mise run --skip-deps test
GO_PACKAGES='./internal/modules/automations/... ./internal/modules/agent ./internal/mcpapi ./internal/app/hearthd' mise run --skip-deps vet
mise run web-test
mise run web-build
mise run validate
```

A1's missing-variant check is a temporary local edit to one annotated interface's family, followed by focused lint; remove the probe afterward. It does not require a permanent test-only production seam. A2–A8 use domain/repository tests and real migrated SQLite; A9 uses existing API parity, schema, integration, and frontend tests. A10 is the final full command plus diff review.

The full suite requires the configured toolchain, dependency access, and a reachable Docker daemon for real-Mosquitto tests. No production deployment or external household access is required. If manually checking the simulator, use the repository's worktree-local `mise run simulator-start` and `mise run simulator-stop` tasks with a fresh development database.

## Risks and trade-offs

| Risk | Mitigation |
| --- | --- |
| Interfaces hide typed nils or shared mutable bytes | Accept concrete value variants at explicit boundaries; retain bounded owned normalization and aliasing checks. |
| New variant silently misses an encoder/evaluator | Annotate every family, keep strict exhaustiveness configuration, and verify the temporary-variant lint check. |
| Typed evidence accidentally weakens retained corruption checks | Preserve standalone, snapshot-relative, row-order, and transition validation separately; exercise malformed retained data through repository reads/writes. |
| Contract rewrite loses JSON null or numeric precision in MCP | Reuse exact output handling and explicit canonical schemas; exercise the existing precision/null parity cases. |
| New Cause format leaks into device-owned NATS contracts | Keep conversion in `automations/nats` and preserve device fact contract fixtures. |
| Lifecycle migration changes pre-reservation interruption or public evidence | Preserve the optional completed reservation and independently optional verified link for failure; assert these paths at repository and transport boundaries. |
| Broad migration leaves old shape assumptions in examples or rendering | Complete backend/frontend/agent/docs cutover together; search legacy field and omitted-kind patterns as part of diff review. |

Choose ordinary interpreters over per-node execution methods to keep lifecycle and transaction ownership visible. Choose explicit DTO mapping over automatic domain serialization to preserve transport independence. Choose handwritten concrete variants over a generator because introducing a generator is a separate maintenance decision.

## Prepared-definition follow-up

A later read-only prepared definition could own canonical bytes, compiled schedules, Command positions, Step indexes, and scoped reference sets. Its main benefit would be making preparation and mutation contracts enforceable, especially the current `CronTrigger.Expression`/compiled-schedule relationship.

That work must name actual consumers and ownership, distinguish structural preparation from current Devices validation, and preserve revision/snapshot identity. Preparing reference sets must never cache branch State values or move reads ahead of reached execution. Evaluate that change after this migration exposes the remaining preparation complexity. A bytecode format, general graph, or process-wide plan cache has no demonstrated need here.

## Review state

Settled with the user: all Automation unions, permission to change JSON, fresh development database, and prepared definitions as a follow-up.

The user approved the concrete type names, value-payload policy, lifecycle variants, and public Cause/Fact/evidence shapes by requesting implementation of this specification. D1–D6 are implemented.

## Implementation evidence

- Temporary Trigger, RunOutcome, evidence/branch, and Condition-summary variants produced exhaustiveness lint failures. All probes were removed.
- Domain and migrated-SQLite regressions cover owned recursive definitions, strict concrete-value boundaries, retained Fact matching, interruption before reservation, exact timestamps under clock rollback, evidence ordering, branch prefixes, and atomic failed decisions.
- Summary corruption tests verify materialized-column validation without decoding full snapshots or Condition decisions.
- HTTP and MCP reuse the canonical definition/evidence codecs and concrete lifecycle/history DTOs. Duplicate MCP output models were removed. Each published response schema bundles its recursive references locally. OpenAPI components retain resource IDs so the same references also resolve at Huma's standalone `/schemas/...json` endpoints; endpoint regressions compile the actual served documents.
- Output-override tests cover both registration paths, structured failures, rejected invalid/nil success, recursive references, and exact large-number/null output. Published schema tests reject contradictory evidence and outcome shapes.
- `mise run validate` passed on the integrated tree: generation, formatting, module tidying, fixture execution, Go race tests, lint, vet, 51 frontend tests, and the production frontend build. `git diff HEAD --check` passed.
- Operator examples, architecture notes, module ownership, and superseded-spec references describe the v2 cutover. Use a fresh development database path; no database was deleted or converted.
