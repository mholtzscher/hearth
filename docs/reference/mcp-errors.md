# MCP result and failure contracts

The `/mcp` endpoint and built-in Agent share an MCP server catalog. The Agent's
[MCP bridge](../../internal/modules/agent/mcp_tools.go) excludes direct Automation
definition writes; its available tools are not identical to the external catalog.
Dashboard authoring uses the Agent's native `compose_view` tool, not external
MCP. External discovery and calls expose neither `compose_view` nor the removed
`present_view`.
MCP failure results are not HTTP Problem Details. A tool failure does not authorize
an action retry or prove that no effect occurred.

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

## Result contracts

Discovery advertises complete input schemas but no tool `outputSchema`,
resources, or resource templates. Core still validates successful results
against prepared contracts before returning the original encoded JSON.
Validation does not insert output defaults, round numbers, or fill missing
required results. An invalid success becomes `internal_error`.

Collections return compact summaries and omit `next_cursor` on their last page.
`get_entity` also returns `operations`, which maps each operation the Entity's
current support enables to the Entity type's JSON Schema for its
`execute_entity_command` parameters.
Detail tools expose retained evidence; actions return durable identities rather
than HTTP response bodies. Each protocol owns its models and mappings.

The current tool catalog and result contracts live in module registration and
operation files, rather than a separate handwritten inventory:

- [Devices](../../internal/modules/devices/mcpapi/register.go)
- [Adapters and Functions](../../internal/modules/adapters/mcpapi/register.go)
- [Automations](../../internal/modules/automations/mcpapi/register.go)
- [Notifications](../../internal/modules/notifications/mcpapi/register.go)

The native `compose_view` tool accepts up to 32 KiB of arguments. Invalid shape
returns `invalid_arguments` with one diagnostic per problem, up to eight, sorted
by argument path. An unknown property or card type also lists the keys or types
the schema allows at that path. Diagnostics never repeat submitted keys or
values. An invalid composition or edit returns `invalid_definition` for its
first failing source. `draft_required` means an edit needs a selected
draft, while `draft_selected` means a separate view needs a turn without one.
These native Agent observations are not external MCP results.

`execute_entity_command` checks parameters against that schema before creating
a Command. A schema rejection returns `invalid_request` with `message: invalid
command parameters` and `diagnostics` sorted by parameter path:
`missing_property`, `unknown_property` (with the schema's `allowed` parameter
names), `type_mismatch`, or `constraint_violation`. Diagnostics never repeat
submitted keys or values, and no Command record is created.

The unauthenticated endpoint shares HTTP's trusted-household boundary.
Adapter and Automation prose, descriptors, schemas, and results are untrusted
data, not system instructions or authority to act. IDs are not credentials.

## Input validation

All tools use one typed registration pipeline over the SDK's lower-level API.
Pure custom decoders run before validation against the full advertised schema.
`create_automation` and `replace_automation` decode definitions once, returning
[definition diagnostics](automation-definition-diagnostics.md) for invalid
documents. Neither handlers nor services run until decoding and schema validation
succeed. Shared diagnostics contain fixed codes and schema-known paths/names,
never parser excerpts, submitted values, or rejected property names. Ordinary
binding type errors reuse schema diagnostics to identify safe field paths, while
module-owned decoder errors keep their codes and details. HTTP, MCP,
and NATS retain their own mapping and recovery owners.
Shared diagnostics suppress expected caller cancellation; this does not add a
new cancellation envelope.

MCP uses the SDK's default HTTP request-body limit; excess requests receive
HTTP 413 before tool dispatch. The
[server boundary test](../../internal/platform/mcpapi/server_test.go) pins that
limit. There are no per-tool argument-envelope byte caps. Automation definitions
and Function arguments retain their domain-owned limits, defined by the
[Automation validator](../../internal/modules/automations/definition_validation.go)
and [Function JSON contract](../../contracts/functionschema/json.go).
The built-in Agent bypasses HTTP but uses the same domain validation. Outer JSON
whitespace does not count toward normalized domain values. REST body limits
remain protocol-local.

## Optional filters and decode-time codes

Omit an optional filter to request an unfiltered collection. Explicit null does
not mean omission and never reaches the service:

| Call | Result |
| --- | --- |
| `list_entities` with `{}` | Lists Entities without a Device filter |
| `list_entities` with `{"device_id":null}` | Modeled `invalid_device_id` rejection with message `device_id must not be null` |
| `list_commands` with `{}` | Lists Commands without an Entity filter |
| `list_commands` with `{"entity_id":null}` | Modeled `invalid_entity_id` rejection with message `entity_id must not be null` |
| `list_entities` with `{"label_ids":null}` | Modeled `invalid_label_id` rejection |
| `list_entities` with `{"types":null}` | Modeled `invalid_request` rejection |
| `update_entity` with `{"enabled":null}` | Modeled `invalid_request` rejection |
| `update_entity` or `update_device` without editable fields | Returns the current receipt without writing |
| `update_entity` or `update_device` with `"area_id":null` | Clears the Area; an Entity then inherits its Device's Area |
| `update_entity` or `update_device` with `"tag_ids":null` | Modeled `invalid_label_id` rejection; `[]` clears tags |

Devices typed argument decoding also uses `invalid_entity_id`,
`invalid_device_id`, `invalid_command_id`, `invalid_label_id`, and
`invalid_limit` for malformed identities or out-of-range page sizes. Label tools
report `invalid_label`, `label_not_found`, `label_name_conflict`, and
`label_limit_reached`; a malformed `list_entities` selector reports
`invalid_selector`. A metadata update naming an unknown Label reports
`label_not_found` before the general `invalid_request`. Shared structural rejections, including
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
| Label name conflict or catalog bound | 409 problem with `code: label_name_conflict` or `label_limit_reached` | `label_name_conflict` or `label_limit_reached` |
| Unknown Label in a metadata patch or selector | 400 native problem | `label_not_found` |

Function reads use `adapter_not_found`, `function_not_found`, and
`invocation_not_found` (HTTP 404). Invocation rejection uses `invalid_input`
(422), `catalog_changed` or `catalog_conflict` (409), and `adapter_unavailable`
or `admission_closed` (503). MCP represents these as modeled failures.
A terminal non-success invocation instead returns HTTP 200 with the retained
record; MCP returns `isError`, a `failure_code` equal to its status, and a compact
`invocation` receipt. Internal failures after admission may include the known
`invocation_id` in either protocol.

The Agent consumes modeled MCP failures as tool observations; transport/session and trace
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
modeled failure text. Success projections, domain numeric bounds, receipt
identities, and execution/retry policy are unchanged.

## Verification owners

- [Devices wire contracts](../../internal/modules/devices/mcpapi/result_contract_test.go) and [argument rejection](../../internal/modules/devices/mcpapi/argument_rejection_test.go) cover null rejection, omission, and exact result projections.
- [Devices official-client contracts](../../internal/modules/devices/mcpapi/tool_contract_integration_test.go) cover coded typed rejection, shared schema rejection, modeled failures, and retained Command identities.
- [Shared MCP contracts](../../internal/platform/mcpapi/tool_integration_test.go) cover structured modeled failures, generic unmodeled failures, and JSON-RPC errors.
- [Exact output contracts](../../internal/platform/mcpapi/exact_output_test.go) cover opaque numbers and text/structured agreement.
