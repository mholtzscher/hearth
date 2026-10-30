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
| `code` | Fixed classification such as `invalid_definition`, `invalid_condition`, `expression_type`, `expression_limit`, `expression_value_invalid`, or a Function reference reason code |
| `path` | JSON Pointer relative to the complete request body/argument object; the empty string identifies its root |
| `step_id` | Optional validated owning Step identity |
| `condition_id` | Optional validated failing Condition identity, not an ancestor's identity |
| `span` | Optional `{start,end}` range in the decoded expression string, not in its JSON encoding |

Span offsets are **zero-based UTF-8 byte offsets**, with an inclusive `start` and
exclusive `end`. They are not Unicode character offsets or JavaScript UTF-16
offsets. For example, the unexpected `)` in `'私' + )` occupies bytes `[8,9)`.
JSON `\u79c1` escaping does not change those decoded-string offsets. A located
empty range is distinct from an unlocated failure; the latter omits `span`.

`expression_type` includes native compile/type/syntax rejection; it is not a new
parser taxonomy. Unlocated failures do not acquire invented source ranges.
Unknown property names and invalid variable/Condition identifiers are not echoed.
Schema diagnostics stop at the safe containing structure when necessary.

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
object. SDK schema rejection may occur before these modeled diagnostics and
remains an unstructured SDK-owned result.

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
