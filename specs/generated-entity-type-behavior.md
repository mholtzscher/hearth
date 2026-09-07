# Generate Complete Built-in Entity Types from Declarative Definitions

- **Status:** Implemented
- **Date:** 2026-08-22
- **Effort:** XL
- **Supersedes:** the unified-support decision to keep per-type deadlines, outcome matching, and cross-document validation handwritten

## Problem

The current generator derives structural Go bindings, codecs, and typed SDK facades from JSON Schemas plus `entitytype.json`, but every Entity type still requires handwritten Go for semantic validation and core catalog assembly. Adding a type therefore requires coordinated edits across the Entity-type package, SDK, and core.

For a solo-maintained project, the scalable authoring interface should be one declarative Entity-type definition bundle. Adding a built-in type should not require handwritten per-type Go or operation-name switches.

## Decision

Make Entity-type generation a deep build-time module. Its complete authoring interface is:

1. authoritative JSON Schemas for support, State, and operation parameters;
2. one versioned `entitytype.json` manifest containing the small behavior DSL;
3. one language-neutral `examples.json` conformance fixture.

A repository-level `go generate` pass will generate all per-type Go, typed SDK facades, semantic conformance tests, core catalog definitions, and the aggregate built-in registry.

The behavior DSL is a small, typed relation language compiled to direct Go. It is not interpreted at runtime. V1 holds the operators required by power/v1, brightness/v1, colortemp/v1, colorxy/v1, colorhs/v1, and colormode/v1. CEL, runtime type installation, and a general-purpose expression language remain out of scope.

## Goals

- Adding a built-in Entity type requires no handwritten per-type Go.
- JSON Schemas remain authoritative for each document's structure and local constraints.
- The manifest owns cross-document behavior, operation deadlines, and outcome matching.
- DSL references are checked against schema-derived types during generation.
- Core and SDK use the same generated semantic functions.
- Active Command outcomes cannot depend on mutable current support.
- Generated output remains deterministic, committed, and drift-checked.

## Non-goals

- Runtime installation or interpretation of Entity types.
- CEL or another runtime expression evaluator.
- Full JSON Schema draft 2020-12 to Go type coverage.
- User-defined functions, loops, conditionals, literals, or arbitrary arithmetic in DSL v1.
- Per-type Go escape hatches. A missing semantic capability is added to the shared generator only when required by a concrete Entity type.
- Generating transport, persistence, or Command orchestration code that is not type-specific.

## Authoring interface

Each built-in Entity type directory contains only declarative inputs and generated outputs:

```text
entitytypes/<package>/
├── entitytype.json
├── examples.json
├── state.schema.json
├── support.schema.json
├── <operation>-parameters.schema.json
├── zz_generated_types.go
├── zz_generated_codecs.go
├── zz_generated_behavior.go
└── zz_generated_conformance_test.go
```

There are no handwritten `.go` files in a concrete Entity-type package or its typed SDK package.

### Manifest v1

Brightness/v1 is the complete v1 example:

```json
{
  "manifest_version": 1,
  "type": "hearth.brightness/v1",
  "state_schema": "state.schema.json",
  "support_schema": "support.schema.json",
  "state_validation": [
    {
      "op": "lte",
      "left": {"root": "state", "path": ""},
      "right": {"root": "support", "path": "/state/maximum"}
    }
  ],
  "operations": {
    "set": {
      "parameters_schema": "set-parameters.schema.json",
      "deadline_ms": 10000,
      "outcome": "observed",
      "parameter_validation": [
        {
          "op": "lte",
          "left": {"root": "parameters", "path": "/value"},
          "right": {"root": "support", "path": "/state/maximum"}
        },
        {
          "op": "multiple_of",
          "left": {"root": "parameters", "path": "/value"},
          "right": {"root": "operation_support", "path": "/step"}
        }
      ],
      "satisfied_when": [
        {
          "op": "eq",
          "left": {"root": "parameters", "path": "/value"},
          "right": {"root": "state", "path": ""}
        }
      ]
    }
  },
  "examples": "examples.json"
}
```

Power/v1 uses empty State and parameter validation arrays and a one-element `eq` outcome list.

