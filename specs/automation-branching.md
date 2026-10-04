# Automation branching

**Representation follow-on:** [Automation variants](automation-variants.md)
replaces the Go union containers and public JSON shapes below with explicit
variants and v2 schemas. Execution semantics are retained. Use the operator guide
for current authoring examples.

Status: Implemented on `feat/automation-branching`, authorized by the user's
implementation request on 2026-10-03. D1–D6 are implemented on the branch; this
does not claim merge or deployment. The contracts below record the approved
design and implemented behavior. See the [operator guide](../docs/automation-branching.md).
Date: 2026-10-03. Type: Feature plan. Effort: XL.

## Problem and evidence

The household needs one Automation to execute different command sequences for
different Triggers and current Entity States. Twelve of the 24 standalone Home
Assistant definitions inspected on 2026-10-03 use `if` or `choose`. The Office
Control Dial nests a brightness-dependent `if` inside a trigger-selected `choose`.
Freezer and litter alerts select different actions for different thresholds.

Hearth already matches Observation transitions, Entity Events, held State, and
restricted cron schedules. It records matching Trigger IDs, but every admitted
Run executes the same flat command sequence. Splitting branches into separate
Automations duplicates definitions and replaces one shared busy guard with
independent guards.

Success means these routing patterns can be expressed in one bounded definition,
executed against truthful State evidence, and explained from retained history.
This feature does not claim complete migration of the household automations.

## Pre-feature architecture

The existing `automations` product module owns definitions, admission, execution,
and history. Its `api` package maps HTTP/MCP; its `sqlite` package owns repository
operations, SQL queries, and generated `dbsqlc`. `devices` owns State snapshots
and Commands. Application assembly injects the existing dependencies.

- `definition.go` has a flat `[]Step` of static Entity Operations.
- `run.go`, `execution.go`, and `sqlite/admission.go` assume a command's array
  index is its persistent attempt position.
- `automation_run_steps` stores command-shaped attempts, not control flow.
- `AutomationDevices.GetEntityStateSnapshot` already supplies coherent reads.
  No new devices interface is needed for branch-time reads.
- Admission Conditions use three-valued logic and retain leaf evidence.
- Runs use immutable definition snapshots. Startup interrupts active Runs;
  workers never resume or replay commands.
- HTTP and MCP publish the embedded v1 definition schema. Web authoring is
  read-only; its tables currently assume flat Steps.

This change stays inside the existing module and its transports. It adds no
product module, dependency, worker, NATS contract, or configuration setting.

## Confirmed behavior

1. Support both nested `if`/`then`/`else` and ordered `choose`/`default` Steps.
2. Branch Conditions reuse State comparisons and `all`/`any`/`not`, and add
   Trigger-ID matching. Trigger-ID Conditions are invalid at admission.
3. Read State when execution reaches each branching Step. All alternatives in
   one `choose` use one coherent snapshot and one evaluation time. A reached
   nested Step reads a new snapshot.
4. Only a true root selects an arm. False tries the next alternative or fallback.
   An unknown root fails the Run immediately without falling through.
5. `choose` executes only its first true alternative in definition order.
   After the selected sequence completes, execute the following surrounding
   Steps. No match without a default is a successful no-op.
6. Manual Runs have no matching Trigger IDs. Manual admission bypass never
   bypasses branch evaluation.
7. Record each reached branch decision and its evidence before executing selected
   commands. Do not evaluate unselected nested Steps for history.
8. Existing flat definitions and retained history remain readable. Preserve the
   one-active-Run rule, stop-on-command-failure, and interrupt-on-restart policy.
9. Failure does not undo earlier commands, select another arm, or retry the Run.

## Scope

Includes the recursive definition contract, validation and reference checks,
branch evaluation, sequential tree execution, durable decision history,
HTTP/MCP parity, and read-only web definition/history rendering.

Excludes delays, waits, loops, parallel execution, Run queues, cancellation APIs,
resumption, dynamic parameters or targets, variables, expression languages,
time/date Conditions, new Trigger families, scene invocation, provider Steps,
device adapters, and a visual authoring editor.

Both `if` and `choose` remain public constructs. They share evaluation and
sequence-execution helpers rather than separate execution engines.

### 80/20 scope reduction

Keep the agreed nested branching behavior and durable Condition evidence. Reduce
implementation contracts and presentation work that do not change household
behavior:

| Keep | Remove or defer | Work avoided |
| --- | --- | --- |
| Private evaluation of validated branch Steps | Exported whole-definition evaluator | New public boundary and redundant preparation |
| Insert-once branch decisions | Successful duplicate writes, including terminal Runs | Duplicate lookup, evidence equality, and terminal-status exceptions |
| Existing command JSON shape | Optional `kind:command` input alias | Alternate wire shape and normalization cases |
| Basic nested definition outline, decision table, expandable evidence | Integrated selected-path history presentation | UI path annotation and command/evidence tree composition |
| Explicit module ownership and required contracts | Mandatory file-per-helper breakdown | Premature fragmentation; related validation and codecs stay together |

Duplicate writes are faults because this executor has no decision-write retry or
replay path. Keep the commit-before-command guarantee, atomic failure evidence,
and all reference/lifecycle validation. These cuts reduce scope without claiming
an 80 percent reduction in engineering effort.

## Definition contract

