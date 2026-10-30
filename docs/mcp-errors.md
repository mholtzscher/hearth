# MCP failure contracts

The `/mcp` endpoint and built-in Agent use the same registered tools. MCP failure
results are not HTTP Problem Details. A tool failure does not authorize an action
retry or prove that no effect occurred.

## Recognize the boundary

| Boundary | Client result | Stable fields |
| --- | --- | --- |
| SDK input-schema rejection | `isError: true` with text and no structured failure | No Hearth `failure_code` guarantee; SDK validation text is not a stable machine-code contract |
| Module typed-argument rejection | `isError: true` with text and no structured failure | Where the module defines a decode-time code, it leads the text as `code: explanation` |
| Modeled handler `ToolError` | `isError: true`, text, and `structuredContent` | `failure_code`, `message`, and optional `details` |
| Modeled internal handler failure | The same modeled structure, with fixed safe text | `failure_code: internal_error`; retained private causes are diagnosed server-side, not included in the result |
| Unexpected unmodeled handler failure | `isError: true` with fixed text `internal error`, without structured failure fields | No `failure_code` guarantee; the server owns its safe diagnostic |
| JSON-RPC protocol failure | A protocol error rather than a modeled tool result | Handle it through the MCP client or JSON-RPC error channel |

Check the protocol error channel first. For a tool result, check `isError` before
reading a success projection. A modeled failure has structured fields; clients
must also support unstructured rejection and unexpected-failure results. Do not
assume every non-success contains `structuredContent.failure_code`, and do not
interpret SDK wording as a module-defined code.

The server does not move SDK validation into handlers to produce one universal
error envelope. HTTP, MCP, and NATS retain their own mapping and recovery owners.
Shared diagnostics suppress expected caller cancellation; this does not add a
new cancellation envelope.

## Optional filters and decode-time codes

Omit an optional filter to request an unfiltered collection. Explicit null does
not mean omission and never reaches the service:

| Call | Result |
| --- | --- |
| `list_entities` with `{}` | Lists Entities without a Device filter |
| `list_entities` with `{"device_id":null}` | Unstructured rejection text `invalid_device_id: device_id must not be null` |
| `list_commands` with `{}` | Lists Commands without an Entity filter |
| `list_commands` with `{"entity_id":null}` | Unstructured rejection text `invalid_entity_id: entity_id must not be null` |

Devices typed argument decoding also uses `invalid_entity_id`,
`invalid_device_id`, `invalid_command_id`, and `invalid_limit` for malformed
identities or out-of-range page sizes that reach that decoder. Missing required
fields, unknown fields, wrong scalar types, and non-nullable nulls can instead be
rejected by the SDK schema before typed decoding. Those SDK rejections have no
module-code guarantee.

## Intentional HTTP and MCP differences

Do not translate between these protocols by guessing from error text:

| Failure | HTTP | MCP |
| --- | --- | --- |
| Missing Entity, Device, or Command | 404 native problem with fixed detail | `entity_not_found`, `device_not_found`, or `command_not_found` |
| Missing Automation or history entry | 404 problem with `code: not_found` | `automation_not_found` or `automation_history_entry_not_found` |
| Invalid save-time Function reference | 422 problem with `code: invalid_function_reference` and structured diagnostics | `invalid_automation` with `details.step_id` and `details.reason_code` |
| Disabled-Entity Command | 409 problem with `code: entity_disabled` and `command_id` | `entity_disabled` with `details.command_id` |
| Function persistence failure with a retained Invocation identity | 500 problem with `code: internal_error` and optional `invocation_id` | `internal_error` with optional `details.invocation_id` |
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

## Verification owners

- [Devices wire contracts](../internal/modules/devices/mcpapi/result_contract_test.go) and [argument rejection](../internal/modules/devices/mcpapi/argument_rejection_test.go) cover null rejection, omission, and exact result projections.
- [Devices official-client contracts](../internal/modules/devices/mcpapi/tool_contract_integration_test.go) cover coded typed rejection, SDK rejection, modeled failures, and retained Command identities.
- [Shared MCP contracts](../internal/platform/mcpapi/tool_integration_test.go) cover structured modeled failures, generic unmodeled failures, and JSON-RPC errors.
- [Exact output contracts](../internal/platform/mcpapi/exact_output_test.go) cover opaque numbers and text/structured agreement.