`deadline_ms` is a positive integer rather than a Go duration string so the manifest remains language-neutral.

### DSL types

The generator owns these input shapes:

```go
type manifest struct {
    ManifestVersion int                          `json:"manifest_version"`
    TypeID           string                       `json:"type"`
    StateSchema      string                       `json:"state_schema"`
    SupportSchema    string                       `json:"support_schema"`
    Stateless        bool                         `json:"stateless,omitempty"`
    StateValidation  []rule                       `json:"state_validation,omitempty"`
    SupportValidation []rule                      `json:"support_validation,omitempty"`
    Operations       map[string]operationManifest `json:"operations"`
    Examples         string                       `json:"examples"`
}

type operationManifest struct {
    ParametersSchema   string `json:"parameters_schema"`
    DeadlineMS         int64  `json:"deadline_ms"`
    Outcome            OutcomeKind `json:"outcome"`
    ParameterValidation []rule `json:"parameter_validation,omitempty"`
    SatisfiedWhen      []rule `json:"satisfied_when"`
}

type rule struct {
    Op        operator   `json:"op"`
    Left      reference  `json:"left"`
    Right     *reference `json:"right,omitempty"`
    Tolerance *int64     `json:"tolerance,omitempty"`
    Modulus   *int64     `json:"modulus,omitempty"`
}

type reference struct {
    Root referenceRoot `json:"root"`
    Path string        `json:"path"`
}
```

`operator` is exactly `eq`, `gte`, `lte`, `multiple_of`, `is_true`, `near`, `circular_near`, `in`, `in_if_present`, `eq_optional`, `gte_if_present`, or `lte_if_present`. `outcome` is required on every operation and is either `observed` or `dispatched`; `stateless` is an optional manifest flag defaulting to false. `satisfied_when` is a nonempty array of conjunctive rules for observed operations and an empty array for dispatched operations, which carry no outcome matcher. Manifest constants use optional pointers during parsing so absent and zero stay distinct.

`referenceRoot` is context-dependent:

| Context | Allowed roots |
| --- | --- |
| State validation | `state`, `support` |
| Support validation | `support` |
| Parameter validation | `parameters`, `support`, `operation_support` |
| Outcome matching | `parameters`, `state` |

Outcome matching deliberately cannot reference `support` or `operation_support`. This preserves active Command interpretation when re-registration changes current support.

`path` is an RFC 6901 JSON Pointer relative to its root. An empty path selects the root value. The `_if_present` and `eq_optional` operators may select optional-leaf fields with required intermediate segments; all other V1 rules may select only required scalar fields, and references to objects or arrays are rejected. Optional operation presence is handled by generated support selection before operation validation.

### Type checking

Generation resolves every reference against the loaded schemas and assigns it a schema-derived scalar kind.

- `eq` requires operands of the same scalar kind.
- `gte` and `lte` require operands of the same numeric kind and compile to `left >= right` and `left <= right`, respectively.
- `multiple_of` requires integer operands.
- `is_true` requires a Boolean `left` only and accepts no `right` or constants.
- `near` requires integer `left` and `right` plus an integer `tolerance >= 0`, and compiles to `abs(left-right) <= tolerance`.
- `circular_near` requires integer `left` and `right` plus a positive integer `modulus` with `0 <= tolerance < modulus/2`, and compiles to the shortest circular distance `<= tolerance`.
- `in` requires a string `left` and a nonempty unique string array `right`, and compiles to a membership loop.
- `in_if_present` requires an optional string leaf `left` and a possibly empty unique string array `right`; it passes when the leaf is absent and otherwise requires membership.
- `eq_optional` is true when both sides are absent, or both present and equal; false on one-sided absence or present-but-unequal.
- `gte_if_present`/`lte_if_present` require an optional numeric leaf `left` and a fully required numeric `right`; they pass vacuously when the leaf is absent.
- `support_validation` rules reference only the `support` root and compile to a generated `ValidateSupport` function invoked by `normalizeSupport` at registration and command resolution, so invalid support (for example decoded `minimum > maximum`) fails in core regardless of adapter.
- `type: number` state and support scalars bind to Go `float64`. Integers through 2^53 are exact; validation and equality operate on decoded binary64 values, so overflow fails codec decode while underflow may round to zero.
- Distance operators read only bounded nonnegative integer schemas, and circular operands stay below the modulus; the compiler carries these bounds into rule compilation. Generated distance code subtracts in an order that keeps operands nonnegative, so supported extremes cannot overflow.
- Unknown operators, per-operator unsupported fields, missing operands or constants, fractional constants, and empty lists fail generation.
- Unknown roots, invalid pointers, optional paths outside the `_if_present`/`eq_optional` operators, incompatible operands, and unsupported schema constructs fail generation with the manifest path, operation, rule index, and offending reference.
- Generated `multiple_of` code guards a zero divisor even when the support schema excludes zero.
- State equality defaults to schema-structural typed equality. The generator emits `==` for comparable State types and `reflect.DeepEqual` otherwise. No equality DSL override is added until a concrete type requires one.