Keep `urn:hearth:schema:automation-definition:v1` and the existing top-level
fields. `steps` becomes an array of Command, If, or Choose Steps. Unknown fields,
explicit nulls, contradictory kind-specific fields, and trailing JSON remain
invalid. Commands retain static parameters and canonical Entity targets.

### Command Step

Existing command JSON stays valid and is still the canonical output shape:

```json
{"id":"lamp-on","entity_id":"ent_01950000-0000-7000-8000-000000000001","operation":"set","parameters":{"value":true}}
```

Command Steps have no `kind` field on the wire; explicit `kind:command` is
rejected. If and Choose Steps require their explicit `kind`. Compatibility is
with old documents on new Core; older Core cannot consume branching documents.

### If Step

```json
{
  "id":"set-office-level",
  "kind":"if",
  "conditions":{
    "id":"bright","kind":"entity_state",
    "entity_id":"ent_01950000-0000-7000-8000-000000000002",
    "value_pointer":"","operator":"gt","operand":110
  },
  "then":[{
    "id":"dim","entity_id":"ent_01950000-0000-7000-8000-000000000002",
    "operation":"set","parameters":{"value":35}
  }],
  "else":[{
    "id":"brighten","entity_id":"ent_01950000-0000-7000-8000-000000000002",
    "operation":"set","parameters":{"value":91}
  }]
}
```

Required fields are `id`, `kind`, `conditions`, and `then`. `else` is optional.
Each present sequence is nonempty. Omission means no action; `[]` and `null` are
invalid for optional sequences. Numbers in examples are illustrative Entity
values, not promises of a particular device's brightness scale.

### Choose Step

```json
{
  "id":"route-button",
  "kind":"choose",
  "branches":[
    {
      "id":"turn-on",
      "conditions":{"id":"on-trigger","kind":"trigger","trigger_ids":["on"]},
      "steps":[{
        "id":"power-on","entity_id":"ent_01950000-0000-7000-8000-000000000001",
        "operation":"set","parameters":{"value":true}
      }]
    },
    {
      "id":"turn-off",
      "conditions":{"id":"off-trigger","kind":"trigger","trigger_ids":["off"]},
      "steps":[{
        "id":"power-off","entity_id":"ent_01950000-0000-7000-8000-000000000001",
        "operation":"set","parameters":{"value":false}
      }]
    }
  ]
}
```

Required fields are `id`, `kind`, and `branches`. `default` is an optional
nonempty Step sequence. Each branch requires `id`, `conditions`, and `steps`.
Branch IDs are unique within their Choose Step and use the existing slug grammar.
Stable IDs identify alternatives in history without depending on display order.

An If or Choose Step may appear anywhere a Step is allowed. A Choose branch
sequence can contain an If Step, as needed by the office dial.

### Trigger-ID Conditions

`{"id":"button","kind":"trigger","trigger_ids":["single","double"]}`
is true when its list intersects the immutable Run's `MatchedTriggerIDs`.

- The list has 1–32 unique IDs, all referencing Triggers in the same definition.
- It matches any listed ID. Use `all` over separate leaves to require multiple
  matches, or `not` to negate the result.
- It never returns unknown. Manual Runs have an empty match set, so the leaf is
  false. A negated Trigger-ID Condition can therefore be true on a manual Run.
- Observation, Entity Event, held-state, and scheduled sources use the recorded
  match set identically. Scheduled admission may match multiple IDs.
- Definition replacement cannot change the source context of an admitted Run.
- Trigger Conditions are rejected anywhere inside top-level admission Conditions.
  Manual invocation accepts no synthetic trigger context.

### Bounds and validation

| Bound | Contract |
| --- | --- |
| Command Steps | 1–32 across all arms, including unreachable arms |
| All Steps | At most 64 command/if/choose nodes across the definition |
| Step depth | At most 8; top-level Steps count as depth 1 |
| Sequence size | 1–32 direct children for each present sequence |
| Choose alternatives | 1–32 per Choose Step |
| Condition tree | At most 64 nodes and depth 8 per root |
| Total Condition nodes | At most 256 across admission and all branch roots |
| Definition size | Existing 64 KiB normalized limit |
| IDs | Existing 1–63 byte subject-safe slug grammar |

Step IDs are globally unique across every arm. Condition IDs are unique within
their root tree; history addresses them by Step ID, optional branch ID, and
Condition ID. Admission retains its independent Condition ID namespace.

Count every defined node, not just a possible execution path. Enforce bounds
while walking freely constructed Go values as well as decoded JSON, so depth
limits terminate cyclic pointer structures. Do not defer size/depth enforcement
until after recursive encoding. Normalize and own command parameter bytes and
condition operands as today.

Save-time reference validation visits every arm: Entities must exist, State
Conditions must reference stateful Entities, Commands must validate against
current support, and Trigger references must exist. Unknown/unavailable State
does not itself make a definition invalid. Command support is revalidated when
the selected Command executes, preserving the existing command boundary.

The JSON schema uses recursive `$defs` for Steps and separate admission and
branch Condition variants. Local array bounds belong in the schema; aggregate
counts, depth, uniqueness, and cross-references also require Go validation.

## Evaluation and execution

### State reads and ordering

Admission collectors remain scoped to `Definition.Conditions` for Fact, manual,
held-state, and scheduled admission. Do not reuse the full-tree save-time
reference collector for admission reads. Branch State is first read when its
branching Step is reached, never just because that State appears in a definition.

