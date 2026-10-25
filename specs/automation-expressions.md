# Automation expressions and Run value flow

**Status:** Implemented and locally verified on 2026-10-25. Remote CI is tracked on the delivery PR.
**Date:** 2026-10-25

**Branch:** `docs/automations-expr-spec`

**Baseline:** `origin/main` at `7d2c5f78f7b937e118e548bdbd0b66b2ab300236`

## Problem statement

**Who:** Technical household operators and agents authoring Hearth Automations.
**What:** Steps currently accept fixed inputs. An Automation cannot calculate a Command from retained State, reuse a Function result, maintain Run-local variables, or branch on those values.
**Why it matters:** Ordinary household flows require value transformation, not just fixed actions: filter a forecast, calculate a lighting target, assemble a notification, and choose an action using earlier results.
**Evidence:** The isolated `prototype-automation-expressions` experiment demonstrated these flows with Expr. Its Expr adapter was smaller than its CEL adapter, but it did not establish production persistence, strict missing-value semantics, or runtime bounds. Expr's native missing-map-key behavior can make `vars.occupied != true` succeed when `occupied` was never set. Production must not inherit that behavior accidentally.

## Proposed solution

Embed `github.com/expr-lang/expr` in the Automations module as a **pure value evaluator**, initially pinned to the experimentally inspected `v1.17.8`. Hearth retains ownership of Steps, branching, current-State reads, validation, effect admission, history, and interruption. Expressions receive only owned JSON values; they cannot invoke Functions, dispatch Commands, read State, access secrets, or discover targets.

Introduce definition v3 with uniform literal/expression bindings, optional initial Run variables, `set_variables` and `read_state` Steps, Function output capture, and a branch-only expression Condition. The executor carries one Run-local context through its existing sequential traversal. It resolves values only when their Step is reached, commits evidence before effects or context advancement, and never reconstructs a live executor from history.

Retain exact resolved payloads, assignments, State selections, and Function outputs in bounded Run-owned data evidence. Extend existing reservation/completion transactions rather than placing a second transaction before an effect. Migrate stored definitions and historical snapshots to v3 without inventing missing historical value evidence.

### Settled product choices

- Full value flow, including collection predicates and transformations such as `all`, `filter`, and `map`.
- Static Entity IDs, Operation names, Adapter IDs, Function keys/fingerprints, and notification destinations.
- Required missing references fail; optional access/defaulting is explicit; JSON null is a real value.
- Uniform `{ "literal": ... }` / `{ "expr": "..." }` bindings, accepting a breaking format cutover.
- Signed int64 integers and float64 fractional values in the expression environment; unrepresentable integer inputs fail rather than round.
- Actual bounded values retained with Run history, not ordinary logs.
- `read_state` fails on unusable evidence by default; explicit `on_unusable: "null"` allows a fallback. Operational read failures never become null.
- This spec has its own workspace. Do not merge the prototype wholesale or modify its workspace.

### Selected complexity reductions

The 2026-10-25 refinement keeps the full value-flow feature but narrows its first release:

- Use the focused helper allowlist below; defer local `let` and additional convenience helpers.
- Defer dynamic Delay. Its duration still uses a uniform **literal-only** binding on the wire.
- Start with a basic ordered evidence inspector and expandable exact JSON, not specialized layouts for every value variant.
- Store journal identity/order/time only in SQL columns and variant data only in JSON; assemble the public record on read.
- Keep expression-based initial variables; only dynamic Delay was selected for deferral in that question.

The explicit request to implement this complete spec on 2026-10-25 approves the contract, including these refinements. Material deviations still require review.

## Scope and deliverables

Sizes are estimates of focused implementation effort, not commitments: M = 1–3 hours, L = 1–2 days, XL = more than 2 days. D1 is the highest-uncertainty item and must pass its semantic gate before dependent integration.

| ID | Deliverable / outcome | Effort | Owning paths | Depends on | Acceptance |
| --- | --- | --- | --- | --- | --- |
| D1 | Strict, bounded Expr bridge; JSON conversion; semantic conformance tests | XL | `internal/modules/automations/internal/exprvalue/`, `go.mod`, `go.sum` | — | A1–A4 |
| D2 | v3 bindings, new Step/Condition variants, structural preparation and reference validation | L | Automations domain codecs/schemas; Devices and Adapters validation seams | D1 | A5–A7 |
| D3 | Run data evidence, atomic persistence gates, migration and retention | XL | Automations repository/SQLite; platform DB migrations | D2 | A8–A10 |
| D4 | Sequential value-flow execution, State reads, Function outputs and reconciliation | XL | Automations execution files | D1–D3 | A11–A14 |
| D5 | HTTP/MCP contracts and a basic exact-JSON evidence inspector | L | Automations `httpapi/`, `mcpapi/`; `web/src/api/`, `web/src/pages/` | D2–D4 | A15–A16 |
| D6 | Cross-boundary journeys, regression checks and operator documentation | L | Owner-local tests, `internal/app/hearthd/`, Automations README, relevant docs | D1–D5 | A17–A18 |

Expected total: approximately 3–5 focused engineer-weeks including integration and review; scheduling depends on available focus and the D1 gate. Split XL work into independently checked changes before implementation, not parallel incompatible versions of the contract.

### Non-goals

- Arbitrary scripting, user functions, loops as Steps, parallel branches, dynamic effect routing, or expression-driven Triggers/admission Conditions.
- Persistent/global variables, sharing context between Runs, new retry/resume behavior, or exactly-once external effects.
- A generic application-wide expression service, engine plugin interface, user-selectable engines, or a new workflow DSL.
- Hard execution deadlines, an instruction-budget guarantee, process isolation, or treating Expr's allocation accounting as a memory sandbox.
- A visual Automation editor or expression autocomplete. The dashboard remains an inspector with its existing enablement edit.
- Dynamic Delay durations, local `let`, convenience helpers outside the initial allowlist, and specialized per-variant value presentation. These are deferred, not substitutes for strictness, exact values, or retained evidence.
- Changing Device Fact, Adapter SDK, NATS, Command, or notification wire protocols to carry expression source.

## Current architecture and ownership

`definition.go` owns sealed Step variants. `branch_execution.go` traverses selected sequences; existing per-kind positions are not a global Step order. `repository.go` and the SQLite adapter gate reservations, branch selection, completion, and interruption. Current State is already available through `AutomationDevices.GetEntityStateSnapshot`, with explicit missing-Entity/missing-State evidence.

`ValidateCommand` and `ValidateContractInput` currently combine target/contract checks with concrete payload validation. Dynamic values require splitting those responsibilities without weakening the original independently callable operations. `function_execution.go` currently compares retained invocation arguments with static snapshot arguments; that comparison must use the committed resolved inputs instead.

Canonical schema files belong to Automations. HTTP and MCP publish those contracts through separate transport packages; neither imports the other's models. The frontend already preserves original Automation JSON for exact numeric display and enablement replacement. Extend that mechanism rather than serializing parsed JavaScript numbers.

## Authoring contract: definition v3

### Bindings

A binding is exactly one of the following objects, with no extra keys:

```json
{"literal": {"value": 40}}
{"expr": "{value: vars.target}"}
{"literal": null}
```

An omitted required binding, an empty binding, both keys, a non-string `expr`, or an empty/whitespace-only expression is invalid. No string interpolation or recursive interpretation occurs. `{"literal":{"expr":"not executed"}}` is ordinary JSON. Expression results are never evaluated again.

| Field | v3 type / rule |
| --- | --- |
| `variables` | Optional object of variable-name → binding; defaults to `{}` |
| Command `parameters` | Binding resolving to an object; static Entity/Operation unchanged |
| Function `arguments` | Binding resolving to an object; static Adapter/key/fingerprint unchanged |
| Notification `title`, `message` | Separate bindings resolving to strings; existing content bounds apply |
| Delay `duration_ms` | Literal-only binding containing int64 in `[1, 86400000]`; expression variant rejected at save time |
| `set_variables.assignments` | Nonempty object of variable-name → binding |
| Branch expression Condition `value` | Binding resolving to a boolean |

Only configurable Step values and the new expression Condition use bindings. Trigger match operands, State Condition operands, freshness bounds, names, identifiers, selectors, and routing fields remain static and retain their current format. There are no `_expr` sibling fields.

Delay uses `"duration_ms": {"literal": 1000}` in v3. Even a constant expression such as `{"expr": "1000"}` is invalid for this field; dynamic duration support is deferred, not silently evaluated at save time.

Variable names match `[A-Za-z_][A-Za-z0-9_]{0,62}` and may not start with `__hearth_`. At most 64 variables may exist at once. A `set_variables` Step can introduce or overwrite names, but cannot delete them or mutate `run`, `trigger`, or `steps`. Nested object updates require assigning a new value to the top-level variable; no assignment operator exists inside an expression.