Rules in each validation array are conjunctive and evaluated in declaration order. The first failed rule returns a deterministic generated error identifying both references and the relation. Error text is diagnostic, not a versioned wire contract. Satisfaction arrays render logical conjunction with unchanged public signatures (`func SetSatisfied(parameters SetParameters, state State) bool`); structural State equality remains exact.

### Conformance examples

`examples.json` contains named support scenarios:

```json
{
  "cases": [
    {
      "name": "maximum-80-step-5",
      "support": {
        "state": {"maximum": 80},
        "operations": {"set": {"step": 5}}
      },
      "states": [
        {"value": 75, "valid": true},
        {"value": 85, "valid": false}
      ],
      "operations": {
        "set": {
          "parameters": [
            {"value": {"value": 75}, "valid": true},
            {"value": {"value": 76}, "valid": false}
          ],
          "outcomes": [
            {"parameters": {"value": 75}, "state": 75, "satisfied": true},
            {"parameters": {"value": 75}, "state": 70, "satisfied": false}
          ]
        }
      }
    }
  ]
}
```

Every manifest requires at least one case and each case requires valid and invalid State examples. Every operation present in that case's support requires valid and invalid parameter examples plus satisfied and unsatisfied outcomes; unsupported optional operations are omitted from the case. Power/v1 and brightness/v1 migrate their handwritten per-type tests into these fixtures.

The generator emits `zz_generated_conformance_test.go`; `go test` executes the authoritative codecs and generated semantic functions against the examples. The generator does not implement a second DSL interpreter merely to evaluate fixtures.

## Generated behavior interface

Each Entity-type package exposes generated functions and constants:

```go
func ValidateState(Support, State) error
func EqualState(State, State) bool
func ValidateSupport(Support) error

const SetDeadline time.Duration
func ValidateSetParameters(Support, SetSupport, SetParameters) error
func SetSatisfied(SetParameters, State) bool
```

Names are derived from operation names. These functions are the single semantic implementation used by both the core catalog and typed SDK facade. Generated `ObservationInput` includes typed `Support`; Observation construction validates that support and applies `ValidateState` before encoding. Generated SDK conformance tests exercise valid and support-incompatible State examples.

`zz_generated_codecs.go` continues to own document-local JSON Schema validation and normalization. Support-dependent State and parameter rules run after both participating documents have passed their own codecs.

## Generated core catalog

Generation emits `internal/modules/devices/zz_generated_entitytypes.go`. It:

- defines stable `EntityTypeID` constants for all manifests;
- compiles each generated codec set;
- calls `DefineOperation` with generated support selection, validation, deadline, outcome kind, and outcome functions (nil matcher for dispatched operations);
- calls `DefineEntityType` with generated State validation, support validation, equality, and the stateless flag;
- constructs `NewBuiltinTypeCatalog` from every manifest in sorted type-ID order.

`internal/modules/devices/catalog.go` retains only the generic typed framework and erased `TypeCatalog` implementation. It does not import concrete Entity-type packages or contain per-type switches.

Adding or removing a manifest updates the aggregate registry without a handwritten core edit.

### Bulb attribute types

