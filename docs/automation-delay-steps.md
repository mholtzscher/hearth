# Automation delay Steps

Delay Steps are implemented on `feat/automation-delay-steps`. This guide describes
the branch implementation, not a merged or deployed release.

A Delay Step pauses an admitted Run for a fixed elapsed duration. Use it for
Command-delay-Command sequences or inside selected If/Choose arms. It differs
from a Held-State Trigger, which waits before considering admission, and a Cron
Trigger, which considers admission at a household-local clock time.

## Definition contract and bounds

```json
{"id":"wait-five-minutes","kind":"delay","duration_ms":300000}
```

`duration_ms` is a required integer from 1 through 86400000 inclusive, up to
24 hours per Step. Omitted, null, zero, negative, fractional, and out-of-range
values are invalid. A delay accepts no Command or branch fields. Its Step ID
must be unique throughout the definition. Commands still omit `kind`.

Millisecond precision describes the input, not a scheduling-latency guarantee.
The wait starts when execution reaches the Step after preceding Steps succeed.
Time spent recording its start counts toward the duration. Later Steps require
both elapsed time and committed completion evidence. Unselected or unreached
delays create neither timers nor delay evidence. A selected nested sequence
returns to ordinary sequential execution after it completes.

| Item | Limit |
| --- | --- |
| Triggers | 1 through 32, including for delay-only definitions |
| Each present sequence | 1 through 32 direct children |
| All Command, If, Choose, and Delay nodes | At most 64 across every arm |
| Step depth | At most 8, with top-level Steps at depth 1 |
| Command leaves | 0 through 32 across every arm |
| Choose alternatives | 1 through 32 per Choose Step |
| Each Condition root | At most 64 nodes and depth 8 |
| Condition nodes across admission and branch roots | At most 256 |
| Normalized definition | At most 64 KiB |
| Trigger, Step, Condition, and alternative IDs | 1 through 63 bytes, `^[a-z0-9][a-z0-9_-]{0,62}$` |

Every delay counts as one Step node, never as a Command position or Entity
reference. Optional branch arms may be omitted, but present arms must be
nonempty. Other Trigger and Condition contracts remain unchanged. See the
[branching guide](automation-branching.md) for branch syntax; this feature
supersedes that version's Command minimum and exclusion of delays.

## A complete HTTP example without device operations

This example needs a running branch Core, `curl`, and `jq`. The disabled Cron
definition still has a valid Trigger, but only explicit manual invocation runs
it. It has no Entity references and sends no device Commands. For the local
simulator stack, start with `mise run simulator-start` and obtain the Core port
with `mise env --json | jq -r .SIM_CORE_PORT`. Core requires the documented
readable agent API key file. Use the loopback API address, not a production
endpoint.

```sh
base=http://127.0.0.1:8080
# For the worktree simulator instead:
# base="http://127.0.0.1:$(mise env --json | jq -r .SIM_CORE_PORT)"

definition='{
  "name": "Manual two-second wait",
  "enabled": false,
  "triggers": [
    {"id": "daily", "kind": "cron", "expression": "0 12 * * *"}
  ],
  "steps": [
    {"id": "wait-two-seconds", "kind": "delay", "duration_ms": 2000}
  ]
}'

created=$(curl --fail-with-body -sS -X POST "$base/v1/automations" \
  -H 'content-type: application/json' -d "$definition")
automation_id=$(printf '%s' "$created" | jq -er .id)
curl --fail-with-body -sS "$base/v1/automations/$automation_id"

admitted=$(curl --fail-with-body -sS -X POST \
  "$base/v1/automations/$automation_id/runs" \
  -H 'content-type: application/json' -d '{}')
run_id=$(printf '%s' "$admitted" | jq -er .id)
printf '%s\n' "$admitted" | jq .

# Read history again after the wait; admission is not completion.
curl --fail-with-body -sS "$base/v1/automations/$automation_id/history"
curl --fail-with-body -sS \
  "$base/v1/automations/$automation_id/history/$run_id" | jq .
```

Repeat the history detail GET manually until the Run is terminal. Successful
completion has `status: "succeeded"`, `steps: []`, and one completed delay.
The initial admission response has `delays: []`; it does not wait for execution.

To replace the definition, read its current `revision`, then send a full
replacement with the same definition document, including all nested delays:

```sh
revision=$(curl --fail-with-body -sS "$base/v1/automations/$automation_id" | jq -er .revision)
replacement=$(jq -n --argjson revision "$revision" --argjson definition "$definition" \
  '{expected_revision: $revision, definition: $definition}')
curl --fail-with-body -sS -X PUT "$base/v1/automations/$automation_id" \
  -H 'content-type: application/json' -d "$replacement"
```

For a Command-delay-Command sequence, discover a simulator power Entity with
`GET /v1/entities`, confirm boolean `set` support, and replace `steps` with:

```json
[
  {"id":"on","entity_id":"ent_01950000-0000-7000-8000-000000000001","operation":"set","parameters":{"value":true}},
  {"id":"wait","kind":"delay","duration_ms":2000},
  {"id":"off","entity_id":"ent_01950000-0000-7000-8000-000000000001","operation":"set","parameters":{"value":false}}
]
```

