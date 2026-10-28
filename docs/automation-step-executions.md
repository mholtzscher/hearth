# Automation Step execution storage

Each Run retains one sparse `automation_step_executions` trace. A row identifies
one durably recorded reach by `(run_id, step_id)`; its sequence is allocated in
the insertion transaction. Unreached and unselected instructions have no rows.
Missing evidence does not prove an instruction was never visited and cannot
establish successful progress.

Definition/history v3 extends this same trace with `read_state`, `set_variables`,
actual resolved effect inputs, copied Function outputs and safe value failures.
Initialization is Run-level evidence. A reached pre-reservation value failure has
no fabricated external identity; provider success followed by unusable output
retains that success while failing Step/Run progress. There is no parallel Step
journal. See [Automation expressions](automation-expressions.md).

The immutable definition revision owns configuration. Native columns own kind,
phase, sequence, and lifecycle timestamps. Strict typed JSON owns only execution
evidence: reserved identities, verified links, provider/Invocation outcomes, and
branch evaluations/selections. Branch selection is instantaneous, not a span
over its children. A successful selection, including no match, uses the common
`succeeded` phase; it does not establish that any child succeeded. Neither
wall-clock order nor timestamp monotonicity defines execution order.

## Transition boundaries

### Internal lifecycle model

Every reached Step uses a closed kind-specific execution state. Command,
Notification, and Function states distinguish running, succeeded, failed, and
interrupted; Delay distinguishes running, succeeded, and interrupted. Branch
decisions already form a closed instantaneous selection/failure family.

Only terminal variants carry a completion timestamp. Command reservations and
verified success evidence remain distinct; Notification failures distinguish
pre-admission rejection from authoritative provider failure; Function failures
retain their catalog and invocation evidence. Phase, completion time, and Run
failure derive from these variants. They are not independently mutable fields.

Runtime helpers pass reserved identities and typed terminal states directly to
the common recording inputs. Terminal-state interfaces exclude running values
from completion evidence; independent repository and codec validation still
reject malformed identities, times, reasons, and contradictory links. No generic
executor or callback registry is introduced. The lifecycle-only refactor changed
neither SQL nor public HTTP/MCP JSON; migration 23 separately adds the v3 value-flow
contract described above.

### Persistence checks

All recording operations independently validate their input. They read the
transaction's running parent and retained definition, not the current head.
They never hold the transaction during external execution or a Delay.

| Operation/input | Transaction-local checks and reads |
| --- | --- |
| `RecordStep` / `CommandStarted` | Valid Run/Step/time and Command/correlation reservation; retained Command instruction is next on the selected path; no duplicate reach or running sibling; allocate sequence and insert before dispatch |
| `RecordStep` / `DelayStarted` | Valid identity/time; retained Delay is next; no duplicate reach or active sibling; duration remains in the definition |
| `RecordStep` / `NotificationStarted` | Valid identity/time and Attempt reservation; retained Notification is next; no duplicate reach or active sibling; preserve globally unique Attempt reservation |
| `RecordStep` / `FunctionStarted` | Valid identity/time and Invocation reservation; retained Function is next; no duplicate reach or active sibling; preserve globally unique Invocation reservation |
| `RecordStep` / `BranchDecided` | Valid typed decision against retained branch configuration and Run matches; branch is next, insert-once; selection commits before child traversal; unknown/error also fails parent atomically |
| `RecordStep` / `CommandStopped` | Fixed interruption reason and valid time; retained Command is next; insert terminal evidence without fabricated reservation and interrupt parent atomically |
| `CompleteStep` / `CommandFinished` | Running target and parent; retained instruction kind; exact Command/correlation reservation and any verified link; typed result; failure/interruption terminalizes parent in the same transaction |
| `CompleteStep` / `DelayFinished` | Running Delay and parent; retained instruction kind; terminal time and optional fixed interruption reason; interruption terminalizes parent atomically |
| `CompleteStep` / `NotificationFinished` | Running target and parent; retained instruction kind; exact Attempt reservation; provider evidence or established pre-admission/interruption reason; non-success terminalizes parent atomically |
| `CompleteStep` / `FunctionFinished` | Running target and parent; retained instruction kind; exact Invocation reservation; typed terminal state and matching completion time; non-success terminalizes parent atomically |
| `CompleteRun` | Valid terminal outcome and running parent; load and validate complete trace once; success requires every selected instruction's successful result |
| Scoped cleanup/startup | Update running trace rows before running parents in one transaction; preserve terminal evidence and uncertain reservations; scoped cleanup leaves unrelated Runs untouched |

Ordinary starts read only bounded identity, order, kind, phase, and branch
outcome/selection labels, never prior Condition, Command, provider, or Function
payloads. Completion reads only its target payload. The single-running partial
unique index and reservation expression indexes remain final database guards.
There is no process-global definition or progress cache.