Initial bindings are resolved after Run admission and before the first Step. All see the same empty `vars` and `steps` maps; references to these roots in initial expressions are rejected at save time, including through `get`/`has`. Initial expressions may use immutable `run` and `trigger`. Initialization is one atomic assignment batch, not map-iteration-dependent execution. Failure leaves an admitted, failed Run with data-failure evidence, not a Skip.

All assignments in one `set_variables` Step see the context from before that Step. For example, assigning `a = vars.b` and `b = vars.a` swaps values. Resolve in lexicographic name order for deterministic error selection, validate the resulting context and retain the entire batch atomically; do not expose partial assignments. A variable assigned in a selected branch remains visible after that branch. Unselected arms perform no evaluations or assignments.

### New Steps and expression Conditions

```json
{
  "id": "capture-temperature",
  "kind": "read_state",
  "entity_id": "ent_019a1b2c-3d4e-7000-8000-000000000001",
  "value_pointer": "",
  "max_age_seconds": 300,
  "on_unusable": "null"
}
```

`entity_id` and RFC 6901 `value_pointer` are required and static; empty pointer selects the whole State value. `max_age_seconds` is optional, using the existing Condition range and age semantics. `on_unusable` defaults to `fail` and accepts only `fail` or `null`.

```json
{
  "id": "calculate-target",
  "kind": "set_variables",
  "assignments": {
    "target": {"expr": "int(min(100, max(0, (steps['capture-temperature'].output ?? 20) * 2)))"}
  }
}
```

```json
{
  "id": "is-bright",
  "kind": "expression",
  "value": {"expr": "vars.target > 40"}
}
```

The last object is a Condition leaf inside existing `if.conditions` or `choose.branches[].conditions`, not a new branch Step. Existing `all`/`any`/`not`, State Conditions, Trigger Conditions, and first-match Choose behavior remain available. Admission Condition trees reject expression leaves at any depth.

### Complete value-flow example

The IDs and fingerprint below are placeholders for existing household resources/contracts. The `forecast` Function in this example returns `{ "hours": [{ "rain": boolean, "temperature": number }] }`.

```json
{
  "name": "Forecast-aware evening lighting",
  "enabled": true,
  "triggers": [{"id": "evening", "kind": "cron", "expression": "0 18 * * *"}],
  "variables": {"target": {"literal": 25}},
  "steps": [
    {
      "id": "forecast", "kind": "function",
      "adapter_id": "weather", "function_key": "forecast",
      "contract_fingerprint": "sha256-v1:0000000000000000000000000000000000000000000000000000000000000000",
      "arguments": {"literal": {"hours": 6}}
    },
    {
      "id": "outside", "kind": "read_state",
      "entity_id": "ent_019a1b2c-3d4e-7000-8000-000000000001",
      "value_pointer": "", "max_age_seconds": 300, "on_unusable": "null"
    },
    {
      "id": "calculate", "kind": "set_variables",
      "assignments": {
        "target": {"expr": "all(steps.forecast.output.hours, {.rain == false}) ? 25 : 60"},
        "temperatures": {"expr": "map(filter(steps.forecast.output.hours, {.rain}), {.temperature})"},
        "outside": {"expr": "steps.outside.output ?? 20"}
      }
    },
    {
      "id": "adjust", "kind": "if",
      "conditions": {"id": "wet", "kind": "expression", "value": {"expr": "vars.target > 25"}},
      "then": [{
        "id": "light", "kind": "command",
        "entity_id": "ent_019a1b2c-3d4e-7000-8000-000000000002",
        "operation": "set_level", "parameters": {"expr": "{value: vars.target}"}
      }]
    }
  ]
}
```

Operation/argument shapes remain the referenced contracts' responsibility; the example is not a new universal `set_level` contract.

## Expression environment and semantics

### Environment

Only these roots are exposed. All values are deep-owned JSON-compatible data, never domain structs with methods, raw provider objects, or Go service references.

| Root | Shape and lifetime |
| --- | --- |
| `run` | `{id, automation_id, revision, started_at}`; immutable admitted metadata; UTC timestamp string |
| `trigger` | `{kind, matched_trigger_ids, ...}`; immutable projection of retained admission cause |
| `vars` | Current Run-local variable map; replaced only after committed assignment evidence |
| `steps` | Map of successfully completed output-producing Step IDs → `{output: <JSON value>}` |

`trigger.kind` is `manual`, `schedule`, `device_fact`, or `held_state`, matching the cause, not the matched Trigger's configuration kind. A Device Fact cause additionally exposes `fact` using the existing public Fact shape, including an optional `previous_value`; a held-state cause exposes `evidence` using its existing public shape. Manual and schedule causes have neither field. `matched_trigger_ids` is always present, including an empty array for manual Runs. Do not fabricate event payloads, schedule timestamps, previous State, or held State values absent from retained evidence.

Only successful Function and `read_state` Steps produce `steps.<id>.output` in this version. A deliberately null State selection is a successful output. Failed, unreached, or not-yet-completed Steps have no entry. Command/notification status is still available in history, not as a speculative output API. `set_variables`, Delay and branch Steps do not produce output entries.

Expressions have no live clock, random source, environment lookup, network, database, filesystem, secret store, or implicit State access. They are deterministic for a given value environment and language version. Capture current State with `read_state`; use `run.started_at` for immutable start-time evidence.

### Required and optional access

Every evaluated ordinary map member/index lookup is required. Missing keys, indexing a null/non-container, and out-of-bounds array indexes fail the binding. A present key containing null is not missing. Ordinary Expr `??` handles an existing null; it does **not** suppress missing access on its left-hand side.

Hearth supplies two explicit optional helpers:

```text
has(container, key) -> bool
get(container, key, default) -> JSON value
```

For object keys (strings) or nonnegative array indexes (int64), `has` returns whether the location exists and `get` returns the present value, including null, or the default if absent. A null container counts as absent for these helpers; a wrong non-null container/key type is an error. Helper arguments are evaluated eagerly: a required missing reference used as `container` or `default` still fails. Compose nested `get` calls or use a lazy conditional for optional paths.

| Expression / context | Result |
| --- | --- |
| `vars.occupied != true`, missing key | Fails; never selects a false/true branch by accident |
| `get(vars, "occupied", false) != true` | `true` if absent |
| `has(vars, "occupied")`, present null | `true` |
| `get(vars, "occupied", false)`, present null | null, not `false` |
| `vars.occupied ?? false`, present null | `false` |
| `vars.occupied ?? false`, missing key | Fails |
| `false && vars.missing` | `false`; right side not evaluated |
| `has(steps, "forecast") ? steps.forecast.output : []` | Explicit lazy handling of an unreached Function |
| `all([{ok: true}, {}], {.ok})` | Fails on the second element; predicate members are also strict |

Native optional chaining (`?.`) is rejected in this initial Hearth dialect. Explicit helpers avoid a second, subtly different missing/null policy and simplify the strict AST rewrite. This is a deliberate syntax restriction, not a claim that upstream Expr lacks optional chaining.

### Compiler policy and strict lookup implementation

Implement this inside `internal/modules/automations/internal/exprvalue`, not as a generic platform facility:

1. Check UTF-8/source bytes, parse, and validate source AST size, depth, allowed node kinds, roots, helpers and predicate nesting before running the optimizer.
2. Reject method calls, environment access via `$env`, first-class function values, local `let` declarations, user function calls/declarations, optional chains, ranges, slices, regex operations, exponentiation, builtin-override bypass syntax (`::`), and unlisted builtins. Allow scalar operators, ternaries, `??`, object/array literals, indexing, membership, and allowed collection predicates. Validate syntax tokens/AST nodes, not substring matches inside string literals.
3. Use `expr.Patch(ast.Visitor)` to replace each source member/index lookup with a private checked lookup call. Include dynamic indexes and predicate shorthand such as `.temperature`/`#.temperature`. Reject access to compiler-private names from source.
4. The private helper distinguishes key absence from present null and returns an error for required missing/type/index failures. Preserve locations for safe JSON-pointer plus source-span diagnostics. Apply rewriting once to source nodes; do not recursively patch generated helper calls. Normalize integer constant nodes to int64; do not rely on the host's `int` width for arithmetic.
5. Register the fixed environment and allowlist, disable unlisted builtins, and compile. Disable optimization initially so constant folding cannot bypass numeric/lookup policy or perform an expensive transformation before the gate. Programs are reused only within an unchanged prepared definition, never persisted or shared with mutable VM state.
6. Evaluate against owned JSON values using a fresh VM with `MemoryBudget: 32768` (Expr accounting units, not bytes). Enforce bridge/result bounds, normalize the result, then apply destination validation. Guard all host-helper errors/panics at the evaluator boundary; map faults to fixed failure codes without exposing provider values.

