# MCP failure contracts

The `/mcp` endpoint and built-in Agent use the same registered tools. MCP failure
results are not HTTP Problem Details. A tool failure does not authorize an action
retry or prove that no effect occurred.

## Recognize the boundary

| Boundary | Client result | Stable fields |
| --- | --- | --- |
| Shared JSON, binding, or schema rejection | `isError: true`, matching JSON text and `structuredContent` | `failure_code: invalid_arguments`, fixed `message: invalid tool arguments`, and safe `diagnostics` |
| Module typed-argument rejection | The same modeled JSON structure | Existing module code, such as `invalid_entity_id` or `invalid_automation`, plus safe module details |
| Modeled handler `ToolError` | `isError: true`, matching JSON text and `structuredContent` | `failure_code`, `message`, and optional flat domain detail fields |
| Modeled internal handler failure | The same modeled structure, with fixed safe text | `failure_code: internal_error`; retained private causes are diagnosed server-side, not included in the result |
| Unexpected unmodeled handler failure | `isError: true` with fixed text `internal error`, without structured failure fields | No `failure_code` guarantee; the server owns its safe diagnostic |
| JSON-RPC protocol failure | A protocol error rather than a modeled tool result | Handle it through the MCP client or JSON-RPC error channel |

Check the protocol error channel first. For a tool result, check `isError` before
reading a success projection. A modeled failure has structured fields; clients
must also support unstructured unexpected-failure results. Do not
assume every non-success contains `structuredContent.failure_code`, and do not
interpret error prose as a module-defined code.

The Go `ToolError.Details` map is merged directly into `structuredContent` beside
`failure_code` and `message`. There is no wire `details` wrapper. For example, a
modeled disabled-Entity Command failure exposes `structuredContent.command_id`,
not `structuredContent.details.command_id`.

All tools use one typed registration pipeline over the SDK's lower-level API.
Pure custom decoders run before validation against the full advertised schema.
`create_automation` and `replace_automation` decode definitions once, returning
[definition diagnostics](automation-definition-diagnostics.md) for invalid
documents. Neither handlers nor services run until decoding and schema validation
succeed. Shared diagnostics contain fixed codes and schema-known paths/names,
never parser excerpts, submitted values, or rejected property names. HTTP, MCP,
and NATS retain their own mapping and recovery owners.
Shared diagnostics suppress expected caller cancellation; this does not add a
new cancellation envelope.

## Optional filters and decode-time codes

Omit an optional filter to request an unfiltered collection. Explicit null does
not mean omission and never reaches the service:

| Call | Result |
| --- | --- |
| `list_entities` with `{}` | Lists Entities without a Device filter |
| `list_entities` with `{"device_id":null}` | Modeled `invalid_device_id` rejection with message `device_id must not be null` |
| `list_commands` with `{}` | Lists Commands without an Entity filter |
| `list_commands` with `{"entity_id":null}` | Modeled `invalid_entity_id` rejection with message `entity_id must not be null` |

Devices typed argument decoding also uses `invalid_entity_id`,
`invalid_device_id`, `invalid_command_id`, and `invalid_limit` for malformed
identities or out-of-range page sizes. Shared structural rejections, including
missing required or unknown fields, use `invalid_arguments`. Custom scalar errors
can take precedence over structural failures; no first-error order is promised
when several arguments are invalid. Integer envelope scalars accept exact integral
spellings such as `1.0` and `1e0`, but never round near-integers into valid limits.

## Intentional HTTP and MCP differences

Do not translate between these protocols by guessing from error text:

| Failure | HTTP | MCP |
| --- | --- | --- |
| Missing Entity, Device, or Command | 404 native problem with fixed detail | `entity_not_found`, `device_not_found`, or `command_not_found` |
| Missing Automation or history entry | 404 problem with `code: not_found` | `automation_not_found` or `automation_history_entry_not_found` |
| Invalid save-time Function reference | 422 problem with `code: invalid_function_reference` and structured diagnostics | `invalid_automation` with flat `step_id` and `reason_code` |
| Disabled-Entity Command | 409 problem with `code: entity_disabled` and `command_id` | `entity_disabled` with flat `command_id` |
| Function persistence failure with a retained Invocation identity | 500 problem with `code: internal_error` and optional `invocation_id` | `internal_error` with optional flat `invocation_id` |
| Invalid Notification input | 422 native problem, without a required machine code | `invalid_input` |
| Closed Notification admission | 503 native problem | `admission_closed` |

Function domain codes otherwise retain their protocol-local mappings. The Agent
consumes modeled MCP failures as tool observations; transport/session and trace
persistence faults remain turn-fatal. This does not make HTTP and MCP envelopes
interchangeable.

Keep receipt identities and provider receipt text as strings. Successful tool
results preserve exact opaque JSON number tokens and matching text/structured
content. Error handling must not round-trip these values through binary floating
point, invent a receipt, or repeat an action to obtain a different outcome. Follow
the existing Command, Invocation, Notification, or Automation history identity.

Opaque raw inputs preserve numeric tokens, including domain-permitted exponents
outside float64's range. Successes and modeled failures preserve exact server
response JSON. The official Go SDK client still decodes structured responses
through floating point; this change does not fix extreme-number result decoding
in that client or the built-in Agent. Its HTTP request inspection can also reject
extreme exponents before sending a valid tool request; raw HTTP fixtures verify
the server boundary independently. The Agent preserves server text unchanged
when available and uses structured content only as a fallback.

These intentionally replace text-only input failures and `code: explanation`
modeled failure text. Tool names, success projections, domain numeric bounds,
receipt identities, and execution/retry policy are unchanged.

## Verification owners

- [Devices wire contracts](../internal/modules/devices/mcpapi/result_contract_test.go) and [argument rejection](../internal/modules/devices/mcpapi/argument_rejection_test.go) cover null rejection, omission, and exact result projections.
- [Devices official-client contracts](../internal/modules/devices/mcpapi/tool_contract_integration_test.go) cover coded typed rejection, shared schema rejection, modeled failures, and retained Command identities.
- [Shared MCP contracts](../internal/platform/mcpapi/tool_integration_test.go) cover structured modeled failures, generic unmodeled failures, and JSON-RPC errors.
- [Exact output contracts](../internal/platform/mcpapi/exact_output_test.go) cover opaque numbers and text/structured agreement.
