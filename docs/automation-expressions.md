# Automation expressions and Run values

Definition/history v3 adds calculations without making Core a script runner.
Entity IDs, Operations, Adapter/Function identities and notification destinations
remain static. Expr v1.17.8 evaluates only owned JSON data, with no I/O, secrets,
live clock, randomness, methods or dynamic dispatch.

## Authoring

Configurable values use exactly one binding: `{"literal":<JSON>}` or
`{"expr":"source"}`. There is no interpolation or recursive interpretation;
`{"literal":{"expr":"ordinary data"}}` is not executed. Both keys, extra keys,
missing required values and empty expression source are invalid.

Bindings apply to Command `parameters` and Function `arguments` (objects),
notification `title` and `message` (strings), optional initial `variables`,
`set_variables.assignments`, and branch expression Condition `value` (bool).
Delay is deliberately literal-only: `"duration_ms":{"literal":1000}`. Even a
constant expression is rejected; native elapsed waiting is unchanged.

```json
{
  "name":"Calculated lighting",
  "enabled":true,
  "triggers":[{"id":"evening","kind":"cron","expression":"0 18 * * *"}],
  "variables":{"seed":{"expr":"trigger.kind == 'manual' ? 1 : 2"}},
  "steps":[
    {"id":"temperature","kind":"read_state",
     "entity_id":"ent_019a1b2c-3d4e-7000-8000-000000000001",
     "value_pointer":"","max_age_seconds":300,"on_unusable":"null"},
    {"id":"calculate","kind":"set_variables","assignments":{
      "target":{"expr":"int(min(100, max(0, (steps.temperature.output ?? 20) * 2)))"}
    }},
    {"id":"bright","kind":"if","conditions":{
      "id":"threshold","kind":"expression","value":{"expr":"vars.target > 40"}
    },"then":[{"id":"apply","kind":"command",
      "entity_id":"ent_019a1b2c-3d4e-7000-8000-000000000002",
      "operation":"set","parameters":{"expr":"{value: vars.target}"}}]}
  ]
}
```

Replace the IDs and payload shapes with registered Entities and their actual
Operation contracts. This is an example calculation, not a universal lighting
policy. Initial expressions can read immutable `run` and `trigger`, not `vars`
or `steps`. Every assignment batch sees the pre-batch context: `a=vars.b` and
`b=vars.a` swap values. Batches apply atomically after evidence commits, survive
selected branches, and are isolated between Runs. There are no global variables,
deletion, local `let`, or assignment operators inside expressions.

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

Ordinary access is required: `vars.occupied != true` fails if the variable is
absent. Present null differs from missing; `??` defaults an existing null, not a
failed lookup. Use `get(vars,"occupied",false)` or a lazy conditional such as
`has(steps,"forecast") ? steps.forecast.output : []`. Helper arguments are eager;
a missing reference in a default argument still fails. Native `?.` is rejected.

Helpers are `len`, `min`, `max`, `round`, `string`, `int`, `float`, `join`, `sum`,
`all`, `filter`, `map`, plus Hearth `get` and `has`. For example:
`map(filter(steps.forecast.output.hours, {.rain}), {.temperature})`.
Unlisted helpers are rejected. Condition groups evaluate every leaf; laziness
applies inside expressions, not across a Condition tree. Expression failure is
not State unknown and never selects fallback/else by accident.

Original JSON ingress preserves exact signed int64, including `9007199254740993`.
Mathematically integral decimals/exponents must fit int64; fractional values use
finite float64. Invalid/unrepresentable integers, overflow and nonzero underflow
to zero fail rather than round. Evaluated values retain their live numeric type;
JSON history cannot preserve every int/float distinction and is never a resumed
expression environment. Opaque literal effect payloads preserve vendor numbers,
but unsupported numbers cannot later enter an expression through State/output.

Integer addition, subtraction, multiplication, negation and integer `sum`
deliberately **wrap at 64 bits**: `9223372036854775807 + 1` becomes
`-9223372036854775808`. Bound values before overflow; destination validation is
not an overflow detector. Division/mixed arithmetic use float64 and may lose
integer precision. `int` explicitly truncates toward zero with range checks;
`round` retains float semantics. Neither `int` nor `float` parses strings.
The expression spelling `-9223372036854775808` has an out-of-range positive token;
use exact JSON or `-9223372036854775807 - 1` instead.

## Limits and execution

| Item | Limit |
| --- | --- |
| Definition / immutable revision | 131,072 encoded bytes |
| Create/replace envelope | 196,608 bytes |
| Expression | 4,096 UTF-8 bytes; 256 AST nodes; depth 16 |
| Nested predicates | 2, including nesting through inputs |
| Value | 65,536 encoded bytes; depth 16; 8,192 nodes |
| Array/object | 128 elements/members |
| Variables / initialization or assignment batch | 64 names / 65,536 bytes |
| Live context | 262,144 encoded bytes |
| Retained value evidence | 2 MiB / 256 logical records, including reserved failure capacity |

Existing Step-tree limits remain; the two data kinds share the 32 effect/data
leaf ceiling. Intermediate constructions are bounded too. Expr's extra 32,768
allocation units are not bytes or a full memory sandbox.

There is **no hard execution timeout or VM preemption**. Cancellation is checked
before/after evaluation. A valid adversarial two-predicate workload with 128
elements and a near-limit string measured roughly 2.3 seconds/evaluation on the
original development host; the initial release explicitly accepts this cost.
That is not an upper bound or responsiveness guarantee. A Run worker can be
occupied for several seconds; keep household expressions small.

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
diagnostics use binding pointers, spans and fixed codes, never raw provider values
or expression source.

Before upgrade, stop Core cleanly and take a consistent SQLite backup, accounting
for WAL state. Retain its matching binary. Migrations 21/22 on main retain their
documented earlier development resets. Migration 23 itself is preserving: it
wraps v2 Step fields in every retained immutable revision, including non-head
revisions referenced by history. It preserves metadata, existing executions,
holds, receipts and schedule progress. Old Runs are `legacy_unavailable`; no
historical values are manufactured. Active old Runs are interrupted, not resumed.

Core's migration provider validates stored v2 documents against a frozen schema
inside the upgrade transaction before changing data. Use Core's normal startup
upgrade path; do not execute migration 23's SQL file with a standalone runner,
which would bypass that validation.

New public writes accept v3 only, with schema IDs
`urn:hearth:schema:automation-definition:v3` and
`urn:hearth:schema:automation-history:v3`. There is no mandatory body version.
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