When reaching an If Step, collect its Condition's Entity references. For a Choose
Step, collect references from all its immediate alternatives' Conditions, but
not from their nested sequences. Read the deduplicated set once through the
existing `GetEntityStateSnapshot`. Trigger-only constructs need no devices read.

Sample one UTC evaluation time after a successful read, before evaluating the
construct. All evaluated alternatives use this time and snapshot. Evidence-age
rules retain existing semantics. The snapshot is coherent but not atomic with
subsequent commands; another Run or physical action can change State afterward.

Within a Condition tree, evaluate all leaves as today. Use existing three-valued
composition: `any(true, unknown)` is true and `all(false, unknown)` is false.
Only an unknown root triggers branch failure. For Choose, evaluate alternatives
in order and stop at the first true or unknown root. Later alternatives are not
evaluated or recorded. They were included in the coherent State read, so a read
failure affecting that requested batch fails the construct even if an earlier
Trigger-only alternative would have matched.

After selecting an arm, durably record its decision, execute its sequence
sequentially, and return to the surrounding sequence. A nested branch obtains
fresh evidence after its preceding commands complete. Never reevaluate an
already-recorded decision because State changed.

### Failures and lifecycle

| Event | Result |
| --- | --- |
| Root Condition unknown | Record decision `unknown`; atomically fail Run with `branch_condition_unknown` |
| State read error or deadline | Record decision `error`; atomically fail Run with `branch_state_read_failed` |
| Incomplete snapshot coverage | Record decision `error`; atomically fail Run with `branch_snapshot_incomplete` |
| Corrupt State | Record decision `error`; atomically fail Run with `branch_state_corrupt` |
| Invalid supposedly prepared branch | Existing executor-fault path; stop execution and latch readiness failure |
| Decision persistence failure | No selected command dispatch; use existing executor-fault interruption/readiness path |
| Selected Command failure | Existing failed Step and failed Run behavior; do not try another arm |
| Admission closes during execution | Stop before the next decision or Command; mark Run interrupted with `core_stopping` |
| Core restart | Interrupt active Run with `core_restarted`; preserve committed evidence; never resume |

Use a bounded five-second context for each branch State read and decision write,
matching existing bounded automation persistence operations. Check lifecycle
admission before evaluation and again before executing child Steps. A decision
may commit just before shutdown; it is still a selection record, not proof of
execution. Existing Command drain and outcome policies remain authoritative.

Expected unknown evidence fails only that Run; it does not close readiness.
Read errors are operational errors, not unknown evidence. Safe failure codes
appear in history; raw state, parameters, and provider errors stay out of logs.
There are no evaluation retries. A failed or interrupted Run preserves earlier
effects and may contain both successful Commands and unattempted commands.

Branch faults and drain require Run-only interruption helpers. The existing
`stopRunForDrain` and `recordExecutorFault` complete a command attempt at a supplied
position, so they cannot be called with a branch's position, position zero, or a
descendant command's position. At a branch, attempt `CompleteRun` with
`interrupted` and the appropriate code, leaving every command attempt untouched.
Executor faults also close admission and fail readiness even if interruption
cannot be persisted. Drain alone does not latch an executor fault unless its
interruption write fails. In `service.go`, add a Run/Step-ID fault diagnostic
path that does not invent a command position; retain command-position diagnostics
for failures genuinely associated with a command attempt.

A Run succeeds if the selected execution path completes, including a path that
selects no Command. Unselected command attempts remain `not_attempted`.
Definitions still contain at least one command across the tree.

## Implementation types

### Step model

Keep existing command fields to avoid forcing unrelated callers to rebuild their
command Steps. At normalization, empty `Kind` means Command; normalized Steps
have an explicit kind. The codec alone preserves the legacy command JSON shape.

```diff
diff --git a/internal/modules/automations/definition.go b/internal/modules/automations/definition.go
@@
 type Step struct {
     ID            StepID
+    Kind          StepKind
     EntityID      devices.EntityID
     OperationName devices.OperationName
     Parameters    devices.CommandParameters
+    If            *IfStep
+    Choose        *ChooseStep
 }
@@
-    Steps      []Step // 1–32, IDs unique and execution ordered
+    Steps      []Step // Bounded recursive sequence; IDs unique across the tree.
```

New `internal/modules/automations/branching.go` owns:

```go
type StepKind string
const (
    StepKindCommand StepKind = "command"
    StepKindIf      StepKind = "if"
    StepKindChoose  StepKind = "choose"
)

type BranchID string
type IfStep struct {
    Conditions Condition
    Then       []Step
    Else       []Step // nil means omitted
}
type ChooseStep struct {
    Branches []ChooseBranch
    Default  []Step // nil means omitted
}
type ChooseBranch struct {
    ID         BranchID
    Conditions Condition
    Steps      []Step
}
```

A normalized Command sets only command fields. An If sets only `If`; a Choose
sets only `Choose`. Kind-specific foreign fields are rejected rather than ignored.

### Conditions

Reuse the current tree and State comparison implementation. Explicit validation
scope prevents the new leaf from leaking into admission.

```diff
diff --git a/internal/modules/automations/conditions.go b/internal/modules/automations/conditions.go
@@
 const (
     ConditionEntityState ConditionKind = "entity_state"
+    ConditionTrigger     ConditionKind = "trigger"
@@
 type Condition struct {
     ID          ConditionID
     Kind        ConditionKind
     EntityState *EntityStateCondition
+    Trigger     *TriggerCondition
     Children    []Condition
     Child       *Condition
 }
@@
 type ConditionNodeResult struct {
     ID            ConditionID
     Result        ConditionResult
+    Trigger       *TriggerConditionEvidence
```

