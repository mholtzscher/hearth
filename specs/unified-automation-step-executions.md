# Unified Automation Step executions

Status: approved for implementation by the user's explicit request to implement this sketch. The user selected typed JSON evidence with native indexed lifecycle columns, atomic Command/Run failure, selected-path completeness checks, and inclusion of #216 in the unified-table delivery.

The user subsequently approved exposing the unified `executions` array through
HTTP, MCP, and the browser. That approval replaces the original compatibility
projection requirement. Existing endpoints change in place, without legacy arrays
or a second API version for this shape.

Implementation now replaces the five production child-evidence paths with the
unified trace and migration 22. The Automations and SQLite integration suites,
public HTTP/MCP tests, and Core integration checks have passed during iteration.
A live simulator HTTP journey verified mixed selected/unselected history and
failure stopping later effects. Final validation and the repeated before/after
comparison are recorded in [implementation verification](../docs/automation-step-executions.md#verification-and-measurement).

The selected-path completeness rule appears under [Transaction and failure rules](#transaction-and-failure-rules). Implementation proceeds in an isolated worktree and stops before merge.

Grounded against `main` at [`a6fa9af`](https://github.com/mholtzscher/hearth/commit/a6fa9af421f4fd1cc4f784602c4bbf3be58d39f0). The checkout was initially three commits behind. A later `git pull --ff-only`, requested by the user, verified that local `main` is at this revision and already up to date. No product code changed.

The user selected a reset of all Automation-owned data for this cutover. Implement that reset as a new migration and exercise it against disposable test databases. Do not modify a deployed database during development.

## Problem

Automations stores one execution in five different child-evidence tables. Commands preallocate every definition leaf, including unselected leaves. Other kinds record only reached Steps. The worker carries stable Command positions and four independent reached counters. Starting a Step reconstructs the full Run and all five evidence collections to establish shared lifecycle rules.

The shared-definition work is already merged. A Run now references one immutable definition revision, while Skips remain inline. The unified table should build on that representation rather than restore per-Run definition copies.

### Ticket check

| Issue | Observed state | Evidence |
| --- | --- | --- |
| #218, pruning indexes and cursor seeks | Implemented and merged | [PR #221](https://github.com/mholtzscher/hearth/pull/221), `10f85e4` |
| #215, history summary projections | Implemented and merged | [PR #223](https://github.com/mholtzscher/hearth/pull/223), `ba47f29` |
| #220, shared definition revisions | Implemented and merged | [PR #224](https://github.com/mholtzscher/hearth/pull/224), `a6fa9af` |
| #216, targeted transition reads | Open | Current transition code still reconstructs the full Run |
| #217, Fact candidate routing | Open | Current Fact admission still loads enabled definitions |
| #219, receipt compaction | Open | Receipt representation remains unchanged |

The three merged PRs show successful backend and web checks. This planning session did not rerun their tests or reproduce their measurements. PR #224 used an explicitly approved Automation-only reset rather than a lossless historical conversion.

### Current source anchors

- [`repository.go`](https://github.com/mholtzscher/hearth/blob/a6fa9af421f4fd1cc4f784602c4bbf3be58d39f0/internal/modules/automations/repository.go) exposes separate start/completion operations for each evidence family.
- [`branch_execution.go`](https://github.com/mholtzscher/hearth/blob/a6fa9af421f4fd1cc4f784602c4bbf3be58d39f0/internal/modules/automations/branch_execution.go#L15-L65) owns the per-kind counters.
- [`run_evidence.go`](https://github.com/mholtzscher/hearth/blob/a6fa9af421f4fd1cc4f784602c4bbf3be58d39f0/internal/modules/automations/sqlite/run_evidence.go) reconstructs the aggregate to check active evidence.
- [`execution.go`](https://github.com/mholtzscher/hearth/blob/a6fa9af421f4fd1cc4f784602c4bbf3be58d39f0/internal/modules/automations/execution.go) reserves Commands before dispatch and can record interruption before reservation.
- [`dbqueries/automations.sql`](https://github.com/mholtzscher/hearth/blob/a6fa9af421f4fd1cc4f784602c4bbf3be58d39f0/internal/modules/automations/sqlite/dbqueries/automations.sql) retains the summary projection, history indexes, and shared revision references.

## Usage, caller's view

The following calls are proposed, not implemented. Existing per-kind execution functions keep responsibility for external operations and ownership verification. They share one persistence lifecycle instead of one storage API per kind.

### Command reservation and completion

```go
// Consumer: automations Service, using its injected Repository.
err := repository.RecordStep(ctx, StepExecutionRecord{
    RunID: run.ID, StepID: step.ID,
    Evidence: CommandStarted{StartedAt: now, Reservation: reservation},
})
// Only a successful commit permits ExecuteCommand with that reservation.

// After the existing Devices reconciliation establishes the result:
err = repository.CompleteStep(ctx, StepExecutionCompletion{
    RunID: run.ID, StepID: step.ID,
    Evidence: CommandFinished{
        CompletedAt: completedAt, Reservation: reservation, Outcome: outcome,
    },
})
```

### Reached branch selection

```go
// Evaluate fresh State using the current branch policy, then commit once.
err := repository.RecordStep(ctx, StepExecutionRecord{
    RunID: run.ID, StepID: step.ID,
    Evidence: BranchDecided{Decision: selection},
})
// A committed positive selection permits traversal of the selected children.
// Unknown/error selection and failed parent commit together.
```

### Retained history

```go
entry, err := service.GetHistoryEntry(ctx, automationID, string(run.ID))
// The canonical Run contains its definition and one ordered Executions slice.
// HTTP and MCP serialize its Executions directly through the canonical codec.
// The browser displays one cross-kind execution timeline.
```

The repository allocates sequence numbers. Callers no longer allocate Command positions or per-kind reached positions for persistence.

For a selected path containing an If, a Command, a Delay, and a Notification, retained rows look like this:

| Sequence | Step ID | Kind | Phase | Evidence |
| --- | --- | --- | --- | --- |
| 0 | `route` | if | succeeded | Then selected, evaluated State leaves |
| 1 | `turn-on` | command | succeeded | Reserved identity and verified satisfied Command |
| 2 | `settle` | delay | succeeded | Start and completion timestamps |
| 3 | `announce` | notification | succeeded | Reserved attempt and retained provider acceptance |

The unselected Else leaves have no execution rows or public placeholders. The `route` row does not become evidence that `turn-on`, `settle`, or `announce` completed.

## Shape

### Data structures

Proposed SQL shape. This is an outline, not migration-ready SQL. Canonical-ID, bounds, per-kind payload, and transition constraints must be filled in during approved implementation.

```sql
CREATE TABLE automation_step_executions (
    run_id        TEXT NOT NULL REFERENCES automation_history(id) ON DELETE CASCADE,
    step_id       TEXT NOT NULL,
    sequence      INTEGER NOT NULL CHECK (sequence BETWEEN 0 AND 63),
    kind          TEXT NOT NULL,
    phase         TEXT NOT NULL,
    started_at    TEXT NOT NULL,
    completed_at  TEXT,
    evidence_json TEXT NOT NULL CHECK (json_valid(evidence_json)),
    PRIMARY KEY (run_id, step_id),
    UNIQUE (run_id, sequence)
);

CREATE UNIQUE INDEX automation_step_executions_one_running_idx
ON automation_step_executions(run_id) WHERE phase = 'running';
```

Keep indexed lifecycle fields in columns and kind-specific reservation/result evidence in a small typed JSON document. Do not copy Step parameters, notification messages, Function arguments, or Condition definitions already available from the immutable revision. Preserve per-execution values, selected branches, reservations, verified links, failure reasons, and catalog evidence.

Preserve the current uniqueness of Notification Attempt and Function Invocation reservations with partial expression indexes over their typed payload fields. Enforce required field presence and kind/phase consistency at persistence boundaries; `json_valid` alone is insufficient. There is no foreign key to independently retained external attempts or invocations.

The JSON document does not duplicate the row's identity, sequence, lifecycle timestamps, or generic lifecycle fields. Source-evidence timestamps, such as a Condition leaf's Observation time, retain their own meanings in the payload. The codec derives the row columns from typed execution details when writing and validates their relationship when reading.

### One row per durably recorded reach

- The definition Step ID identifies the instruction. The pair of Run ID and Step ID identifies its one execution under today's no-loop/no-retry contract.
- `sequence` is one contiguous, cross-kind record order assigned in the same transaction as insertion. Use the Run's current maximum plus one under that transaction, with uniqueness as the final guard. Do not introduce another UUID, attempt counter, persisted next-sequence cache, or global sequence.
- A row records either a committed start or an instantaneous terminal result. A Command stopped before reservation gets an interrupted row without a fabricated Command ID.
- Unselected and unreached Steps have no rows. No admission-time Command rows remain.
- Absence means no durable Step evidence, not proof that the executor never visited the instruction. A failed start write or a stop before reservation can leave no row. Neither API nor browser invents a start, outcome, effect, or explanation for that absence.
- Ordering comes from sequence, never wall-clock sorting. Preserve diagnostic timestamps and existing per-kind validity rules. Do not add a `completed_at >= started_at` constraint.
- Branch rows describe completed selection, not an interval spanning their children. Set both lifecycle timestamps to the existing evaluation time for this instantaneous record; do not claim an evaluation duration. A successful selection is `succeeded`; unknown/error is `failed`. No branch remains `running` while its children execute. Success describes only the selection, not its selected children.

### Common lifecycle, distinct results

| Kind | Allowed common phases | Retained result meaning |
| --- | --- | --- |
| Command | running, succeeded, failed, interrupted | Required Command outcome attained, or established failure/interruption; retain satisfied/dispatched distinction |
| Delay | running, succeeded, interrupted | Elapsed wait completed or interrupted; duration still comes from the definition |
| Notification | running, succeeded, failed, interrupted | Provider acceptance or established failure/interruption, never delivery/readership |
| Function | running, succeeded, failed, interrupted | Retained Invocation success or established failure/interruption, never inferred execution |
| If/Choose | succeeded, failed | Selection/no-match or unknown/error; says nothing about child completion |

`phase` is a SQL projection, not a second discriminator stored alongside a Go variant. Keep module-owned sealed value variants and exhaustive dispatch. Do not replace typed outcomes with maps, `any`, or one optional-field Go struct.

Proposed declarations, with implementations deliberately omitted:

```go
type StepExecutionRecord struct {
    RunID    RunID
    StepID   StepID
    Evidence InitialStepEvidence
}

// Sealed value variants: CommandStarted, DelayStarted, NotificationStarted,
// FunctionStarted, BranchDecided, CommandStopped.
type InitialStepEvidence interface { isInitialStepEvidence() }

type StepExecutionCompletion struct {
    RunID    RunID
    StepID   StepID
    Evidence CompletedStepEvidence
}

// Sealed value variants retain each family's existing ownership/result rules.
// Include the expected reservation when the completion verifies a reserved link.
type CompletedStepEvidence interface { isCompletedStepEvidence() }

type StepExecution struct {
    StepID   StepID
    Sequence int
    Details  StepExecutionDetails // sealed typed lifecycle and evidence
}

type StepExecutionDetails interface { isStepExecutionDetails() }

// Replace the per-kind recording methods in the existing consuming interface.
// Other existing Repository capabilities are omitted from this sketch.
type Repository interface {
    RecordStep(context.Context, StepExecutionRecord) error
    CompleteStep(context.Context, StepExecutionCompletion) error
}
```

### Transaction and failure rules

1. Admission still atomically records the Run/Skip, receipt, decision, and schedule progress where applicable. It no longer allocates empty Command attempts.
2. `RecordStep` independently validates input and resolves the Step against the Run's retained revision. In one transaction it checks the running parent, appropriate kind, no prior row for that Step, no active sibling, and prior evidence required by the operation, then inserts the next sequence. It never performs external work.
3. `CompleteStep` verifies the running target and expected reservation before recording a terminal result. Failed or interrupted results also terminalize the Run atomically. Successful results permit later execution only after commit.
4. Terminal records inserted through `RecordStep` follow the same parent rule. Unknown/error branch evidence and failed parent commit together. A Command stopped before reservation and its interrupted parent also commit together. Branch recording is insert-once, and positive selection does not imply that any selected child has started.
5. A successful Run must have a complete successful trace of its selected path. Validate it against the bounded snapshot and recorded selections once at Run completion. Apply the same pure trace validator on retained detail reads. Reject missing required rows, unselected executions, or an order inconsistent with the selected path. Partial prefixes are legitimate for running, failed, or interrupted Runs, not evidence of success. Absence never establishes progress.
6. Startup recovery interrupts running execution rows and their parents in one transaction. Operation-scoped cleanup touches only the affected running Run. Preserve terminal evidence and uncertain reservations. Do not adopt external outcomes, retry effects, or resume the trace after restart.
7. Repeating a Step start, decision insert, or terminal transition remains a rejected transition, not a retry protocol. Existing cleanup remains safe on an already-terminal Run. A commit reported as failed stops advancement and uses the existing single cleanup attempt.

Ordinary writes use parent status, the target Step/reservation, indexed active-state checks, and the required bounded control evidence. They must not rebuild all historical outcome payloads to check one transition. Detail reading loads the unified rows once; completion may do the selected-path validation once. No transaction remains open during external work or waiting.

Two agreed consistency changes are explicit rather than claims of existing behavior. Command failure and Run failure become atomic, matching the other kinds. Every Step on the selected path must also have the required retained result before successful completion, with the same completeness check when reading retained successful history. The current generic Command completion guard alone does not prove that coverage.

### Public execution contract

Replace the five public history arrays with one sparse `executions` array in the
existing HTTP and MCP operations. This is an intentional breaking change.

- Each entry contains `step_id`, `sequence`, `kind`, `phase`, `started_at`, optional `completed_at`, and typed `evidence`.
- Use one zero-based sequence across all kinds. Only durably reached Steps appear; no `not_attempted` placeholder is synthesized.
- Keep common lifecycle phases and distinct typed outcomes such as `satisfied`, `dispatched`, and `accepted` inside evidence.
- Keep static configuration in the shared revision. The browser derives Delay duration and diagnostic due time from the snapshot and recorded start.
- Expose reserved identities as reservations, not proof of external admission or effects. Only ownership-verified Command IDs become Command detail links.
- Keep lightweight history collection queries unchanged. A summary read must not load execution rows or the revision document.
- Replace the browser's per-kind tables with one ordered timeline. Preserve kind-specific evidence and external-detail links.
- Do not add an execution UUID, cursor, parallel legacy response, or schema version for this change.

### Ownership and subtraction

`automations` owns typed execution records, codecs, and selected-path validation. Its SQLite package owns the single table, constraints, query sources, and transaction orchestration. Existing service executors retain Devices/Notifications/Functions reconciliation and lifecycle policy. Application shutdown ordering remains unchanged, including the Function reservation mutex and internal-admission guarantees.

Replace the five evidence tables, their query families, preallocation, per-kind persistence counters, and repeated active/interruption enumeration. Replace the five canonical mutable Run evidence arrays with `Run.Executions`. Remove the old public arrays and compatibility projection. Migrate callers, browser types, fixtures, and tests in the same change.

This gives a deeper persistence interface: two typed recording operations hide sequence allocation, cross-kind lifecycle checks, and atomic parent updates. Callers still own external evidence reconciliation because storage cannot establish provider outcomes.

This follows [Model the domain](file:///home/michael/.agents/skills/mstack/principles/model-the-domain.md) by separating instructions from reached executions, and [Minimize reader load](file:///home/michael/.agents/skills/mstack/principles/minimize-reader-load.md) by replacing the existing paths rather than adding another shared header above them. [Boundary discipline](file:///home/michael/.agents/skills/mstack/principles/boundary-discipline.md) keeps independent repository validation while avoiding full-history reconstruction for local checks.

## Synthesis decision

This is a direct design grounded in the current implementation, with a separate read-only investigation of migration constraints. That investigation confirmed that valid post-21 order could be reconstructed from the snapshot and selected path, but that absent start evidence cannot reveal a failed write's visited instruction. The user selected reset-all after the investigation began, so this sketch deliberately does not adopt its preservation/backfill option. No independent competing implementation or performance experiment was run.

Recommend one sparse table with common indexed lifecycle columns and typed per-kind evidence. It removes the physical split and the Command-only preallocation model while preserving the distinctions the executor and public history actually need.

## Tradeoffs accepted

- We accept a new internal codec and per-kind payload validation in exchange for one storage lifecycle and one ordered trace.
- We accept decoding a small Step payload on targeted reads in exchange for avoiding a wide nullable table with fields belonging to unrelated kinds.
- We accept breaking public history consumers in exchange for one execution model without a compatibility projection. HTTP, MCP, and the browser migrate together.
- We accept the user-selected Automation-only reset in exchange for no historical-order inference, legacy table reader, or dual-write period.
- We accept selected-path validation at successful Run completion and retained successful detail reads in exchange for preventing sparse absence from becoming invented success. The user agreed to this consistency change.

These are structural benefits, not measured speedup or storage-reduction claims.

## Alternatives considered

| Shape | Decision |
| --- | --- |
| Keep five tables and only optimize their queries | Smaller change for #216, but retains separate positions, preallocation, and repeated lifecycle/table lists. Not the base for this sketch. |
| Add a shared execution header above five payload tables | Gives common identity but retains multiple persistence paths and adds join/write coordination. Reject unless later evidence proves typed JSON cannot meet the contracts. |
| Put every kind-specific field in native nullable columns | Strong direct SQL visibility, but grows a cross-kind matrix of unrelated fields and checks. Prefer bounded typed payloads for values not used in selection. |
| Append a separate event for every transition | Preserves more chronology but requires state folding and more writes. The current product needs retained execution state and decisions, not replay or an event-sourced engine. |

## Cutover and verification

Use the next new migration after revision sharing. Do not edit migration 21 or modify a deployed database during development.

The user-selected reset clears Automation definitions, retained definition revisions, Run/Skip history and child evidence, holds, receipts, and schedule progress. Other module schemas and data remain intact. Preserve the post-#220 revision-sharing layout and the #218 history indexes. Recreate desired Automations after the upgrade. Receipt continuity is deliberately lost across this disposable cutover; there is no general promise of deduplication across the reset.

Drop the superseded child tables and introduce the unified table under one transaction with foreign keys enabled. Migration failure must roll back both data and schema. Later startups must not reset new data. Keep the existing consumer/recovery policies and do not replay retained history. Down should refuse automatic conversion; binary rollback requires the matching pre-upgrade database backup.

Implementation checkpoints:

1. Add pure typed-record, codec, public-schema, and selected-path behavior tests using explicit expected traces. No database in unit tests.
2. Add real SQLite migration/repository integration tests. Cover mixed kinds, unique sequence, one active Step, no duplicate reach, wrong reservations, parent guards, false/unknown branches, terminal evidence, clock regression already allowed by kind policy, and interruption before Command reservation.
3. Verify sparse public history on selected/unselected nested branches, early failures, delayed Runs, source-history expiry, and zero-side-effect paths. Verify HTTP/MCP schema agreement and the browser's mixed-kind timeline.
4. Exercise shutdown, startup, and persistence-fault boundaries with existing Core/simulator journeys where they expose a distinct assembly guarantee. Assert no later external effect after failed persistence and no restart replay.
5. Populate every Automation-owned table for the reset test, compare all unrelated schemas and rows, inject rollback failures, and prove a second startup preserves new data.
6. Compare equivalent mixed-Step file-backed workloads before and after with the same driver, WAL, durability, and connection settings. Include sparse and dense branches. Run at least five alternating samples per side after explicit warmup, verify identical selected effects and terminal outcomes, and report medians and ranges. Record rows written, query/decode work, transaction duration, allocations, and retained bytes. Profile separately to identify the remaining limiter; do not assume JSON or fewer tables is automatically faster.
7. Use focused mise tasks while iterating. After integration, unset `GO_PACKAGES`, run `mise run validate`, and review generated/formatting changes.

The test choices follow [Test behavior](file:///home/michael/.agents/skills/mstack/principles/test-behavior.md). The checkpoints follow [Verifiable units](file:///home/michael/.agents/skills/mstack/principles/verifiable-units.md). They are proposed validation, not checks run during this planning session.

## Agreed decisions and remaining risks

- The user agreed to typed per-Step JSON evidence with native indexed lifecycle columns rather than a wide native-column table.
- The user agreed to atomic Command/Run failure. Moving the parent update into the existing Step transaction removes the executor's separate failure-completion write without adding a distributed transaction or new lock.
- The user agreed to complete selected-path evidence before Run success, with the same validation on retained successful detail reads. Missing required results, unselected executions, and impossible order must not become successful history.
- The user selected reset-all-Automation-data and explicitly approved implementation of this sketch. Document the required deployment backup and cutover procedure; do not modify deployed data during development.
- The user agreed to include #216's targeted-transition-read goals in the unified-table PR. Design the new repository operations with targeted reads from the start, rather than optimizing the five-table aggregate first. Close #216 only after its acceptance criteria are verified; do not claim that the schema change alone satisfies them.
- #217 and #219 remain separate. This sketch does not compact receipts, expire them, add Fact routing, or replace SQLite.

Revisit the design if implementation requires generic payload maps, repeated lifecycle rules in parallel paths, or a persistent duplicate execution model. Do not conceal those problems behind another adapter or registry.

## Next implementation step

After agreement on this sketch and separate implementation authorization, prepare a workspace from the current remote base and implement the typed execution record and its behavior tests before wiring the new SQLite cutover and repository lifecycle.
