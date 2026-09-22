# Automation conditions

**Status:** Approved and implementation-ready.
**Effort:** XL overall, split into ordered, independently testable deliverables.
**Extends:** [Fact-driven automations](automations.md). This spec supersedes its current-State exclusion, section 15's rejection of Boolean Conditions, manual admission outcome shape, and Skip provenance. The exclusion of multi-Device-Fact correlation and all other guarantees remain.
**Decision:** [ADR 0022](../docs/adr/0022-evaluate-conditions-from-state-snapshots.md).

## 1. Problem and recommendation

Hearth can match one Device Fact and execute an ordered sequence of Steps. It cannot require current State such as darkness or absence before admission because Trigger comparisons inspect only the initiating Fact.

Add one optional, bounded Condition tree over current Entity State. Nodes compose with `all`, `any`, and `not`. A Trigger starts an automatic admission decision, Conditions allow or block it, and Steps define the work. State changes do not trigger Condition evaluation.

Devices remains the sole authority for State; do not add another State cache. Read one coherent State batch outside automation transactions, then evaluate Conditions against transaction-loaded current definitions during atomic admission. Persist each evaluated predicate's evidence so history remains explanatory after State changes or pruning. Reuse the existing JSON Pointer and comparison code, with the separate three-valued policy defined below.

## 2. Settled product contract

### 2.1 Definition and boolean composition

A definition may omit `conditions`. Omission preserves today's unconditional-after-Trigger behavior and is preserved on encode; explicit JSON `null` is invalid. When present, `conditions` is one root node, not an array.

Every node has an author-supplied `id`, unique across that definition's entire Condition tree. IDs use the existing subject-safe slug grammar and a 1 to 63 byte limit; Condition, Trigger, and Step ID namespaces are independent. Child order is retained for deterministic evidence. Node kinds are closed:

| Kind | Required family fields | Meaning |
| --- | --- | --- |
| `entity_state` | `entity_id`, `pointer`, `operator`, `operand`; optional `max_age_seconds` | Compare one selected current-State value with a static operand |
| `all` | nonempty `children` array | All children must be true |
| `any` | nonempty `children` array | At least one child must be true |
| `not` | exactly one `child` | Negate true/false; preserve unknown |

Unknown fields and contradictory family fields are invalid. Maximum 64 total nodes, maximum depth 8 counting the root as depth 1, and the existing 64 KiB normalized definition limit all apply. Typed Go callers must be rejected for cycles, over-depth trees, duplicate IDs, contradictory payloads, and aliasing hazards before unbounded recursion or encoding. Normalization returns owned copies of nodes and JSON bytes.

Reuse `eq`, `ne`, `lt`, `lte`, `gt`, `gte`, JSON Pointer syntax/256-byte limit, canonical array indices, exact numerical comparison, and JSON equality semantics from existing Observation comparisons. Definition validation checks pointer syntax and operand/operator compatibility. Save-time validation requires each referenced Entity to exist and be stateful, but does not require a present State, compatible current selected value, availability, enablement, or healthy owner. Current support must not reinterpret retained accepted State. Unsupported runtime paths/types are evaluation results, not reasons to reject a structurally valid definition.

### 2.2 Three-valued logic

Values are `true`, `false`, and `unknown`. Only a true root admits a Run.

| Children | `all` | `any` |
| --- | --- | --- |
| All true | true | true |
| All false | false | false |
| True and false | false | true |
| True and unknown | unknown | true |
| False and unknown | false | unknown |
| All unknown | unknown | unknown |

For arbitrary nonempty groups: `all` is false if any child is false, otherwise unknown if any is unknown, otherwise true; `any` is true if any child is true, otherwise unknown if any is unknown, otherwise false. `not(true)=false`, `not(false)=true`, `not(unknown)=unknown`.

Evaluate **every leaf**, including branches that cannot change the root result (no short-circuiting). History therefore explains every predicate, not whichever branch happened to short-circuit. A true `any` may legitimately admit despite an unknown sibling; that sibling's leaf evidence remains visible. Only `entity_state` leaves are recorded as evidence; group and `not` results are derived from their children.

Leaf unknown reasons, in precedence order:

1. `entity_missing`: an explicitly requested canonical Entity does not exist at the snapshot read.
2. `state_missing`: it exists but has no accepted State.
3. `evidence_in_future`: `max_age_seconds` is present and `observed_at > evaluated_at`.
4. `evidence_expired`: bounded evidence age exceeds the configured maximum.
5. `pointer_missing`: the valid pointer cannot select a value, including a noncanonical runtime array index.
6. `type_mismatch`: for `eq`/`ne`, selected value and operand have different top-level JSON kinds; for ordering, either side is not a JSON number. Same-kind containers with different members, nested member types, or element values are unequal, not type-mismatched.

A comparison of valid same-type values produces true or false. JSON `null` is a real selected value, not missing evidence. `ne` on incompatible types is unknown, not true. An unknown group or `not` node has no invented leaf reason; its recorded children explain it.

Corrupt stored JSON or definitions, impossible evidence identities, and infrastructure failures are errors, not unknown values. State corruption rolls back admission, negatively acknowledges the Fact, writes no outcome, and logs `automation.condition_state_corrupt` without values. This can block the single-pending-ack consumer until repair or stale classification. After 30 seconds, stale precedence records the usual Skips without reading State. Retention does not extend the execution window or promise later execution. Manual corruption returns safe HTTP 500. Existing Observation Triggers still return false for missing or incompatible values, including `ne`.

### 2.3 Freshness, availability, and timing