New definitions in `conditions.go`:

```go
type TriggerCondition struct { TriggerIDs []TriggerID }
type TriggerConditionEvidence struct { MatchedTriggerIDs []TriggerID }
```

For a Trigger leaf, evidence contains the matching intersection in the leaf's
configured ID order, encoded as an array, empty on false. It contains no State
fields or unknown reason. State-leaf history retains its existing wire shape.
The immutable Run carries the full match set and predicate definitions.

Keep exported `EvaluateConditions` admission-only, with its current signature.
Add branch evaluation separately while sharing recursive evaluator internals;
do not maintain two implementations of State comparisons or boolean logic.

### Branch evidence

New `branch_decision.go` owns the immutable record:

```go
type BranchOutcome string
const (
    BranchThen    BranchOutcome = "then"
    BranchElse    BranchOutcome = "else"
    BranchChosen  BranchOutcome = "branch"
    BranchDefault BranchOutcome = "default"
    BranchNoMatch BranchOutcome = "no_match"
    BranchUnknown BranchOutcome = "unknown"
    BranchError   BranchOutcome = "error"
)

type BranchConditionEvaluation struct {
    BranchID   *BranchID // nil for If; set for every evaluated Choose alternative
    Evaluation ConditionEvaluation
}
type BranchDecision struct {
    Position         int // Reached-decision order, independent of command positions
    StepID           StepID
    Kind             StepKind
    EvaluatedAt      time.Time
    Outcome          BranchOutcome
    SelectedBranchID *BranchID // iff Outcome == BranchChosen
    Evaluations      []BranchConditionEvaluation
    FailureCode      *string // iff unknown or error
}
```

`EvaluatedAt` is the shared successful evaluation time, or the attempted read's
start time for a read error. Every included evaluation carries that same time.
An error may retain a completed false prefix if failure occurred during later
evaluation. Never fabricate partial leaf evidence or a result for a failed tree.
The error record has no selected arm. Unknown retains the evaluated prefix ending
in unknown. Record no branch completion status: selected commands have their own
attempts, and overall completion belongs to the Run.

`branch_decision.go` owns the evidence types, strict encoding/decoding, and
structural coherence. `branch_decision_validation.go` owns validation against
the immutable snapshot and match set.
Validate outcome/kind combinations, prefix order, root results, selected IDs,
timestamps, and leaf shape against the immutable snapshot at repository write.
Do not re-read current State or recompute decisions from current definitions.

```diff
diff --git a/internal/modules/automations/run.go b/internal/modules/automations/run.go
@@
 type Run struct {
@@
     Steps             []StepAttempt
+    BranchDecisions   []BranchDecision
 }
```

## Interfaces and ownership

`branch_evaluation.go` provides a private pure evaluator used by execution. It
consumes a normalized If/Choose Step from the immutable Run snapshot and the
Run's validated matched Trigger IDs. Definition management and persistence own
whole-definition preparation; do not repeat that preparation for each branch or
add an exported wrapper solely for independently constructed test input.

```go
func evaluateBranch(
    step Step,
    roots []Condition,
    required []devices.EntityID,
    matchedTriggerIDs []TriggerID,
    snapshot devices.EntityStateSnapshot,
    evaluatedAt time.Time,
) (BranchDecision, error)
```

The executor collects the Step's roots and Entity IDs once for the State read
and passes those unchanged values to this helper. An impossible kind or prepared
shape makes `evaluateReachedBranch` return an empty decision and an executor
error before the read. The pure helper checks snapshot coverage and evaluates
the prepared Step. After this construct-wide coverage check, the shared Condition
evaluator consumes covered roots without recollecting their references. Admission
retains its own root coverage check. The branch helper does not read devices,
write history, assign decision order, or execute commands. Unknown is a decision,
not a Go error. For operational evaluation errors, return a populated
`BranchDecision` alongside the typed error. It carries `Outcome: BranchError`,
the matching failure code, Step identity, evaluation time, and all fully completed false alternatives before
the failure. Discard the failing Condition tree's partial evidence. Incomplete
snapshot coverage is checked before evaluating any alternative, so it returns
an error decision with no evaluations and the existing typed snapshot error.
Corrupt State retains the existing devices error classification. Do not adopt
the admission evaluator's empty-result-on-error behavior for these operational
branch errors.

The executor assigns the decision's position and persists the populated error
decision, atomically failing the Run. State read failures occur before this pure
helper is called; the executor constructs an error decision with no evaluations
and the read-start time. Invalid prepared values take the Run-only executor-fault path
instead of persisting an invented evaluation result.

The executor collects references and reads State through the existing
`AutomationDevices` interface. Private `executeSequence` and `executeBranch`
helpers in `branch_execution.go` traverse the snapshot and use the same existing
Command execution helper extracted from `executeRun`. No generic workflow engine
or new provider interface is introduced.

```diff
diff --git a/internal/modules/automations/repository.go b/internal/modules/automations/repository.go
@@
     CompleteStep(context.Context, StepCompletion) error
+    RecordBranchDecision(context.Context, RunID, BranchDecision) error
     CompleteRun(context.Context, RunCompletion) error
```

