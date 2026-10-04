# Automation branching

Automation branching is implemented on `feat/automation-branching`. This guide
describes that Core version, not a merged or deployed release.

An Automation can use nested `if` and ordered `choose` Steps to select static
Command sequences. Admission Conditions decide whether a Run starts. Branch
Conditions decide what an admitted Run does when it reaches a branching Step.
They can compare current Entity State or match the Run's recorded Trigger IDs.

## A complete nested definition

Discover registered IDs with `GET /v1/entities`. Replace the illustrative IDs
below with your button event Entity and controllable power Entity. The button
must support `single_press` and `double_press`; the power Entity must support
`set` with boolean `value` parameters and have boolean State. Saving validates
every arm, including arms that a particular Run will not select.

This definition routes a single press through a nested State-dependent toggle,
routes a double press to off, and turns on for a manual invocation:

```sh
curl -X POST http://127.0.0.1:8080/v1/automations \
  -H 'content-type: application/json' \
  -d '{
    "name": "Office button routes power",
    "enabled": true,
    "triggers": [
      {
        "id": "single",
        "kind": "entity_event",
        "entity_id": "ent_01950000-0000-7000-8000-000000000001",
        "event_name": "single_press"
      },
      {
        "id": "double",
        "kind": "entity_event",
        "entity_id": "ent_01950000-0000-7000-8000-000000000001",
        "event_name": "double_press"
      }
    ],
    "steps": [
      {
        "id": "route-button",
        "kind": "choose",
        "branches": [
          {
            "id": "toggle",
            "conditions": {"id": "single-match", "kind": "trigger", "trigger_ids": ["single"]},
            "steps": [
              {
                "id": "toggle-power",
                "kind": "if",
                "conditions": {
                  "id": "is-on",
                  "kind": "entity_state",
                  "entity_id": "ent_01950000-0000-7000-8000-000000000002",
                  "value_pointer": "",
                  "operator": "eq",
                  "operand": true,
                  "max_age_seconds": 300
                },
                "then": [
                  {"id": "toggle-off", "entity_id": "ent_01950000-0000-7000-8000-000000000002", "operation": "set", "parameters": {"value": false}}
                ],
                "else": [
                  {"id": "toggle-on", "entity_id": "ent_01950000-0000-7000-8000-000000000002", "operation": "set", "parameters": {"value": true}}
                ]
              }
            ]
          },
          {
            "id": "off",
            "conditions": {"id": "double-match", "kind": "trigger", "trigger_ids": ["double"]},
            "steps": [
              {"id": "double-off", "entity_id": "ent_01950000-0000-7000-8000-000000000002", "operation": "set", "parameters": {"value": false}}
            ]
          }
        ],
        "default": [
          {"id": "manual-on", "entity_id": "ent_01950000-0000-7000-8000-000000000002", "operation": "set", "parameters": {"value": true}}
        ]
      }
    ]
  }'
```

Use the returned `aut_` ID for later requests. Full replacement still uses
`PUT /v1/automations/{automation_id}` with `expected_revision` and `definition`.
HTTP and MCP accept the same recursive definition and publish the same v1 schema.
The web UI shows definitions and history but does not provide an authoring editor.

## Step and Condition contract

- A Command Step has `id`, `entity_id`, `operation`, and `parameters`. It has no
  `kind` field. Explicit `"kind":"command"` is invalid.
- An If Step has `id`, `"kind":"if"`, `conditions`, and a nonempty `then`
  sequence. It may have a nonempty `else` sequence.
- A Choose Step has `id`, `"kind":"choose"`, and ordered `branches`. Each
  alternative has `id`, `conditions`, and a nonempty `steps` sequence. It may
  have a nonempty `default` sequence.
- Omit `else` or `default` for no action on a false/no-match result. An explicit
  empty array or null is invalid. Go callers use nil for an omitted optional arm.
- Any sequence can contain Commands, If Steps, and Choose Steps. After a selected
  sequence completes, execution continues with the following surrounding Step.
  An all-false construct without a fallback is a successful no-op.

Branch State Conditions reuse `entity_state`, `all`, `any`, and `not` from the
[Conditions guide](automation-conditions.md). `value_pointer` selects inside
the State value, not the Entity API response. The empty string selects the whole
value. `pointer` is a deprecated input alias; do not supply both names.

A `trigger` leaf is true when any configured `trigger_ids` ID belongs to the
immutable Run match set. Its list contains 1–32 unique IDs referencing Triggers
in the same definition. Use `all` over separate leaves to require multiple
matches, or `not` to negate a match. This leaf never returns unknown. It works
with Observation, Entity Event, held-state, and scheduled admission alike,
including multiple matched scheduled IDs. Trigger leaves are invalid anywhere
in top-level admission `conditions`.

Manual Runs have no matched Trigger IDs. Every trigger leaf is therefore false,
and its negation can be true. Manual invocation accepts no synthetic Trigger
context. `{"bypass_conditions":true}` bypasses only admission Conditions; branch
Conditions still evaluate normally. In the example, manual invocation selects
`default` and never reads the nested toggle's State.

### Authoring bounds

| Item | Limit |
| --- | --- |
| Triggers | 1–32 |
| Command Steps across all arms | 1–32 |
| All command/if/choose nodes | At most 64 |
| Step depth | At most 8, with top-level Steps at depth 1 |
| Each present sequence | 1–32 direct children |
| Choose alternatives | 1–32 per Choose Step |
| Each Condition root | At most 64 nodes and depth 8 |
| Condition nodes across admission and all branch roots | At most 256 |
| Normalized definition | At most 64 KiB |
| Trigger, Step, Condition, and alternative IDs | 1–63 bytes, `^[a-z0-9][a-z0-9_-]{0,62}$` |