Do **not** pre-scan required references against a live environment. That would reject valid lazy conditionals and cannot enforce dynamic predicate lookups. Strictness must be checked at the actual executed lookup. Do not implement a second general-purpose AST evaluator or fork Expr. D1 must prove the visitor approach against the acceptance matrix before D2–D4 proceed; a failed semantic gate returns to design review, not a silent fallback to native nil behavior.

The focused initial builtin allowlist is **`len`, `min`, `max`, `round`, `string`, `int`, `float`, `join`, `sum`, `all`, `filter`, `map`**. This covers arithmetic/conversion, notification assembly and the representative collection flows without supporting the full Expr helper catalogue. Use Hearth wrappers where needed for numeric conversions, collection/string output bounds and integer accumulation. `string` accepts only scalar JSON values, not arbitrary object formatting. `get`/`has` are additional Hearth helpers, not uncontrolled upstream reflection.

`abs`, `ceil`, `floor`, `lower`, `upper`, `trim`, `split`, `replace`, `count`, `any`, `none`, `one` and all other unlisted builtins are deferred and rejected at compile time. No regex, date/time, JSON parsing/serialization, sorting, grouping, flattening, arbitrary reflection, or caller-supplied callable values in the initial dialect. Use separate `set_variables` Steps instead of local `let` expressions; this does not change simultaneous assignment within a Step.

### Numbers and JSON conversion

- Decode expression ingress with `json.Decoder.UseNumber`; never route JSON through `float64` first. Inspect the exact decimal value using bounded standard-library decimal/rational conversion. A mathematically integral JSON number, including `1.0` or `1e3`, must fit signed int64 and becomes int64. Otherwise convert to finite float64. Reject overflow, nonzero underflow to zero, invalid JSON, duplicate object keys, and trailing data. Limits apply before conversion; a short token such as `1e1000000000` must be classified and rejected without allocating its expanded power of ten.
- Reject integral inputs outside `[-9223372036854775808, 9223372036854775807]`, even when float64 could approximate them. Preserve `9007199254740993` exactly. This applies whenever JSON enters the expression value environment: initial variables, assigned values, Trigger data, State selections and Function outputs.
- Decimal/fractional expression literals use float64; integer literals use int64. Reject out-of-range integer literals at compile time. Upstream parses a sign separately: `-9223372036854775808` is rejected because its positive token is out of range; use `-9223372036854775807 - 1` or an exact JSON input to obtain the signed minimum. Do not fork the lexer merely to change this spelling. Do not expose platform-dependent `int` values to authors.
- `+`, `-`, `*`, unary negation and integer `sum` on integers follow signed 64-bit wraparound, not checked arithmetic or arbitrary precision. For example, `9223372036854775807 + 1` is `-9223372036854775808`. This is intentional Expr-compatible behavior, not a safe saturating calculation. Authors must bound values before overflow; destination validation is not an overflow detector.
- `/` and mixed fractional arithmetic use float64 and can lose precision beyond its exact integer range. Integer remainder remains integer; division/remainder errors and non-finite results fail. No implicit rounding of fractional values is permitted.
- Hearth's `int(x)` explicitly truncates a finite numeric value toward zero and rejects a result outside int64; `float(x)` explicitly converts to finite float64. Neither parses arbitrary strings. `round` retains float semantics; `int(round(x))` requests an integer. Command/Function schemas retain their own integer constraints; do not silently round to satisfy them. Delay is literal-only and its literal must be an int64, not a numeric string or fractional value.
- Results must be null, bool, string, int64, finite float64, arrays, or string-keyed objects composed of those types. Normalize permitted internal numeric representations to this domain and reject other Go values. Encode integer tokens exactly. Carry the typed result and encoded evidence together: do not encode and then decode an evaluated float to obtain the next live value. Thus `3 / 1` remains a float in `vars`; use `int(...)` to explicitly request an integer result. JSON itself does not preserve an internal int/float distinction on historical re-decoding; history is evidence, never a resumed VM environment.

Static literal Command/Function payloads remain opaque exact JSON validated by their existing destination contract. Do not round or newly reject an old vendor integer merely to migrate it. If that value subsequently enters the expression environment as Trigger data, a variable, State, or a Function output, the rules above apply and an unsupported number fails explicitly. This compatibility exception does not allow an unrepresentable integer into Expr.

### Bounds and execution model

These are initial fixed language-policy constants, not per-Automation settings:

| Limit | Bound |
| --- | --- |
| Encoded normalized definition / Run snapshot | 131,072 bytes, raised from 66,080 for wrapper migration |
| One expression source | 4,096 UTF-8 bytes |
| Source AST | 256 nodes, depth 16 |
| Nested predicate bodies | 2; includes nesting through a predicate's input expression |
| Array length / object member count in expression values | 128 per container |
| Value depth / total nodes | 16 / 8,192 per value |
| One expression input/output value or retained payload fragment | 65,536 encoded UTF-8 bytes |
| One initialization/assignment batch | 65,536 encoded bytes for the entire values object |
| Whole live environment (`run`, `trigger`, `vars`, `steps`) | 262,144 encoded bytes |
| Variables | 64 names |
| VM allocation accounting | 32,768 upstream units per evaluation; not a byte bound |
| Run data evidence | 256 records / 2,097,152 encoded record bytes per Run |

Keep existing Step sequence/tree limits, branch depth, Condition node/depth limits and the 32 combined effect leaves. Count `set_variables` and `read_state` in that existing 32-leaf ceiling, as well as in the 64-total-node ceiling; Delay continues to count only toward existing sequence/tree bounds. Do not make a new unbounded Step category.

Enforce value bounds on intermediate constructions, not just the final result. The visitor adds private value guards around array/map construction, collection builtin results and predicate return values; these guards count depth/nodes/encoded bytes before serialization. A predicate may not build an over-limit value merely because a later filter discards it. String `+` uses a bounded private addition helper that preserves numeric addition but checks predicted string size before concatenation. `join` and scalar `string` similarly check predicted output size before allocation, so joining bounded elements cannot bypass the value cap with an oversized intermediate string. No wrappers for deferred helpers are required. Native map/filter allocations still use the VM's additional allocation guard; neither guard is advertised as a complete process-memory sandbox.

The per-fragment journal cap applies to opaque literal payloads too; destination-specific lower limits still win. A legacy payload may migrate losslessly but exceed the new reached-value cap; do not silently truncate it. Report `expression_limit` when reached and document this cutover limitation. Do not reject an entire Run because an unused branch contains a runtime-invalid value: compilation/structural checks happen at save time, value checks when reached. An over-limit immutable Trigger environment fails initialization even if no later expression reads it; do not silently truncate it.

Reserve one bounded failure record (at most 1,024 bytes) inside the record/byte budget so exhausting evidence capacity can atomically record `run_data_limit` and fail the Run. Budget inputs, assignments and output capacity before any subsequent effect/context advancement; an external Function may still return an over-limit result after its own effect has happened.

These bounds constrain input, storage and plausible collection work; they are **not** a hard CPU or wall-time guarantee. Do not advertise a timeout, abandon evaluation in an unjoined goroutine, or claim cancellation can preempt the VM. Check shutdown/cancellation before and after evaluation. Benchmark worst-case admitted predicate compositions; reducing limits or introducing process isolation requires a separate design decision if measured cost is unacceptable. Collection predicates are required scope, not silently removed to satisfy a benchmark.

## Types and interfaces

The following are proposed implementation shapes, not copy-ready complete files. Keep sealed sum types and existing normalization conventions. Imports and unchanged fields are omitted only where context is obvious.

### Definition and Condition types — D2

New `value_binding.go` in `internal/modules/automations`:

```go
type ValueBinding struct{ Body ValueBindingBody }

//sumtype:decl
type ValueBindingBody interface{ isValueBindingBody() }
type LiteralValue struct{ Value json.RawMessage } // nonempty; JSON null is valid
type ExpressionValue struct {
    Source string
    program *exprvalue.Program // private, populated during normalization; never encoded
}
func (LiteralValue) isValueBindingBody() {}
func (ExpressionValue) isValueBindingBody() {}

type VariableName string
type SetVariablesStep struct{ Assignments map[VariableName]ValueBinding }
type ReadStateUnusablePolicy string // "fail" or "null"; omitted decodes to "fail"
type ReadStateStep struct {
    EntityID devices.EntityID
    Pointer string
    MaxAgeSeconds *int64
    OnUnusable ReadStateUnusablePolicy
}
type ExpressionCondition struct{ Value ValueBinding }

func (SetVariablesStep) isStepBody() {}
func (ReadStateStep) isStepBody() {}
func (ExpressionCondition) isConditionBody() {}
```