`RecordBranchDecision` is an independent repository validation boundary. In one
transaction it verifies the parent is a running Run, resolves the Step from its
immutable snapshot, validates the record, appends it, and, for unknown/error,
marks the Run failed with the same code and repository completion time. A
selection/no-match leaves the Run running. No command or devices read occurs
inside this transaction.

Decision writes and retained reads use `ValidateBranchDecisionWithPreparedSnapshot`
to reuse the immutable snapshot's `DecodeDefinition` preparation across records.
They still validate every record's shape, match set, and snapshot-relative evidence.
Freely constructed or edited snapshots must pass through a definition preparation
boundary before evidence validation. Test fixtures compose normalization and
evidence validation in test support rather than a separate production wrapper.

Any duplicate `(run_id, step_id)` or decision position is an executor fault,
whether the evidence is identical or different. Preserve the original row and
never overwrite it. Writes on terminal Runs are rejected. Positions must be the
next contiguous reached-decision position. There is no duplicate-read/equality
protocol and no automatic decision-write retry. If commit success is ambiguous,
stop without dispatching the selected sequence and enter the executor-fault
path. Failure to persist interruption also closes admission; startup recovery
handles any still-running row.

Decision evaluation times must not precede Run start or the previous decision.
Equal evaluation times are allowed; contiguous positions establish reached order.

No new HTTP routes, manual invocation fields, MCP tools, event messages, or
Core settings are needed. Existing create/replace validation failures retain
their current transport error mapping.

## Command positions and durable history

### Stable command-leaf positions

Flatten all command leaves in deterministic definition order:

1. Walk each sequence left to right.
2. For If, walk `then`, then `else` if present.
3. For Choose, walk alternatives in order, then `default` if present.
4. Assign consecutive zero-based positions only to Command Steps.

Use this traversal in `NewRunSnapshot`, admission row creation, execution lookup,
history mapping, and web display. Existing flat definitions retain exactly their
old positions. Branch IDs and Step IDs belong to the immutable snapshot; later
definition edits do not remap old attempts.

Keep `automation_run_steps` and `StepAttempt` command-only for compatibility.
Their names remain internal/public legacy names; comments and UI clarify that
they represent Command Step attempts. Branching Steps receive neither Command
IDs nor command-attempt rows. A selected command at position 5 may execute
before one at position 9 while positions 6–8 remain unattempted.

### Decision table

Migration `00010_automation_branch_decisions.sql` adds a child table.

```sql
CREATE TABLE automation_run_branch_decisions (
    run_id TEXT NOT NULL REFERENCES automation_history(id) ON DELETE CASCADE,
    step_id TEXT NOT NULL,
    position INTEGER NOT NULL CHECK (position >= 0 AND position < 64),
    decision_json TEXT NOT NULL
        CHECK (json_valid(decision_json) AND json_type(decision_json) = 'object'),
    PRIMARY KEY (run_id, step_id),
    UNIQUE (run_id, position)
);
```

The repository enforces Run-only parent kind, immutable snapshot membership,
payload/column agreement, chronological append, and terminal status rules. The
table is not executable work. Do not add summary columns or rebuild
`automation_history`; existing schedule triggers, indexes, and foreign keys stay
intact. History pruning cascades to decisions through the existing parent delete.

Queries in `sqlite/dbqueries/automations.sql` add insertion and ordered reads.
Uniqueness constraints reject duplicates. Regenerate `sqlite/dbsqlc`; never
hand-edit generated files.
History detail loads branch rows ordered by decision position. Old Runs yield
an empty decision list. Run history continues to decode its original snapshot
without checking references against current devices or live definitions.

The migration Down removes only this table. It cannot make stored branching
definitions readable by an older binary and loses decision evidence. Binary
rollback requires restoring a pre-feature database backup, rather than claiming
that schema Down is a complete compatibility rollback.

### Public evidence

Add `branch_decisions` as an always-present array on Run detail in HTTP and MCP.
Existing `steps` remains the command attempt array. Admission
`condition_decision` and Skip history remain unchanged.

```json
{
  "branch_decisions":[{
    "position":0,
    "step_id":"route-button",
    "kind":"choose",
    "evaluated_at":"2026-10-03T12:00:00Z",
    "outcome":"branch",
    "selected_branch_id":"turn-off",
    "evaluations":[
      {"branch_id":"turn-on","evaluation":{
        "evaluated_at":"2026-10-03T12:00:00Z","result":"false",
        "nodes":[{"id":"on-trigger","result":"false","trigger":{"matched_trigger_ids":[]}}]
      }},
      {"branch_id":"turn-off","evaluation":{
        "evaluated_at":"2026-10-03T12:00:00Z","result":"true",
        "nodes":[{"id":"off-trigger","result":"true","trigger":{"matched_trigger_ids":["off"]}}]
      }}
    ]
  }]
}
```

API DTOs mirror the defined Step variants and decision fields with explicit
snake_case JSON tags and omission rules. Reuse existing Condition DTO mapping
for evaluations, adding optional `trigger` evidence. Public DTOs never expose
reserved Command/correlation IDs or SQL types.

In `web/src/api/types.ts`, replace the flat Step type with a discriminated union:

```ts
type AutomationStep = AutomationCommandStep | AutomationIfStep | AutomationChooseStep;
type AutomationCommandStep = {
  id: string; kind?: never;
  entity_id: string; operation: string; parameters: Record<string, unknown>;
};
type AutomationIfStep = {
  id: string; kind: "if"; conditions: AutomationBranchCondition;
  then: AutomationStep[]; else?: AutomationStep[];
};
type AutomationChooseStep = {
  id: string; kind: "choose";
  branches: { id: string; conditions: AutomationBranchCondition; steps: AutomationStep[] }[];
  default?: AutomationStep[];
};
```

`AutomationBranchCondition` extends the existing recursive State Condition union
with the trigger leaf. Keep admission typing State-only. Add decision/evaluation
interfaces matching the public example and `branch_decisions` to `AutomationRun`.

### Read-only UI

- Show an indented definition outline with Step IDs and explicit
  Then/Else/alternative/Default labels. It need not annotate execution paths.
- Add a separate chronological decision table with Step ID, selected arm or
  outcome, and evaluation time. Each row expands to display recorded Condition
  evidence; readable formatted JSON is sufficient for the first version.
- Retain the existing command-attempt table and Command links. Include Step ID
  and stable command position so operators can relate it to the outline.
- Leave unattempted commands labeled `not_attempted`. Do not derive a new UI
  status distinguishing unselected commands from commands never reached.
- A decision denotes selection, not command completion. The decision table and
  command-attempt table must make that distinction clear.
- Discover referenced Entities recursively, including State Conditions in all
  arms. Report defined Step and command counts using the bounded tree traversal.
- Preserve old flat history rendering with an empty decision array.

Defer integrated tree/command/evidence rendering, selected-path highlighting,
and inferred unselected/not-reached annotations. All underlying evidence remains
available through history detail for a later presentation improvement.

## Project layout

Paths below assign implementation ownership, not a requirement to create a file
for every helper. The new files group cohesive responsibilities; split further
only when implementation demonstrates a distinct concern. Tests belong alongside
their owning production behavior.

```text
internal/
├── modules/automations/
│   ├── definition.go                         # modify: recursive Step fields
│   ├── definition_codec.go                   # modify: additive wire variants
│   ├── definition_validation.go              # modify: bounded tree preparation/reference validation
│   ├── automation-definition.schema.json     # modify: recursive strict schema
│   ├── branching.go                          # new: Step variants and traversal
│   ├── conditions.go                        # modify: trigger predicate/evidence
│   ├── conditions_codec.go                   # modify: branch/admission scope
│   ├── conditions_validation.go              # modify: scoped tree validation
│   ├── conditions_evaluation.go              # modify: shared leaf evaluation
│   ├── conditions_decision_codec.go          # modify: preserve admission-only validation
│   ├── branch_evaluation.go                  # new: pure branch selection
│   ├── branch_decision.go                    # new: evidence contract and retained codec
│   ├── branch_decision_validation.go         # new: snapshot-relative evidence validation
│   ├── branch_execution.go                   # new: traversal and decision writes
│   ├── execution.go                          # modify: reusable command execution
│   ├── service.go                            # modify: branch fault diagnostics without command positions
│   ├── run.go                                # modify: leaf attempts and decisions
│   ├── repository.go                         # modify: RecordBranchDecision seam
│   ├── README.md                             # modify: ownership and boundaries
│   ├── api/
│   │   ├── models.go                         # modify: recursive definition DTOs
│   │   ├── conditions.go                     # modify: scoped predicates/evidence
│   │   ├── branching.go                      # new: decision DTO mapping
│   │   ├── mcp_outputs.go                    # modify: history parity
│   │   └── mcp_definition_schema.go          # modify: recursive schema publication if needed
│   └── sqlite/
│       ├── admission.go                      # modify: all command-leaf rows
│       ├── branch_decisions.go               # new: atomic evidence/failure writes
│       ├── history_mapping.go                # modify: retained decision reads
│       ├── dbqueries/automations.sql         # modify: decision SQL
│       └── dbsqlc/                           # regenerate from query sources
├── platform/db/migrations/
│   └── 00010_automation_branch_decisions.sql  # new: additive child table
└── app/hearthd/
    └── automations_branching_integration_test.go # new: runtime/history/lifecycle checks
web/src/
├── api/types.ts                              # modify: Step union and decisions
└── pages/
    ├── AutomationDetailPage.tsx              # modify: recursive references/history
    ├── AutomationStepTree.tsx                # new: basic indented definition outline
    ├── AutomationsPage.tsx                   # modify: truthful tree counts
    └── automation-fetch-fake.ts              # modify: branch history fixtures
docs/
├── automation-branching.md                   # new at implementation: operator guide
├── automation-conditions.md                  # modify: admission vs branch terminology
├── automation-gap-analysis.md                # modify: implemented capability status
└── architecture.md                          # modify: branching execution constraint
GLOSSARY.md                                  # modify: agreed domain vocabulary
specs/
├── automation-branching.md                   # this contract
├── automations.md                            # modify at implementation: superseded flat-only rules
├── automation-conditions.md                  # modify at implementation: scoped Conditions
└── automation-validation-boundaries.md       # modify at implementation: recursive boundaries
```

Historical feature specs' exclusions remain descriptions of their original
scope. Superseding links identify current behavior without rewriting a prior
scheduling feature as if it implemented branching. Operational docs describe
implementation on this branch, not a merged or deployed release. The user's
preexisting glossary edits supply the agreed vocabulary and remain preserved.