`max_age_seconds` is optional, integer, and ranges from 1 through 2,592,000 inclusive. Its absence means compare retained State regardless of age. With a bound, age is `evaluated_at - State.ObservedAt`; equality with the bound is allowed. An accepted unchanged Observation refreshes the evidence clock. Future evidence is unknown only when an age bound is applied.

This uses Core's broker-assigned Observation time, not Adapter acquisition time or upstream change time. A new report may describe an older physical measurement, so the bound does not promise recent sampling or mean "the value held for N seconds."

Unavailable, unknown-availability, and disabled Entities may supply retained State. Availability, enablement, and State freshness are separate concepts. Command execution still enforces its own current rules.

The batch read is coherent across all requested Entities, but it precedes the admission transaction. `evaluated_at` is the single Core admission-decision time supplied for the repository transaction, used for both Fact freshness and all predicate ages. It is not claimed to be the exact database snapshot timestamp. State may change between its read, admission, and any Command. Conditions are checked once for the committed admission, never before subsequent Steps; no lock or physical-state guarantee extends into execution. Incoming Observation Facts may be older than the current State snapshot, even for the same Entity.

### 2.4 Automatic admission and precedence

Preserve current-definition matching, at most one active Run per Automation, and atomic outcomes for all matching Automations for one Fact. Disabled/unmatched Automations still record nothing. For each current matching Automation, in Automation ID order:

1. Existing `(fact_id, automation_id)` receipt: duplicate; no new decision for that Automation.
2. Fact older than 30 seconds: `stale_fact` Skip, Conditions not evaluated.
3. Existing running Run: `automation_busy` Skip, Conditions not evaluated.
4. Conditions omitted: ordinary Run.
5. Conditions present: evaluate against the covered State snapshot. True admits a Run; false records `conditions_false`; unknown records `conditions_unknown`.

Every automatic Condition Skip commits with a matched-Fact receipt in the same transaction. Redelivery never re-evaluates that decision, even after State/definition changes, Core restart, or pruning of its explanatory history. This retains the existing unmatched-Fact redelivery caveat; do not introduce receipts for every unmatched Fact.

A Skip creates no Step rows, reserves no Command identities, and starts no workers. Admission remains all-or-nothing across matching Automations. Evidence failure for one eligible conditional sibling can delay an unconditional sibling instead of partially committing the Fact.

### 2.5 Manual invocation and bypass

`POST /v1/automations/{automation_id}/runs` keeps its existing endpoint and no-body behavior. Optional body: `{"bypass_conditions": true}`. Omitted body, `{}`, or explicit false apply Conditions. A present body must be one strict JSON object; unknown fields, `null`, nonboolean bypass values, and trailing JSON are rejected. Limit this body to 1 KiB and publish the optional request body in runtime OpenAPI.

- Manual invocation continues to ignore definition enablement and has no Trigger/Fact.
- Existing admission gates, missing-definition checks, and busy checks precede Conditions. Manual busy remains HTTP 409 `automation_busy` with no new Skip.
- Normal manual admission evaluates Conditions; false or unknown commits a manual Skip, no Run/Steps/Commands, then returns HTTP 409 with that Skip's history reference.
- Explicit bypass does not read State or evaluate any Condition. It is recorded in the admitted Run. It bypasses **only** Conditions, never busy checks, gates, save-time definition integrity, or execution-time Command validation.
- If Conditions are absent, classify the decision `not_configured`. The bypass affects nothing to record; `bypass_requested` is derived from the mode, so an unconditioned bypass is indistinguishable from any other unconditioned Run.
- There is no new manual idempotency key. Each completed manual condition-blocked request creates a distinct Skip. Never automatically repeat an ambiguous manual POST.

The repository returns a successful committed Run-or-Skip result. Only the Service, **after commit**, turns a committed manual Skip into a typed blocked error. Returning that error from the transaction callback would roll back the required history and is forbidden.

### 2.6 History and privacy

All new Runs and Skips retain a Condition decision with one mode:

| Mode | Snapshot | Evaluation | Applicable cases |
| --- | --- | --- | --- |
| `not_configured` | absent | absent | Definition omitted Conditions |
| `not_evaluated` | present | absent | Automatic stale/busy Skip with Conditions |
| `bypassed` | present | absent | Explicit manual bypass of configured Conditions |
| `evaluated` | present | present | Eligible automatic/manual admission with Conditions |

`bypass_requested` is derived from the mode: true if and only if mode is `bypassed`, false otherwise, including for automatic outcomes and unconditioned definitions. It is written for wire compatibility and ignored when decoding. A Condition-blocked Skip cannot be bypassed. For a Run, the decision's Condition snapshot must equal its full definition snapshot's Conditions.

An evaluation stores the root result, `evaluated_at`, and every `entity_state` leaf result in definition pre-order; group and `not` results are derivable from their children and are not duplicated as evidence. Each leaf stores its selected value when the pointer resolves, its Observation ID and `observed_at` when State exists, result, and any unknown reason. Expired/future evidence still retains identities/times and the selected value if resolvable, but does not compare it for admission. Raw entire Entity State is not duplicated when a pointer selected only one member; selecting the root intentionally retains the entire value. Missing values are omitted; a selected JSON null is encoded as `null`.

The stored decision must satisfy these invariants. Write-time structural
validation plus the history table's CHECK constraints are the integrity guard;
reads decode the retained decision and trust it rather than re-deriving the
evaluation evidence.