```diff
diff --git a/internal/modules/automations/definition.go b/internal/modules/automations/definition.go
@@ type CommandStep struct {
-    Parameters    devices.CommandParameters
+    Parameters    ValueBinding
@@ type NotificationStep struct {
-    Title         string
-    Message       string
+    Title         ValueBinding
+    Message       ValueBinding
@@ type FunctionStep struct {
-    Arguments           json.RawMessage
+    Arguments           ValueBinding
@@ type Definition struct {
     Conditions *Condition
+    Variables map[VariableName]ValueBinding
     Steps      []Step
```

Delay's domain and persistence types stay unchanged: `DelayStep.DurationMS` and `DelayExecution.DurationMS` remain int64, and `DelayStart` gains no data-record field. The v3 wire codec accepts only a literal binding and unwraps it into the existing static domain duration; encoding wraps that duration again. `DelayDurationWithPreparedSnapshot` continues to derive history duration from the immutable snapshot. Existing reached-delay transactions and native elapsed waiting remain the execution path; no redundant duration journal record is needed. Extend all closed Step/Condition dispatch, equality/copy routines, reference collectors and codecs for the other new variants; sum-type exhaustiveness is part of D2, not left to runtime defaults.

Successful expression Condition leaves add `ExpressionMatchEvidence{Matched bool}` to the existing sealed `ConditionEvidence` family. Their public node result is `{id, kind: "expression", result: "true"|"false", value: boolean}`. An expression failure is a branch error, not Condition `unknown` or boolean false. Existing State `unknown` semantics remain unchanged.

### Pure evaluator — D1

New `internal/modules/automations/internal/exprvalue/evaluate.go` owns concrete types/functions:

```go
type Environment struct {
    Run map[string]any `expr:"run"`
    Trigger map[string]any `expr:"trigger"`
    Vars map[string]any `expr:"vars"`
    Steps map[string]any `expr:"steps"`
}

type Program struct { /* private immutable Expr program and source metadata */ }
type Value struct {
    Data any // owned normalized JSON value; preserves live int64/float64 type
    JSON json.RawMessage // bounded canonical bytes for evidence/destinations
}
type Failure struct {
    Code string // fixed code; Error() never includes source values
    Start int   // source byte offset, inclusive
    End int     // source byte offset, exclusive
}
func Compile(source string) (*Program, error)
func Evaluate(program *Program, env Environment) (Value, error)
func DecodeValue(raw json.RawMessage) (Value, error)
```

This capability justifies a private package: it has distinct Expr/AST dependencies and a pure semantic test boundary. Do not add an evaluator interface or service injection slot for imaginary engines. Each normalized expression binding keeps its immutable compiled program in the private field above; its current definition JSON Pointer is supplied by the resolving traversal for diagnostics. Source is persisted in the binding; compiled programs and their runtime metadata are never encoded. Validate arbitrary definitions at every independently callable repository/codec entry point; retain preparation within each workflow rather than recompiling on every reached Step. Copy/equality routines compare source semantics without treating program pointer identity as part of definition identity.

### Target validation — D2

```diff
diff --git a/internal/modules/automations/dependencies.go b/internal/modules/automations/dependencies.go
@@ type AutomationDevices interface {
+    ValidateCommandReference(context.Context, devices.EntityID, devices.OperationName) error
     ValidateCommand(context.Context, devices.CommandInput) (devices.CommandParameters, error)
@@ type AutomationFunctions interface {
+    ValidateContractReference(context.Context, adapters.ContractReference) error
     ValidateContractInput(context.Context, adapters.ContractInput) (json.RawMessage, error)
```

New Adapters-owned type in `function_contract.go`:

```go
type ContractReference struct {
    AdapterID string
    FunctionKey FunctionKey
    ContractFingerprint ContractFingerprint
}
```

Devices' new reference method checks Entity existence, supported Operation and applicable reference rules without inventing a dummy payload or requiring control availability. Adapters' new reference method checks the current Adapter, Function and fingerprint without dummy arguments. Existing concrete input methods keep their complete validation for all other callers.

At save time, validate every static reference, including unselected arms and `read_state` Entity statefulness via the existing `ValidateConditionEntity`. Compile all expression bindings. Validate literal destination values completely and retain existing normalization. Dynamic destination values defer shape/schema checks until resolution; do not claim they are fully validated at save time. Statically spelled `steps.<id>`/`steps['id']` references must name an output-producing Step in this definition; reachability/ordering remains a runtime requirement. Unknown variable names and computed member keys are runtime checks, not a false promise of static flow analysis.

At runtime, validate resolved Command parameters, Function arguments/fingerprint and message contents before reservation. Delay retains existing static-duration validation and start checks. Recheck changing facts through the destination's normal execution/admission path. A catalog/target change between validation and admission retains existing failure semantics; no new optimistic locking protocol is needed.

### Run data evidence — D3

New `run_data.go` in Automations:

```go
type RunDataRecord struct {
    Sequence int // contiguous zero-based journal order; separate from per-kind positions
    StepID *StepID // nil only for initialization / initialization failure
    RecordedAt time.Time
    Body RunDataBody
}
//sumtype:decl
type RunDataBody interface{ isRunDataBody() }

type VariablesInitialized struct{ Values json.RawMessage } // object, including {}
type InputsResolved struct{ Values json.RawMessage } // object of destination field -> exact value
type VariablesAssigned struct{ Values json.RawMessage } // entire assignment object
type StateCaptured struct {
    EntityID devices.EntityID
    Pointer string
    ReadAt time.Time
    Observation *ObservationEvidence
    UnusableReason *ConditionUnknownReason // missing/age/pointer reasons only
    Output json.RawMessage // nonempty, including null
}
type FunctionOutputCaptured struct{ Output json.RawMessage }
type RunDataFailed struct {
    Path string // definition JSON Pointer, no values or expression text
    FailureCode string
    SourceStart *int // optional expression source span
    SourceEnd *int
    State *StateReadFailure // only for read_state_unusable
}
type StateReadFailure struct {
    EntityID devices.EntityID
    Pointer string
    ReadAt time.Time
    Observation *ObservationEvidence
    UnusableReason ConditionUnknownReason
}
// Each concrete body implements isRunDataBody; codec discriminators are:
// variables_initialized, inputs_resolved, variables_assigned,
// state_captured, function_output, failed.
```

Public wire record: `{sequence, step_id?, recorded_at, kind, ...variant fields in snake_case}`. `InputsResolved.Values` uses `parameters`, `arguments`, or `title`/`message` as applicable. Record normalized values actually sent, not the original expression result before destination normalization. Static Delay duration is already retained in the immutable snapshot and existing delay evidence. State metadata distinguishes an actual selected null from a null caused by `on_unusable`.

Add `DataEvidence string` (`recorded` or `legacy_unavailable`) and `Data []RunDataRecord` to Run detail and its canonical history schema. New Runs always use `recorded`. Skips have no data journal. Old Runs use `legacy_unavailable` and an empty `data` array. This is not a claim that an old Run evaluated no values.

Extend `StepStart`, `FunctionStart` and `NotificationStart` with an `Inputs RunDataRecord` field. `DelayStart` stays unchanged. Extend `FunctionCompletion` with `Data *RunDataRecord`: for successful verified invocations this is exactly one output or data-failure record; other completions must omit it. The failure case retains the Function's successful provider outcome but atomically fails the parent Run because its output could not enter the value environment.

```diff
diff --git a/internal/modules/automations/repository.go b/internal/modules/automations/repository.go
@@ type Repository interface {
     CompleteStep(context.Context, StepCompletion) error
+    // Initialize/assign/capture only; commits evidence before context advancement.
+    RecordRunData(context.Context, RunID, RunDataRecord) error
+    // Commits failure evidence and failed Run together; no effect reservation.
+    FailRunData(context.Context, RunID, RunDataRecord) error
@@
-    RecordBranchDecision(context.Context, RunID, BranchDecision) error
+    RecordBranchDecision(context.Context, RunID, BranchDecision, *RunDataRecord) error
```

The optional final argument on `RecordBranchDecision` is only for a matching expression failure. The repository validates it and atomically appends it with the existing error decision and failed Run. Successful expression booleans live in branch evidence, not redundant journal records.

Repository entry points independently validate record shape, UTC time, size, sequence, running parent, snapshot Step kind, binding field set, phase and uniqueness. Literal resolved values must match the prepared literal after existing normalization. Dynamic values are not re-evaluated inside SQLite; the repository verifies their shape/bounds and binding ownership, while the service owns evaluation and external schema validation. Direct repository callers cannot bypass structural/persistence invariants, but the repository is not an independent proof of arbitrary expression evaluation.

