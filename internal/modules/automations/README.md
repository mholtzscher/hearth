# Automations module ownership

Automations is one product module. Its packages separate domain behavior from
HTTP, NATS, and SQLite implementation details; Triggers, Runs, and Steps remain
parts of that module rather than independent packages or services.

## Package boundaries

| Package | Owns |
| --- | --- |
| `automations` | Definitions, Trigger matching, Condition definitions and three-valued evaluation, admission and execution use cases, retention policy, domain types, repository interfaces, and Run worker lifecycle |
| `automations/httpapi` | HTTP operations, transport models, schema publication, and HTTP error mapping |
| `automations/mcpapi` | MCP tools, argument decoding, transport models, schemas, and MCP error mapping |
| `automations/internal/apicursor` | Module-private cursor encoding shared by HTTP and MCP |
| `automations/internal/exprvalue` | Pure Expr compilation/evaluation, exact value ingress and historical evidence normalization |
| `automations/nats` | Device Fact wire decoding, consumer resources, and acknowledgement policy |
| `automations/sqlite` | Atomic persistence operations, query selection, row mapping, query sources, and generated query code |

The API, NATS, and SQLite packages depend on `automations`, not the reverse.
Application assembly constructs the SQLite repository and injects it through the
existing `Repository` interface. `DefinitionRepository`
remains the narrower definition-management capability; there is no parallel
store aggregate or generic transaction framework.

