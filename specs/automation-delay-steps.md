# Automation delay Steps

Status: Implemented on `feat/automation-delay-steps`; not a merge or deployment claim
Approved by: User through planning dialogue
Date: 2026-10-04
Type: Feature plan
Effort: L, approximately 1 to 2 days including tests and integration. Confidence is moderate; lifecycle races and transport parity are the main uncertainty.

## Problem and scope

Hearth needs ordered Automations that pause between device operations, such as turning a light on, waiting five minutes, and turning it off. Existing Steps can command or branch, but cannot wait within an admitted Run. A Held-State Trigger waits before admission and does not solve this problem.

Add a fixed elapsed-time Delay Step throughout the existing bounded Step tree. Keep execution in the existing Run worker with an interruptible Go timer. Persist reached wait evidence, not executable continuations. HTTP and MCP support authoring; the browser supports definition and history rendering only.

This specification extends [Automations](automations.md) and [branching](automation-branching.md). It supersedes their exclusion of delay Steps and their requirement for at least one Command leaf. Other admission, Command, branching, and recovery rules remain unchanged. Domain language is defined in [the glossary](../GLOSSARY.md).

## Agreed behavior

1. A Delay Step specifies an integer `duration_ms` from 1 through 86400000 inclusive. Zero, negative, fractional, omitted, null, and out-of-range durations are invalid. Millisecond precision is input precision, not a scheduling-latency guarantee.
2. Its wait starts when execution reaches it, after preceding Steps succeed. Time spent persisting its start counts toward the duration. Later Steps wait for both elapsed time and durable completion evidence.
3. Delays work at top level and inside selected If/Choose sequences. Unselected or unreached delays create no execution record or timer. Returning from a selected sequence continues ordinary sequential execution.
4. A delayed Run remains `running`, occupying the Automation's existing single-active-Run slot. Existing admission precedence remains intact. Eligible automatic invocations get `automation_busy` Skips; manual invocations return the existing busy conflict, not a new Skip.
5. Delay-only definitions are valid and still need the existing Triggers. Present sequences remain nonempty, with at most 32 children, 64 total Step nodes, depth 8, and at most 32 Command leaves. Command leaves may number zero. Every delay counts as one node, never as a Command position. Other Trigger, Condition, size, and Choose bounds remain unchanged.
6. There is no total Run-duration limit or new process-wide Run cap. Several consecutive delays may keep a Run active for days. Fact freshness applies to admission, not Step execution.
7. Delays measure elapsed time with Go's monotonic timer behavior. Wall-clock correction does not shorten or extend them. Recorded `started_at` and `completed_at`, plus derived `due_at`, are diagnostic UTC timestamps, not timer authority or restart instructions. History derives duration from the immutable Run snapshot and due time from that duration plus the recorded start time.
8. Graceful shutdown wakes pending delays promptly, records interruption, and executes no remaining Steps. Commands already in flight retain their existing shutdown policy. Caller disconnection does not cancel an admitted Run.
9. Restart interrupts active delay records and Runs atomically with `core_restarted`. No wait resumes, no overdue Step executes, and no Command is replayed.
10. Definition replacement, disablement, or deletion does not change an active Run's snapshot, duration, or remaining sequence. Admission Conditions are not rechecked after waiting. Reached branches still read current coherent State.
11. Start and completion writes are required execution gates. A persistence fault stops that Run, attempts interruption, and latches admission/readiness failure without execution retry. Latching also wakes other pending delays. No fault invents a Command attempt.
12. A successful Run completed its selected sequence. A delay-only Run may succeed with `steps: []`. History exposes reached delays separately from Command attempts and branch decisions.

## Non-goals

- State waits, clock-time waits, calculated durations, loops, parallel Steps, queues, retrigger-reset modes, retries, compensation, or resumable Runs.
- User-facing Run cancellation or changes to device Command cancellation.
- A durable scheduler, NATS timer subjects, broker jobs, or a continuation queue.
- Browser authoring, a general Automation editor, countdown promises, new polling, or push notifications.
- Changing Command-attempt positions, branch-decision positions, history pagination, retention configuration, or existing endpoint/tool identities.

## Discovery and recommendation