### Executor-owned live context — D4

Use one private `runValues` owned by the active executor, containing decoded variables/outputs plus the immutable environment and next journal sequence. Pass its pointer through recursive `executeSequence`/branch calls or attach it to the existing cursor. It is not shared between Runs or stored globally. Deep-copy values at ownership boundaries; no helper may mutate input maps/slices.

Resolve bindings once against the pre-Step environment, normalize destination inputs, and reuse the same owned bytes for persistence, effect invocation and reconciliation. For data values, retain `exprvalue.Value.Data` for live context and `Value.JSON` for history, preserving arithmetic types without a JSON round trip. In-memory context changes occur only after a successful matching repository commit. Never reconstruct or re-evaluate resolved inputs while verifying a returned Command, notification attempt or Function invocation.

## Execution, failures and durability

### Ordering by Step kind

| Reached operation | Required durable gate | Then |
| --- | --- | --- |
| Initialization | Record entire `variables_initialized` batch, including `{}` | Expose initial variables; start Steps |
| `set_variables` | Record entire `variables_assigned` batch | Replace assigned names in live `vars` |
| `read_state` | Read snapshot; record `state_captured` | Expose `steps.<id>.output` |
| Command | Resolve/validate; existing Command reservation and `inputs_resolved` in one transaction | Execute and verify against committed parameters |
| Notification | Resolve/validate; attempt link and inputs in one transaction | Send and verify against committed title/message |
| Function | Resolve/validate; invocation link and inputs in one transaction | Invoke; verify; commit Function completion plus copied output before exposing it |
| Delay | Existing reached-delay row; static duration comes from the immutable snapshot | Existing native elapsed wait and completion gate; no expression or data journal record |
| Branch | Evaluate reached roots; commit existing decision, with expression evidence/failure | Execute only selected children |

No new cross-module transaction or outbox is introduced. Reservation remains evidence of admission, not proof that an external effect occurred. If persistence fails, do not invoke the effect or advance live data; follow the existing executor-fault interruption path. If an effect has already happened and completion/output persistence fails, stop subsequent Steps; do not retry the effect to recover an output.

Function reconciliation must continue verifying invocation identity, route, fingerprint, catalog, timing and output-schema evidence. Compare invocation arguments to the **committed normalized resolved arguments**, not `FunctionStep.Arguments` or a newly evaluated expression. Copy only a verified `adapters.Succeeded.Result` into the journal/context. Function failure or unknown outcome supplies no output. Pruning independent Function invocation history must not remove a retained Run's copied output.

Command and notification reconciliation must similarly compare against committed dynamic inputs while preserving all existing ownership checks. A runtime value failure before reservation is a failed Run/data event, not a fabricated external attempt or verified identity.

### Explicit State reads

Each reached `read_state` calls the existing coherent snapshot seam for exactly its static Entity. Capture `read_at` after the read returns, then evaluate age using `ObservedAt` and the existing Condition semantics: future/expired is unusable only when a maximum age is configured; exact maximum age is usable. A later `read_state` intentionally takes a new snapshot. Ordinary expressions never refresh a prior output.

Unusable-reason precedence is Entity missing, State missing, bounded evidence in future, bounded evidence expired, then pointer missing. `on_unusable: "null"` records that reason and known Observation metadata and exposes null. Default `fail` records `read_state_unusable` with its `StateReadFailure` details and terminates the Run. A selected JSON null is usable and has no unusable reason.

Read errors, uncovered requested Entity IDs, corrupt State JSON/evidence, unsupported numeric values and size violations always fail; they are not absence and cannot be converted to null. Use fixed codes `read_state_read_failed`, `read_state_snapshot_incomplete`, `read_state_corrupt`, or the relevant value/limit code. Do not add availability, Adapter-health or enablement filtering: this seam reads retained accepted State, not live reachability. Save-time statefulness validation does not guarantee that State exists at execution time.

### Branch behavior

Expression leaves must resolve to bool, never truthiness, null, a string, or a number. Existing Condition groups continue their non-short-circuit leaf evaluation; therefore an error in an expression sibling fails the branch even if another leaf is false. Lazy evaluation applies **inside an expression** (`&&`, `||`, `?:`, `??`), not as a changed Condition-tree rule.

Choose still evaluates alternative roots until the first true result. An expression failure aborts without selecting default/else, using Branch `error` with completed earlier alternative evaluations retained. A State Condition `unknown` retains existing `branch_condition_unknown` behavior. Evaluate all reached roots against unchanged Run values; expression evaluation cannot assign variables. Existing immediate-branch State snapshot coverage remains coherent and excludes State referenced only by explicit later `read_state` Steps.

### Failure codes and lifecycle

Use fixed classes: `expression_missing`, `expression_type`, `expression_evaluation`, `expression_value_invalid`, `expression_limit`, `resolved_input_invalid`, `read_state_unusable`, the State-read codes above, and `run_data_limit`. Preserve destination-specific catalog/admission/dispatch failures after reservation. Safe diagnostics identify Step/binding JSON Pointer and source span; ordinary logs contain IDs and codes only, not expressions, resolved payloads, provider result text or raw Expr errors.

Data/value failures atomically fail the Run. Operational persistence failures interrupt through the existing executor-fault path. Shutdown/restart keeps current interruption semantics, including active Delay/Function/Command evidence. Context is discarded, not checkpointed for recovery. Startup interrupts active Runs; it must not evaluate expressions, reread State, assign variables, or reissue effects from the journal. An interrupted Run may legitimately retain resolved inputs with no established external outcome.

## Persistence and migration — D3

Create the next available Goose migration after `00019_function_diagnostic_timestamps.sql` (currently proposed `00020_automation_expressions.sql`; renumber if main advances). Its Up operation is transactional and offline before normal admission.

1. Raise definition/snapshot byte checks and codec limits to 131,072; set create/replace HTTP/MCP argument-envelope guards to 196,608 bytes, accounting for the HTTP overflow probe using existing transport conventions rather than confusing document size with request size. The stricter normalized-document bound still applies independently of envelope size.
2. Recursively rewrite only **actual** Step positions in `automations.definition_json` and `automation_history.run_snapshot_json`, including all `if` and `choose` arms: wrap Command `parameters`, Function `arguments`, Notification `title`/`message`, and Delay `duration_ms` in `{"literal": ...}`. Do not walk arbitrary provider JSON as Steps. Preserve absent optional fields and all scalar values; no expression interpretation is involved.
3. Preserve exact number tokens and literal strings, including provider objects that contain `kind`, `steps`, `literal`, or `expr`. Use raw JSON fragments rather than extracting numeric SQL values and reserializing them. Prove this against SQLite integration fixtures; a migration that converts large integers through SQLite REAL is unacceptable.
4. Preserve IDs, revisions, timestamps, enabled status, conditions, holds, schedule progress and every existing child evidence row. If rebuilding parent tables to change CHECK constraints, follow the child-preservation discipline in migration 00017 with foreign keys enabled. Test every child table, not just Commands. Do not increment definition revisions for a representation-only migration.
5. Add history data-evidence provenance, marking existing Runs `legacy_unavailable`; normal new admissions explicitly write `recorded`. Do not synthesize journal inputs/outputs from current State, current catalogs or partially retained external histories.
6. Create Run data storage with cascading parent retention. The proposed table is:

```sql
CREATE TABLE automation_run_data (
    run_id TEXT NOT NULL REFERENCES automation_history(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL CHECK (sequence BETWEEN 0 AND 255),
    step_id TEXT,
    kind TEXT NOT NULL CHECK (kind IN (
        'variables_initialized', 'inputs_resolved', 'variables_assigned',
        'state_captured', 'function_output', 'failed'
    )),
    recorded_at TEXT NOT NULL,
    payload_json TEXT NOT NULL CHECK (json_valid(payload_json)
        AND json_type(payload_json) = 'object'
        AND length(CAST(payload_json AS BLOB)) <= 131072),
    PRIMARY KEY (run_id, sequence)
);
```

SQL columns are the only persisted source of the record envelope: `sequence`, `step_id`, `kind` and `recorded_at` are **not repeated** in `payload_json`. That JSON contains only the selected variant's fields, such as `values` or `output`. The `kind` column selects a strict variant decoder. Provider keys nested inside values remain opaque data, not envelope fields. The SQLite adapter reconstructs `RunDataRecord` from the validated columns and decoded body; the canonical public encoder assembles the unchanged flattened wire record. There is no duplicate-envelope equality check or second authoritative identity.

