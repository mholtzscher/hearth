# Automation expressions and retained values

Automation definition/history v3 adds calculated inputs without turning Core into
a script runner. Entity IDs, Operations, Function identities/fingerprints and
notification destinations are always static. Expressions are pure Expr v1.17.8
programs: no I/O, clocks, randomness, secrets, methods or dynamic dispatch.

## Authoring

Configurable values use exactly one binding: `{"literal": <JSON value>}` or
`{"expr": "<expression>"}`. There is no interpolation or recursive evaluation.
`{"literal":{"expr":"ordinary data"}}` is never executed. Omitted required
bindings, both keys, extra keys and empty expression source are invalid.

Bindings apply to Command `parameters` (object), Function `arguments` (object),
notification `title` and `message` (strings), optional initial `variables`,
`set_variables.assignments`, and branch expression Condition `value` (boolean).
Delay is deliberately **literal-only**, e.g. `"duration_ms":{"literal":1000}`;
even a constant expression is rejected. Native elapsed waiting is unchanged.

```json
{
  "name": "Calculated lighting",
  "enabled": true,
  "triggers": [{"id":"evening","kind":"cron","expression":"0 18 * * *"}],
  "variables": {"target":{"expr":"trigger.kind == 'manual' ? 25 : 40"}},
  "steps": [
    {"id":"temperature","kind":"read_state",
     "entity_id":"ent_019a1b2c-3d4e-7000-8000-000000000001",
     "value_pointer":"","max_age_seconds":300,"on_unusable":"null"},
    {"id":"calculate","kind":"set_variables","assignments":{
      "target":{"expr":"int(min(100, max(0, (steps.temperature.output ?? 20) * 2)))"}
    }},
    {"id":"bright","kind":"if","conditions":{
      "id":"above-threshold","kind":"expression","value":{"expr":"vars.target > 40"}
    },"then":[
      {"id":"apply","kind":"command",
       "entity_id":"ent_019a1b2c-3d4e-7000-8000-000000000002",
       "operation":"set","parameters":{"expr":"{value: vars.target}"}}
    ]}
  ]
}
```

Replace the example IDs with registered Entities and use their actual Operation
contracts. Temperature units remain those of the selected Entity; this example
is a calculation, not a universal temperature-to-brightness policy.

## Run-local context

Expressions can read only `run`, `trigger`, `vars` and `steps`. `run` exposes
admitted ID, Automation ID/revision and immutable start-time evidence. `trigger`
exposes retained admission cause and `matched_trigger_ids`; manual Runs have an
empty match set. Fact and held-State payloads appear only when actually retained.

Initial expressions may read `run` and `trigger`, not `vars` or `steps`. Each
assignment batch sees the pre-batch context; `a = vars.b` and `b = vars.a` swap
values. Batches are atomic and lexicographically ordered for error selection.
Variables survive selected branches and are isolated between Runs. There are no
global variables, deletion, local `let`, or in-expression assignment.

Only successful Functions and `read_state` Steps expose `steps.<id>.output`.
Unselected, unreached and failed Steps have no output. Function outputs are
copied only after verifying retained invocation ownership, contract and outcome.
History pruning of independent invocation records cannot remove copied Run data.

`read_state` reads a fresh coherent retained-State snapshot when reached. Its
static RFC 6901 pointer selects a value; `""` selects the whole value. Missing
Entity/State/pointer and bounded future/expired evidence fail by default.
`on_unusable:"null"` explicitly permits those cases and records their reason.
A selected JSON null is usable. Database/read errors, corrupt State, incomplete
coverage, unsupported numbers and limit failures **never** become null. Retained
State is not a live reachability or availability check.

## Missing values, helpers and numbers

Ordinary member/index access is required: `vars.occupied != true` fails when the
variable is absent. Present null is different from missing. `??` defaults an
existing null, not a failed lookup. Use `get(vars,"occupied",false)` or a lazy
conditional such as `has(steps,"forecast") ? steps.forecast.output : []`.
Helpers' arguments are eager; missing references inside a default still fail.
Native `?.` is not supported.

The initial helper set is `len`, `min`, `max`, `round`, `string`, `int`, `float`,
`join`, `sum`, `all`, `filter`, `map`, plus Hearth `get` and `has`. For example:
`map(filter(steps.forecast.output.hours, {.rain}), {.temperature})`.
Convenience helpers outside this list are rejected rather than silently enabled.
Condition groups still evaluate every leaf; lazy evaluation applies inside Expr
(`&&`, `||`, `?:`, `??`), not across a Condition tree. Expression errors fail a
branch, never select fallback and never become State `unknown`.