The `automations` product module already owns Run workers and the domain-oriented `Repository`. Its SQLite package owns transactions and generated queries; its API package maps shared service behavior to HTTP and MCP. `devices` remains the Command authority. Application assembly owns shutdown ordering.

`executeSequence` dispatches command and branch families. `CommandLeaves` assigns positions only to Commands. Admission starts workers after transaction commit and records snapshots independently of current definitions. `StopAdmission` currently only closes an admission gate, and `Drain` joins detached workers without canceling them. Simply adding `time.Sleep` would hold graceful shutdown open.

| Option | Benefit | Cost | Decision |
| --- | --- | --- | --- |
| Sleep without wait history | Few changes | Blocks shutdown and cannot explain reached waits | Reject |
| Interruptible native Run timer with audit rows | Preserves existing traversal and recovery; records waits | Retains one goroutine and timer per active wait | Choose |
| Separate scheduler or durable continuation engine | Releases workers; could support future resumption | New execution-progress ownership and recovery policy, unnecessary here | Reject |
| Derive duration and due time from snapshot and start | Removes duplicate columns, start-input metadata, and disagreement checks; preserves history output | Requires the immutable snapshot already loaded for Run detail | Choose over storing duplicate wait metadata |

Use `time.NewTimer` and a service-owned broadcast stop context. Do not inject a timer factory or introduce a clock/scheduler interface solely for tests. Existing `Dependencies.Now` supplies diagnostic wall timestamps; native `time.Now` and `time.Since` establish the private monotonic interval. No new configuration, wire event, NATS resource, dependency, or product package is needed.

## Implementation contract

### Domain and retained types

Existing-family changes are focused additions:

```diff
diff --git a/internal/modules/automations/definition.go b/internal/modules/automations/definition.go
@@
 type Step struct {
@@
     If            *IfStep
     Choose        *ChooseStep
+    Delay         *DelayStep
 }
diff --git a/internal/modules/automations/branching.go b/internal/modules/automations/branching.go
@@
     StepKindChoose StepKind = "choose"
+    StepKindDelay  StepKind = "delay"
diff --git a/internal/modules/automations/run.go b/internal/modules/automations/run.go
@@
     Steps           []StepAttempt
     BranchDecisions []BranchDecision
+    Delays          []DelayExecution
```

New domain definitions belong in `internal/modules/automations/delay.go`:

```go
type DelayStep struct {
    DurationMS int64
}

type DelayStatus string

const (
    DelayRunning     DelayStatus = "running"
    DelayCompleted   DelayStatus = "completed"
    DelayInterrupted DelayStatus = "interrupted"
)

type DelayExecution struct {
    StepID      StepID
    Position    int // Contiguous reached-delay order, independent of Commands and branches.
    DurationMS  int64 // Derived from this Step in the immutable Run snapshot.
    Status      DelayStatus
    StartedAt   time.Time
    DueAt       time.Time // Derived from StartedAt plus the snapshot duration.
    CompletedAt *time.Time
    FailureCode *string
}

type DelayStart struct {
    RunID     RunID
    StepID    StepID
    Position  int
    StartedAt time.Time
}

type DelayCompletion struct {
    RunID       RunID
    StepID      StepID
    Status      DelayStatus // Only completed or interrupted.
    CompletedAt time.Time
    FailureCode *string
}
```

`DelayExecution` is a history projection, not the stored row shape. Its duration comes from the identified Delay Step in the immutable Run snapshot, and its due time is `StartedAt.Add(time.Duration(DurationMS) * time.Millisecond)`. Neither field is accepted by `DelayStart` or stored in the delay table. Required start and completion timestamps must be nonzero UTC values. Running records have neither completion nor failure. Completed records require completion and no failure. Interrupted records require completion and one of the existing interruption reasons, `core_stopping`, `core_restarted`, or `executor_fault`.

Do not reject completion merely because wall time precedes start/due time after clock correction. Position establishes reached-delay order; wall timestamps do not. A completed record means elapsed waiting ended and evidence committed, not that a subsequent Step executed.

Definition preparation validates arbitrary input at existing entry points. Delay nodes must have a nonnil Delay payload and no Command, If, or Choose payload. Other families reject a nonnil Delay payload. Clone normalized payloads, enforce the existing shared tree bounds, remove the minimum-Command check, and collect no Entity references for delays. `CommandLeaves` explicitly ignores delays. Snapshot construction initializes `Delays` as an empty slice.