One record can contain multiple bounded fragments, e.g. title and message. Enforce a 131,072-byte maximum for the complete assembled public record, the 2 MiB aggregate of assembled record bytes, reserved failure capacity and phase uniqueness in the same transaction as inserts; the SQL payload-size check is an additional backstop, not a substitute for envelope-inclusive limits. Total records are also bounded by the primary-key/CHECK constraint. Add a unique partial index for `(run_id, step_id, kind)` when `step_id IS NOT NULL`; initialization uniqueness and valid phase ordering are repository checks.

For migrated Runs, history and reconciliation helpers may extract known static inputs from migrated literal snapshots; they must not claim copied Function outputs or construct journal rows. Startup first interrupts active legacy Runs using existing rules, with no execution under the new dialect.

Update canonical schema IDs to `urn:hearth:schema:automation-definition:v3` and `urn:hearth:schema:automation-history:v3`, including every HTTP/MCP ref-rewriting prefix. There is no mandatory `version` property in definition bodies; schema IDs identify the contract. New public writes accept v3 only and reject v2 scalar fields; stored migration is the compatibility path. Do not retain dual write formats indefinitely.

Back up the database before upgrade and document restoring that backup with the previous binary as the rollback path. Goose Down must refuse this lossy downgrade; it must not unwrap expression bindings or fabricate static values. A corrupt or over-limit migration input fails the transaction without partial conversion or silent disabling. Integration fixtures at the old maximum size must demonstrate that valid wrapper expansion fits the new limit.

## HTTP, MCP and dashboard — D5

Keep existing routes, operation IDs, authentication, pagination and manual-run inputs. No preview endpoint, arbitrary expression-evaluation tool or new event stream is needed. Update create/replace schemas and request byte limits, definition reads, Run detail schemas/mappers, branch evidence, and published OpenAPI/MCP output schemas. Follow existing invalid-definition problem/tool-error mappings; report safe binding-pointer diagnostics, never raw Expr messages containing values. Binding objects and new variants must be represented by canonical closed `oneOf` schemas, not loose undocumented maps.

Frontend shapes in `web/src/api/types.ts`:

```ts
export type AutomationValueBinding = { literal: unknown } | { expr: string };
// Replace parameters/arguments/title/message types with bindings.
// AutomationDelayStep.duration_ms is {literal: number}, not AutomationValueBinding.
// Add variables?: Record<string, AutomationValueBinding> to AutomationDefinition.
export type AutomationSetVariablesStep = {
  id: string; kind: "set_variables";
  assignments: Record<string, AutomationValueBinding>;
};
export type AutomationReadStateStep = {
  id: string; kind: "read_state"; entity_id: string; value_pointer: string;
  max_age_seconds?: number; on_unusable?: "fail" | "null";
};
// Extend AutomationBranchCondition with:
// {id: string; kind: "expression"; value: AutomationValueBinding}
// Extend AutomationRun with data_evidence and the six-variant data record union.
```

Display literal vs expression explicitly in the Step tree; never show a calculated value as a fixed target. Include `read_state` Entities in affected-Entity links. The initial evidence inspector is a basic table/list ordered by journal sequence with Step ID (or initialization), kind, recorded time and failure labels, plus an expandable exact-JSON view of each complete public record. This exposes initialization/assignment/output/State details without dedicated value layouts for each variant. Keep existing per-kind attempt positions separate. Label old Runs “Value evidence unavailable (created before expression support)”. A retained value is evidence, not permission to replay an action. Rich per-variant value presentation is deferred; evidence/API completeness is not.

Extend `automation-json.ts` source preservation to binding objects, initial-variable/assignment objects and each complete public history data record. Continue copying the entire exact definition for enablement replacement. Render retained binding/record JSON from its original source, not `JSON.stringify` on parsed numbers. A record object contains even scalar outputs, so the basic inspector can use object-backed source spans; standalone primitive-fragment accessors and specialized scalar layouts are not required. Test `9007199254740993` and int64 extrema in definitions, Run outputs and enablement PUTs. Native parsed values may be used for UI structure, never as authoritative numeric reserialization.

History summary lists stay small: data records appear only on detail, not every list row. Retention follows the parent Run's existing history policy and authorization. Document that retained variables, State and Function results may contain household-sensitive data; secrets belong in Adapter configuration, not Automation arguments or expressions. This feature does not add redaction heuristics or a second retention setting.

## Proposed project layout

Paths below are responsibility boundaries; helper/test filenames can be split further for readability without adding new product modules. `*` means existing owner-local files affected by the new closed variants, not permission for unrelated cleanup.

```text
specs/
└── automation-expressions.md                 # new — this approval-gated implementation contract
internal/
├── modules/
│   ├── automations/
│   │   ├── value_binding.go                  # new, D2 — binding sum type/codec/normalization
│   │   ├── run_data.go                       # new, D3 — evidence variants and validation
│   │   ├── run_values.go                     # new, D4 — executor-owned context/resolution
│   │   ├── variable_execution.go             # new, D4 — initialization and atomic assignments
│   │   ├── read_state_execution.go           # new, D4 — explicit capture and unusable policy
│   │   ├── definition*.go                    # modify, D2 — v3 fields/bounds/preparation
│   │   ├── conditions*.go                    # modify, D2/D4 — branch-only expression leaf
│   │   ├── condition_evidence*.go            # modify, D2 — boolean expression evidence
│   │   ├── branch_*.go                       # modify, D4 — context and error decision gates
│   │   ├── execution*.go                     # modify, D4 — initialization/resolved Commands
│   │   ├── function*.go                      # modify, D2/D4 — reference checks, outputs/reconcile
│   │   ├── notification*.go                  # modify, D2/D4 — bound messages/reconcile
│   │   ├── dependencies.go                   # modify, D2 — consumer-owned validation seams
│   │   ├── repository.go                     # modify, D3 — atomic data/effect gates
│   │   ├── run*.go                           # modify, D3 — detail/provenance/copy/codecs
│   │   ├── automation-definition.schema.json # modify, D2 — canonical v3 authoring contract
│   │   ├── automation-history.schema.json    # modify, D3 — canonical v3 evidence contract
│   │   ├── internal/exprvalue/               # new, D1 — no domain services or persistence
│   │   │   ├── compile.go                    # AST policy, strict rewrite, immutable Program
│   │   │   ├── evaluate.go                   # Environment, Failure, pure evaluation
│   │   │   ├── json.go                       # lossless numeric ingress/result normalization
│   │   │   ├── helpers.go                    # bounded pure helper allowlist
│   │   │   └── *_test.go                     # semantic corpus, fuzz seeds, benchmarks
│   │   ├── sqlite/
│   │   │   ├── run_data.go                   # new, D3 — phase/budget checks and inserts
│   │   │   ├── *.go                          # modify, D3 — effect/history/retention transactions
│   │   │   ├── dbqueries/*.sql               # modify, D3 — data and resolved-input queries
│   │   │   └── dbsqlc/                       # regenerate, D3 — checked-in query output
│   │   ├── httpapi/                          # modify, D5 — operation-local DTO/schema mappings
│   │   ├── mcpapi/                           # modify, D5 — independent tool/schema mappings
│   │   └── README.md                         # modify, D6 — language, limits, examples, ownership
│   ├── devices/command.go                    # modify, D2 — static Operation reference check
│   └── adapters/function_contract.go         # modify, D2 — ContractReference and validation
├── platform/db/migrations/
│   └── 00020_automation_expressions.sql      # new, D3 — next available migration number
└── app/hearthd/*_test.go                     # modify/new, D6 — public assembled journeys
web/src/
├── api/
│   ├── types.ts                             # modify, D5 — v3 discriminated contracts
│   ├── automation-json.ts                   # modify, D5 — exact literal/evidence source
│   └── automation-json.test.ts              # modify, D5 — exact-number regressions
└── pages/
    ├── automation-step-tree.ts              # modify, D5 — exhaustive summaries/references
    ├── AutomationStepTree.tsx               # modify, D5 — binding/new variant rendering
    ├── AutomationDetailPage.tsx             # modify, D5 — basic ordered record/exact-JSON inspector
    └── *Automation*.test.tsx                 # modify, D5 — component contract coverage
go.mod / go.sum                              # modify, D1 — pinned Expr dependency
```

No production event/configuration types are added: existing Device Facts trigger admission unchanged, existing dependency assembly supplies Devices/Adapters/Notifications, and limits are fixed dialect constants. Update affected test doubles and module consumers explicitly; do not create a service locator or require Expr configuration in Core startup.

## Acceptance criteria