- Empty/unknown mode values are invalid. `condition_decision_json` is `NOT NULL` and must be an explicit object; an empty payload is malformed, not an implicit `not_configured`.
- `not_configured` has neither snapshot nor evaluation. `not_evaluated` and `bypassed` have a snapshot but no evaluation. `evaluated` has both.
- `not_evaluated` occurs only on an automatic stale/busy Skip with configured Conditions; `bypassed` occurs only on a manual Run with `bypass_requested=true`. Automatic outcomes always have `bypass_requested=false`.
- `bypass_requested` is false for every mode except `bypassed`; there is no separate requested-bypass record for unconditioned definitions.
- An evaluated Run has a true root. A Condition Skip has an evaluated false or unknown root matching its reason. Manual Skips permit only these Condition reasons.
- Evaluation leaves exactly match snapshot `entity_state` IDs and pre-order; the root result agrees with its recorded children under the truth table.
- A true/false leaf has selected value, Observation ID, and observed time, with no unknown reason. An unknown leaf has exactly one listed reason.
- `entity_missing`/`state_missing` leaves have neither selected value nor Observation metadata. `pointer_missing` has Observation metadata and no selected value. `type_mismatch` has both metadata and selected value. `evidence_expired`/`evidence_in_future` have metadata and retain a selected value if the pointer resolves; their earlier reason takes precedence over pointer/type failures.
- Observation ID and observed time are always present together. A selected JSON null is present evidence. At admission, the selected value is captured if and only if a State exists and its pointer resolves; history validation never consults newer State to reinterpret that evidence.

History detail includes the Condition tree so Skips remain explainable after definition replacement/deletion. Fact Skips retain matched Trigger snapshots. Manual Skips have source `manual`, no Fact, and an empty matched-trigger list. History summaries include source, condition decision mode, root result when evaluated, and bypass flag, but not the full tree or predicate values. History detail includes the full decision. All evidence is pruned with its Run/Skip under the existing automation retention policy; there is no separate evidence retention schedule. Fact receipts remain intact.

State values are already visible through the trusted API. This feature retains selected values in additional history; do not put them, operands, whole trees, or raw JSON into logs or HTTP error detail. Existing network trust/authentication boundaries are unchanged.

## 3. Types and ownership

Snippets define the implementation contract, not a demand for unrelated file reorganization. Existing structs retain fields not shown.

### 3.1 Condition domain in new `automations/conditions.go`

```go
// ConditionID identifies one node within a definition's Condition tree.
type ConditionID string

// ConditionKind is the closed set of Condition node kinds.
type ConditionKind string
const (
    ConditionEntityState ConditionKind = "entity_state"
    ConditionAll         ConditionKind = "all"
    ConditionAny         ConditionKind = "any"
    ConditionNot         ConditionKind = "not"
)

// Condition is a bounded discriminated tree; Kind determines its payload.
type Condition struct {
    ID          ConditionID
    Kind        ConditionKind
    EntityState *EntityStateCondition // entity_state only
    Children    []Condition // all/any only, nonempty
    Child       *Condition  // not only
}

// EntityStateCondition compares one retained State selection with a static operand.
type EntityStateCondition struct {
    EntityID      devices.EntityID
    Pointer       string
    Operator      ComparisonOperator
    Operand       json.RawMessage
    MaxAgeSeconds *int64
}

// ConditionResult preserves unknown through boolean negation.
type ConditionResult string
const (
    ConditionTrue    ConditionResult = "true"
    ConditionFalse   ConditionResult = "false"
    ConditionUnknown ConditionResult = "unknown"
)

// ConditionUnknownReason explains unusable predicate evidence.
type ConditionUnknownReason string
// Closed literals: entity_missing, state_missing, evidence_in_future,
// evidence_expired, pointer_missing, type_mismatch; define one named constant each.

// ConditionNodeResult records one node, including selected JSON null.
type ConditionNodeResult struct {
    ID            ConditionID
    Result        ConditionResult
    UnknownReason *ConditionUnknownReason
    SelectedValue json.RawMessage // nil = not selected; []byte("null") = selected null
    ObservationID *devices.ObservationID
    ObservedAt    *time.Time
}

// ConditionEvaluation contains every entity_state leaf in definition pre-order.
type ConditionEvaluation struct {
    EvaluatedAt time.Time
    Result      ConditionResult
    Nodes       []ConditionNodeResult
}

// ConditionDecisionMode distinguishes evidence from deliberate non-evaluation.
type ConditionDecisionMode string
// Closed literals: not_configured, not_evaluated, bypassed, evaluated.

// ConditionDecision is immutable admission explanation, not executable work.
// A sealed interface: exactly one of four explanations exists, built by the
// NotConfiguredDecision, NotEvaluatedDecision, BypassedDecision, and
// EvaluatedDecision constructors, so envelope coherence holds by construction.
type ConditionDecision interface {
    DecisionMode() ConditionDecisionMode
    BypassRequested() bool // true if and only if the mode is bypassed
    DecisionSnapshot() *Condition
    DecisionEvaluation() *ConditionEvaluation
}

func NormalizeConditions(root Condition) (Condition, error)
func RequiredConditionEntityIDs(root Condition) ([]devices.EntityID, error)
func EvaluateConditions(root Condition, snapshot devices.EntityStateSnapshot,
    evaluatedAt time.Time) (ConditionEvaluation, error)
```

Evaluation, normalization, and required-ID collection are pure. Required IDs are sorted and deduplicated; malformed typed trees return an error. Evaluation first verifies complete coverage; a missing map key returns `ConditionSnapshotRequiredError` carrying the full tree's required Entity set and an empty evaluation. It never produces a leaf result for an uncovered key. Only a covered `Exists=false` entry produces `entity_missing`, and only a covered `Exists=true, State=nil` entry produces `state_missing`. Reuse internal JSON decoding/Pointer/equality/rational-number helpers in `comparison.go`; add an explicit type-compatibility check before comparing. Do not call the bool-only `MatchObservationComparison` to derive a three-valued result. Existing Trigger tests must remain unchanged and green.

