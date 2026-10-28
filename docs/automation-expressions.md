# Automation expressions and Run values

Definition/history v1 adds calculations without making Core a script runner.
Entity IDs, Operations, Adapter/Function identities and notification destinations
remain static. Expr v1.17.8 evaluates owned JSON data without I/O or secrets.
Hearth exposes no host functions and disables `now()`, `date()` and `timezone()`
(which can load host zoneinfo); use immutable Run timestamps instead of a live clock.

## Authoring

Literal fields keep their ordinary shape. Exactly one of Command `parameters`
or `parameters_expr` is required; likewise Function `arguments`/`arguments_expr`,
and each Notification `title`/`title_expr` and `message`/`message_expr` pair.
Expressions must produce the same object or string type as the literal field.
There is no interpolation, binding envelope or recursive interpretation:
`"parameters":{"expr":"ordinary vendor data"}` is not executed.

Initial values use `variables` for literal JSON and `variables_expr` for source
strings. Assignments use `assignments` and `assignments_expr`. Names cannot overlap
between the maps, and their combined count is bounded. A branch expression Condition
is `{"id":"cold","kind":"expression","expr":"vars.temperature < 20"}` and must
produce a bool. It is not allowed in admission Conditions.
Delay remains literal-only: `"duration_ms":1000`. Wrappers and `duration_ms_expr`
are rejected; native elapsed waiting is unchanged.

```json
{
  "name":"Calculated lighting",
  "enabled":true,
  "triggers":[{"id":"evening","kind":"cron","expression":"0 18 * * *"}],
  "variables_expr":{"seed":"trigger.kind == 'manual' ? 1 : 2"},
  "steps":[
    {"id":"temperature","kind":"read_state",
     "entity_id":"ent_019a1b2c-3d4e-7000-8000-000000000001",
     "value_pointer":"","max_age_seconds":300,"on_unusable":"null"},
    {"id":"calculate","kind":"set_variables","assignments_expr":{
      "target":"int(min(100, max(0, (steps.temperature.output ?? 20) * 2)))"
    }},
    {"id":"bright","kind":"if","conditions":{
      "id":"threshold","kind":"expression","expr":"vars.target > 40"
    },"then":[{"id":"apply","kind":"command",
      "entity_id":"ent_019a1b2c-3d4e-7000-8000-000000000002",
      "operation":"set","parameters_expr":"{value: vars.target}"}]}
  ]
}
```

Replace the IDs and payload shapes with registered Entities and their actual
Operation contracts. This is an example calculation, not a universal lighting
policy. Initial expressions can read immutable `run` and `trigger`, not `vars`
or `steps`. Every assignment batch sees the pre-batch context: `a=vars.b` and
`b=vars.a` swap values. Batches apply atomically after evidence commits, survive
selected branches, and are isolated between Runs. There are no global variables,
deletion, or assignment operators that mutate Run context. Native `let` introduces
only an expression-local value.

Only successful Functions and `read_state` expose `steps.<id>.output`. Unreached,
unselected and failed Steps have no output. Function results are copied after
verifying retained invocation ownership, arguments, contract and outcome; later
independent invocation pruning cannot remove the Run's copied value.

`read_state` takes a fresh coherent retained-State snapshot when reached. An empty
RFC 6901 pointer selects the whole value. Missing Entity/State/pointer and bounded
future/expired evidence fail by default. Explicit `on_unusable:"null"` permits
those cases and records their nonempty reason. A selected JSON null is usable.
Database errors, uncovered snapshots, corrupt State and numeric/size failures
never become fallback null. Retained State is not a live reachability check.

## Missing values and numbers

The language is native Expr, not a Hearth dialect. A missing map key returns
`nil`: `vars.occupied != true` is true when absent, and `vars.target ?? 20` supplies
a default for missing or nil. Use membership (`"occupied" in vars`) to distinguish
absence from a present nil. Access through nil may fail; guard a possibly unreached
output with `steps.forecast?.output ?? []`. Step references are runtime lookups,
not a static dependency graph. There are no Hearth `get` or `has` helpers.