- [x] **A1 — Strict lookups:** Ordinary missing variables, Step outputs, nested keys, computed keys and predicate members fail. Null remains present. Explicit `get`/`has` defaults and lazy conditionals work as specified; no eager reference preflight.
- [x] **A2 — Numeric contract:** int64 extrema and `9007199254740993` survive ingress/evaluation/encoding. Out-of-range integral inputs/literals fail. Fractional arithmetic, explicit conversion, wraparound, remainder/division failures and non-finite/underflow cases match the contract.
- [x] **A3 — Language policy:** The focused allowlist, including `all`/`filter`/`map`, works; local `let`, deferred/unlisted helpers, forbidden roots, methods, private helpers, `$env`, optional chains, ranges, regex and slices fail compilation. Helpers cannot mutate context or return host objects.
- [x] **A4 — Limits:** At/beyond source, AST, nesting, container, value, environment and expansion bounds have deterministic results. Representative worst-case admitted collection compositions have recorded benchmark results and no unbounded helper allocation. No hard-timeout claim is made.
- [x] **A5 — Format:** Both binding variants round-trip where supported; absent/null/both-key/extra-key errors are exact. Delay accepts only literal int64 duration bindings and rejects even constant expressions. v2 scalar writes fail. Provider JSON containing `expr`/`literal` is never code. All Step/Condition closed-family dispatch is updated.
- [x] **A6 — Validation split:** Save rejects bad static targets/fingerprints and invalid literal payloads in any arm. Dynamic object payloads compile without dummy arguments, then validate against the real destination at execution. Original concrete validation operations remain safe for direct callers.
- [x] **A7 — Placement:** Admission trees reject expression leaves recursively; branches accept boolean expression bindings. Static output-Step references reject unknown/non-output Steps; runtime absence remains strict.
- [x] **A8 — Atomic gates:** Failed data/start commits permit no effect/context advancement. Successful effect reservations retain the exact inputs used. Duplicate, wrong-parent, wrong-kind/phase, sequence and budget violations fail at repository entry points. Capacity failure retains a bounded failure record. Envelope fields persist only in SQL columns; payload-only rows reconstruct the same public records, with exact values and envelope-inclusive byte accounting.
- [x] **A9 — Migration:** Fresh DB and v2 upgrade pass. Every Step arm and old near-limit document converts losslessly; all existing child rows/holds survive. Revision/timestamps remain unchanged, numeric tokens remain exact, old history is labelled unavailable, partial conversion rolls back, and Down refuses a lossy downgrade.
- [x] **A10 — Retention:** Journal records cascade with parent history. Copied Function results remain available after independent invocation pruning; no old outputs are fabricated.
- [x] **A11 — Variables:** Initial evaluation is deterministic and admitted failure is visible; assignment batches see old values, apply atomically, survive selected-branch traversal, and stay isolated between concurrent Runs. No output appears for an unreached Step.
- [x] **A12 — State:** Scalar/object/null selection, freshness boundaries, missing Entity/State/pointer, future evidence, null opt-in, read failure, corrupt data and incomplete coverage are distinct. Only actual unusable evidence may become null. Later captures see new State while prior output remains unchanged.
- [x] **A13 — Effects and outputs:** A Function result feeds a variable, branch and later fixed-target Command/notification. Verification uses committed resolved inputs. Wrong invocation ownership, unknown outcome, malformed/over-limit output and output-commit failure stop downstream work without replay or falsifying the provider outcome.
- [x] **A14 — Control flow/recovery:** Lazy Expr paths, eager Condition groups and first-match Choose retain their specified semantics. Literal-bound Delay keeps existing snapshot-derived int64 duration, native elapsed waiting and interruption gates, without expression evaluation or duplicate duration journal rows. Shutdown/restart interrupts rather than resumes; data history triggers no expressions, State reads or effects.
- [x] **A15 — Public parity:** HTTP and MCP accept/return v3 bindings and all history variants, publish valid v3 schema refs, reject equivalent invalid inputs, enforce envelope limits, and keep route/tool IDs unchanged. Errors/logs do not leak values or raw expression source.
- [x] **A16 — Dashboard:** Tree/Condition rendering is exhaustive, static reference links include State captures, dynamic bindings are labelled, and the basic inspector shows ordered record metadata/failure labels with expandable complete exact JSON. Legacy availability is visible. Scalar/nested int64 evidence and enablement replacement remain exact without JavaScript reserialization; specialized per-variant layouts are not required.
- [x] **A17 — Household journeys:** Public Core journeys cover forecast filtering/mapping, State fallback, mutable target calculation, Function output branching, expression-based initialization, notification text and literal-bound Delay, plus a missing required output that fails without dispatch. Retained history explains every resolved value after State/catalog/history changes.
- [x] **A18 — Completion:** Current regression suite and full repository validation pass, generated/format/module changes are reviewed, and operator docs include numeric overflow, optional access, limits, sensitive history, cutover and backup rollback. Unrelated failures are reported as failures, not waived as success.

## Validation plan

Read `docs/testing.md` before adding tests. Pure expression semantics are unit tests; SQLite migration/atomicity/retention are integration tests with real SQLite. Protocol/app journeys use their existing integration/e2e fixtures. Do not move all behavior to a broad public-boundary test just because a feature crosses modules.

The following use existing mise tasks. Test names/selectors are to be added with the implementation; package-level commands do not depend on invented test names.

| Acceptance | Boundary / expected result | How to run | Prerequisites |
| --- | --- | --- | --- |
| A1–A4 | Pure compiler/bridge corpus, helper purity, fuzz seed corpus; all pass | `GO_PACKAGES='./internal/modules/automations/internal/exprvalue' mise run test` | Go toolchain; new package from D1 |
| A4 | Largest admitted nested predicates/transformations; record ns/op and B/op and inspect growth across sizes 1/16/128 | `GO_PACKAGES='./internal/modules/automations/internal/exprvalue' mise run test -- -run='^$' -bench=. -benchmem` | Add owner-local benchmarks; no external services |
| A5–A7, A11–A14 | Domain codec/value-flow units and narrow dependency-failure doubles; pass without SQLite/brokers | `GO_PACKAGES='./internal/modules/automations ./internal/modules/devices ./internal/modules/adapters' mise run test` | No external services |
| A8–A10 | Real SQLite transactions, all migration predecessors, failed commit/capacity/retention paths; no effects/partial rows | `GO_PACKAGES='./internal/modules/automations/sqlite' mise run test -- -tags=integration` | Local SQLite fixtures; no physical hardware |
| A11–A15 | Service and HTTP/MCP integration using existing real-boundary fixtures; parity and no-replay assertions pass | `GO_PACKAGES='./internal/modules/automations/...' mise run test -- -tags=integration` | Existing embedded/test services required by those packages |
| A16 | Frontend source-preservation and component tests plus typecheck/build pass | `mise run web-test` and `mise run web-build` | Node/pnpm; install task handled by mise |
| A17 | Assembled Core public journeys; inspect returned Run detail and fixed-target effects, not merely log lines | `GO_PACKAGES='./internal/app/hearthd' mise run test -- -tags=integration,e2e` | Existing simulated/test Adapter and NATS fixtures; no physical hardware |
| A18 | Full generation, formatting, tidying, lint, all-level tests, vet, frontend/build checks | `env -u GO_PACKAGES mise run validate` | All repo toolchains; reachable Docker daemon for real-Mosquitto tests/builds |

Optional focused local race check: append `-- -race` to the relevant `mise run test` invocation. CI enables race instrumentation. For live simulator follow-up, use `mise run simulator-start` and `mise run simulator-stop`, not a manually shared NATS daemon; simulator startup currently also requires the documented OpenAI key. Live simulator work is additional verification, not necessary to prove pure semantics or migration correctness.

**Spec-workspace baseline:** `env -u GO_PACKAGES mise run validate` passed on 2026-10-25 against this document-only workspace: generation/format/tidy, lint, vet, all-level Go tests, 91 frontend tests, and frontend build. It left no generated/format/module diff. This validates the current baseline, not any unimplemented acceptance criterion above. Vite reported its existing large-chunk advisory.

Before production implementation, establish a fresh focused baseline in the implementation workspace. The prototype's previous full-validation failure in `TestDelayWallClockJumpsPreserveNativeInterval/48h0m0s` was a timing result on a different branch; ten focused repeats passed. It does not replace the fresh passing spec-workspace result or establish a future implementation baseline.

## Risks and mitigations