The bounded discriminated JSON form lives in new `conditions_definition.go` and the embedded `automation-definition.schema.json`; use strict recursive `$defs`/`oneOf` for shape and bounded domain validation for overall depth/count/IDs. `conditions` is optional, not nullable. Add a strict decision/evaluation JSON codec in `conditions_history.go` for persisted explanations; transport DTOs map explicitly rather than exporting persistence structs.

### 3.2 Existing automation types in `automations/model.go`

```diff
@@ type Definition struct {
     Triggers []Trigger
+    Conditions *Condition
     Steps []Step
@@ type Run struct {
     Snapshot Definition
+    ConditionDecision ConditionDecision
@@ type Skip struct {
-    Fact DeviceFactSummary
+    Source RunSource
+    Fact *DeviceFactSummary
     MatchedTriggers []Trigger
+    ConditionDecision ConditionDecision
@@ type HistorySummary struct {
     Fact *DeviceFactSummary
+    Source RunSource
+    ConditionMode ConditionDecisionMode
+    ConditionResult *ConditionResult
+    BypassRequested bool
```

Reuse `RunSource`'s existing `device_fact`/`manual` values for Skip provenance; amend its comment to cover admission provenance rather than renaming it throughout the module. Add `SkipConditionsFalse = "conditions_false"` and `SkipConditionsUnknown = "conditions_unknown"` to `SkipReason`.

Extend `AdmissionSkip`'s safe logging projection with Source and nullable Fact identity so manual Skips never log fabricated empty Fact fields. Existing counters keep their meanings. `RecordedSkips` includes Condition Skips, duplicates remain duplicates, and no extra counter is required.

### 3.3 Devices-owned coherent evidence in new `devices/entity_state_snapshot.go`

```go
// EntityStateSnapshotEntry distinguishes a requested missing Entity from absent State.
type EntityStateSnapshotEntry struct {
    EntityID EntityID
    Exists   bool
    State    *State
}

// EntityStateSnapshot covers every explicitly requested ID, including negative evidence.
type EntityStateSnapshot struct {
    Entries map[EntityID]EntityStateSnapshotEntry
}

// ErrEntityStateSnapshotCorrupt diagnoses unusable stored evidence, not a malformed Fact.
var ErrEntityStateSnapshotCorrupt = errors.New("entity state snapshot contains corrupt stored evidence")

func (service *Service) GetEntityStateSnapshot(ctx context.Context,
    ids []EntityID) (EntityStateSnapshot, error)
func (service *Service) ValidateConditionEntity(ctx context.Context, id EntityID) error
```

A missing map key means **not requested/not covered**; `Exists=false, State=nil` means requested but missing; `Exists=true, State=nil` means no accepted State. `Exists=false` with non-nil State is invalid. Every returned State and map key must agree on Entity identity. Copies must own their JSON bytes.

`GetEntityStateSnapshot` validates/canonicalizes/deduplicates IDs, returns an empty snapshot without SQL for an empty list, and returns every requested ID exactly once or an error for the entire call. Never filter disabled/unavailable Entities or apply current support to old State. `ValidateConditionEntity` shares devices' private stateful-Entity reference-validation logic with `ValidateObservationTrigger`, without expanding Trigger semantics; use a condition-specific permanent reference error mapped to `ErrInvalidAutomation` at the automations seam.

Add to `devices.ReadRepository` and its SQLite implementation:

```go
GetEntityStateSnapshot(context.Context, []EntityID) (EntityStateSnapshot, error)
```

Implement one statement over a requested-ID relation, left-joined to `entities` and `entity_states`. Use a validated JSON array parameter with SQLite `json_each` in the devices-owned query source, so cross-Automation fan-out does not hit SQLite's host-parameter count limit. One statement gives one coherent snapshot without a cross-module transaction. SQL errors, deadlines, and invalid stored State fail the whole read. Preserve `devices.ErrEntityStateSnapshotCorrupt` through Service and repository wrapping so automations can log `automation.condition_state_corrupt`. It is not `ErrInvalidDeviceFact` and never enters the NATS termination path.

### 3.4 Admission interfaces in `automations/repository.go` and `conditions_admission.go`

```diff
@@ type AutomationDevices interface {
+    ValidateConditionEntity(context.Context, devices.EntityID) error
+    GetEntityStateSnapshot(context.Context, []devices.EntityID) (devices.EntityStateSnapshot, error)
@@ type Repository interface {
-    AdmitDeviceFact(context.Context, DeviceFact, time.Time) (AdmissionResult, error)
+    AdmitDeviceFact(context.Context, DeviceFact, devices.EntityStateSnapshot, time.Time) (AdmissionResult, error)
-    AdmitManualRun(context.Context, AutomationID, time.Time) (Run, error)
+    AdmitManualRun(context.Context, ManualRunInput, devices.EntityStateSnapshot, time.Time) (ManualAdmissionResult, error)
```