Unrelated pre-existing evidence corruption is detected on detail reconstruction
or final trace validation, not by decoding all historical outcomes for an
otherwise local transition. Every interpreted target, configuration, and control
value still receives its operation's checks. Summary history queries do not load
the revision document or trace.

## Execution and public history

Executors retain ownership of external reconciliation. Only successful durable
completion permits the next effect. An ambiguous persistence result stops the
Run and makes one scoped cleanup attempt; it never retries the external effect.
Startup classifies unresolved work as interrupted, without adoption or replay.
The existing Function reservation mutex and application shutdown ordering remain.

HTTP and MCP expose the same sparse `executions` array directly from the retained
trace. Each entry has `step_id`, global zero-based `sequence`, `kind`, `phase`,
`started_at`, optional `completed_at`, and strictly typed `evidence`. Only reached
instructions appear; unreached Commands have no synthesized `not_attempted`
entry. Delay configuration remains in the immutable snapshot. Branch success
records selection only, while Notification success means acceptance and preserves
the provider outcome. Successful detail reads enforce the selected-path
completeness rule as completion. There is no execution UUID, attempt counter,
replay, or schema revision.

This intentionally breaks the former five-array history contract in place. HTTP,
MCP, and the browser migrate together; no compatibility projection remains.
Reserved Command and correlation IDs are public evidence of a reservation, not
proof of admission or dispatch. Only `verified_command_id` provides a verified
Command detail link. The browser renders one cross-kind timeline per Run.

## Upgrade and rollback

