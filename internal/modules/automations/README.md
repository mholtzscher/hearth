# Automations module ownership

Automations is one product module. Its packages separate domain behavior from
HTTP, NATS, and SQLite implementation details; Triggers, Runs, and Steps remain
parts of that module rather than independent packages or services.

## Package boundaries

| Package | Owns |
| --- | --- |
| `automations` | Definitions, Trigger matching, Condition definitions and three-valued evaluation, admission and execution use cases, retention policy, domain types, repository interfaces, and Run worker lifecycle |
| `automations/api` | HTTP operations, MCP tools and resources, transport models, cursors, and error mapping |
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
| Service create/replace | Typed structure and current Devices references |
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
| Repository branch decision write | Freely constructed evidence against immutable snapshot and matches; running parent, insert-once identity, contiguous position, nondecreasing time, atomic failure |
| Repository delay start | Freely constructed identity, UTC start, and contiguous reached position against a running parent's immutable prepared snapshot; insert once after earlier delays are terminal |
| Repository delay completion | Terminal evidence and legal running-delay transition under a running parent; interruption terminalizes delay and Run atomically |
| Step/Run completion | Terminal outcome input and legal persisted transition |
| Persistence decoding | Decode retained representation; preserve existing corruption checks |

Device Fact matching helpers operate on validated definitions and facts; export
alone does not make them independent input boundaries.
`ValidateAndMatchScheduledTriggers` is an independent boundary for freely
constructed definitions. Service snapshot preparation and transactional schedule
admission use
`MatchPreparedScheduledTriggers` on unchanged repository-returned definitions
to reuse schedules compiled during decoding without re-normalizing or re-encoding.
JSON, pointer, and duration handling still parses defensively.

## File responsibilities

- `definition.go`, `definition_codec.go`, `definition_validation.go`, and
  `definition_management.go` own definition types, encoding, validation, and
  service operations respectively; `definition_validation.go` also prepares
  normalized, size-checked bytes for repository writes. Its `prepareDefinition`
  helper validates structure, owns canonical copies, and compiles cron clock
  fields; callers enforce raw or encoded size limits. `conditions_codec.go`
  handles Condition trees. `definition_wire.go` owns concrete Step and Trigger
  wire DTOs, and `definition_labels.go` derives scalar labels from value bodies.
  Steps, Triggers, and Conditions use sealed, annotated Body unions. Input
  boundaries accept only supported concrete values, not nil or pointer bodies.
  The v2 definition schema requires `kind` for Commands and accepts only
  `value_pointer` for comparisons.
- `branching.go` owns recursive Step families, bounded tree preparation, and
  `CommandLeaves`. That traversal consumes unchanged normalized sequences, not
  arbitrary Go input. Its slice indexes are stable command-attempt positions;
  it walks Then before Else and Choose alternatives before Default. Branch
  nodes and delays never consume a command position. Every delay counts toward
  tree bounds, but definitions may have zero Command leaves.
- `branch_evaluation.go` selects from prepared branch roots using shared Condition
  evaluation. `branch_execution.go` owns reached-branch reads, decision writes,
  sequential tree traversal, and Run-only interruption. It collects roots and
  Entity IDs once per reached branch and passes them into evaluation for coverage
  checks and selection. Admission and branch evaluation check coverage before
  calling the shared Condition evaluator, which does not recollect references.
  `execution.go` owns Command attempts and verified outcomes.
- `branch_decision.go` and `branch_result.go` own typed selection results;
  `branch_decision_codec.go` selects strict family/outcome DTOs for retained JSON.
  `branch_decision_validation.go` validates arbitrary decisions against immutable
  snapshots and match sets. `sqlite/branch_decisions.go` owns atomic appends and
  failure transitions, and retained row identity/order checks. Decision writes and
  retained reads reuse the snapshot prepared by `DecodeDefinition` for every
  evidence record; each record still receives shape and snapshot-relative checks.
  `api/branching.go` reuses the canonical branch codec for HTTP and MCP output.
- `delay.go` owns delay types and evidence validation. `delay_execution.go`
  owns native elapsed waiting and persistence gates. `snapshot_steps.go`
  resolves evidence identities in prepared immutable Step trees.
  `sqlite/delays.go` owns transactional start/completion and retained history
  projection; `api/delays.go` maps delay evidence for HTTP and MCP.
- `fact_processing.go`, `manual_runs.go`, `held_state_processing.go`, and
  `schedule_processing.go` own the admission workflows. `schedule_matching.go`
  owns cron parsing and immutable prepared clock fields, and matches the sampled
  current minute in the household location. Normalization retains that preparation
  privately on `CronTrigger`; changing its expression requires normalization again.
  `conditions_snapshot.go` reads Condition State;
  `conditions_decision.go` decides from that snapshot.
- `repository.go` defines persistence contracts; `dependencies.go` defines the
  Devices seam and service configuration. `admission_results.go` carries outcomes.
- `fact.go` owns the concrete Observation and Entity Event Fact values and their
  validation. `admission_cause.go` carries owned manual, Fact, held-state, or
  schedule provenance; `admission_cause_validation.go` checks retained provenance
  against matched Trigger snapshots without applying live Fact freshness limits.
  `fact_labels.go` derives existing receipt, SQL, and log labels.