JSON ingress preserves exact signed int64 integers, including `9007199254740993`.
Mathematically integral JSON decimals/exponents must fit int64; fractional values
use finite float64. Unrepresentable integers, overflow and nonzero underflow to
zero on ingress fail rather than round. Expression integer literals use int64;
the positive token in `-9223372036854775808` is out of range, so use exact JSON or
`-9223372036854775807 - 1` instead.

Integer addition/subtraction/multiplication/negation and integer `sum` deliberately
**wrap at 64 bits**: `9223372036854775807 + 1` becomes
`-9223372036854775808`. Bound calculations before overflow; destination validation
is not an overflow detector. Division/mixed arithmetic use float64 and may lose
integer precision. `int` explicitly truncates toward zero with range checks;
`round` retains float semantics. Neither `int` nor `float` parses strings.
Opaque literal Command/Function payloads keep exact vendor numbers; this does not
permit out-of-range integers to enter the expression context later.

## Limits and execution

| Item | Limit |
| --- | --- |
| Definition/Run snapshot | 131,072 encoded bytes |
| Create/replace request envelope | 196,608 bytes |
| Expression | 4,096 UTF-8 bytes; 256 AST nodes; depth 16 |
| Nested predicates | 2, including nesting through their inputs |
| Value | 65,536 encoded bytes; depth 16; 8,192 nodes |
| Array/object | 128 elements/members |
| Variables / initialization or assignment batch | 64 names / 65,536 encoded bytes |
| Whole live context | 262,144 encoded bytes |
| Run journal | 256 records / 2 MiB, including reserved failure capacity |

Existing Step tree/depth limits remain. `read_state` and `set_variables` count
toward the 32 combined effect/data leaf ceiling. Intermediate strings, containers
and predicate results are bounded too. Expr's additional 32,768 allocation units
are not bytes or a full memory sandbox.

There is **no hard execution timeout or VM preemption**. Cancellation is checked
before/after evaluation. A deliberately expensive admitted two-predicate workload
with 128 elements and a near-limit string took about 2.3 seconds/evaluation on the
development host; that cost was explicitly accepted for the initial release.
This is not an upper bound or a responsiveness guarantee. Keep household
expressions small; a Run worker can be occupied for several seconds.

Inputs commit atomically with effect reservations; assignments and State/output
records commit before live context advancement. Persistence faults stop downstream
work. A Function may already have effected external work when an oversized result
fails the Run; its successful provider outcome is preserved. There is no retry to
recover output. Shutdown/restart interrupts active Runs, discards live context and
never evaluates journal data or replays effects.

## History and upgrade

Run detail contains `data_evidence:"recorded"` and ordered `data` records for
initialization, resolved inputs, assignments, State captures, Function outputs
and failures. These are separate from per-kind attempt positions. Delay duration
remains in the immutable snapshot and existing wait evidence, not a duplicate
journal row. The dashboard shows record metadata and expandable complete exact
JSON, including integers beyond JavaScript's exact range. List summaries do not
include journal payloads.

**Retained values can reveal household-sensitive information.** They use existing
history access and retention, not ordinary logs or a second retention policy.
Keep credentials in Adapter configuration, not expressions/arguments. Safe failure
diagnostics identify binding pointers/spans and fixed codes, never raw provider
values or expression source. Evidence is not permission to replay an action.

Before upgrading, stop Core and take a consistent SQLite backup (including any
WAL state, using SQLite backup tooling or a cleanly closed database). Keep the old
binary and its matching backup. Startup migration 00020 wraps actual stored v2
Step values losslessly in literal bindings, including every branch and retained
Run snapshot; revisions, timestamps and child evidence are preserved. Old Runs
are marked `legacy_unavailable` with no invented value records. Active legacy
Runs are interrupted by startup, never resumed.

New public writes accept **v3 only**; rewrap static values in existing authoring
clients. There is no mandatory body version field; canonical schema IDs are
`urn:hearth:schema:automation-definition:v3` and
`urn:hearth:schema:automation-history:v3`. A migrated opaque literal can exceed the
new reached-payload cap and then fail explicitly when reached, without truncation.

Rollback requires stopping Core, restoring the **pre-upgrade backup** and using
the matching previous binary. Goose Down intentionally refuses the lossy downgrade;
do not unwrap expressions or point an older binary at the upgraded database.
