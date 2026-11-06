# Entity-type contracts

State and support JSON Schemas define the built-in data shapes. The behavioral
manifest defines operations, bounds relative to support, and Command satisfaction.
State fields are derived from the schemas, not a manifest allowlist.

## Numeric State annotations

Numeric scalar schemas may carry one `hearth` object:

```json
{"type":"integer","minimum":0,"maximum":100,"hearth":{"unit":"%"}}
```

`unit` is a nonempty fixed unit code or a closed support reference
`{"root":"support","path":"/state/unit"}`. Omission means dimensionless `1`.
Referenced units must be nonempty strings. An absent optional unit resolves to
`1`; an empty or wrongly typed value is invalid. Fixed codes are checked against
the presentation UCUM catalog; dynamic device codes may be opaque labels.

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