- `run_outcome.go` and `step_outcome.go` separate terminal outcomes from active
  lifecycle states. `lifecycle_labels.go` derives public and stored labels and
  verified evidence. `sqlite/lifecycle_mapping.go` rejects contradictory retained
  rows, including partial reservations, while allowing completed interruption
  before reservation. `api/provenance.go` owns the public Cause and Fact DTOs.
  `history_model.go` carries Run-or-Skip value results and materialized summary
  bodies; listing does not decode full snapshots or Condition decisions.
- `condition_evidence.go` derives leaf results from Known State, Unknown State,
  or Trigger intersections. `condition_evidence_validation.go` enforces retained
  evidence shape; branch validation additionally checks it against the snapshot.
  `conditions_decision_codec.go` selects concrete decision and leaf DTOs.
  `condition_summary.go` projects the four materialized explanation variants;
  `sqlite/condition_summary.go` rejects contradictory summary columns.
- `automation-definition.schema.json` and `automation-history.schema.json` own
  the v2 public contracts. `api/output_schema.go` publishes self-contained
  recursive schemas for HTTP and MCP. Both transports use the same encoders;
  MCP's explicit schema and exact output preserve raw JSON numbers and nulls.
  `api/models.go` reuses the canonical definition and matched-Trigger codecs;
  `api/lifecycle.go` and `api/history.go` select concrete public lifecycle and
  history DTOs. Test-only response readers do not constrain production models.
- `comparison.go` owns State comparisons; `json_codec.go` owns strict JSON
  decoding helpers; `pagination.go` owns shared page limits.
- `api/conditions.go` maps Conditions; `api/manual_run_body.go` decodes manual
  requests. `sqlite/held_state.go` persists holds and due outcomes, while
  `sqlite/execution.go` owns Run and Step transitions, including interruption.

## Development cutover

Start this version against a fresh development database path and re-enter
definitions using the v2 format. Old Automation documents and retained history
are unsupported; there is no fallback decoder or automatic database reset.
All Command Steps require `kind: "command"`; comparisons accept only
`value_pointer`. Public provenance is a required `cause` object, and Condition
leaves include `kind`. Devices-owned NATS envelopes retain their existing format.

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
be equal; contiguous decision positions establish reached order. A decision is
selection evidence, not a Command attempt or proof of completion. `Run.Steps`
contains attempts for every defined Command leaf; unselected and unreached
commands remain `not_attempted`. `Run.BranchDecisions` contains only reached
branches; Runs that reach no branch return an empty array through both transports.

Expected unknown fails only that Run. Decision persistence faults stop dispatch,
attempt Run-only interruption, and latch admission/readiness failure. Branch
drain and faults never invent or complete a command attempt. Startup preserves
committed evidence and interrupts active Runs without resuming execution.

Migration `00010_automation_branch_decisions.sql` adds the cascading decision
child table. Down drops decision evidence only, not branching definitions or
snapshots. Older binaries require their matching database and frontend.
See the [branching specification](../../../specs/automation-branching.md) and
[operator guide](../../../docs/automation-branching.md) for bounds and rollback.

## Delay execution and evidence

The Service owns a stop broadcast independent of admission caller contexts and
device Command contexts. `StopAdmission` signals `core_stopping`; executor
faults signal `executor_fault`. Pending delays wake promptly, while in-flight
Commands retain their existing shutdown policy. `Drain` joins workers and does
not create a second cancellation authority. Application assembly still owns
shutdown ordering.

`delay_execution.go` starts monotonic elapsed accounting before recording the
reached delay. It commits start evidence, waits only for the remaining duration,
then commits completion before continuing traversal. SQLite transactions never
remain open during a timer wait. Diagnostic UTC start/completion timestamps use
the existing `Dependencies.Now`; they do not control the native timer. No timer
factory, durable scheduler, NATS job, or executable continuation is introduced.

`Run.Delays` contains only reached delays in contiguous order, independently of
Command and branch positions. History derives duration from the identified
Delay Step in the immutable snapshot and due time from recorded start plus that
duration. Stored rows contain neither duration nor due time. Completion wall
time may precede start or due after clock correction; position establishes
reached order. Invalid identity, timestamps, status shape, or snapshot data
causes a read error, not synthesized progress. A terminal Run cannot retain a
running delay.

A start or completion persistence fault stops later Steps, attempts interruption,
latches admission/readiness failure, and wakes other pending delays. There is
no execution retry. Ambiguous or unavailable writes retain the last provable
durable state for startup recovery. Startup atomically interrupts active delays,
Command attempts, and Runs with `core_restarted`; it never resumes timers or
replays Commands. A completed delay proves waiting ended and evidence committed,
not that its next Step executed. A delay-only Run can succeed with `steps: []`.

Migration `00011_automation_run_delays.sql` adds cascading child evidence.
Running parents remain protected by existing history retention. Down drops
evidence only; older binary rollback requires a pre-feature database backup
because definitions and snapshots still contain delay nodes. Existing HTTP/MCP
operations and canonical schema support authoring with no new tools. Browser
definitions and delay history are read-only with manual refresh.
See the [delay specification](../../../specs/automation-delay-steps.md) and
[operator guide](../../../docs/automation-delay-steps.md).

## Transaction boundaries

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
- History pruning leaves running Runs and matched-Fact receipts intact.

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