Before changing schedule repository capabilities or worker ownership, read the
[Service and repository boundaries](../../../specs/scheduled-automation-triggers.md#service-and-repository-boundaries)
and [app lifecycle](../../../specs/scheduled-automation-triggers.md#app-lifecycle)
contracts.

The devices-facing `AutomationDevices` seam stays in the domain. SQLite never
executes device Commands, publishes NATS messages, or starts Run workers.
Conditions read a coherent State snapshot through that seam. Held-state admission
is the narrow exception: its repository reads device-owned `observations` for
Observation identity and `receive_order` during Fact processing, and
`entity_states` for current State at expiry, inside the shared SQLite transaction.
It does not write device tables or maintain a second State projection.

## Validation boundaries

| Entry point | Owns |
| --- | --- |
| Definition JSON decoding | Strict wire shape, bounds, typed structure, owned normalized values |
| Service create/replace | Typed structure and current Devices/destination/Function references; destination availability and Function callability are not save-time validity |
| Repository create/replace | Typed structure, encoded size, revision concurrency |
| NATS fact decoding | Wire contract and mapped fact integrity |
| Service fact receipt | Fact integrity before matching or dependency reads |
| Repository fact admission | Fact integrity and transaction-local eligibility |
| Service schedule activation | Nonzero activation time and configured household location |
| Repository schedule activation | Nonzero activation time and monotonic persisted watermark |
| Repository schedule admission | Valid tick, transaction-local definition preparation and eligibility, snapshot coverage, atomic outcomes and watermark |
| `ValidateAndMatchScheduledTriggers` | Structural safety and encoded size for freely constructed definitions; valid minute and location |
| `MatchPreparedScheduledTriggers` | Valid minute and location; consumes unchanged normalized definitions and retained compiled schedules, without structural revalidation |
| Public condition helpers | Structural safety for freely constructed trees |
| Admission Condition evaluation | Snapshot coverage and evaluation of supplied evidence |
| Private branch evaluation | Consumes prepared Steps and immutable Run matches; checks snapshot coverage without re-preparing the whole definition |
| `ValidateBranchDecisionWithPreparedSnapshot` | Decision evidence and Run match validation against an unchanged prepared snapshot, without repeating structural preparation or encoding |
| `StepExecutionDetails.Validate` | Kind-owned intrinsic identities, timestamps, lifecycle states, and evidence; no previous execution, snapshot, or live dependency reads |
| `EncodeStepExecutionDetails` | Supported value representation and intrinsic validation, then kind-owned payload encoding and shared lifecycle envelope assembly |
| `PrepareStepExecutionDetails` | Reached-record identities and intrinsic details; returns validated details and their encoded envelope without persisting |
| `CompleteStepDetails` | Current/proposed intrinsic values and legal transition, including immutable reservations; returns completed details and their encoded envelope |
| `ValidateStepValueOwnershipWithPreparedSnapshot` | Snapshot-relative ownership of unchanged, intrinsically validated details from preparation, completion, decoding, or trace validation; recorded input/output presence and failure locations |
| Repository `RecordStep` | Freely constructed reached evidence against the retained revision and matches; next selected Step, insert-once identity, contiguous cross-kind sequence, no active sibling, atomic parent failure/interruption |
| Repository `CompleteStep` | Typed terminal evidence, immutable reservation, running target and parent; failed/interrupted Step and parent commit atomically |
| Run completion | Terminal outcome and legal persisted transition; success requires a complete successful selected-path trace |
| Persistence decoding | Decode retained representation; preserve existing corruption checks |

Device Fact matching helpers operate on validated definitions and facts; export
alone does not make them independent input boundaries.
`ValidateAndMatchScheduledTriggers` is an independent boundary for freely
constructed definitions. Service snapshot preparation and transactional schedule
admission use
`MatchPreparedScheduledTriggers` on unchanged repository-returned definitions
to reuse schedules compiled during decoding without re-normalizing or re-encoding.
JSON, pointer, and duration handling still parses defensively.

Execution validation has three owners. Each concrete details kind implements
`Validate` beside its lifecycle model, with branch execution validation in
`branch_decision.go`. Transition helpers check record identity and current versus
proposed state. SQLite checks retained configuration, selected-path eligibility,
and current parent state inside the write transaction.

Preparation and completion return both details and an encoded envelope. SQLite
reuses that envelope rather than validating and encoding it again. Standalone
encoders and decoders remain independent input boundaries. Private encoders
consume validated values. Private decoders check wire shape before the shared
decoder validates the constructed value.
Trace validation checks each reached execution once during selected-path
traversal and derives lifecycle labels without serializing evidence. It still
checks branch evidence against the retained definition and immutable matches.
Independent codec, transition, and trace entry points retain their own checks;
this is not a module-wide promise to validate each value only once.

Snapshot-relative value checks consume those validated details without repeating
intrinsic validation or reparsing effect inputs. Intrinsic validation owns each
kind's input field set, types and bounds. Destination-normalized effect inputs
need not equal definition literals; literal variable assignments still must.

The closed details interface also requires `Kind`, `Phase`, `StartTime`,
`CompletionTime`, `RunFailure`, and private `encodeValidated` methods. Lifecycle
and parent-outcome projections live beside each kind's state model. The private
encoding methods live in the kind codecs and return evidence payloads, not SQL
rows or complete public responses. The common encoder validates first, calls the
kind's encoding method, and builds the shared envelope from its projections.

Decoding keeps an explicit wire-kind switch because the concrete value does not
exist yet. The supported-representation gate, transition orchestration, and
selected-path traversal also retain their own dispatch. There is no generic
codec registry or dynamic handler registration.

## File responsibilities

The [complete file-purpose map](../../../docs/automation-file-map.md) inventories
every module file, including tests, fixtures, schemas, SQL, and generated code.

- `definition.go`, `definition_codec.go`, `definition_validation.go`, and
  `definition_management.go` own definition types, encoding, validation, and
  service operations respectively. `definition_reference_validation.go` owns live
  dependency checks; `definition_validation.go` remains pure and also prepares
  normalized, size-checked bytes for repository writes. Its `prepareDefinition`
  helper validates structure, owns canonical copies, and compiles cron clock
  fields; callers enforce raw or encoded size limits. `conditions_codec.go`
  handles Condition trees. `definition_wire.go` owns concrete Step and Trigger
  wire DTOs, and `definition_labels.go` derives scalar labels from value bodies.
  Steps, Triggers, and Conditions use sealed, annotated Body unions. Input
  boundaries accept only supported concrete values, not nil or pointer bodies.
  The v3 definition schema requires `kind` for Commands, value bindings for effect
  inputs, and only `value_pointer` for comparisons. Condition preparation returns
  an owned tree with its compiled expression bindings retained, rather than
  validating and compiling them again during copying.
- `branching.go` owns recursive Step families, bounded tree preparation, and
  `CommandLeaves`. That traversal consumes unchanged normalized sequences, not
  arbitrary Go input. Its slice indexes are stable command-attempt positions;
  it walks Then before Else and Choose alternatives before Default. Branch
  nodes, delays, notifications, and Functions never consume a command position. Commands,
  notifications, Functions, State reads, and assignments share the 32 effect/data
  leaf bound. Every delay counts toward
  tree bounds, but definitions may have zero Command leaves.
- `branch_evaluation.go` selects from prepared branch roots using shared Condition
  evaluation. `branch_execution.go` owns reached-branch reads, decision writes,
   sequential tree traversal, and Run-only interruption. Persistence allocates
   execution sequence; traversal carries neither Command positions nor per-kind
   reached counters. It collects roots and
  Entity IDs once per reached branch and passes them into evaluation for coverage
  checks and selection. Admission and branch evaluation check coverage before
  calling the shared Condition evaluator, which does not recollect references.
   `command.go` owns Command normalization and parameter validation, matching
   the kind-specific preparation in `delay.go`, `notification.go`, and `function.go`.
   `read_state.go` and `set_variables.go` own their respective definitions and
   preparation; `value_binding.go` keeps shared binding/variable normalization.
   `command_execution.go` owns Command reservations, reconciliation, completion,
   and pre-reservation drain evidence. `run_execution.go` owns Run orchestration,
   completion, diagnostics, and bounded Run-scoped fault cleanup.
  `notification_execution.go` owns reserved notification links, a single Send,
  and authoritative GetAttempt reconciliation before advancing.
  `function_execution.go` owns reserved invocation links, one internal Function
  invocation, and authoritative GetInvocation reconciliation. It advances only
  after both retained invocation success and Automation completion persistence.
- `branch_decision.go` and `branch_result.go` own typed selection results;
  `branch_decision_codec.go` selects strict family/outcome DTOs for retained JSON.
  `branch_decision_validation.go` validates arbitrary decisions against immutable
   snapshots and match sets. `sqlite/step_executions.go` owns atomic appends and
   failure transitions, and retained row identity/order checks. Decision writes and
  retained reads reuse the snapshot prepared by `DecodeDefinition` for every
  evidence record; each record still receives shape and snapshot-relative checks.
   `branch_execution_codec.go` reuses the canonical branch codec inside typed
   execution evidence; `step_execution_codec.go` dispatches all kinds for storage
   and public HTTP/MCP output.
- `delay.go` owns Delay definition preparation and snapshot duration lookup.
  `delay_execution_state.go` owns Delay lifecycle states and evidence validation.
  `delay_execution.go` owns native elapsed waiting and persistence gates. `snapshot_steps.go`
  resolves evidence identities in prepared immutable Step trees.
  The public execution contains only lifecycle and evidence. The browser derives
  Delay duration and diagnostic due time from the retained definition and start.
- `notification_execution_state.go` owns reached notification evidence and its boundary
   validation; Notifications independently owns attempt history.
    `step_execution.go` owns closed reached/completion variants and canonical
     execution details and derived properties. `step_execution_transitions.go`
     prepares and applies reached/completion evidence and returns reusable encoded
     envelopes. `step_execution_codec.go`
     encodes typed payloads separately
    from native lifecycle columns. `step_execution_trace.go` validates selected-path
     order and completeness. HTTP and MCP serialize `Run.Executions` as one sparse
     `executions` array with a global zero-based `sequence` and kind-specific
     evidence. No compatibility projection or `not_attempted` placeholder remains.
     Absence never establishes a successful selected Step or an external effect.
   Browser definitions/evidence are read-only, and enablement replacement keeps
    notification nodes intact. Independent attempt expiry never rewrites Step status.
- `data_execution_state.go` owns instantaneous assignment, State-read and value-failure
  validation and projections. `data_execution_codec.go` owns their wire DTOs and
  encoding/decoding, while the runtime gates stay in `variable_execution.go` and
  `read_state_execution.go`.
- `sqlite/value_evidence.go` owns atomic initialization and native value accounting.
  It charges complete value-bearing execution metadata, including terminal growth,
  while pre-existing Condition evaluations retain their independent history budget.
  The 2 MiB/256-record value budget includes a 4 KiB terminal failure allowance;
  pending effects also leave 512 bytes for completion without charging unused space.
  Ordinary transitions read only the parent, control labels and target execution;
  interruption accounting reads only unresolved recorded effects. Detail reads
  independently reconstruct and check counters. Initialization encoding/decoding
  combines intrinsic and snapshot ownership checks once within each workflow;
  standalone codec and trace boundaries retain their independent checks.
- `function.go` owns static Function Step normalization and current contract
  reference validation. `function_execution_state.go` owns closed lifecycle
  states and evidence validation; `function_execution_codec.go` owns their wire
  encoding. `sqlite/step_executions.go` owns all
   Step reservations, atomic terminal Step/Run transitions, and row decoding.
  Adapters independently owns Invocation identity, routing, evidence, and pruning;
  Automation history never joins or writes Adapter-owned invocation records.
- `fact_processing.go`, `manual_runs.go`, `held_state_processing.go`, and
  `schedule_processing.go` own the admission workflows. `schedule_matching.go`
  owns cron parsing and immutable prepared clock fields, and matches the sampled
  current minute in the household location. Normalization retains that preparation
  privately on `CronTrigger`; changing its expression requires normalization again.
  `conditions_snapshot.go` reads Condition State;
  `conditions_decision.go` owns the admission-only decision model, constructors,
  and evaluation-to-Skip policy. `conditions.go` keeps the shared Condition trees
  and evaluation models used by both admission and branching. Branch selection
  remains in `branch_evaluation.go` and the branch decision files.
- `repository.go` defines persistence contracts; `dependencies.go` defines the
  Devices, Notifications, and Functions seams and service configuration. `notification.go`
  prepares static notification Steps using Notifications-owned message validation.
  `admission_results.go` carries outcomes.
- `fact.go` owns the concrete Observation and Entity Event Fact values and their
  validation. `admission_cause.go` carries owned manual, Fact, held-state, or
  schedule provenance; `admission_cause_validation.go` checks retained provenance
  against matched Trigger snapshots without applying live Fact freshness limits.
  `fact_labels.go` derives existing receipt, SQL, and log labels.
- `run.go` owns Run types, outcomes, lifecycle states, completion validation,
  snapshot construction, and derived labels.
  `command_execution_state.go`, `delay_execution_state.go`,
  `notification_execution_state.go`, and `function_execution_state.go` own closed
  running/terminal Step states. Completion time
  and phase derive from those variants, not parallel optional fields. Each kind's
  `*_execution_codec.go` preserves its existing wire evidence; the shared codec
  owns dispatch and the public envelope. Runtime helpers pass reservations and
  terminal evidence directly into `RecordStep` and `CompleteStep`, without a
  second family of per-kind start/completion wrappers.
  `sqlite/lifecycle_mapping.go` rejects contradictory retained
   parent rows; Step codecs reject partial reservations while allowing completed
    Command interruption before reservation. `httpapi/provenance.go` owns HTTP Cause and Fact DTOs;
  `mcpapi/get_automation_history_entry.go` owns MCP provenance DTOs and reuses the
  canonical definition, Condition, and execution encoders.
  `history_model.go` carries Run-or-Skip value results and materialized summary
  bodies; listing does not decode full snapshots or Condition decisions.
- `condition_evidence.go` derives leaf results from Known State, Unknown State,
  or Trigger intersections. `condition_evidence_validation.go` enforces retained
  evidence shape; branch validation additionally checks it against the snapshot.
  `conditions_decision_codec.go` selects concrete decision and leaf DTOs.
  `condition_summary.go` projects the four materialized explanation variants;
   `sqlite/condition_summary.go` rejects contradictory summary columns.
- `conditions_decision_evidence_codec.go` encodes prepared Run admission evidence
  without repeating configuration and assembles full decisions on reads. SQLite
  stores immutable definition revisions separately from current heads and Runs;
  Run detail and execution reads resolve their retained revision, not the current
  head. Skips keep compact inline snapshots. Existing history maintenance collects
  unreferenced revisions in bounded batches. See
  [revision storage and migration](../../../docs/automation-definition-revisions.md).
- `automation-definition.schema.json` and `automation-history.schema.json` own
  the v3 public contracts. `httpapi/schema_publication.go` and transport-local
  contract files publish recursive schemas for HTTP and MCP. Both transports use canonical encoders;
  MCP's explicit schema and exact output preserve raw JSON numbers and nulls.
  `httpapi/automation_models.go` reuses the canonical definition codec;
  `httpapi/lifecycle.go` and `httpapi/run_mapping.go` select public lifecycle and
  history DTOs. MCP models remain transport-local. Test-only response readers do
  not constrain production models.
- `comparison.go` owns State comparisons; `json_codec.go` owns strict JSON
  decoding helpers; `pagination.go` owns shared page limits.
- `httpapi/conditions.go` maps Conditions; `httpapi/start_automation_run.go`
  decodes manual HTTP requests. `mcpapi/automation_arguments.go` owns MCP scalar
  arguments. `sqlite/held_state.go` persists holds and due outcomes, while
   `sqlite/execution.go` owns Run completion and one unified
   child-before-parent interruption transaction. A Run scope preserves terminal
  parents and sibling evidence; startup recovery interrupts all running rows.

## Development cutover

Current definition/history v3 adds pure expression value flow on this revision
and unified-execution architecture. `internal/exprvalue` owns checked lookups,
exact ingress, evidence normalization and bounded immutable programs.
`value_binding.go` owns definition bindings; `run_values.go` owns one executor's
live context. Initialization persists at Run level; assignments, State captures,
resolved effect inputs and verified Function outputs persist with their existing
unified execution. No per-kind repository API or second ordered Step journal is
introduced. Live float/int values are never reconstructed from history.

Migration 23 preserves post-22 revisions and history, wrapping v2 static fields
without changing revision identities. It labels prior value evidence unavailable.
Use Core's migration provider for its transactional frozen-v2 validation. Back up
before upgrade; Down refuses lossy conversion. Databases from the unmerged #222
expression migration 20 are a separate lineage and must not be silently adopted.
See [Automation expressions](../../../docs/automation-expressions.md).

Migrations 21 and 22 intentionally clear existing Automation definitions, history,
held state, receipts, and schedule progress while leaving other modules' data
untouched. Those cutovers originally required re-entering definitions using v2;
current authoring uses v3, with migration 23 preserving post-22 definitions/history.
There is no compatibility decoder or replay for data discarded by the earlier
cutovers. See [upgrade guidance](../../../docs/automation-definition-revisions.md#upgrade-and-rollback).
All Command Steps require `kind: "command"`; comparisons accept only
`value_pointer`. Public provenance is a required `cause` object, and Condition
leaves include `kind`. Devices-owned NATS envelopes retain their existing format.

See [Step execution storage](../../../docs/automation-step-executions.md) for the
unified transition checklist, targeted-read boundaries, and public projection.

## Conditions

Definition preparation validates the structure of every arm. Service save-time
reference checks visit every arm, including unreachable commands and State
predicates. Trigger-ID Conditions are branch-only and reference Triggers in the
same definition. Admission helpers and snapshot
collectors remain scoped to `Definition.Conditions`; save-time validation does
not read State or expand admission's requested Entity set. Condition IDs are
root-local, Step IDs are globally unique, and alternative IDs are Choose-local.

An Automation definition may carry one optional, bounded admission Condition
tree whose nodes compare selected current Entity State or compose with `all`,
`any`, and `not`. Conditions are optional per definition and preserve omission. Evaluation
is pure and three-valued over an immutable State snapshot without
short-circuiting; only a true root admits, and every `entity_state` leaf's
evidence is recorded so history explains a decision after State or the
definition changes. Group and `not` results are derivable from their recorded
children and are not duplicated as evidence. A decision is one of four
explanations built through sealed constructors. Retained codecs independently
reject unsupported value representations, malformed leaf evidence, and bypass
flags inconsistent with the mode. They preserve established admission evidence;
branch reads also validate evidence relative to the immutable snapshot and match
set. The transports, codecs, and persistence shapes for
definitions, evaluations, and decisions live with this module.

Conditions never initiate execution. Admission Conditions are evaluated once
per admission.
Devices remains the authority for State: the Service pre-reads one coherent
batch through the devices seam outside the admission transaction and never
merges samples from different reads. Transaction-loaded current definitions are
the admission authority; a definition edit that newly requires an uncovered
Entity makes the transaction write nothing and return a retryable coverage error.

Automatic and manual admissions share the protocol. Duplicate, stale, and busy
precedence precedes Condition evaluation in the transaction; automatic admission
may have already pre-read State for enabled matching configured definitions. False
or unknown Conditions commit an explainable Skip and start no workers. Manual
invocation may explicitly bypass Admission Conditions, which is recorded in the
admitted Run and bypasses nothing else, including branch evaluation. The Service
turns a committed manual Condition Skip into a typed blocked error only after
the transaction commits, so the required history is never rolled back.

See [the Conditions specification](../../../specs/automation-conditions.md) for
the implementation contract and [the operator guide](../../../docs/automation-conditions.md)
for the definition, manual, and history examples.

## Branch execution and evidence

Each reached If reads its root's State references. Each reached Choose reads
all immediate alternative roots' references once with one coherent snapshot and
one UTC evaluation time. Reached nested branches read again after preceding
Commands. Admission collectors never include branch references, and unselected
nested Steps have no reads or decisions. Trigger-only constructs need no read.
All leaves inside an evaluated tree retain evidence. Choose stops at its first
true or unknown root; unknown fails the Run without fallback. Manual Runs have
an empty match set and cannot supply synthetic Trigger IDs.

The executor commits each selection before dispatching selected children.
Unknown/error decision evidence and Run failure commit atomically. Decisions
are insert-once, with no duplicate-success, retry, or replay protocol. Times may
be equal; the cross-kind execution sequence establishes reached order. A successful
branch selection uses `succeeded` but does not prove child completion.
`Run.Executions` contains only durably reached Steps, including branch selections.
Unselected and unreached Commands have no execution row or public placeholder.

Expected unknown fails only that Run. Decision persistence faults stop dispatch
and make one atomic Run-scoped interruption attempt, then log; they do not close
module admission or fail readiness. Branch drain and faults never invent a
command attempt. Startup preserves
committed evidence and interrupts active Runs without resuming execution.

Migration 10 originally added separate branch evidence. Migration 22 replaces
that table with unified Step executions and resets all Automation-owned data.
Older binaries require their matching database and frontend.
See the [branching specification](../../../specs/automation-branching.md) and
[operator guide](../../../docs/automation-branching.md) for bounds and rollback.

## Delay execution and evidence

The Service owns a stop broadcast independent of admission caller contexts and
device Command contexts. `StopAdmission` signals `core_stopping`; operation
faults never signal this broadcast. Shutdown wakes pending delays promptly, while in-flight
Commands retain their existing shutdown policy. `Drain` joins workers and does
not create a second cancellation authority. Application assembly still owns
shutdown ordering.

`delay_execution.go` starts monotonic elapsed accounting before recording the
reached delay. It commits start evidence, waits only for the remaining duration,
then commits completion before continuing traversal. SQLite transactions never
remain open during a timer wait. Diagnostic UTC start/completion timestamps use
the existing `Dependencies.Now`; they do not control the native timer. No timer
factory, durable scheduler, NATS job, or executable continuation is introduced.

`Run.Executions` records reached delays in the same sequence as other Step kinds.
Duration comes from the identified Delay Step in the immutable snapshot; the
browser derives diagnostic due time from recorded start plus that duration.
Neither stored nor public execution evidence duplicates duration or due time.
Completion wall time may precede start or due after clock correction; sequence establishes
reached order. Invalid identity, timestamps, status shape, or snapshot data
causes a read error, not synthesized progress. A terminal Run cannot retain a
running delay.

A start or completion persistence fault stops later Steps and makes exactly one
bounded `InterruptRun` transaction for that Run's running delays, attempts, and
parent, with `executor_fault`, then logs. It preserves terminal rows, immutable
branch decisions, and uncertain Command reservations without inventing verified
links. It never closes module admission, fails readiness, or wakes sibling waits.
There are no cleanup retries, busy-waits, or execution retries. If cleanup fails,
only the affected Automation may remain busy until startup recovery; independent
Automations and later Runs whose own admission writes succeed continue. Startup
atomically interrupts active delays,
Command attempts, and Runs with `core_restarted`; it never resumes timers or
replays Commands. A completed delay proves waiting ended and evidence committed,
not that its next Step executed. A delay-only Run contains only Delay executions.

Migration 11 originally added separate Delay evidence, superseded by migration 22.
Running parents remain protected by existing history retention. Down drops
evidence only; older binary rollback requires a pre-feature database backup
because definitions and snapshots still contain delay nodes. Existing HTTP/MCP
operations and canonical schema support authoring with no new tools. Browser
definitions and delay history are read-only with manual refresh.
See the [delay specification](../../../specs/automation-delay-steps.md) and
[operator guide](../../../docs/automation-delay-steps.md).

## Transaction boundaries

### Notification execution and evidence

Reached notification leaves reserve a stable attempt ID and commit their logical
link before calling Send once. Unselected/unreached leaves have no evidence or
attempt. Notifications use the same cross-kind execution sequence as other Steps.
GetAttempt is authoritative even when Send returns
an error: reserved identity, destination, exact title/text and validated terminal
evidence must match before adoption. Only durably confirmed acceptance permits
later Steps. Rejected, not-sent and unknown outcomes fail the Run atomically with
the reached Step's distinct outcome/reason. Missing destination or closed internal
notification admission produces a failed Step with no provider outcome.
An attempt-ID collision is never adopted, even for identical message content;
Notifications reports the specific conflict cause alongside its existing
persistence error class.

An unverifiable outcome or persistence fault stops execution and makes the existing
single bounded Run-scoped interruption transaction. It preserves prior terminal
notification evidence and reservations; no retries, resends, or sticky admission
failure follow. Failed cleanup can leave only that Automation busy until startup
recovery. Restart interrupts running Step evidence with `core_restarted`, without
adopting provider evidence or resuming execution. Shutdown keeps internal
Notifications admission open through Automation drain, including a reservation
committed immediately before StopAdmission.

Migration 13 originally added separate Notification evidence, superseded by
migration 22. Attempt IDs are logical links with no foreign key to Notifications.
Pruning either history never removes the other owner's records; a retained
successful Step keeps its accepted outcome when attempt detail expires. Migration
22 resets Automation-owned data rather than converting old Runs. Binary rollback
requires the matching pre-upgrade backup and frontend.

File boundaries organize related code; they do not divide existing transactions.

- Fact admission loads current definitions, matches Triggers, checks durable
  duplicate receipts, and commits every matching Run or Skip with its receipt,
  initial Steps, and Condition decision in one transaction. Stale-Fact
  classification precedes busy classification, and the Service pre-reads State
  only for enabled matching definitions with Conditions. A definition edit that
  makes the supplied snapshot incomplete writes nothing and returns a coverage
  error; the Service does not retry the admission.
- Held-state Fact processing advances each hold's Observation receive-order
  cursor in the admission transaction. At expiry, the transaction rechecks the
  hold, definition, and current State before committing one Run or Skip and
  consuming the hold. If Condition snapshot coverage is incomplete, the Service
  rereads the candidates and evidence and retries within its two-second admission
  bound. Ordinary Fact and manual coverage errors are returned without retry.
- Manual admission checks the current definition and active Run before recording
  one immutable Run snapshot, its Condition decision, and its initial Steps.
  Disabled Automations still permit manual invocation.
- Schedule admission loads current definitions and commits every matching Run or
  Skip, initial Steps, Condition decisions, and the UTC minute watermark in one
  transaction. Activation consumes its minute without outcomes; later ticks
  inspect only the sampled current minute and never replay a backlog. Missing
  Condition snapshot coverage retries preparation within the two-second admission
  bound. `sqlite/schedule.go` owns the atomic tick and watermark.
- Definition replacement and deletion enforce the expected revision atomically.
  They also remove that Automation's held-state rows.
- Step and Run writes preserve terminal-outcome checks. Startup interruption
  records interruption; it does not replay execution or infer Command success.
- Branch decision writes validate the running parent and immutable snapshot, then
  append evidence and, for unknown/error, fail the Run in the same transaction.
  State reads and Command execution remain outside that transaction.
- Delay start validates running-parent and snapshot-relative identity before
  inserting evidence once. Completion transitions only a running delay;
  interruption updates delay and parent atomically with one time and reason.
  Ordinary Run completion cannot leave a running delay behind. Startup recovery
  includes running delays in the existing interruption transaction.
- History pruning leaves running Runs and matched-Fact receipts intact, then
  collects revisions referenced by neither a current head nor any retained Run.

Only after successful admission commits does the Service start Run workers.
Consumer acknowledgement follows durable admission, not worker completion.
See [the held-state specification](../../../specs/held-state-triggers.md) for
the hold cursor, expiry, and restart rules.
See [the schedule specification](../../../specs/scheduled-automation-triggers.md)
for current-minute admission, household-local matching, and no-replay rules.

Pure domain decisions and snapshot construction operate on domain values. SQL
encoding, generated row types, and transaction orchestration stay in SQLite.

## Tests and generation

Persistence tests live beside the SQLite implementation and exercise migrated
real databases through its public API. Domain and orchestration tests remain
with the behavior they protect; external-package Service tests may assemble the
public SQLite repository without adding a production import back to SQLite.

`sqlite/dbqueries` supplies `sqlite/dbsqlc` through root `sqlc.yaml`. Regenerate and
validate with `mise run validate`; do not hand-edit generated query code.

### Schedule admission measurement

Run the opt-in bounded measurement without race instrumentation:

```sh
HEARTH_SCHEDULE_TIMING=1 \
  GO_PACKAGES='./internal/modules/automations -run=TestScheduleServiceLargeDefinitionSetAdmission$ -race=false -count=3 -v' \
  mise run --skip-deps test
```

The fixture uses migrated file-backed SQLite and the real Service, with 1,000
enabled definitions, one cron Trigger, one Condition leaf, and one Command Step
each. At the sampled minute, 100 definitions match and their Conditions evaluate
true over 100 distinct State Entities. The other 900 schedules do not match.
The measurement includes definition preparation, the scripted coherent State
read, the atomic admission transaction, and worker launch. Worker completion and
fixture creation are outside the measured interval.

On the local Linux development host on 2026-10-02, three runs took
161.492 to 161.731 ms through the Service and 101.499 to 102.536 ms inside the repository
tick, below the unchanged two-second admission deadline. Every run admitted and
executed 100 Commands. This is not a capacity guarantee for larger definitions,
slow Devices reads, competing database writes, or 1,000 simultaneous Runs. Race
instrumentation exceeded the admission deadline for this fixture, so it is not a
CI timing assertion. An exploratory 1,000-simultaneous-match run returned from
admission in 532.657 ms without race instrumentation, but its worker completion
failed under load. It does not establish supported execution capacity.