### Definition JSON and transport types

Canonical encoding includes the existing author-supplied Step ID:

```json
{"id":"wait-five-minutes","kind":"delay","duration_ms":300000}
```

Extend the canonical embedded schema's Step `oneOf` with this family:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "kind", "duration_ms"],
  "properties": {
    "id": {"$ref": "#/$defs/conditionID"},
    "kind": {"const": "delay"},
    "duration_ms": {"type": "integer", "minimum": 1, "maximum": 86400000}
  }
}
```

No Command fields or branch fields are permitted on this JSON family. Commands continue to encode without `kind`; existing definitions round-trip unchanged. Add an optional `*int64` duration field to the codec DTO, set it only for delays, and map it to/from `DelayStep`. Strict schema validation owns omission/null and cross-family rejection before decoding.

```diff
diff --git a/internal/modules/automations/api/models.go b/internal/modules/automations/api/models.go
@@
-    Kind string `json:"kind,omitempty" enum:"if,choose"`
+    Kind string `json:"kind,omitempty" enum:"if,choose,delay"`
+    DurationMS *int64 `json:"duration_ms,omitempty" minimum:"1" maximum:"86400000"`
@@
     BranchDecisions []AutomationBranchDecisionBody `json:"branch_decisions"`
+    Delays []AutomationDelayExecutionBody `json:"delays"`
```

The new API output type belongs in `api/delays.go`:

```go
type AutomationDelayExecutionBody struct {
    StepID      string     `json:"step_id"`
    Position    int        `json:"position" minimum:"0" maximum:"63"`
    DurationMS  int64      `json:"duration_ms" minimum:"1" maximum:"86400000"`
    Status      string     `json:"status" enum:"running,completed,interrupted"`
    StartedAt   time.Time  `json:"started_at"`
    DueAt       time.Time  `json:"due_at"`
    CompletedAt *time.Time `json:"completed_at,omitempty"`
    FailureCode *string    `json:"failure_code,omitempty"`
}
```

Both HTTP and MCP always emit `delays: []` when no delay was reached, including older retained Runs and initial manual admission responses. Use this nonrecursive output DTO directly in MCP's Run DTO and mapping; it contains no open JSON leaves. Recursive definition Steps retain MCP's existing `any` mapping. The canonical definition schema automatically feeds both OpenAPI and MCP input discovery; add parity tests rather than a second hand-maintained schema.

```diff
diff --git a/web/src/api/types.ts b/web/src/api/types.ts
@@
-export type AutomationStep = AutomationCommandStep | AutomationIfStep | AutomationChooseStep;
+export type AutomationStep = AutomationCommandStep | AutomationIfStep | AutomationChooseStep | AutomationDelayStep;
@@
   branch_decisions: AutomationBranchDecision[];
+  delays: AutomationDelayExecution[];
```

```ts
export interface AutomationDelayStep {
  id: string;
  kind: "delay";
  duration_ms: number;
}

export interface AutomationDelayExecution {
  step_id: string;
  position: number;
  duration_ms: number;
  status: "running" | "completed" | "interrupted";
  started_at: string;
  due_at: string;
  completed_at?: string;
  failure_code?: string;
}
```

### Repository interfaces and persistence

```diff
diff --git a/internal/modules/automations/repository.go b/internal/modules/automations/repository.go
@@
     RecordBranchDecision(context.Context, RunID, BranchDecision) error
+    RecordDelayStart(context.Context, DelayStart) error
+    CompleteDelay(context.Context, DelayCompletion) error
     CompleteRun(context.Context, RunCompletion) error
