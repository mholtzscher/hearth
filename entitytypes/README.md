# Entity-type contracts

State and support JSON Schemas define the built-in data shapes. The behavioral
manifest defines operations, bounds relative to support, and Command satisfaction.
State fields are derived from the schemas, not a manifest allowlist.

## Numeric State annotations

Numeric scalar schemas may carry one `hearth` object:

```json
{"type":"integer","hearth":{"unit":"1","percent_of":{"root":"support","path":"/state/maximum"}}}
```

`unit` is a nonempty fixed unit code or a closed support reference
`{"root":"support","path":"/state/unit"}`. Omission means dimensionless `1`.
Referenced units must be nonempty strings. An absent optional unit resolves to
`1`; an empty or wrongly typed value is invalid. Fixed codes are checked against
the presentation UCUM catalog; dynamic device codes may be opaque labels.

Optional `percent_of` requires effective fixed unit `1` and references a required
numeric support property with a strictly positive lower bound. It describes
presentation only: native State, parameter validation and exact Command
satisfaction do not change. Unit and scale references become immutable for an
Entity at first registration, including optional-unit presence.

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
`contracts/state-fields/v1/state-fields.json`, including numeric unit/scale
descriptors and deduplicated immutable interpretation references. Generated
lookups return safe copies. Generation checks missing, stale and orphaned owned
outputs. Fixed units belong only to State annotations, never duplicate support
properties. This coordinated contract cutover requires a fresh database; no
legacy support decoder or migration is provided.
