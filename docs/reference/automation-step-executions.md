# Automation Step execution storage

Each Run retains one sparse `automation_step_executions` trace. A row identifies
one durably recorded reach by `(run_id, step_id)`; its sequence is allocated in
the insertion transaction. Unreached and unselected instructions have no rows.
Missing evidence does not prove an instruction was never visited and cannot
establish successful progress.

Definition/history v1 extends this same trace with `read_state`, `set_variables`,
actual resolved effect inputs, copied Function outputs and safe value failures.
Initialization is Run-level evidence. A reached pre-reservation value failure has
no fabricated external identity; provider success followed by unusable output
retains that success while failing Step/Run progress. There is no parallel Step
journal. See [Automation expressions](automation-expressions.md).

A `sub_automation` row links a parent Step to the child Run it admitted, or
records why no child Run started. The child keeps its own trace; the parent row
holds only the link and outcome. See
[Reusable Automations](../guides/reusable-automations.md).

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
interrupted; Delay distinguishes running, succeeded, and interrupted.
SubAutomation distinguishes running, succeeded, rejected (`automation_not_found`,
`automation_busy`, or a Condition Skip), child failed, and interrupted; rejected
and child-failed rows both use the `failed` phase. Branch decisions, `read_state`,
`set_variables`, and value-resolution failures are instantaneous: each inserts
one `succeeded` or `failed` row whose completion time equals its start time.

Only terminal variants carry a completion timestamp. Command reservations and
verified success evidence remain distinct; Notification failures distinguish
pre-admission rejection from authoritative provider failure; Function failures
retain their catalog and invocation evidence. Phase, completion time, and Run
failure derive from these variants. They are not independently mutable fields.

Runtime helpers pass reserved identities and typed terminal states directly to
the common recording inputs. Terminal-state interfaces exclude running values
from completion evidence; independent repository and codec validation still
reject malformed identities, times, reasons, and contradictory links. There is
no generic executor or callback registry.

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
| `RecordStep` / `ValuesAssigned` | Retained `set_variables` Step is next; assigned values agree with the Step's literal assignments; insert-once terminal row |
| `RecordStep` / `StateRead` | Retained `read_state` Step is next; read output, Observation evidence, or unusable reason; insert-once terminal row |
| `RecordStep` / `ValueResolutionFailed` | Retained Step of the named kind is next; insert a failed row with a safe value failure and no fabricated reservation; fail parent atomically |
| `AdmitSubAutomationRun` | Retained SubAutomation is next; in one transaction, either reject (`automation_not_found`, `automation_busy`, or a Condition Skip with its `skip_id`) and fail parent, or persist the child Run and a running row linking `child_run_id` |
| `CompleteStep` / `CommandFinished` | Running target and parent; retained instruction kind; exact Command/correlation reservation and any verified link; typed result; failure/interruption terminalizes parent in the same transaction |
| `CompleteStep` / `DelayFinished` | Running Delay and parent; retained instruction kind; terminal time and optional fixed interruption reason; interruption terminalizes parent atomically |
| `CompleteStep` / `NotificationFinished` | Running target and parent; retained instruction kind; exact Attempt reservation; provider evidence or established pre-admission/interruption reason; non-success terminalizes parent atomically |
| `CompleteStep` / `FunctionFinished` | Running target and parent; retained instruction kind; exact Invocation reservation; typed terminal state and matching completion time; non-success terminalizes parent atomically |
| `CompleteSubAutomationStep` | Running SubAutomation and parent; linked child is terminal and names this parent Run, Step, and target Automation; copy child failure code on failure or interruption; non-success fails parent atomically |
| `CompleteStep` / `SubAutomationStopped` | Running SubAutomation; local `core_stopping` only; interrupt the row and parent atomically |
| `CompleteRun` | Valid terminal outcome and running parent; load and validate complete trace once; success requires every selected instruction's successful result |
| Scoped cleanup/startup | Update running trace rows before running parents in one transaction; preserve terminal evidence and uncertain reservations; scoped cleanup leaves unrelated Runs untouched |

Ordinary starts read only bounded identity, order, kind, phase, and branch
outcome/selection labels, never prior Condition, Command, provider, or Function
payloads. Completion reads only its target payload. The single-running partial
unique index and the unique Attempt, Invocation, and child Run expression
indexes remain final database guards.
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

Migration 23 preserves post-22 records and retained literal definition revisions
byte-for-byte. It labels prior value evidence unavailable rather than
inventing inputs/outputs. Use Core's migration provider, which validates frozen
v2 documents before applying the SQL transaction; do not run its SQL standalone.

Migration 24 removes aggregate value-history counters and converts retained failure
targets to logical fields or Condition IDs. It preserves revisions, execution
values and exact number tokens. Core performs the conversion transactionally;
Down refuses to recreate discarded spans or counters.

Migration 25 rebuilds `automation_step_executions` to admit the `sub_automation`
kind and its child Run index. It copies existing rows unchanged. Down always
refuses; restore the pre-upgrade backup with its matching binary.