```go
// These sentinels live in automations/errors.go; typed errors preserve errors.Is.
var (
    ErrConditionSnapshotRequired = errors.New("automation condition snapshot coverage is incomplete")
    ErrAutomationConditionsBlocked = errors.New("automation conditions prevented manual admission")
)

// AdmissionTimeout bounds one automatic or manual admission, including pre-reads and its transaction.
const AdmissionTimeout = 2 * time.Second // conditions_admission.go

// ManualRunInput carries explicit operator intent; no Command identities are accepted.
type ManualRunInput struct {
    AutomationID     AutomationID
    BypassConditions bool
}

// ManualAdmissionResult is exactly one successfully committed Run or Skip.
type ManualAdmissionResult struct {
    Run  *Run
    Skip *Skip
}

// ConditionSnapshotRequiredError reports an uncovered Entity from a definition edit race, never partial admission.
type ConditionSnapshotRequiredError struct {
    RequiredEntityIDs []devices.EntityID // COMPLETE required set, sorted/deduplicated
}
// Error returns "automation condition snapshot coverage is incomplete".
// Is matches ErrConditionSnapshotRequired; this is internal orchestration, not HTTP 400.

// ConditionsBlockedError refers only to a successfully committed manual Skip.
type ConditionsBlockedError struct {
    AutomationID AutomationID
    SkipID       SkipID
    Reason       SkipReason
}
// Error returns "automation conditions prevented manual admission".
// Is matches ErrAutomationConditionsBlocked; the API maps it to 409.
```

```diff
--- a/internal/modules/automations/admission.go
+++ b/internal/modules/automations/admission.go
@@
-func (service *Service) StartManualRun(ctx context.Context, id AutomationID) (Run, error)
+func (service *Service) StartManualRun(ctx context.Context, input ManualRunInput) (Run, error)
```

`ReceiveDeviceFact` keeps its public signature. Change `automations/api/register.go`'s `Automations` interface to `StartManualRun(context.Context, automations.ManualRunInput) (automations.Run, error)`. D3 bridges the existing handler with a false bypass; D4 parses the body. Update every caller and fake, and preserve the compile-time assertion that `*devices.Service` implements `AutomationDevices`.

### 3.5 Pre-read, coverage-gated orchestration

Transaction-loaded definitions are the admission authority. Automatic and manual admission use this protocol:

1. Acquire the existing admission reservation and check existing gates. Keep the reservation through definition and evidence pre-reads and the final outcome.
2. Wrap the pre-reads and one repository transaction in the two-second `automations.AdmissionTimeout`, shortened by the caller's earlier deadline. The NATS `DeviceFactAdmissionTimeout` remains an alias of that constant; HTTP never imports NATS.
3. For automatic admission, Service reads every enabled definition, matches Triggers with the Fact, and computes the sorted, deduplicated Entity union required by matching configured Conditions. For manual admission, Service reads the requested definition and computes its required set only when Conditions are configured and bypass was not requested.
4. Service reads that complete set through devices once in one coherent batch (or uses an empty snapshot when none is required), then invokes the repository exactly once. It never merges samples from different reads.
5. Inside the repository transaction, load current definitions and plan **all** matching outcomes without writes or identity allocation. Apply duplicate/stale/busy precedence before evaluating eligible, non-bypassed Conditions.
6. Return `ConditionSnapshotRequiredError` if the transaction's current eligible Conditions require an Entity absent from the supplied snapshot. Return the affected definition's complete sorted required set, roll back, and return an empty result. This is a rare definition-edit race; do not persist even stale/busy/unconditional sibling outcomes on that pass.
7. With coverage, evaluate every eligible tree, validate all proposed decisions, then atomically persist all Fact outcomes and receipts (or one manual Run/Skip). Any error rolls the whole transaction back. Return only successfully committed results.
8. Register Run workers or log Skips after commit. For a manual Skip, release the reservation and construct `ConditionsBlockedError` after success, outside the transaction callback.

The NATS consumer uses its existing negative-acknowledgement policy for uncommitted transient failures, including `ErrConditionSnapshotRequired`. HTTP maps that error to safe 503 `condition_snapshot_unavailable`; ordinary database errors and snapshot acquisition failures retain the existing safe HTTP 500 mapping. Snapshot corruption follows §2.2.

Cancellation before a known commit retains existing behavior. Clients must reconcile an ambiguous manual commit through history and must not retry it automatically. Evidence failures do not latch an executor fault or become Condition Skips. Malformed persisted data retains its existing error classification.

## 4. Persistence contract

Edit `internal/platform/db/migrations/00001_initial.sql` according to the existing development-only migration policy. Existing development databases must be recreated; no deployed database upgrade or old-binary/new-schema compatibility is promised. Old definition JSON remains readable under the new schema.

Add to `automation_history`:

```sql
skip_source TEXT CHECK (skip_source IS NULL OR skip_source IN ('device_fact', 'manual')),
condition_decision_json TEXT NOT NULL CHECK (
    json_valid(condition_decision_json) AND json_type(condition_decision_json) = 'object'
)
```

Extend `skip_reason`'s closed values with `conditions_false`, `conditions_unknown`. Keep existing Run source columns and partial unique index; do not rename the entire history schema. `condition_decision_json` is shared by Runs/Skips and encodes `ConditionDecision` with snake_case fields. Every row writes an explicit decision; the column is `NOT NULL`.

Each row also carries derived decision summary columns (`condition_mode`, `condition_bypassed`, `condition_result`) written from the decision at insert time and backfilled by migration for earlier rows, so the history listing projection never parses the full snapshot-and-evidence document. The decision document stays authoritative for detail reads.

Replace the old unconditional Fact requirement in the history-kind CHECK; adding columns alone is insufficient. This focused SQL diff preserves the other existing Run/Skip and all-or-nothing Fact checks:

```diff
@@
             AND skip_matched_triggers_json IS NULL AND skip_reason IS NULL
+            AND skip_source IS NULL
@@
         OR (kind = 'skip'
             AND skip_matched_triggers_json IS NOT NULL AND skip_reason IS NOT NULL
-            AND fact_id IS NOT NULL
+            AND skip_source IS NOT NULL
+            AND (
+                (skip_source IS NOT NULL AND skip_source = 'device_fact'
+                    AND fact_id IS NOT NULL
+                    AND json_array_length(skip_matched_triggers_json) > 0)
+                OR (skip_source IS NOT NULL AND skip_source = 'manual'
+                    AND fact_id IS NULL
+                    AND json_array_length(skip_matched_triggers_json) = 0
+                    AND skip_reason IN ('conditions_false', 'conditions_unknown'))
+            )
             AND run_snapshot_json IS NULL AND run_source IS NULL AND run_status IS NULL
```

Keep `automation_history_fact_outcome_idx`'s `WHERE fact_id IS NOT NULL` predicate so repeated blocked manual POSTs remain distinct.

Runs keep their existing required fields and null Skip fields. New automatic Skips require `skip_source='device_fact'`, complete Fact evidence, nonempty matched Trigger snapshots, and a decision. Manual Skips require `skip_source='manual'`, all Fact columns null, `skip_matched_triggers_json='[]'`, one of the two Condition reasons, and a decision. Skips have no Run fields or Step rows; the decision JSON holds their Condition snapshot.

A Run snapshot containing Conditions without a decision, a Skip with a null source or decision, and partial evidence are corruption. Write-time structural validation plus the SQL provenance checks are the integrity guard; SQL does not reproduce the recursive evaluator, and reads decode and trust the retained evidence.

Update explicit write/select queries and sqlc-generated models, mapping helpers, summaries, and pruning tests. No new table or index is needed; dropping `automation_history` in Goose Down already removes its new columns. Do not add destructive separate column-drop statements. Retention must delete the explanation atomically with its history row and leave matched-Fact receipts untouched.

`definition_json`/`run_snapshot_json` remain optional-field-compatible strict v1 documents. Add optional `conditions` rather than requiring a new version marker. The schema `$id` stays `urn:hearth:schema:automation-definition:v1`; revision remains a definition edit counter, not a schema version.

## 5. HTTP and example contracts

### Definition fragment

The following `conditions` object can be added to any otherwise valid existing definition; IDs below are illustrative canonical Entity IDs:

```json
{
  "conditions": {
    "id": "lighting-allowed",
    "kind": "all",
    "children": [
      {
        "id": "room-dark",
        "kind": "entity_state",
        "entity_id": "ent_01950000-0000-7000-8000-000000000001",
        "value_pointer": "",
        "operator": "lt",
        "operand": 30,
        "max_age_seconds": 300
      },
      {
        "id": "other-room-unoccupied",
        "kind": "not",
        "child": {
          "id": "other-room-occupied",
          "kind": "entity_state",
          "entity_id": "ent_01950000-0000-7000-8000-000000000002",
          "value_pointer": "",
          "operator": "eq",
          "operand": true,
          "max_age_seconds": 120
        }
      }
    ]
  }
}
```

The actual Entities must be registered as `hearth.numericsensor/v1` with illuminance (`lx`) support and `hearth.binarysensor/v1` with occupancy semantics; there is no separate illuminance Entity type. Missing occupancy State makes `other-room-occupied` unknown, and its `not` remains unknown. Absence is not permission to turn on the light.

### Transport shapes in `automations/api/models.go` and `api/errors.go`

```diff
@@ type StartAutomationRunInput struct {
     AutomationID string `path:"automation_id" doc:"Canonical Hearth Automation ID"`
+    Body *StartAutomationRunBody
@@ type AutomationDefinitionBody struct {
     Triggers []AutomationTriggerBody `json:"triggers"`
+    Conditions *AutomationConditionBody `json:"conditions,omitempty"`
@@ type AutomationRunBody struct {
+    ConditionDecision AutomationConditionDecisionBody `json:"condition_decision"`
@@ type AutomationSkipBody struct {
-    Fact DeviceFactSummaryBody `json:"fact"`
+    Source string `json:"source" enum:"device_fact,manual"`
+    Fact *DeviceFactSummaryBody `json:"fact,omitempty"`
+    ConditionDecision AutomationConditionDecisionBody `json:"condition_decision"`
@@ type automationProblemError struct {
     Code string `json:"code"`
+    HistoryID string `json:"history_id,omitempty"`
+    HistoryURL string `json:"history_url,omitempty"`
```

New manual body DTO (in `api/models.go`):

```go
// StartAutomationRunBody carries only the optional Condition bypass request.
type StartAutomationRunBody struct {
    BypassConditions bool `json:"bypass_conditions,omitempty"`
}
```

Use a pointer Body and explicit `RequestBody{Required:false}` with a non-nullable object schema, optional boolean `bypass_conditions`, and `additionalProperties:false`. Keep Huma body validation enabled. Omitted bytes yield nil Body, which the handler maps to false; a present JSON `null` is invalid.

Huma v2.39.1 does not validate JSON null for an optional property. Implement strict `UnmarshalJSON([]byte) error` on `StartAutomationRunBody` in `api/conditions.go`. Decode a non-null object into `map[string]json.RawMessage`, reject unknown keys, and accept a present `bypass_conditions` only when its trimmed value is exactly `true` or `false`. Assign the bool after full validation and return fixed, payload-free errors. Huma retains body-size, schema-validation, and error-transport ownership.

New `api/conditions.go` owns `AutomationConditionBody`, `AutomationConditionDecisionBody`, `AutomationConditionEvaluationBody`, and `AutomationConditionNodeResultBody`, plus mappings. They mirror the exact snake_case fields/enums of §3, with explicit DTO string IDs, RFC3339Nano UTC timestamps, and raw selected JSON. Omit family-inapplicable fields; do not emit nullable placeholders except selected JSON null. Leaf definitions are flattened as the example, not nested under `entity_state`.