| Risk | Likelihood | Impact | Mitigation / deciding check |
| --- | --- | --- | --- |
| Strict AST rewriting misses implicit predicate/dynamic lookups or breaks laziness | Medium | Wrong household branch | D1 gate with nested predicates, dynamic indexes, missing/null cases and rejection of deferred syntax/helpers; no native-nil fallback |
| Collection work or intermediate allocations exceed expected cost | Medium | Core responsiveness | Tight fixed AST/container/predicate limits, bounded expansion wrappers, Expr allocation guard and recorded boundary benchmarks; do not claim VM preemption |
| Integer wraparound or float mixing surprises authors | Medium | Incorrect calculated target | Explicit contract/examples; exact ingress and conversion tests; bound calculations; no claim that destination validation detects overflow |
| Migration rounds provider JSON or loses nested/child evidence | Medium | Irrecoverable history/definition corruption | Exact-token fixtures, all arms/child tables, transactional migration, backup/restore rollback |
| Resolved input/output commit is separated from effect/context gate | Medium | Unexplainable effects or false subsequent decisions | Extend existing reservation/completion transactions; inject commit failures at each boundary |
| Retained Function result is larger/unsupported despite provider success | Medium | Run fails after a real effect | Preserve successful provider evidence plus data failure; stop downstream work, never retry for output recovery |
| Values reveal household-sensitive information | High | Privacy exposure in existing history access | Existing authorization/retention, explicit operator documentation, no payloads/source in routine logs, credentials stay Adapter-owned |
| Expr upgrade changes arithmetic/AST/predicate semantics | Medium | Behavioral incompatibility | Pin dependency; run Hearth conformance corpus before upgrade; do not describe upstream defaults as the complete Hearth language |

## Trade-offs made

| Chose | Over | Because |
| --- | --- | --- |
| Expr plus a narrow Hearth semantic bridge | CEL or implementing a language | Settled engine choice; compact prototype; Go integration and required collection tools |
| Uniform v3 bindings and explicit migration | `_expr` siblings / ambiguous string interpolation | One authoring shape and unambiguous arbitrary JSON literals |
| Runtime checked AST lookups and explicit optional helpers | Eager missing-reference scan / native nil semantics | Correct laziness, dynamic indexes and per-element checks |
| Explicit State-capture Step | Implicit `state(entity)` helper | Visible static read set, testable failures, retained values and no hidden I/O |
| Mutable Run-local variables with simultaneous assignment | Shared variables / order-dependent map assignment | Useful household value flow without concurrency or ordering ambiguity |
| Append-only data records integrated with existing effect rows | A second generic workflow executor/state machine | Explainability while preserving existing ownership and no-replay semantics |
| Documented native int64 wraparound | Speculative checked-arithmetic rewrite of every operator | Makes behavior explicit without expanding the engine bridge into a new numeric language |
| Bounds and measurement | Claimed hard expression timeout | Honest embedded-VM guarantees while retaining requested predicates |
| Opaque exact legacy literal payloads | Rejecting/rounding vendor numbers during migration | No-loss compatibility; unsupported values still cannot enter Expr |
| Focused helpers without local `let` | Broad first-release language surface | Covers the representative flows with fewer custom wrappers and semantic combinations; assignments remain explicit Steps |
| Literal-bound Delay | Calculated waits in the first release | Preserves uniform authoring/migration while reusing existing duration execution/history |
| Basic ordered exact-JSON inspector | Dedicated layouts for every value variant | Retains complete exact evidence with less frontend machinery |
| SQL envelope plus variant-only JSON payload | Repeating identity/order/time in full-record JSON | One authoritative persisted envelope; public records and atomicity remain unchanged |

## Open questions and approval

No blocking product questions remain from discovery. The complete contract was explicitly approved for implementation on 2026-10-25; the limits, helper allowlist, explicit optional syntax, integer wraparound and compatibility exception are approved choices, not previously shipped guarantees.

### D1 gate evidence and accepted cost

On 2026-10-25, the strict bridge's conformance tests, focused lint and vet passed. Prepared-program benchmarks on Linux/amd64 (Ryzen 9 3900X, no race instrumentation) exercised sizes 1/16/128. At size 128, nested `all`, filter/map and nested map measured approximately 2.25 ms, 0.224 ms and 3.67 ms respectively (three repetitions).

A deliberately expensive admitted expression, `all(vars.items, {all(vars.items, {len([vars.payload]) == 1})})`, with 128 items and a valid 65,529-byte string measured 2.27–2.30 seconds/evaluation across three one-evaluation repetitions. A separate CPU profile attributed about 99% of sampled CPU to repeated encoded-string size accounting (`quotedSize`); profiling is not included in those timing results. This is component evidence, not an end-to-end responsiveness measurement or a bound on all admitted programs.

After being offered bounded accounting optimization, lower limits, or accepting this cost, the user explicitly chose **Accept measured cost**. Keep the simpler accounting and existing limits. Evaluation can occupy a Run worker for several seconds without preemption; do not advertise a hard deadline or describe allocation accounting as a complete CPU/memory sandbox. This acceptance clears the measured-cost review gate, not the remaining integration acceptance criteria.

Implementation must return for review if the strict-lookup gate cannot be met using the upstream visitor API, bounded helper intermediates cannot be enforced, or measured admitted collection cost requires a different execution boundary. Do not silently weaken strictness, remove requested predicates, or add a process/worker architecture.

## Success metrics

- The representative household journeys can be expressed without dynamic targets, hidden reads or provider-specific scripts in Core.
- Every new Run's reached dynamic effect input and successful reusable output is explained by retained immutable evidence; no reliance on current State or surviving invocation history.
- Missing required references cannot silently choose a branch, and database failures cannot silently become null State.
- Definition/history migration preserves exact values and existing interruption-without-replay behavior; the new reached-value limits are explicit cutover constraints rather than silent conversions.
- All acceptance criteria have owner-local deciding checks, and the approved implementation passes the full repository gate.

## Implementation evidence

All six deliverables are implemented. Acceptance evidence is grouped by its owning boundary:

| Acceptance | Deciding evidence |
| --- | --- |
| A1–A4 | `internal/exprvalue/{semantics,limits,evidence}_test.go`; compiler/decoder/evaluation corpus and 1/16/128-element benchmarks; measured-cost approval above |
| A5–A7, A11–A14 | `value_flow_test.go`, `value_effects_test.go`, `run_data_test.go` and retained branch/Delay/lifecycle regressions |
| A8–A10 | SQLite `run_data*_integration_test.go`, real deferred-FK commit failures and independent Adapter invocation pruning; platform DB expression migration fixtures |
| A15 | Both protocol packages' `value_contract_test.go`, canonical v3 schema and request-envelope checks |
| A16 | 99 frontend tests and live inspector/enablement journey with exact `9007199254740993` and `9007199254740994` values |
| A17 | `TestAutomationExpressionsPublicValueFlow/http` and `/mcp` through production Core, SDK Functions, fixed-target Commands and notifications |
| A18 | Full `env -u GO_PACKAGES mise run validate`; focused local race checks for public value flow and reviewed numeric/persistence regressions |

Independent reviews found and corrected malformed nested-arm migration acceptance, resolved-float evidence reclassification, empty State unusable reasons and inconsistent retained Function-failure validation. Numeric, State-reason and retained-failure regressions were observed failing before their fixes.

Local live evidence is retained, not committed, under `.data/verification/20261025T220743Z-expressions/`. Its HAR records exactly one browser manual-Run POST and one enablement PUT. The Function output and full-definition replacement preserve the exact integer above JavaScript's safe range. All owned browser/simulator processes were stopped. The native browser CLI was 0.39.2; a process-local guide-path override loaded the installed 0.39.0 guide after default lookup failed, with no tooling changes.

## Grounding references

- [Architecture](../docs/architecture.md), [testing boundaries](../docs/testing.md), and [coding standards](../CODING_STANDARDS.md).
- Existing [Step types](../internal/modules/automations/definition.go), [executor](../internal/modules/automations/branch_execution.go), [Function reconciliation](../internal/modules/automations/function_execution.go), and [repository](../internal/modules/automations/repository.go).
- Existing [coherent State snapshot](../internal/modules/devices/entity_state_snapshot.go), [definition-size migration](../internal/platform/db/migrations/00017_automation_definition_size.sql), and [source-preserving dashboard JSON](../web/src/api/automation-json.ts).
- Expr v1.17.8 source inspected during discovery: [visitor API](https://github.com/expr-lang/expr/blob/v1.17.8/ast/visitor.go), [compiler options](https://github.com/expr-lang/expr/blob/v1.17.8/expr.go), [numeric parser](https://github.com/expr-lang/expr/blob/v1.17.8/parser/parser.go), and [VM accounting](https://github.com/expr-lang/expr/blob/v1.17.8/vm/vm.go). These establish available integration mechanisms, not proof that the unimplemented Hearth bridge meets A1–A4.
- Isolated experiment commit `e1f4a3c694f502f17d71a3ccdcd3c62b6797e469`; examples and adapter-size findings are exploratory evidence only.