The registry additionally holds four generic Zigbee2MQTT bulb types. `hearth.numericsensor/v1` is a read-only number sensor with empty operations and `gte`/`lte` state validation plus `support_validation` rejecting `minimum > maximum`. `hearth.enumsetting/v1` is an observable setting whose dynamic choices validate state and parameters through `in` with `eq` satisfaction. `hearth.numericsetting/v1` is an observable bounded number or named choice discriminated by `mode`, using the present-guarded operators for validation and `eq` plus `eq_optional` for satisfaction. `hearth.enumaction/v1` is a stateless action with an empty-object state schema, manifest `"stateless": true`, a `trigger` operation with `"outcome": "dispatched"` and empty `satisfied_when`, and `in` parameter validation against dynamic values. A dispatched command completes with terminal status `dispatched` carrying no observation ID or value; it never satisfies via observation, and stateless types reject all observations with `invalid_value` while reads report `state: null`.

## Generation interface

Generation moves to one repository-level directive:

```go
//go:generate go run ../internal/cmd/entitytypegen -root ..
```

The supported command interface is:

```text
go generate ./entitytypes
go run ./internal/cmd/entitytypegen -root . -check
```

`go generate ./...` continues to work. Per-type `generate.go` files and the single-manifest generation mode are removed.

The root pass loads and validates all manifests before writing any output. It then renders per-type files plus aggregate outputs deterministically. Generated codec files embed the exact schema paths declared by each manifest, including nested paths and filenames outside the `*.schema.json` convention. In write mode it removes obsolete files only when they contain the generator ownership header. In check mode, missing, stale, or orphaned owned files fail the command.

## Generator module layout

The command remains one deep module with the CLI as its external interface. Files split by implementation responsibility without exposing new packages or interfaces:

```text
internal/cmd/entitytypegen/
├── main.go                         # modify — CLI, root orchestration, strict manifest/schema loading
├── behavior.go                     # new — DSL validation and typed rule model
├── examples.go                     # new — strict conformance-example loading
├── render.go                       # new — per-type render orchestration
├── render_types.go                 # move — bindings and codecs
├── render_behavior.go              # new — semantic functions and conformance tests
├── render_sdk.go                   # move — typed facade
├── render_sdk_conformance.go       # new — typed Observation conformance tests
├── render_catalog.go               # new — aggregate core definitions/registry
├── render_catalog_conformance.go   # new — aggregate core conformance tests
├── output.go                       # new — deterministic writes/check/orphan cleanup
└── *_test.go                       # modify/new — tests through root generation seam
```

The split creates implementation locality; callers still learn one command and one manifest contract.

## Repository changes

```text
entitytypes/
├── generate.go                                      # new — sole go:generate directive
├── entitytype-manifest.schema.json                  # new — language-neutral manifest v1 schema
├── powerv1/
│   ├── entitytype.json, examples.json               # modify/new — complete declarative definition
│   ├── behavior.go, doc.go, generate.go             # delete — no handwritten per-type Go
│   └── zz_generated_*.go                            # regenerate
└── brightnessv1/
    ├── entitytype.json, examples.json               # modify/new — complete declarative definition
    ├── behavior.go, doc.go, generate.go             # delete — no handwritten per-type Go
    └── zz_generated_*.go                            # regenerate

sdk/adapter/
├── powerv1/                                         # generated files only
└── brightnessv1/                                    # generated files only

internal/modules/devices/
├── catalog.go                                       # modify — generic framework only
├── zz_generated_entitytypes.go                     # new — all built-in definitions
├── zz_generated_entitytypes_test.go                # new — generated catalog conformance
└── catalog_test.go                                  # modify — generic catalog behavior only

docs/adr/
└── 0015-generate-entity-type-behavior.md            # new — DSL-over-CEL decision
```

`CONTEXT.md` does not change: this is an implementation/source-of-truth decision, not a domain terminology change.

## Implementation order

| Deliverable | Effort | Depends on |
| --- | --- | --- |
| D1. Manifest v1 schema, strict loader, DSL reference resolver, and type checker | L | — |
| D2. Direct-Go behavior and conformance-test rendering | L | D1 |
| D3. Root orchestration, generated catalog registry, and orphan detection | L | D1 |
| D4. Migrate power/v1 and brightness/v1; delete all handwritten per-type Go/tests | M | D2, D3 |
| D5. Reconcile SDK generation, generic tests, documentation, and ADR | M | D4 |