Native operators, conversions, builtins, optional access, predicates and `let`
are supported. For example:
`let hours = steps.forecast?.output?.hours ?? []; map(filter(hours, {.rain}), {.temperature})`.
Condition groups evaluate every leaf; laziness
applies inside expressions, not across a Condition tree. Expression failure is
not State unknown and never selects fallback/else by accident.

Original JSON ingress uses the token's spelling. Integer tokens preserve exact
signed int64, including `9007199254740993`, and fail on overflow. Decimal or
exponent tokens use finite float64, even when mathematically integral. They may
round: `9007199254740993.0` becomes `9007199254740992`, while `1e3` is a float.
Nonfinite numbers and nonzero underflow to zero fail. Evaluated values retain their live numeric type;
JSON history cannot preserve every int/float distinction and is never a resumed
expression environment. Opaque literal effect payloads preserve vendor numbers,
but unsupported numbers cannot later enter an expression through State/output.

Native integer arithmetic can **wrap at 64 bits**: `9223372036854775807 + 1` becomes
`-9223372036854775808`. Bound values before overflow; destination validation is
not an overflow detector. Division/mixed arithmetic use float64 and may lose
integer precision. Conversions such as `int("2")` follow Expr, not strict JSON
ingress rules. Nonfinite or unsupported final results cannot enter context/history;
intermediate arithmetic is native and is not guarded at each operation. Avoid
round-tripping exact integers through `fromJSON`, float conversions or strings
when precision matters.

## Limits and execution

One worker owns one live context per Run. Initialization evaluates against only
`{run, trigger}`; successful initialization commits before exposing
`{run, trigger, vars, steps}`. Expressions receive owned copies and cannot mutate
that live context. History is inspection evidence, never a source for resumed state.

| Reached kind | Durable ordering | Context change after commit |
| --- | --- | --- |
| Set Variables | Evaluate simultaneous batch → validate candidate context → commit assignments | Replace assigned `vars` entries |
| Read State | Capture coherent State → validate candidate output → commit capture | Add `steps.<id>.output` |
| Function | Commit resolved arguments/reservation → invoke → verify result → commit completion/output | Add `steps.<id>.output` |
| Command / Notification | Commit resolved inputs/reservation → execute → commit outcome | None |
| If / Choose | Evaluate Conditions → commit selection → execute selected children | None directly |
| Delay | Commit start → wait → commit completion | None |

`run` and `trigger` never change. A failed or uncertain gate stops downstream work.

| Item | Limit |
| --- | --- |
| Definition / immutable revision | 131,072 encoded bytes |
| Create/replace envelope | 196,608 bytes |
| Expression | 4,096 UTF-8 bytes; native parser limit of 256 nodes |
| Value | 65,536 encoded bytes; depth 16; 8,192 nodes |
| Array/object | 128 elements/members |
| Variables / initialization or assignment batch | 64 names / 65,536 bytes |
| Live context | 262,144 encoded bytes |

Existing Step-tree limits remain; the two data kinds share the 32 effect/data
leaf ceiling. Each evaluation uses a fresh VM with 32,768 allocation units.
This is not a byte budget or a full memory sandbox: not every intermediate
allocation (notably strings) is charged. Input, candidate context and final output
checks remain separate. Constant folding is disabled so constructions occur in
the VM rather than during compilation.

Copying bounds traversal, container sizes and aggregate raw string bytes before
serialization. The actual JSON encoding is then checked against the value or
context limit. Escaping can temporarily allocate more than the final accepted limit.
There is no aggregate Run-history byte or record quota. Intermediate values and
complete Condition evaluations remain available under ordinary history retention.

There is **no hard execution timeout or VM preemption**. Cancellation is checked
before/after evaluation. Native workloads may occupy a Run worker for several
seconds or more; source/node and allocation limits are not a CPU or responsiveness
guarantee. Keep household expressions small.