Replace both illustrative IDs with the discovered simulator ID before saving.
Keep the definition disabled for manual testing. This variant sends Commands;
it is not authorization to operate household hardware. An unsuccessful first
Command prevents the delay and final Command. Interruption during the delay
does not turn the light off or undo the earlier Command.

## MCP parity

The existing `/mcp` tools use the same canonical definition schema and service
behavior. There is no new delay tool, endpoint, configuration, or NATS resource.

| HTTP operation | Existing MCP tool and arguments |
| --- | --- |
| Create definition | `create_automation` with `{"definition": <the complete definition>}` |
| Read definition | `get_automation` with `automation_id` |
| Full replacement | `replace_automation` with `automation_id`, `expected_revision`, and `definition` |
| Manual admission | `start_automation_run` with `automation_id` |
| List history | `list_automation_history` with `automation_id` and optional `limit`/`cursor` |
| Read Run detail | `get_automation_history_entry` with `automation_id` and `entry_id` |

HTTP and MCP Run detail always return a `delays` array, including an empty array
for older Runs or when no delay was reached. Manual `bypass_conditions` affects
only admission Conditions, never busy checks, branch evaluation, or execution
gates.

## Active Runs and interruption

A waiting Run remains `running` and occupies the Automation's single-active-Run
slot. Eligible automatic invocations record `automation_busy` Skips; manual
invocations return HTTP 409 with `automation_busy` and create no busy Skip.
Existing admission precedence remains intact, including stale-Fact checks before
busy classification. There is no queue or retrigger-reset behavior.

There is no total Run-duration limit or new process-wide Run cap. Consecutive
delays can keep a Run active for days, retaining its worker and a timer while
waiting. Fact freshness applies at admission, not after each wait.

Editing, disabling, or deleting a definition does not cancel an active Run or
change its immutable snapshot, duration, or remaining sequence. Admission
Conditions are not rechecked after waiting. A later reached branch reads current
coherent State. Caller disconnection does not cancel an admitted Run, and there
is no user-facing Run cancellation API.

| Interruption code | Meaning |
| --- | --- |
| `core_stopping` | Graceful shutdown woke the pending wait and stopped remaining Steps |
| `core_restarted` | Startup atomically interrupted active delays, Command attempts, and Runs |
| `executor_fault` | Execution persistence failed; Core stopped execution and closed admission |

Shutdown wakes pending waits promptly. Commands already in flight retain their
existing shutdown policy. Restart never resumes a wait, executes an overdue
Step, or replays a Command. A new manual invocation is new work, not recovery.
Completed delays remain completed even if shutdown interrupts the Run before
its following Step.

Start and completion writes are required gates. A persistence fault prevents
later Steps, attempts durable interruption, latches admission/readiness failure,
and wakes other pending delays. Core never retries execution or invents a Command
attempt. If storage remains unavailable or a commit is ambiguous, history may
retain the last provable running state until startup interruption. Check
`/readyz`, storage health, and logs; a missing completion is not permission to
continue manually without checking earlier effects.

## Reading history and the browser

Each reached delay has a zero-based `position` independent of Command and branch
positions, `step_id`, `duration_ms`, `status`, `started_at`, and `due_at`.
Terminal records add `completed_at`; interrupted records also add `failure_code`.
Statuses are `running`, `completed`, and `interrupted`. A completed delay proves
elapsed waiting and committed evidence, not execution of its following Step.
Run success means the selected sequence completed and can include zero Commands.

Core uses monotonic elapsed time for the wait. `started_at`, `completed_at`, and
`due_at` are diagnostic UTC wall-clock timestamps. History derives duration from
the immutable Run snapshot and due time by adding that duration to the recorded
start. Due time is neither timer authority nor a restart instruction. Wall-clock
correction can make completion appear earlier than start or due without changing
the elapsed wait. Positions establish reached order. Corrupt retained identity
or snapshots cause read errors, not invented durations or completion.

The browser shows delay IDs and exact durations in definitions and snapshots,
with a separate Delay executions table. An exact 300000 ms displays as 5 minutes;
1001 ms remains 1001 milliseconds. Delay-only definitions show zero Commands
and no fake Entity links. Empty evidence means no delay was reached, not that
the definition contained none.

The browser displays delay definitions and history. Author through HTTP or MCP. Existing
enablement replacement preserves delays, but disabling still does not stop an
active Run. Use manual history refresh; there is no live countdown, added
polling, or push notification.

## Compatibility and rollback

Ship the schema, runtime, migration, HTTP/MCP mappings, and browser reader types
together. The definition schema remains v1. New Core reads existing definitions
and history without a rewrite or backfill, but older binaries cannot decode
delay definitions or retained snapshots.

Take a pre-feature database backup before upgrading or authoring delays if
binary rollback matters. Migration `00011_automation_run_delays.sql` adds only
delay evidence. Its Down drops the child table and index, not delay nodes in
definitions or snapshots. Rolling back to an older binary requires restoring
the pre-feature database backup. Down is not a binary rollback procedure, and
Down/Up cannot recover dropped evidence. Terminal history pruning cascades delay
evidence deletion; running Runs retain their existing protection.

State waits, calculated durations, resumable suspended Runs, loops, parallel
Steps, retries, compensation, and durable continuation scheduling remain out of
scope. See the [feature specification](../specs/automation-delay-steps.md) and
[gap analysis](automation-gap-analysis.md) for the contract and remaining work.