```

`RecordDelayStart` validates IDs, start time, position, and snapshot-relative identity in one repository transaction. The parent must be a running Run, and the Step must exist and be a delay in its prepared snapshot. Position must equal the number of already reached delays, all earlier delays must be terminal, and the Step must have no existing row. It inserts running evidence once, without duration or due-time metadata. Traversal owns which selected sequence is reached; the repository does not reconstruct executable progress or start timers.

`CompleteDelay` permits only a running delay under a running parent to transition once. Completed updates the delay only. Interrupted updates the delay and parent Run atomically with the same supplied completion time and reason. No duplicate-success protocol or write retry is introduced. Invalid input or transitions return wrapped `ErrInvalidAutomation`; storage and commit failures propagate without falsely claiming durable outcome.

Ordinary `CompleteRun` must not terminalize a parent while leaving a running delay behind. Startup interruption updates all running delays, Command attempts, and Runs in the existing single recovery transaction; completed delays remain unchanged. History loading checks delay identity against a Delay Step in the already decoded snapshot, contiguous position, timestamp validity, status-field coherence, and the invariant that a terminal Run has no running delays. It derives `DurationMS` and `DueAt` from that snapshot and the stored start time when building `DelayExecution`, without extra definition or per-delay snapshot reads. A missing or non-delay Step reference or invalid snapshot is corruption, not permission to use the current definition or invent a duration. Retained corruption causes a read error, never synthesized completion.

Add `00011_automation_run_delays.sql`, or the next unused migration number if development has advanced. Its Up is:

```sql
CREATE TABLE automation_run_delays (
    run_id TEXT NOT NULL REFERENCES automation_history(id) ON DELETE CASCADE,
    step_id TEXT NOT NULL,
    position INTEGER NOT NULL CHECK (position >= 0 AND position < 64),
    status TEXT NOT NULL CHECK (status IN ('running', 'completed', 'interrupted')),
    started_at TEXT NOT NULL,
    completed_at TEXT,
    failure_code TEXT,
    PRIMARY KEY (run_id, step_id),
    UNIQUE (run_id, position),
    CHECK (
        (status = 'running' AND completed_at IS NULL AND failure_code IS NULL)
        OR (status = 'completed' AND completed_at IS NOT NULL AND failure_code IS NULL)
        OR (status = 'interrupted' AND completed_at IS NOT NULL AND failure_code IS NOT NULL
            AND failure_code IN ('core_stopping', 'core_restarted', 'executor_fault'))
    )
);
CREATE UNIQUE INDEX automation_run_delays_one_running_idx
    ON automation_run_delays(run_id) WHERE status = 'running';
```

Use existing fixed-width UTC timestamp encoding. Repository validation handles timestamp validity and parent/snapshot rules; due-time arithmetic belongs to history projection, with no redundant persisted values to compare. Definition preparation owns duration bounds in the retained snapshot. The Down drops only this child table and its index. Cascading deletion ties delay evidence to existing terminal history retention; running parents remain protected by the existing pruning policy. No standalone prune worker is added.

Query sources belong in `sqlite/dbqueries/delays.sql`, with generated outputs in `sqlite/dbsqlc`. Add start, ordered list, conditional completion, and startup interruption queries. Integrate terminal-parent checks and the recovery query into existing execution operations. Keep SQL types behind the existing repository seam and use existing transaction helpers, not a new transaction abstraction.

### Service lifecycle and timer execution

Add a private service-owned cancellation context with a recorded stop cause. Construct it once in `NewService` with `context.WithCancelCause(context.Background())`; it is independent of admission caller contexts and never passed to `devices.ExecuteCommand`. A private `stopExecution(reason string)` first cancels that context with `errors.New(reason)`, then closes admission, both idempotently. The first stop cause wins. `StopAdmission` uses `core_stopping`; both executor-fault latches use `executor_fault` and retain their existing logs/readiness behavior. Delay boundary checks inspect the stop signal as well as admission, so a worker cannot start waiting in the cancel/close transition or mislabel a fault as shutdown.

```diff
diff --git a/internal/modules/automations/service.go b/internal/modules/automations/service.go
@@
     admission *lifecycle.AdmissionGroup
+    executionStop context.Context
+    cancelExecution context.CancelCauseFunc
```

```go
func (service *Service) stopExecution(reason string)
```

`context.Cause(service.executionStop).Error()` supplies the stable reason after cancellation has been observed. These private collaborators belong to the Service, not `Dependencies` or application configuration.

Keep `StopAdmission()` and `Drain(context.Context) error` public signatures unchanged. Drain still joins admitted workers without canceling in-flight Commands. Do not modify `platform/lifecycle.AdmissionGroup` or application shutdown ordering for a delay-specific concern.

The new private executor belongs in `delay_execution.go`:

```go
func (service *Service) executeDelay(
    ctx context.Context, run Run, step Step, position int,
) bool
```

Thread a separate reached-delay counter through the existing recursive traversal:

```diff
diff --git a/internal/modules/automations/branch_execution.go b/internal/modules/automations/branch_execution.go
@@
     decisionPosition *int,