Summary DTO additions: `source`, `condition_mode`, optional `condition_result`, `bypass_requested`. Runtime OpenAPI must mark the manual pointer body optional with the explicit non-nullable schema above, publish `additionalProperties:false` with optional boolean `bypass_conditions`, and include the history-reference fields on the documented 409 Problem Details response. No generated OpenAPI file is checked in.

| Outcome | HTTP contract |
| --- | --- |
| Run admitted | Existing 202 + existing Run-history Location, now includes decision |
| Conditions false/unknown | 409 Problem Details; `code` is `conditions_false`/`conditions_unknown`; `history_id` is the committed `ask_` ID; `history_url` is `/v1/automations/{id}/history/{skip_id}` |
| Busy | Existing 409 `automation_busy`, no new history reference |
| Missing Automation | Existing 404 |
| Bad definition/bypass body | Existing transport/domain 4xx validation conventions; no admission writes |
| Admission gate closed | Existing 503 `admission_unavailable` |
| Definition-edit coverage race | 503 `condition_snapshot_unavailable`, no fabricated Skip |
| Unexpected storage failure | Existing safe 500; no fabricated Skip |

A Condition-blocked 409 requires the JSON history reference. Errors do not require a new Location header. Fetching the reference immediately after a known successful response returns the committed Skip. No values or internal SQL errors appear in the Problem Details document.

Add `docs/automation-conditions.md` during implementation with a complete create-definition request using operator-substituted registered IDs, normal manual request, explicit bypass request, and history inspection examples. Explain unknown, unchanged-evidence freshness, retained unavailable State, and the coherent-but-not-atomic timing limitation. Include the successful `any(true, unknown)` and blocked `not(unknown)` cases.

## 6. Ownership and deliverables

Keep the existing module boundaries. Devices owns State snapshot reads and Entity validation. Automations owns Condition definitions, evaluation, admission, history, and persistence. `automations/api` owns transport DTOs and error mapping; `automations/nats` owns acknowledgement behavior; `app/hearthd` owns assembly and end-to-end tests. A deliverable that changes an interface also owns every caller and test-double update.

This work adds no independent Conditions service, configuration setting, dependency, frontend, Device Fact contract, Adapter contract, or broker resource.

| ID | Concrete outcome | Effort | Owning paths | Depends on | Acceptance |
| --- | --- | --- | --- | --- | --- |
| D2 | Devices-owned coherent batch State evidence and condition reference validation | M/L | `devices/entity_state_snapshot*`, `automation_validation.go`, `repository.go`, `sqlite/entity_state_snapshot*`, `dbqueries/state.sql`, generated devices output and affected read fakes | None | A5; devices seam portion of A6 |
| D1 | Strict Condition model/codec, three-valued evaluation, and history evidence codec; existing Trigger semantics preserved | L | `automations/conditions*` excluding admission files; `comparison*`, definition schema/codec, model condition types, `errors.go` coverage type/sentinel | D2 | A1 through A4; pure codec portion of A8 |
| D3 | Atomic condition-aware automatic/manual admission, pre-read coverage, durable Skip provenance/evidence | L | `automations/conditions_admission*`, `admission*`, repository/errors/model integration, SQLite/query/migration/history files, NATS budget/disposition tests, `api/register.go` signature bridge, all interface callers/fakes | D1, D2 | Service portion of A6; A7 through A13; A16 |
| D4 | Strict optional bypass body, Condition and history DTOs, 409 history references, and runtime OpenAPI | M/L | `automations/api/conditions*`, models/register/errors, and API tests | D3 | API portion of A6; A14, A15 |
| D5 | End-to-end proof, operator examples, documentation, and repository checks | M/L | `app/hearthd/automations_conditions_integration_test.go`, lifecycle tests, `docs/automation-conditions.md`, `README.md`, `CONTEXT.md`, module README, ADR/architecture/spec links, root validation | D4 | A17, A18, and integration of A1 through A16 |

Execute D2, D1, D3, D4, then D5 because the evaluator needs D2's State snapshot type. The stable IDs do not indicate order. Sequence D1 and D3 changes to shared model and definition files. D2 owns generated devices output; D3 owns migration-dependent automation output. Serialize generation, then run repository-wide generation, formatting, tidying, validation, and diff review.

## 7. Acceptance criteria and fault-oriented verification