## Deliverables and dependencies

| ID | Outcome and owning paths | Effort | Depends on | Acceptance |
| --- | --- | --- | --- | --- |
| D1 | Recursive Step model, scoped Conditions, codecs, schema, full-tree reference validation; domain definition/branching/conditions files | L | none | A1–A4 |
| D2 | Branch decision contract and additive durable storage, immutable snapshot/leaf positions, history reads; branch decision files, run.go, repository.go, sqlite, migration | L | D1 | A5–A7 |
| D3 | Branch-time reads and sequential execution with recorded decisions before effects, Run-only interruption; branch_evaluation.go, branch_execution.go, execution.go, service.go | L | D1, D2 | A8–A12, A15 |
| D4 | HTTP/OpenAPI/MCP creation, replacement, schema discovery, and history parity; api files | M | D1–D3 | A13 |
| D5 | Basic definition outline, decision table with expandable evidence, existing command table, counts and recursive references; web files | M | D4 | A14 |
| D6 | End-to-end lifecycle/regression checks and operator/docs integration; app/hearthd checks, module README, listed docs/specs | M | D1–D5 | A15, all regression checks |

Revised aggregate estimate after the 80/20 cuts: XL, approximately 3–5 focused
engineering days, medium confidence. The previous draft estimated 5–8 days.
This is a planning estimate, not a measured saving. Persistence/history
compatibility and recursive HTTP/MCP schema handling remain the main uncertainty;
the largest reduction is the web presentation deliverable. Individual deliverable
size bands overlap and are not additive time commitments.

## Acceptance criteria

- **A1, additive definitions.** Existing flat JSON and retained Run snapshots
  round-trip with unchanged command shapes and command positions. Explicit
  `kind:command` is rejected. Old Run history has no decisions.
- **A2, strict boundaries.** Reject mixed Step fields, unknown/null members,
  duplicate Step/branch IDs in their scopes, bad Trigger references, empty
  sequences, zero-command trees, excessive counts/depth/bytes, and trigger leaves
  in admission Conditions. Test the limit and one beyond it at actual codec and
  directly constructed domain/repository boundaries. Deep/cyclic Go input must
  terminate with validation failure rather than overflow.
- **A3, full-tree references.** Reject an invalid command or missing/state-less
  Condition Entity in an unselected arm at save time. Unavailable or never-seen
  State on a valid stateful Entity remains acceptable. Changing support after
  admission still fails only an attempted unsupported Command.
- **A4, trigger semantics.** For recorded matches `[a,b]`, leaf `[b,c]` records
  true with `[b]`; leaf `[c]` records false. Manual and negated-manual cases,
  held-state provenance, and multiple scheduled matches obey the same rule.
- **A5, stable attempts.** Nested definitions allocate rows for every command
  leaf in deterministic order, no rows for branch Steps, and no Command IDs for
  unattempted leaves. Flat legacy positions are unchanged.
- **A6, atomic evidence.** A selected command cannot dispatch before its decision
  commits. Unknown/error evidence and Run failure commit together. Injecting a
  decision-write failure dispatches no selected command and reaches the existing
  executor-fault path. Identical and changed duplicate writes both fail without
  modifying the original decision. An ambiguous commit result never causes
  dispatch or an automatic retry.
- **A7, migration and retention.** Migrate a version-9 database with flat Run,
  Skip, held-state, and scheduled history and preserve all data, indexes, schedule
  triggers, and foreign keys. Exercise fresh migration, Down/Up on legacy-only
  data, parent pruning cascade, and orphan rejection. Replacing/deleting live
  definitions does not change retained branch evidence or snapshot decoding.
- **A8, routing.** If true/false chooses Then/Else; absent Else is a no-op. Choose
  with two true alternatives executes only the first, all-false chooses Default,
  and all-false without Default is a no-op. Nested completion resumes outer
  siblings in order. A command-free selected path succeeds.
- **A9, evidence timing.** A command changes State before a following branch;
  the branch sees the fresh read. Choose alternatives share one read/time even
  when underlying State changes between their evaluation; a reached nested
  branch reads again. Trigger-only constructs perform no State read.
- **A10, three-valued logic.** Unknown first alternative fails without evaluating
  later alternatives/default; a later unknown after a true alternative is not
  evaluated. `any(true,unknown)` selects and `all(false,unknown)` falls through.
  Evidence distinguishes absent State, JSON null, missing pointer, incompatible
  type, and expired evidence using existing rules.
- **A11, operational errors.** Snapshot failures, incomplete coverage, corrupt
  State, and timeouts fail with their specified codes, never as unknown or false.
  No next command executes. Failure after prior successful commands preserves
  those outcomes without rollback, retry, or fallback execution. An evaluator
  error after a completed false alternative retains that alternative's evidence
  and discards the failed tree's partial results. Pre-evaluation read/coverage
  failures retain no fabricated evaluations.
- **A12, admission separation.** Manual `bypass_conditions:true` bypasses only
  admission. Branch Conditions still evaluate normally. Concurrent invocation
  still gets `automation_busy`; branch failures are Run failures, not Skips.
  Admission reads only its own Condition references. Use an unselected nested
  branch whose State read would fail to prove neither admission nor execution
  fetches that branch's State; immediate Choose alternatives still follow the
  shared-snapshot rule in A9.