Resolved inputs commit with the execution reservation. Assignments, State reads
and outputs commit before live context advancement. A successful provider may
already have effected external work when output conversion fails: history keeps
that provider success and the failed Step/Run value-flow outcome. There is no
retry to recover the output. Persistence uncertainty stops downstream work.
Shutdown/restart interrupts and discards live context without replaying anything.

## Unified history and upgrade

Run detail adds `data_evidence` and `value_initialization`. Initialization is
Run-level evidence, not a synthetic Step. The only Step timeline is `executions`:
effect inputs, copied outputs, assignments, State captures and safe value failures
live with their kind-owned evidence. A pre-reservation failure has no fabricated
Command, Attempt or Invocation ID. Delay configuration stays in the retained
revision. History summaries do not load these payloads.

The dashboard shows ordered execution metadata and expandable complete exact
JSON, plus initialization and legacy labels. Integers beyond JavaScript's safe
range remain exact in the inspector and full-definition enablement replacements.
Retained values are evidence, not permission to replay an action.

**Values may reveal household-sensitive data.** They follow existing history
access and retention, not ordinary logs or a second retention policy. Keep
credentials in Adapter configuration, not expressions or arguments. Safe failure
diagnostics use fixed codes and optional logical `field` or `condition_id`, never
raw provider values or expression source. Fields include `parameters`, `arguments`,
`title`, `message`, `variables.NAME`, and `assignments.NAME`; batch failures use
`variables` or `assignments`, and Trigger ingress uses `trigger`. Step identity
comes from the execution envelope. Whole-Step, Read State and post-success Function
failures omit the field. Choose's evaluated prefix identifies the failing root,
within which the Condition ID is resolved. Definition-time syntax diagnostics
retain source locations.

Before upgrade, stop Core cleanly and take a consistent SQLite backup, accounting
for WAL state. Retain its matching binary. Migrations 21/22 on main retain their
documented earlier development resets. Migration 23 itself is preserving: it
keeps v2 literal definitions byte-for-byte in every retained immutable revision,
including non-head revisions referenced by history. It preserves metadata, existing executions,
holds, receipts and schedule progress. Old Runs are `legacy_unavailable`; no
historical values are manufactured. Active old Runs are interrupted, not resumed.

Core's migration provider validates stored v2 documents against a frozen schema
inside the upgrade transaction before changing data. Use Core's normal startup
upgrade path; do not execute the SQL files for migrations 23 or 24 with a
standalone runner, which would bypass their data conversion.

Migration 24 preserves existing revisions and value history. It converts failure
pointers to logical fields or Condition IDs using the retained revision, removes
runtime spans, and drops aggregate counters. Retained value number tokens stay
unchanged. Historical reads check shape, variable names and execution consistency
without recalculating old literal results under today's numeric rules; new writes
still enforce literal equality. `run_data_limit` remains readable in old history
but is no longer emitted.

Public definition and history schemas use
`urn:hearth:schema:automation-definition:v1` and
`urn:hearth:schema:automation-history:v1`; response bundles also use v1.
The unreleased schema identifiers were reset to v1. There is no mandatory body
version, so this metadata reset does not rewrite stored documents. The frozen v2
schema used by migration 23 describes the earlier pre-expression storage shape.
An oversized opaque literal may migrate losslessly and fail explicitly only if
reached under the new payload cap; it is never truncated.

The closed #222 branch used an **unmerged migration 20** that differs from main's
migration 20. Its development databases are a separate lineage: keep them intact,
use fresh storage or restore a suitable pre-feature backup, and do not assume
renumbering a migration makes those schemas interchangeable. The application does
not automatically reset or adopt such worktree databases.

Rollback requires stopping Core and restoring the matching pre-upgrade backup
with the previous binary. Goose Down refuses lossy conversion. An older binary
must not open the upgraded database.