- **A1. Definition contract.** Omission round-trips without adding Conditions. Strict families accept valid trees and reject null, empty groups, contradictory fields, duplicate IDs, depth 9, node 65, bad age boundaries, invalid pointers/operators, and definitions over 64 KiB. Depth 8 and node 64 succeed. Old definitions and unconditioned Run snapshots decode.
- **A2. Truth table.** The nine ordered pairs for binary `all` and `any`, plus all three `not` inputs, match §2.2. `any(true,unknown)` admits and `not(unknown)` does not. Evaluation records each `entity_state` leaf once in stable pre-order without short-circuiting.
- **A3. Leaf semantics.** Tests cover every unknown reason, JSON null, same-kind unequal containers, exact rational numbers, and `ne 30` against 40. Uncovered keys return the typed coverage error and no evaluation; covered absent State returns unknown. Invalid stored JSON is an error. Existing Trigger missing/type mismatch behavior remains false.
- **A4. Evidence age.** Controlled-clock tests cover no bound, the exact limit, one nanosecond past it, future evidence, and unchanged Observations that advance `observed_at`. Adapter and upstream timestamps, availability, and enablement do not alter the result.
- **A5. Batch snapshot.** Real migrated SQLite distinguishes present, never-observed, and missing Entities in one owned, deduplicated snapshot; an empty request returns empty. With an independent writer atomically changing two rows, each statement sees the complete old or new pair, never a mix.
- **A6. Reference validation.** Through the devices seam and definition API, save rejects unknown or stateless Entities, accepts stateful never-observed, unavailable, or disabled Entities, and preserves Trigger validation.
- **A7. Atomic coverage.** An uncovered snapshot from a definition-edit race writes no history, receipt, or Step. The required set includes only current eligible Conditions, and known absent State counts as covered. One covered transaction commits mixed unconditional and conditional outcomes together; injected storage failure rolls all writes back.
- **A8. Explanation integrity.** Every decision mode and evaluated result round-trips; selected JSON null remains distinct from missing. Leaf identity, time, and value survive State and definition changes, deletion, and device-history pruning. Domain and raw-SQL tests reject inconsistent IDs, results, provenance, reasons, modes, evidence, malformed or envelope-incoherent decision JSON, Skip Fact fields, Run Skip fields, a Skip with a null source, and any row with a null decision. Valid manual Skips decode.
- **A9. Redelivery and pruning.** Changing State and redelivering a Fact after a Condition Skip starts no Run. Pruning that history still leaves the receipt to block later redelivery. Existing unconditional deduplication remains unchanged.
- **A10. Definition races.** Barrier-controlled definition and busy races prove final definitions govern admission. Newly required IDs return `ErrConditionSnapshotRequired` after one pre-read and one transaction; covered-ID operand edits reuse coherent evidence. Admission writes no partial outcomes or starts workers, and does not latch an executor fault.
- **A11. Precedence and reads.** Automatic admission reads State only when an enabled matching definition has Conditions, even if transaction precedence later yields duplicate, stale, or busy. Unconditioned automatic admissions and explicit-bypass or unconditioned manual admissions do not read State. Stale or busy Conditions are not evaluated. No Fact route accepts bypass.
- **A12. Manual behavior.** False or unknown commits a manual Skip with no Fact, Step, or Command, then Service returns the typed history reference. Busy writes no Skip. Bypass records intent, skips State reads, and still applies gates and Command validation. Bodyless unconditioned calls preserve current behavior; repeated blocked calls create distinct Skips.
- **A13. Failure and acknowledgement.** Snapshot failures and definition-edit coverage races commit nothing, start no worker, and trigger existing negative acknowledgement; committed automatic Skips are acknowledged. Storage failures never become unknown Conditions. One-connection SQLite completes without nested-read deadlock. Corrupt State logs the fixed diagnostic, retains the Fact, and writes nothing; repair reprocesses a still-fresh Fact. Manual commit ambiguity is not retried.
- **A14. HTTP contract.** `httptest` covers bodyless, `{}`, false, true, null body, null bypass, other invalid bodies, all Condition results, strict Condition JSON, 202 Location, and 409 history reference. A definition-edit coverage race returns safe 503; snapshot acquisition, ordinary database errors, and corruption return safe 500. Failed admissions create no Skip. The returned URL resolves to the committed Skip, and errors expose no values, operands, or internal text.
- **A15. Public schemas.** Runtime OpenAPI publishes the optional non-null manual body, Boolean bypass, manual Skip provenance, new reasons, and optional 409 history fields. Tests inspect the runtime manual body, 409 schema, and representative payloads.
- **A16. Lifecycle.** Reservations cover definition pre-reads, State reads, and the transaction; drain joins them before SQLite closes. Snapshot failure does not close the executor gate. Only committed true, bypassed, or unconditioned Runs start workers. Later Condition edits do not stop or recheck a Run. Restart interruption and no-replay tests remain green.
- **A17. Vertical slice.** Real Core assembly uses numeric illuminance, binary occupancy, and controllable Entities. Fresh evidence plus a matching Fact dispatches the expected Command when dark and unoccupied; bright or unknown records a readable Skip and no Command; manual bypass is visible and follows normal Command outcomes. Use simulator or local brokers, not physical devices.
- **A18. Integration quality.** The operator guide has complete valid examples, links resolve, generated code is clean, and `mise run validate` passes its required Mosquitto integration tests. The diff contains only intended documentation, formatting, and generated changes, with no new dependency or external contract.

### Test strategy

Use the smallest boundary that can detect each defect. Domain tests derive truth-table, age, and numeric expectations from this contract rather than production helpers. Existing Trigger numeric tests guard shared comparison code. Add bounded Rapid properties for double negation, group permutation, root-result invariance, and normalization round trips. Compare evidence in definition order. Fuzz the bounded decoder for panic freedom and valid round trips. Rapid is already available.

Use real migrated SQLite for constraints, atomic writes, coverage, receipts, and mapping. Use barriers, channels, and a controlled clock for concurrency tests, never sleeps. Transport tests inspect requests, responses, and runtime OpenAPI. Application tests cover the complete Device Fact, admission, Command, and history path.

## 8. Trade-offs and exclusions

- State can change after the coherent read. History records the evaluated evidence but does not claim physical truth or continued validity during Steps.
- One conditional sibling's State failure delays all matching outcomes for that Fact. This preserves atomic fan-out and can delay the single pending delivery until repair or stale classification.
- Evidence increases history size. Bounded trees, selected values, existing pruning, and payload-free logs limit storage and disclosure.
- Manual bypass affects only Conditions. Admission gates, busy checks, definition integrity, and Command validation still apply.

Scope excludes schedules and time windows; historical, duration, or multi-Device-Fact predicates; a general expression language; new Step control flow or retries; browser authoring; dry-run and bulk snapshot APIs; manual idempotency; authentication changes; and new Adapter, Device Fact, or NATS contracts.

No product questions remain. Any material behavior change requires operator agreement.