Count all defined nodes, including unselected arms. Step IDs are unique across
the whole definition. Alternative IDs are unique within their Choose Step.
Condition IDs are unique within one root; admission has its own root namespace.
Unknown fields, contradictory family fields, and null structural members are
invalid. A State comparison operand may be JSON null because it is a value.

Save-time validation checks every Entity and Command reference in every arm.
State predicates require existing stateful Entities, but missing, unavailable,
or disabled State does not itself invalidate the definition. Selected Commands
still validate against current support and execution eligibility when attempted.
Targets and parameters remain static; there are no variables or expressions.

## When State is read

Admission reads only top-level admission Condition references. Reaching an If
reads its Condition's requested Entities once. Reaching a Choose reads the union
of all its immediate alternatives' State references in one coherent snapshot,
even if an early Trigger-only alternative will match. It does not read State
referenced only by nested Steps. A Trigger-only construct needs no State read.

Core samples one UTC evaluation time after a successful read. Every evaluated
Choose alternative shares that time and snapshot. A reached nested branch reads
again after preceding Commands finish. The snapshot is coherent, not atomic
with the later Commands; physical actions or other Runs can change State.
Recorded decisions are never reevaluated after State changes.

Core evaluates every leaf inside an evaluated Condition tree. Three-valued logic
still applies: `any(true, unknown)` is true and `all(false, unknown)` is false.
For Choose, Core evaluates alternatives in order and stops at the first true or
unknown root. The first true root selects its arm. An unknown root fails the Run
immediately, without later alternatives or default. Later alternatives have no
evaluation evidence, although their immediate State references were in the read.
Unselected nested Steps have neither reads nor decision records.

## Failures and interruption

| History code | Meaning |
| --- | --- |
| `branch_condition_unknown` | An evaluated root was unknown, such as missing or expired evidence |
| `branch_state_read_failed` | State acquisition failed or exceeded its deadline |
| `branch_snapshot_incomplete` | The returned batch did not cover every requested Entity |
| `branch_state_corrupt` | Stored State or supplied State evidence was corrupt |

A batch read failure fails the construct even if an earlier Trigger-only
alternative would otherwise match. Each branch read and decision write has a
five-second bound. Read errors are errors, not false or unknown Conditions.
Core commits unknown/error evidence and Run failure atomically. It retains only
fully evaluated alternatives, never a failed tree's partial evidence.

Core commits a selection decision before attempting its children. If that write
fails or its commit is ambiguous, Core dispatches no selected Command, interrupts
the Run when possible, and closes admission with an executor readiness fault.
Expected unknown evidence fails only its Run, not readiness.

A selected Command failure stops the Run. There is no fallback after selection,
no evaluation or execution retry, and no undo of earlier physical effects.
Shutdown stops execution before the next decision or Command and records
`interrupted/core_stopping`. Restart records `interrupted/core_restarted` for
active Runs, preserves committed decisions and completed attempts, and never
resumes or replays Commands. A new manual invocation is new work, not recovery.
The one-active-Run rule and `automation_busy` behavior remain unchanged.

## Inspecting durable history

```sh
curl http://127.0.0.1:8080/v1/automations/aut_.../history
curl http://127.0.0.1:8080/v1/automations/aut_.../history/arn_...
```

Run detail in HTTP and MCP always includes `branch_decisions`, an empty array
for old flat Runs. Each reached decision has a zero-based `position`, `step_id`,
`kind`, `evaluated_at`, `outcome`, and `evaluations`. Outcomes are `then`, `else`,
`branch`, `default`, `no_match`, `unknown`, or `error`. `branch` also records
`selected_branch_id`; unknown/error records a `failure_code`. Choose evaluations
carry `branch_id` and contain only the evaluated definition-order prefix.
Trigger leaf evidence contains `trigger.matched_trigger_ids`, the matching
intersection in configured order, or an empty array for false.

Decision position is reached-decision order, not a Command position. Evaluation
times are nondecreasing and may be equal; position establishes order. `steps`
remains the Command-attempt array, with stable zero-based positions assigned by
walking all sequences left to right, Then before Else, and Choose alternatives
before Default. In the example, `toggle-off`, `toggle-on`, `double-off`, and
`manual-on` have positions 0–3. Branch Steps have no command-attempt row.

A decision proves selection, not dispatch or completion. A committed selection
can sit beside entirely unattempted children after shutdown. Unselected commands
and commands never reached both remain `not_attempted`; do not infer a separate
status. Check Command attempts and the Run outcome for execution results.

The web UI shows a nested definition outline, a separate chronological decision
table with expandable evidence, and the Command-attempt table with Step IDs,
stable positions, and verified Command links. Definition edits or deletion do
not change a Run's immutable snapshot or retained evidence. History pruning
removes decision rows with their parent Run under the existing retention policy.

## Compatibility and rollback

The schema ID remains `urn:hearth:schema:automation-definition:v1`. Old flat
documents and history remain readable on new Core, and their command positions
do not change. This is not forward compatibility: older Core binaries cannot
consume branching documents. Update clients that assume every Step is a Command.

Migration `00010_automation_branch_decisions.sql` adds only the decision child
table and preserves existing history. Its Down drops decision evidence only.
It does not remove branching definitions or convert Run snapshots for older
binaries. Binary rollback requires restoring a pre-feature database backup;
take that backup before upgrading or authoring branching definitions. Down/Up
does not recover dropped evidence.

Branching adds no delay, wait, loop, parallel group, Run queue, cancellation API,
resumption, dynamic parameters, provider Step, or new Trigger family. See the
[implemented branching specification](../specs/automation-branching.md) for the
implementation contracts and [gap analysis](automation-gap-analysis.md) for
remaining household migration work.
