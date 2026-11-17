# Automation definition diagnostics

Definition-save diagnostics describe a rejected request, not a failed Run. They
never include expression source, operand/argument values, or raw dependency text.
Existing HTTP statuses and top-level codes remain: invalid definitions use
400/`invalid_automation`, and invalid Function references use
422/`invalid_function_reference`. Both MCP tools retain `invalid_automation`.

## Diagnostic entries

HTTP problems may contain `diagnostics`. Modeled MCP failures may contain the
same-named member in `structuredContent`. Each entry has:

| Member | Meaning |
| --- | --- |
| `code` | Fixed classification such as `invalid_definition`, `unknown_kind`, `unknown_property`, `missing_property`, `invalid_condition`, `expression_type`, `expression_limit`, `expression_value_invalid`, or a Function reference reason code |
| `path` | JSON Pointer relative to the complete request body/argument object; the empty string identifies its root |
| `step_id` | Optional validated owning Step identity |
| `condition_id` | Optional validated failing Condition identity, not an ancestor's identity |
| `span` | Optional `{start,end}` range in the decoded expression string, not in its JSON encoding |
| `allowed` | Optional schema-defined values accepted at `path`: the `kind` values for a missing or unknown kind, or the property names of an object with an unknown property |

Diagnostic order is unspecified. The first entry does not indicate priority;
clients should use each entry's `path` and `code`.

Steps, Triggers, and Conditions are selected by `kind`. A value with a missing
or unsupported `kind` reports `missing_property` or `unknown_kind` at its `kind`
member with the kinds that union accepts. A value with a supported `kind` is
explained by that kind's schema alone, not by every alternative. For example,
`{"id":"route","kind":"branch",...}` as the first Step reports
`{"code":"unknown_kind","path":"/steps/0/kind","allowed":["command","if","choose",...]}`.

Span offsets are **zero-based UTF-8 byte offsets**, with an inclusive `start` and
exclusive `end`. They are not Unicode character offsets or JavaScript UTF-16
offsets. For example, the unexpected `)` in `'私' + )` occupies bytes `[8,9)`.
JSON `\u79c1` escaping does not change those decoded-string offsets. A located
empty range is distinct from an unlocated failure; the latter omits `span`.

`expression_type` includes native compile/type/syntax rejection; it is not a new
parser taxonomy. Unlocated failures do not acquire invented source ranges.
Unknown property names and invalid variable/Condition identifiers are not echoed.
Schema diagnostics stop at the safe containing structure when necessary: an
unknown property reports `unknown_property` at its containing object, with that
object's declared property names in `allowed`. `missing_property` paths name a
schema-required member, which is never caller input.

## Request envelopes

| Boundary | Example diagnostic path |
| --- | --- |
| HTTP create | `/conditions/children/0` or `/steps/0/conditions/expr` |
| HTTP replacement | `/definition/conditions/children/0` or `/definition/steps/0/conditions/expr` |
| MCP create and replacement | `/definition/conditions/children/0` or `/definition/steps/0/conditions/expr` |

An HTTP Function reference also retains its existing `errors` entry, with a safe
message and Huma-style `location`. A nested replacement Function might use
`body.definition.steps[0].then[0]`; it no longer incorrectly points at `body.steps`.
No source or input `value` is added to that entry.

MCP's Go `ToolError.Details` is **merged beside** `failure_code` and `message` in
`structuredContent`; there is no wire `details` wrapper. Existing flat `path`,
`step_id`, and `reason_code` fields remain. In particular, legacy `path` remains
definition-relative, while each new diagnostic path addresses the full argument
object. The MCP tools advertise the canonical definition schema. A pure typed
field decoder performs one canonical definition decode before shared validation
against that complete schema. Invalid definitions receive these diagnostics as
on HTTP; valid typed definitions pass directly to the handler, without a raw
request reread or discarded preparation result. Independent service validation
remains. Original envelope and canonical-document byte limits are preserved
before compaction or defaults could alter them. When multiple arguments are
invalid, no first-error order is promised. Shared structural rejections use
`invalid_arguments`; all modeled failures publish matching JSON text and
`structuredContent`.

For example, a modeled MCP syntax failure can contain:

```json
{
  "failure_code": "invalid_automation",
  "message": "invalid automation request",
  "path": "/steps/0/conditions/expr",
  "diagnostics": [{
    "code": "expression_type",
    "path": "/definition/steps/0/conditions/expr",
    "step_id": "branch",
    "condition_id": "syntax",
    "span": {"start": 8, "end": 9}
  }]
}
```

## Runtime evidence stays separate

These diagnostics do not change persisted schemas or retained history. Runtime
failures continue to use fixed codes and logical `field`/`condition_id` targets
within their existing Step/decision envelopes. No source path, byte span, replay,
retry, migration, or history rewrite is introduced.