Migration 22 resets all Automation-owned data, replaces the five child tables,
and preserves shared revision storage and history indexes. It does not reset
other modules. Follow the [backup, recreation, and rollback procedure](automation-definition-revisions.md#upgrade-and-rollback).
Later startups preserve post-cutover data; Down refuses automatic conversion.

Migration 23 preserves post-22 records while converting retained definition
revisions to v3 bindings. It labels prior value evidence unavailable rather than
inventing inputs/outputs. Use Core's migration provider, which validates frozen
v2 documents before applying the SQL transaction; do not run its SQL standalone.

Migration 24 removes aggregate value-history counters and converts retained failure
targets to logical fields or Condition IDs. It preserves revisions, execution
values and exact number tokens. Core performs the conversion transactionally;
Down refuses to recreate discarded spans or counters.

## Verification and measurement

The owning unit and SQLite integration suites exercise strict codecs,
selected-path completeness, mixed/nested routing, reservations, atomic failure,
scoped/startup interruption, persistence faults, reset rollback, and retention.
Public transport and Core lifecycle tests exercise their separate contracts.

A live browser journey against the tracked tree at `5d8806c` started one manual
Run containing Choose, If, Command, Delay, Notification, and Function executions.
The retained HTTP response and browser timeline agreed on sequences 0 through 5,
successful phases, and absence of the unselected Command. The Command and
Notification links opened their authoritative details; the retained Function
arguments and result matched the request. HAR inspection confirmed one Run POST.
The isolated simulator stack stopped after verification. This check used simulated
Devices and providers, not hardware or recipient-delivery confirmation.

The opt-in `TestAutomationStepTracePerformance` fixture uses equivalent
file-backed sparse/dense mixed-Step workloads before and after, explicit warmup,
correct-result assertions, and test-local driver instrumentation. Its timing and
allocation totals include that instrumentation. Report measurements separately;
the implementation shape alone establishes no speedup or storage claim.

### Observed comparison

Baseline is `a6fa9af421f4fd1cc4f784602c4bbf3be58d39f0`; the measured
implementation is `2d86cd4dd76c1e089dff1463565a22a91c024c81`, before the
subsequent evidence-type rename, branch-phase simplification, and public API
cutover. These results were not rerun for those follow-ups. Both checkouts used the
same test driver, SHA-256
`63a3922c77f9d76c294f2f64a111841b7d42c491a968d693a782702b8d7c3b9f`.
Five independent process samples per side alternated baseline/current order,
reversing order on even samples. Each scenario used one untimed warmup followed
by ten measured Runs. No OS cache flush ran. The simulator stack and other
agent-owned test workloads were stopped during measurement.

The machine ran Linux 6.18.54 on an AMD Ryzen 9 3900X, 24 logical CPUs, with
Go 1.27.1 and no race instrumentation. Production SQLite connection settings
were unchanged, including WAL, foreign keys, busy timeout, and one connection.
A test-local driver wrapper measured query/exec counts, returned rows/bytes,
and transaction lifetime through Commit/Rollback. These transaction times
include Go work inside the transaction, not just SQLite execution.
A joined goroutine continuously read the unrelated `entity_states` table
through the same connection pool. Both sides used that identical policy;
completed competitor operations vary with duration and scheduling.

Sparse Runs selected four Commands, one Delay, one Notification, one Function,
and two branch decisions. Dense Runs selected five Commands, two Delays, two
Notifications, two Functions, and two decisions. Definitions also contained
unselected mixed-kind arms. Every measured Run succeeded with the expected
selected effects and retained results. The oracle matches each retained
Command/Attempt/Invocation identity to its captured external call and checks
configured routing, content, parameters, and Command order. Wrong-entity,
wrong-argument, and foreign-link negative controls verify rejection. These
checks and their SQL inspection run outside the measured counters and timings.
All data-operation and transaction
error counts were zero. Each sample cancelled one pending competitor read
during its explicit stop/join; that expected cancellation is counted separately.

Medians with minimum/maximum ranges, milliseconds per Run:

| Workload/layout | Complete service cycle | Time inside transactions | History detail |
| --- | ---: | ---: | ---: |
| Sparse, baseline | 75.78 [73.89, 76.54] | 73.69 [71.94, 74.35] | 1.17 [1.14, 1.31] |
| Sparse, unified | 79.94 [78.97, 81.94] | 77.67 [76.84, 79.69] | 1.19 [1.13, 1.40] |
| Dense, baseline | 113.13 [110.97, 115.91] | 110.31 [108.18, 112.88] | 1.29 [1.26, 1.32] |
| Dense, unified | 117.03 [114.17, 119.25] | 114.26 [111.27, 116.38] | 1.39 [1.32, 1.42] |

Work counts and retained bytes per Run:

| Workload/layout | Non-competing queries | Evidence SELECTs | Target SELECTs | Evidence writes | Transactions | Retained rows | Logical evidence bytes |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Sparse, baseline | 101 | 69 | 0 | 23 | 21 | 11 | 2,378 |
| Sparse, unified | 43 | 18 | 7 | 17 | 21 | 9 | 2,710 |
| Dense, baseline | 154 | 105 | 0 | 31 | 31 | 14 | 3,052 |
| Dense, unified | 62 | 26 | 11 | 25 | 31 | 13 | 3,689 |

Median allocated bytes per Run increased from 5.80 MB to 7.25 MB for sparse
Runs and 9.20 MB to 10.96 MB for dense Runs. Median allocation counts increased
from 83,671 to 103,922 and from 131,649 to 155,053. Allocation totals include
the observer and competing reader. Returned string/byte payloads were 70,895
versus 72,303 bytes per sparse Run and 118,542 versus 108,200 per dense Run.
Logical retained bytes count all evidence-row SQL values, not database pages,
indexes, immutable definition revisions, or external source histories. Fewer
rows did not mean fewer bytes in these small fixtures.

Separate CPU/allocation/trace profiles ran 30 iterations per scenario per side.
They point to repeated definition/schema preparation and JSON allocation as
remaining costs. In the current whole-process allocation profile,
`runningExecutionParent` accounts for 65.3% cumulatively, and schema validation
for 52.1%. These overlap and include setup and post-window oracle checks,
unlike the measured sample window.
The evidence does not establish one universal bottleneck or imply that all
remaining wall time is CPU work. Completion now checks retained configuration
and complete success has stronger trace validation than the baseline.

This comparison demonstrates reduced query work, not an end-to-end speedup.
Median complete-cycle time increased 5.5% for sparse Runs and 3.4% for dense
Runs. The dense ranges overlap; the sparse ranges do not in these samples.
Transaction lifetimes and allocations
did not improve. Keep those costs alongside the unified ownership, sparse
recording, and stronger consistency guarantees when evaluating this change.

The [raw sample CSV](automation-step-execution-measurements.csv) records totals
for each ten-Run sample. `retained_rows` and `retained_bytes` describe one Run;
the other work/timing counters describe the whole sample. SQL-shape categories
are heuristics, and returned payload counts are not exact Go decode counts.
Profiles and instrumentation have observer overhead. This is a local synthetic
comparison, not a physical-device, production-capacity, or latency guarantee.

Reproduce each side with the same driver and environment:

```sh
AUTOMATION_PERF=1 AUTOMATION_PERF_ITERATIONS=10 AUTOMATION_PERF_COMPETE=1 \
  GO_PACKAGES='./internal/modules/automations' mise run --skip-deps test -- \
  -tags=integration -run '^TestAutomationStepTracePerformance$' -count=1 -v
```

### Historical validation record — unified execution cutover

`mise run validate` passed with `GO_PACKAGES` unset. It covered generation,
formatting, module tidiness, lint, all Go test levels, vet, 91 frontend tests,
and the frontend build. Local Go tests had no race instrumentation. Independent
review found no material correctness or structural issue.

Live loopback HTTP checks against production Core and native simulated Devices
verified nested selected/unselected evidence, exact Command/Attempt/Invocation
links, and a rejected Notification stopping the later Command. The owned stack
was stopped and its processes/listeners closed. Evidence is under
`.data/verification/20261026T021653Z-2983733/`; measurement logs, full JSON,
and separate profiles are under `/tmp/opencode/unified-step-evidence/` on the
development machine. Browser interaction, hardware, deployed databases, and
live startup recovery were not exercised. Core integration/E2E tests cover
startup interruption and shutdown ordering independently.
