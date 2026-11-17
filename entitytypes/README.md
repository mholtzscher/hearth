# Entity-type contracts

State and support JSON Schemas define the built-in data shapes. The behavioral
manifest defines operations, bounds relative to support, and Command satisfaction.
State fields are derived from the schemas, not a manifest allowlist.

## Behavior manifests

Each type's `entitytype.json` manifest uses its State, support, and operation
parameter JSON Schemas plus `examples.json`. Concrete Entity-type and generated
SDK directories contain no handwritten Go. Run `mise run --skip-deps generate`
to regenerate bindings, codecs, behavior, typed SDK facades, conformance tests,
and the aggregate catalog. Run `mise run validate` and review the generated diff
before committing.

Each operation declares `parameters_schema`, a positive integer `deadline_ms`,
`outcome` (`observed` or `dispatched`), and `satisfied_when`. Observed
operations require a nonempty rule array; dispatched operations require `[]`
and generate no matcher. `stateless: true` omits Observation constructors.
`event_source: true` additionally requires `stateless: true` and no operations.

Rules are conjunctive and evaluated in declaration order. A rule has `op`,
`left`, optional `right`, and the operator-specific `tolerance` or `modulus`.
Paths are RFC 6901 JSON Pointers; the empty path selects the root. References
select required scalar fields except for optional-leaf operators and the
required string-array right operand of membership rules. Objects and optional
intermediate segments are rejected.

| Operator | Operands and meaning |
| --- | --- |
| `eq` | Same scalar kind; equality |
| `gte`, `lte` | Same numeric kind; ordered comparison |
| `multiple_of` | Integers; left is a multiple of right |
| `is_true` | Boolean left only |
| `near` | Integers and `tolerance >= 0`; `abs(left-right) <= tolerance` |
| `circular_near` | Integers, positive `modulus`, and `0 <= tolerance < modulus/2`; shortest circular distance |
| `in` | String left and unique string-array right, which may be empty; membership |
| `in_if_present` | Optional string leaf left and possibly empty unique string-array right; absent passes; item string-length bounds must match the left leaf |
| `eq_optional` | Both absent, or both present and equal |
| `gte_if_present`, `lte_if_present` | Optional numeric leaf left and required numeric right; absent passes |

Both distance operators require operand schemas with explicit nonnegative
integer minimum and maximum bounds. For `circular_near`, operand maxima must
also be below the modulus.

State validation can reference `state` and `support`; support validation only
`support`; parameter validation `parameters`, `support`, and `operation_support`.
`satisfied_when` can reference only `parameters` and `state`. Active Commands
therefore keep their satisfaction rules when re-registration narrows support.

State equality is generated schema-structural equality. `type: number` binds to
Go `float64`; integers through 2^53 are exact, overflow fails decoding, and
underflow may round to zero.

## Numeric State annotations

Numeric scalar schemas may carry one `hearth` object:

```json
{"type":"integer","minimum":0,"maximum":100,"hearth":{"unit":"%","quantity_kind":"brightness"}}
```

`unit` is a nonempty fixed unit code or a closed support reference
`{"root":"support","path":"/state/unit"}`. Omission means dimensionless `1`.
Referenced units must be nonempty strings. An absent optional unit resolves to
`1`; an empty or wrongly typed value is invalid. Dynamic device codes may be
opaque labels. Fixed kind/unit pairs are validated against the Go-owned
`quantitykind` vocabulary, not a UCUM runtime engine.

`range` declares a meaningful presentation extent in the field's canonical unit:

```json
{"type":"integer","hearth":{"unit":"%","range":{"minimum":0,"maximum":100}}}
```

Each bound is a number or a support reference, such as
`{"root":"support","path":"/state/maximum"}`. Referenced bounds must be
required numeric properties. Literal minimum must be below literal maximum;
support bounds are checked when a Range view is saved or presented. They are
presentation facts, not an additional State-admission constraint, and changes
to them are not frozen like unit interpretation. No range is inferred from
general JSON Schema validation limits.

Brightness State and set values are canonical integer percentages from 0 through
100. Support is `{"state":{},"operations":{"set":{}}}`; every integer request
in range is admissible. Adapters round to the nearest native target and report
the actual normalized observed State. Core accepts a linked observation within
one percentage point of the request, including at the endpoints.

Dynamic unit references become immutable for an Entity at first registration,
including optional-unit presence.

`quantity_kind` declares the quantity meaning as a fixed vocabulary name or a
support reference. `quantitykind` owns the accepted kind/unit pairs. Generic numeric
sensors and settings optionally declare `state.quantity_kind`; unknown generic
measurements may omit it. An absent kind may become known on re-registration,
but a known kind cannot subsequently be changed or omitted. This does not alter
the stricter existing unit-presence rule or migrate stored unit strings. A fixed
kind requires a fixed unit; references to a dynamic unit are not supported in
that combination. A support path cannot serve as both a kind and a unit.

`state-field-annotations.schema.json` defines the closed annotation shape. The
generator additionally checks numeric context, reference types and requiredness.
This is not a new general State schema dialect.

## Supported field derivation

The generator walks concrete scalar roots and named object `properties`, emitting
number/integer, boolean and string fields. Containers and arrays are not fields;
annotated array paths are rejected. Root uses pointer `""`. Named tokens match
`^[a-z][a-z0-9_]*$`, with a maximum pointer length of 64. Stateless types emit no
fields. String enums are copied when declared; support-defined choices are not
invented as static enums.

Presence follows required ancestors. Root is always present within an existing
State. Union properties, as in numericsetting, are declared once at the parent;
`oneOf` may restrict requiredness, exclude properties with `not.required`, and
restrict a string discriminator with `const`. Branch-local field definitions,
references and other semantic compositions are rejected rather than guessed.
Conditional fields are `sometimes`, separate from an Entity never having State.

The same model generates the Go lookup and
`contracts/state-fields/v1/state-fields.json`, including numeric unit
descriptors and deduplicated immutable interpretation references. Generated
lookups return safe copies. Generation checks missing, stale and orphaned owned
outputs. Fixed units belong only to State annotations, never duplicate support
properties. No legacy support decoder or migration is provided.