- **A13, transport parity.** HTTP and MCP accept the same nested definition,
  reject the same invalid shapes, preserve exact numeric operands/parameters,
  advertise recursive schema, and return equivalent decision evidence. Existing
  routes, revision checks, and manual request schemas retain their behavior.
- **A14, readable history.** UI shows an indented definition, chronological
  decisions with selected arms/outcomes, expandable recorded evidence, and the
  existing command table and links. A selected arm followed by interruption
  displays a selection decision beside unattempted commands, not a success
  claim. Old flat history and recursive Entity names remain readable.
- **A15, lifecycle.** With a decision committed but its child not dispatched,
  shutdown/restart records interruption and never dispatches on startup. Repeat
  with a completed child and a pending outer sibling. Existing Fact duplicate
  suppression, schedule admission, held-state behavior, and Run snapshots survive
  the executor change. Branch-boundary drain and executor faults leave all
  command attempts untouched, including position zero and unselected descendants;
  executor faults still close admission if the Run interruption write fails.

## Validation plan

Tests must exercise behavior and durable effects rather than mirror private
traversal helpers. Use the existing real SQLite repository and command seams;
avoid a new production abstraction solely for tests. Command-count/order and
persisted decision evidence provide the primary execution assertions.

| Acceptance | Check and location | Command or procedure |
| --- | --- | --- |
| A1–A4, A8–A12 | Definition/domain/execution cases in `internal/modules/automations` | `GO_PACKAGES='./internal/modules/automations' mise run --skip-deps test` |
| A5–A7 | Real SQLite admission/decision/history and migration cases | `GO_PACKAGES='./internal/modules/automations/sqlite ./internal/platform/db' mise run --skip-deps test` |
| A13 | Existing HTTP/OpenAPI/MCP parity checks plus nested fixtures | `GO_PACKAGES='./internal/modules/automations/api' mise run --skip-deps test` |
| A14 | Focused user-visible tree/history rendering checks | `mise run web-test` and `mise run web-build` |
| A6, A9, A12, A15 | App-level committed evidence, ordering, and restart integration | `GO_PACKAGES='./internal/app/hearthd' mise run --skip-deps test` |
| Generated code | Regenerate Entity/schema-related sources and SQL bindings | `mise run --skip-deps generate`; review generated diff |
| Integrated regression | Full repository checks after final changes | `mise run validate`; review formatting, generated, and module changes |

The above checks run locally and require no household devices or live Home
Assistant. Full validation includes real-Mosquitto tests and therefore requires
a reachable Docker daemon. A supplemental UI walkthrough may use
`mise run simulator-start` and must finish with `mise run simulator-stop`; it
requires the simulator's configured agent credentials. Automated acceptance
does not depend on that walkthrough or those credentials.

## Trade-offs and risks

| Choice | Alternative | Reason and cost |
| --- | --- | --- |
| Both If and Choose, shared internals | Only one public branching construct | Matches household authoring patterns; increases schema/codec cases |
| Bounded recursive Steps | Only top-level trigger routing | Handles the actual nested office-dial pattern; requires tree-aware history |
| Branch-time snapshots | One admission snapshot for all decisions | Can react to preceding commands; does not promise State remains unchanged |
| Fail on unknown | Treat unknown as false | Avoids executing fallback on missing evidence; differs from permissive routing |
| One snapshot for all immediate Choose alternatives | Lazy per-alternative reads | Coherent comparisons; errors reading later-alternative references can prevent earlier matches |
| Separate decision rows and command attempts | Rewrite all attempts into generic execution nodes | Preserves existing command history and avoids rebuilding the parent history table |
| Immutable decisions, no resumption | Durable workflow interpreter | Preserves current restart semantics; interrupted work needs a new invocation |
| Private evaluation of validated branches | New exported whole-definition evaluator | Keeps preparation at existing boundaries and avoids a new independent API |
| Insert-once decision writes | Successful identical retries | No retry/replay caller exists; duplicate execution is a fault |
| Outline and separate history tables | Integrated execution tree | Preserves basic diagnosis with less first-version UI work |

| Risk | Mitigation |
| --- | --- |
| Recursion bypasses a validator or reference collector | Central bounded traversal, explicit scope at admission/branch boundaries, bad nested input tests |
| UI or SQL confuses tree indexes with command positions | One specified leaf-order algorithm, immutable IDs, legacy-position regression fixtures |
| Decision appears to prove execution | Separate selection evidence from command attempts; test crash between decision and dispatch |
| Existing API clients assume flat Steps | Preserve command shape; update first-party clients together; document additive variants and old-binary incompatibility |
| Branch State read/persistence failures accidentally fall through | Typed failure mapping, injected-failure assertions, atomic failed-Run evidence |

## Implementation status

Questions 1–13 established the product behavior. The subsequent 80/20 review
retained that behavior while removing the exported evaluator, duplicate-success
protocol, command-kind alias, and rich history presentation. The user's explicit
implementation request authorized this work despite the earlier draft status.
D1–D5, D6 app-level checks, and D6 documentation are implemented on
`feat/automation-branching`. The validation plan above remains the integrated
verification procedure; implementation status does not assert that every check
has run successfully in the current working tree.

Nil optional Go arms mean omission; non-nil empty arms are invalid. Repository
decision times are nondecreasing, so equal timestamps are allowed and position
establishes order. These are implemented design contracts, as is restoration of
a pre-feature database backup for binary rollback, not unresolved assumptions.