+    delayPosition *int,
 ) bool {
@@
+    case StepKindDelay:
+        if !service.executeDelay(ctx, run, step, *delayPosition) {
+            return false
+        }
+        *delayPosition++
```

Pass the same counter through nested `executeBranch`/`executeSequence` calls, starting at zero in `executeRun`. Command and branch counters remain unchanged.

Execution protocol:

1. At the Step boundary, check execution admission and the stop signal. If already closed, interrupt the Run without creating delay evidence. Never manufacture a command position.
2. Capture native `time.Now()` for monotonic elapsed accounting and `Dependencies.Now().UTC()` for diagnostic start time. Take the elapsed duration from the validated Run snapshot. `DelayStart` contains identity, reached position, and diagnostic start time only.
3. Persist `RecordDelayStart` under the existing five-second persistence timeout. Do not hold a SQLite transaction open while waiting. An error never permits later Steps; use the existing Run-only fault path and latch the stop signal.
4. Recheck the stop signal. Compute remaining time as `duration - time.Since(monotonicStart)`, clamped to zero. Use a native timer only when remaining time is positive. Wait on timer completion or the service stop signal, and stop timer resources on every exit. No periodic polling or wall-clock deadline comparison.
5. After a wake, check the stop signal again. If stop is observed before completion starts, interruption wins even if the timer is also ready. Persist interrupted delay and Run atomically, using the latched cause. Do not continue.
6. Otherwise persist completed evidence. After a successful commit, recheck execution admission before any later Step or Run success. If closure happened during that write, preserve the completed delay and interrupt the Run separately. This is valid because completing a wait is not executing a later Step. A closure racing the final check can still occur after that check, as with existing Step admission; already committed success is never rewritten.
7. A completion error attempts atomic `executor_fault` interruption and latches admission. If the repository remains unavailable or the commit outcome is ambiguous, leave the last provable durable state and rely on startup interruption. Never execute later Steps, retry completion, or overwrite an established terminal result.

After the final selected Step, success uses existing `CompleteRun`. A delay-only Run has no Command attempts or device operations, but retains normal admission and snapshot evidence. Keep ordinary Command and branch behavior unchanged; the stop broadcast adds prompt wake-up only for pending delays.

### HTTP, MCP, and browser behavior

No new endpoint or MCP tool is needed. Existing definition create/replace, definition reads, manual admission, and history detail support the new family. History still exposes `duration_ms` and `due_at`; deriving those fields changes storage, not the output contract. Runtime Run/Skip summary status and pagination stay unchanged. Invalid definitions retain existing validation/problem mappings, and busy manual Runs retain 409.

Browser traversal must distinguish delays from Commands explicitly. The current catch-all leaf case in `automation-step-tree.ts` counts any nonbranch as a Command; replace it with command-only collection. Delays consume no command position or Entity reference. Display their Step ID, kind, and exact readable duration in definitions and snapshots. Show whole-unit durations where exact, otherwise show exact milliseconds. For example 300000 ms renders as 5 minutes, and 1001 ms remains 1001 milliseconds.

Run detail adds a Delay executions table with reached-order position, Step ID, duration, status, start, expected due, completion, and interruption reason. Label due time as diagnostic and explain that waiting is interrupted by shutdown/restart. Empty history says that no delay was reached, not that the definition contained none. Use existing manual history refresh; do not add live countdowns or new authoring controls.

Existing enablement replacement must preserve delay nodes exactly. Old flat and branching fixtures gain `delays: []`; command-only counts and links must remain unchanged.

## Project layout and ownership

```text
GLOSSARY.md                                      # modify, domain terms already recorded
specs/
└── automation-delay-steps.md                    # new, authoritative feature contract
docs/
├── architecture.md                             # modify, accepted delay/lifecycle constraints after implementation
├── automation-gap-analysis.md                  # modify, distinguish ephemeral delays from deferred resumable waits
└── automation-delay-steps.md                    # new, operator examples and interruption/rollback warnings
internal/
├── app/hearthd/
│   └── shutdown_test.go                        # new, whole-app drain regression coverage
├── platform/db/migrations/
│   └── 00011_automation_run_delays.sql          # new, cascading delay evidence schema
└── modules/automations/
    ├── README.md                               # modify, ownership and validation boundaries
    ├── definition.go                           # modify, Delay payload
    ├── branching.go                            # modify, family validation, bounds, Command-only traversal
    ├── definition_validation.go                # modify, zero Command leaves and reference validation
    ├── definition_codec.go                     # modify, canonical encoding/decoding
    ├── automation-definition.schema.json       # modify, authoritative delay input schema
    ├── delay.go                                # new, wait types and production-boundary evidence validation
    ├── delay_execution.go                      # new, timer protocol and fault/stop handling
    ├── delay_execution_test.go                 # new, service behavior and lifecycle races
    ├── definition_codec_test.go                # modify, family and duration contract checks
    ├── run.go                                  # modify, empty initialized delay evidence
    ├── repository.go                           # modify, atomic delay write contracts
    ├── service.go                              # modify, service-owned stop broadcast
    ├── execution.go                            # modify, reached-delay counter and final success boundary
    ├── branch_execution.go                     # modify, delay dispatch and recursive counter
    ├── api/
    │   ├── models.go                           # modify, optional input DTO field and Run delays output
    │   ├── branching.go                        # modify, recursive Delay Step mapping
    │   ├── delays.go                           # new, delay execution DTO/mapping
    │   ├── mcp_outputs.go                      # modify, delay Run output parity
    │   └── delay_contract_test.go              # new, HTTP/OpenAPI/MCP parity at real boundaries
    └── sqlite/
        ├── delays.go                           # new, transactions and retained evidence mapping
        ├── delays_test.go                      # new, real SQLite transition/corruption/recovery tests
        ├── execution.go                        # modify, terminal-parent safety and recovery
        ├── history_mapping.go                  # modify, ordered delays and retained integrity checks
        ├── dbqueries/
        │   ├── delays.sql                      # new, start/read/complete/recovery SQL
        │   └── automations.sql                 # modify, parent/terminal query integration
        └── dbsqlc/                             # generated, regenerate from owned SQL/migrations
web/src/
├── api/types.ts                                # modify, Delay Step and execution interfaces
└── pages/
    ├── automation-step-tree.ts                 # modify, delay-aware bounded traversal and exact duration labels
    ├── AutomationStepTree.tsx                  # modify, delay definition rendering
    ├── AutomationDetailPage.tsx                # modify, separate wait evidence table
    ├── AutomationDetailPage.test.tsx           # modify, rendering and replacement preservation
    └── AutomationsPage.test.tsx                # modify, command-only counts for delay definitions
```

If a named test file already exists with a different responsibility split, extend the test beside its current owner rather than duplicating suites. New domain types stay independent of SQLite, Huma, MCP, and browser types. No package move is required. Generated outputs must never be hand-edited.

## Deliverables

| ID | Concrete outcome | Effort | Owning paths | Depends on | Acceptance |
| --- | --- | --- | --- | --- | --- |
| D1 | Validated Delay Step family, canonical codec, zero-Command snapshots | M | `automations/{definition.go,branching.go,definition_validation.go,definition_codec.go,automation-definition.schema.json,delay.go,run.go}` and codec tests | None | A1, A2 |
| D2 | Minimal transactional delay evidence, snapshot-derived history metadata, recovery, retention, generated queries | M | `automations/repository.go`, `automations/sqlite/{delays.go,delays_test.go,execution.go,history_mapping.go,dbqueries,dbsqlc}`, migration | D1 | A3, A6, A9, A10 |
| D3 | Interruptible elapsed waits and shutdown/fault wake-up in recursive Runs | L | `automations/{service.go,delay_execution.go,delay_execution_test.go,execution.go,branch_execution.go}`, app shutdown tests | D1, D2 | A2, A4, A5, A6, A7, A8 |
| D4 | Existing HTTP/MCP operations and discovery support delays with parity | M | `automations/api/{models.go,branching.go,delays.go,mcp_outputs.go,delay_contract_test.go}` | D1, D2, D3 | A1, A10 |
| D5 | Read-only browser definition/history rendering without count or replacement regressions | M | `web/src/api/types.ts`, Step-tree and Automation detail/list files and tests | D4 | A11 |
| D6 | Operator/ownership documentation and complete integration validation | M | `docs/{automation-delay-steps.md,architecture.md,automation-gap-analysis.md}`, module README, this spec, all affected generated outputs | D1 through D5 | A12 |

## Acceptance and validation

Tests must exercise the production entry point that owns the invariant. Avoid standalone validator assertion suites, snapshot-only UI tests, or a test-only timer abstraction. Use real migrated SQLite for transactions and short native waits for execution. Coordinate races with channels or existing repository/device seams, with generous watchdog deadlines instead of exact latency assertions. A 24-hour timer interrupted through the stop signal tests long-wait shutdown without waiting 24 hours.

| ID | Behavior or boundary and expected result | How to check |
| --- | --- | --- |
| A1 | Decode, Service save, repository save, HTTP, and MCP accept valid delay nodes and reject missing/null/zero/negative/fractional/overflow/above-max durations, mixed families, duplicate IDs, and exceeded bounds. No Command minimum; nonempty sequence invariant remains. | Boundary-focused domain, SQLite, and API tests using commands below. Check canonical OpenAPI/MCP schema cases as well as runtime input. |
| A2 | A manual delay-only definition admits, creates no Command, records one completed delay, and succeeds with `steps: []`. Old definitions encode unchanged and read with `delays: []`. | Domain/service tests backed by real repository; assert device seam records no execution call. |
| A3 | Start is insert-once, positions are contiguous, only one delay is running per Run, invalid/duplicate terminal transitions write nothing, and interruption updates delay and Run atomically. Stored rows contain neither duration nor due time. History derives exact duration and due time from the immutable snapshot and recorded start. Invalid Step references, timestamps, status fields, or snapshots cause read failure. | SQLite public-operation tests against migrated file-backed databases, including transaction failure/rollback checks and schema inspection for absent duplicate columns. Replace or delete the current definition and verify retained duration and due time remain unchanged. |
| A4 | Command-delay-Command executes in order after prior success; prior failure creates no delay row. Nested selected waits execute, unselected waits do not. Commands and branches keep their existing position rules. | Service tests using device evidence and persisted history, not internal timer callbacks. |
| A5 | An active wait retains `running`; eligible automatic admission produces a busy Skip and manual admission produces 409 with no new busy Skip. Caller cancellation does not interrupt the wait. | Service/admission and HTTP tests. Preserve stale-before-busy precedence. |
| A6 | StopAdmission/Drain wakes a long delay promptly, records `core_stopping`, and executes no later Step. Startup recovery atomically interrupts active waits and Runs with `core_restarted` and never starts a continuation, even if due time passed. Repeated shutdown calls are safe. | Service and whole-app shutdown tests, plus SQLite recovery tests. A generous watchdog distinguishes prompt wake-up from waiting hours. |
| A7 | Delay-start or completion failure prevents later Commands, latches readiness/admission closed, and wakes unrelated delayed Runs. Unpersistable or ambiguous completion never creates false success or retries execution. | Service tests with bounded repository failure injection through the existing repository seam; assert durable evidence when storage recovers and fail-closed state otherwise. |
| A8 | Changing diagnostic wall time cannot make a delay complete early or extend its native interval. A branch after waiting reads new State; admission Conditions do not run again. Edit/disable/delete preserves the active snapshot. Stop observed before completion begins wins a simultaneous timer wake. | Native short-wait tests using the existing `Dependencies.Now` for diagnostic clock corrections, channel-coordinated boundaries, and persisted results. No privileged host-clock changes required. |
| A9 | Upgrade from migration 00010 preserves old definitions/history; Down removes delay evidence only. Terminal history pruning cascades delay deletion; running waits remain retained. | Migration and repository retention tests using real SQLite. Never run Down against the developer's working DB for this check. |
| A10 | HTTP and MCP create/replace/read/run/history JSON and discovery schemas agree. `delays` is always an array, derived `duration_ms` and `due_at` remain present and exact, optional terminal fields are omitted correctly, and no existing route, tool, status, or pagination contract changes. | Transport contract tests through registered HTTP and MCP handlers, including schema-valid output checks and exact metadata comparisons against the original snapshot and recorded start. |
| A11 | Browser outlines show exact durations and zero Commands for delay-only definitions; no fake Entity links or positions. Run detail renders active/completed/interrupted and empty delay evidence. Enablement PUT preserves nested delays exactly. | Vitest behavioral tests and browser manual procedure below. |
| A12 | Generation is reproducible; focused and full validation pass; docs describe no resumption, diagnostic due times, busy behavior, read-only browser scope, and rollback backup requirement. | `mise run validate`, then review all generated/format/module diffs and documentation links. |

Backend focused validation, run from the repository root:

```sh
mise run --skip-deps generate
mise run --skip-deps format
GO_PACKAGES='./internal/modules/automations/...' mise run --skip-deps test
GO_PACKAGES='./internal/modules/automations/... ./internal/app/hearthd ./internal/platform/db' mise run --skip-deps test
GO_PACKAGES='./internal/modules/automations/... ./internal/app/hearthd ./internal/platform/db' mise run --skip-deps lint
GO_PACKAGES='./internal/modules/automations/... ./internal/app/hearthd ./internal/platform/db' mise run --skip-deps vet
mise run --skip-deps web-test
mise run --skip-deps web-build
mise run validate
```

Unset `GO_PACKAGES` for full validation. Focused SQLite and module checks are local and need no deployment. Full validation includes existing real-Mosquitto tests and requires a reachable Docker daemon. Report a missing prerequisite as a blocker, not a passed check. Review any generated, formatting, or module metadata changes and preserve unrelated work.

Manual browser verification after implementation uses `mise run simulator-start` and `mise run simulator-stop`. This stack requires the documented readable agent key file. Obtain an existing simulator Entity and create a disabled Automation through HTTP or MCP with a normal Trigger and a 2-second delay-only sequence. Manually run it; refresh history to observe completed evidence and zero Command attempts. Create a Command-delay-Command definition against a simulator Entity, check the definition outline and nested wait history, then run a long delay and stop/restart Core using the simulator lifecycle controls to verify interruption and no continuation. No real household operation or external deployment is required.

## Compatibility and rollout

Ship schema, runtime, migration, HTTP/MCP mappings, and browser reader types together. The definition schema remains v1 as an additive family for the new binary; older binaries cannot decode delay definitions or snapshots. Existing definitions and history need no data rewrite or backfill.

Take a database backup before enabling the feature if rollback matters. Applying Down does not remove delays from definitions or retained snapshots. Older binary rollback therefore requires restoring a pre-feature database backup, not merely dropping the new table. The operator guide must state this explicitly.

Update architectural status only once behavior is implemented. Keep historical specs intact except for navigation links if needed; this document states which exclusions it supersedes. Gap analysis should stop claiming that basic elapsed delays require durable executable progress, while continuing to defer resumable waits.

## Risks and mitigations

| Risk | Impact | Mitigation |
| --- | --- | --- |
| Timer/shutdown/completion races | Unexpected later Commands or shutdown blocked by a long wait | Broadcast stop independent of Command context, post-wake checks, atomic interrupted writes, final Step boundary check, race-detector tests |
| Persistence fails after a wait ends | False success or execution without audit evidence | Commit completion before continuing, latch faults, no execution retry, retain last provable state for startup interruption |
| Definition family drifts across codecs, discovery, and browser union | Invalid requests accepted or delay displayed as a Command | One canonical input schema and real HTTP/MCP output/discovery parity tests; explicit leaf classification in browser |
| Long waits consume workers and block retriggers | Increased memory use and many busy Skips | Keep existing bounded definitions and per-Step cap, document busy semantics and no global-cap promise; revisit scheduling only with measured need |
| Backward wall-clock correction looks like malformed chronology | History rejected despite correct elapsed wait | Derive due time from recorded start and snapshot duration; validate timestamp/status shape, not completion wall-time ordering; describe due timestamps as diagnostic |
| Binary rollback loses decode compatibility | Operators cannot start an older Core against delay snapshots | Pre-feature backup, additive migration tests, explicit operator warning |

## Completion gate

No open product or implementation-policy questions remain. Acceptance is behavior-based, deliverables have explicit owners and dependencies, and no new infrastructure is unowned. Success means an operator can author delays through HTTP/MCP, inspect their evidence in the browser, and stop/restart Core during a wait without later device operations or resumed execution.

The user approved this specification through planning dialogue. This planning session does not authorize production implementation.