D2 and D3 may proceed in parallel after D1.

## Test strategy

### Generator tests

- Reject unknown manifest fields and unsupported manifest versions.
- Reject invalid roots, JSON Pointers, optional paths, and object/array operands.
- Reject mismatched `eq`, non-numeric `gte`/`lte`, and non-integer `multiple_of` operands.
- Reject `is_true` on non-Boolean fields, and distance operators on optional, negative-range, unbounded, or incompatible fields.
- Reject unknown operators, per-operator unsupported fields, missing operands or constants, fractional constants, empty satisfaction lists, and roots other than parameters and state in `satisfied_when`.
- Reject circular domains that cannot establish `0 <= tolerance < modulus/2` and operands at or above the modulus.
- Reject `support` references in `satisfied_when`.
- Reject zero/non-positive deadlines and incomplete conformance fixtures.
- Golden-test direct Go for no-op power behavior and maximum/step brightness behavior.
- Generate a temporary third Entity type and prove the aggregate registry includes it without generator source edits.
- Detect missing, stale, and orphaned generated files.
- Prove manifest and output ordering is deterministic.

### Generated conformance tests

- Compile all authoritative schemas.
- Normalize the case support.
- Validate State examples through schema and generated support rules.
- Validate parameter examples through schema and generated support rules.
- Evaluate outcome examples without current support.

### Repository gates

```text
go generate ./...
go run ./internal/cmd/entitytypegen -root . -check
go test ./...
go vet ./...
git diff --check
devenv test
```

## Acceptance criteria

- [x] No concrete Entity-type or typed-facade directory contains handwritten Go.
- [x] Power/v1 and brightness/v1 behavior remains equivalent to the current implementation.
- [x] The generated SDK and core call the same generated parameter validator.
- [x] Generated Observations require support and reject support-incompatible State before publication.
- [x] Generated codecs embed every exact manifest schema path, including nested paths.
- [x] Brightness State above `support.state.maximum` is rejected.
- [x] Brightness `set.value` above the maximum or misaligned to the step is rejected.
- [x] Power and brightness outcomes remain exact equality and do not read current support.
- [x] `satisfied_when` is a nonempty conjunctive rule array in every observed operation and an empty array in every dispatched operation; the old single-object shape does not remain.
- [x] `is_true`, `near`, and `circular_near` outcome operators compile to direct Go with inclusive, overflow-safe distance comparisons.
- [x] `NewBuiltinTypeCatalog` is generated from all manifests with no per-type core code.
- [x] A temporary third type generates bindings, behavior, SDK facade, tests, and catalog assembly without handwritten per-type Go.
- [x] Invalid DSL references and operand types fail during generation with actionable locations.
- [x] `go generate ./...` is deterministic and the check command detects stale/orphaned output.
- [x] All repository gates pass.

## Risks and mitigations

| Risk | Impact | Mitigation |
| --- | --- | --- |
| The DSL grows into a poor general-purpose language | High | Add operators only for concrete Entity types; reconsider CEL when conditionals, collections, or rapid operator growth appear. |
| Schema-to-Go and schema-to-DSL types diverge | High | Resolve both through one schema model and test through root generation, not separate implementations. |
| Generated code spans packages and leaves stale files | Medium | One root transaction, deterministic ownership headers, and orphan detection. |
| Generated diagnostics are difficult to understand | Medium | Include manifest path, operation, rule index, root, and pointer in every generation error. |
| Semantic examples miss important cases | Medium | Require positive and negative admission/outcome cases; retain shared generic integration tests. |
| A manifest edit changes historical meaning under the same type ID | High | Treat behavior as part of the versioned Entity-type contract; semantic changes require a new type version. |

## Reversal trigger

Reconsider CEL if a concrete Entity type requires conditionals, collection operations, custom functions, or enough new operators that the DSL is becoming a general-purpose expression language. Build-time-only generation and outcome immutability remain requirements in that reassessment.
