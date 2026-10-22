# Protocol-specific MCP results and contract preparation

**Status:** Implemented and verified on 2026-10-21, including required managed simulator acceptance A12 on `7ba99ca`. The user-approved environment-only credential and retained-database upgrade repairs are included. Delivery remains one unmerged PR; final-head CI is tracked separately from local acceptance.
**Created:** 2026-10-20
**Investigation baseline:** `f411d5d7294d39272b2df11c953f40c590767750`

**Subsequent scope refinement:** The user rejected production changes whose only
benefit is reusing MCP preparation across test-created servers. Production now
prepares contracts during each server's registration, with no process-wide MCP
contract/schema cache or `sync.Once` getter. Tests own any fixture reuse. This
supersedes the former caching lifetime and performance acceptance requirements;
the cached-revision measurements below are historical, not current performance
claims. Per-call validation and one exact encoding remain required.

The user also approved simplifying registration, duplicated schema shapes,
validation ownership, and input decoding. Registration accepts optional
`InputSchema` and `SuccessSchema` overrides and owns preparation privately.
Ordinary shapes derive from MCP payloads; only constraints that reflection
cannot express remain explicit. SDK input validation, independent domain entry
points, exact raw values, and all 33 advertised input documents remain unchanged.

The latest approved refinement uses Huma only for MCP schema authoring. MCP-owned
payload structs carry constraint tags, native schema hooks express additional
rules, and explicit unions use typed Huma schemas. The SDK still validates and
defaults inputs; Tekuri still validates encoded successes without rewriting
them. This supersedes the earlier prohibition on Huma schema dependencies, not
the independent HTTP/MCP payload and handler boundary. Google remains inside the
SDK and its validator tests, not a second production schema-authoring path.

Schema authoring prioritizes reading each contract in place over removing
repeated literals. Ordinary payloads use tags; small explicit fragments stay
inline. Named builders remain for substantial contracts and meaningful
composition, not scalar shorthand or boolean-selected contract shapes.

## Summary

Make HTTP and MCP sibling `httpapi` and `mcpapi` packages within each module,
over the existing services, with independently owned request and response
models. Redesign MCP results for agent discovery, detailed inspection, and
action receipts. Remove MCP resources and
advertised tool output schemas. Keep the current 33 tool names and input
contracts, domain behavior, and stable failure classifications. Prepare reusable
schemas and success validators during each server's registration, not during
each call or in a cross-server production cache.
Stop building the complete catalog in tests that do not use it.

This is an unshipped contract redesign with no external consumers to migrate.
It removes `hearth://` resources and `tools/list` output-schema metadata, and
changes MCP result JSON. Keep the existing implementation version `1.0.0`;
no version bump, compatibility flag, or legacy MCP implementation is needed.
HTTP/OpenAPI payloads, persistence, NATS, domain behavior, and the built-in
agent's available tool names do not change.

## Confirmed decisions

- Cover architecture and performance across Devices, Automations,
  Adapters including Functions, Notifications, and Core assembly.
- Allow MCP changes rather than require a permanent mirror of HTTP.
- Give HTTP and MCP their own payload types and mappings. The service consumes
  domain inputs and returns domain results, not either protocol's payloads.
- Split the mixed `api` packages in Devices, Automations, Adapters/Functions,
  and Notifications into sibling `httpapi` and `mcpapi` packages. Neither
  imports the other. Each owns its models, mappings, registration, errors,
  service-facing interfaces, and tests.
- Treat protocol naming symmetrically through package qualification. Each
  module/protocol package exposes one `Register` entry point for its complete
  surface; Adapters receives separate management and Function service
  dependencies. Do not repeat HTTP/MCP prefixes on model and helper names.
- Move the domain-neutral typed MCP framework from `internal/mcpapi` to
  `internal/platform/mcpapi`. Module tools, payloads, schemas, and domain error
  classification remain module-owned.
- Use one operation per file in both module transport packages. Keep its
  registration, wrappers, handler, and operation-only helpers together; group
  definitions reused by multiple operations into resource-named files.
- Redesign MCP result shapes, not just type ownership. Keep tool names,
  arguments, cursor semantics, failure codes, and exact-number guarantees.
  Mandatory HTTP/MCP response parity is removed.
- Publish input schemas but no tool output schemas. Retain server-side success
  validation, including explicit canonical contracts for recursive outputs.
- Remove all MCP resources and resource templates. Use the existing read tools
  for discovery and inspection. Do not add or regroup tools in this change.
- Keep the current MCP implementation version. Simulator-backed validation is
  a required acceptance criterion, not an optional walkthrough.

The explicit request to redesign MCP shapes supersedes the earlier draft's
result-payload-preservation choice. The concrete field sets below are the design
proposal for review, not additional decisions the user must restate.
The subsequently approved package split replaces the earlier same-package,
prefix-based naming proposal; it does not change the selected wire contracts.

These decisions supersede mixed-package adapter ownership, resource support,
HTTP-handler reuse, response-payload parity, and MCP output-schema publication
in [the original MCP spec](mcp.md),
[Automation variants](automation-variants.md), and
[Adapter Functions](adapter-functions.md). Canonical input validation, exact
JSON, domain safety, and HTTP schema requirements remain binding.

## Evidence and limits

The existing `TestMCPEndpointExposesTheFullCatalog` covers full HTTP assembly,
MCP connection, and listing. Five alternating fresh test-process runs per mode
with warm Go build caches passed on Linux/amd64, Go 1.27.1, with 24 available
CPUs. Normal execution had a median of 0.32 seconds and a range of 0.31 to 0.32.
Race-enabled execution had a median of 2.71 seconds and a range of 2.69 to 3.15.
These are whole-test timings, not isolated catalog construction or compilation.

A separate CPU and allocation profile over 20 repetitions attributed about 84%
of CPU beneath `newMCPServer` to Automation registration. Sampled cumulative
allocations beneath catalog construction were roughly 110 MiB per build. This
is allocation volume, not retained memory. Async garbage collection consumes
additional CPU outside the catalog call stack. Profiled timings are not used as
benchmark results. Local evidence is in `/tmp/opencode/hearth-mcp-catalog/` and
is not a checked-in dependency.

The baseline source explains the mechanism. Links are pinned because some files
will move or disappear during implementation:

- [`newMCPServer`](https://github.com/mholtzscher/hearth/blob/f411d5d7294d39272b2df11c953f40c590767750/internal/app/hearthd/http_handler.go) registers every
  module before the built-in agent and HTTP endpoint use the same server.
- [`automationOutputSchema`](https://github.com/mholtzscher/hearth/blob/f411d5d7294d39272b2df11c953f40c590767750/internal/modules/automations/api/output_schema.go)
  repeatedly bundles definition and history documents for individual tools.
- [`registerExactOutput`](https://github.com/mholtzscher/hearth/blob/f411d5d7294d39272b2df11c953f40c590767750/internal/mcpapi/exact_output.go) compiles the
  success contract and passes an advertised union to the SDK, which separately
  converts and resolves it.
- [`MCPTools`](https://github.com/mholtzscher/hearth/blob/f411d5d7294d39272b2df11c953f40c590767750/internal/modules/agent/mcp_tools.go) bridges input schemas and
  tool results. It does not use advertised output schemas or resources.
- [`HTTP-only tests`](https://github.com/mholtzscher/hearth/blob/f411d5d7294d39272b2df11c953f40c590767750/internal/app/hearthd/http_handler_test.go) construct the
  full catalog even when asserting only HTTP behavior.

This evidence does not establish a CI-wide speedup or predict the remaining
cost of SDK input-schema resolution. D1 refreshes the baseline before changing
behavior.

## Target architecture

```text
Domain module, for example internal/modules/devices
  Service.GetEntity(domain EntityID) -> domain EntityWithState or domain error

Sibling protocol packages
  devices/httpapi: HTTP request -> handler -> service -> HTTP mapping/encoding
  devices/mcpapi:  MCP arguments -> handler -> service -> MCP mapping/encoding

Each handler maps the domain result after the service returns.
The service never calls an adapter mapper or returns an API payload type.

Schema preparation, server registration lifetime
  input derivation/portability -> immutable input document
  success schema compilation -> immutable success validator

Per-server assembly
  bind isolated services -> register tools -> HTTP endpoint + agent bridge
```

Replace each mixed `api` package with sibling `httpapi` and `mcpapi` packages
directly under its owning module, alongside `nats` and `sqlite`. Do not retain
an `api` compatibility façade or add an empty parent package. This is a
transport ownership boundary, not a new application/service layer.

Core imports them with explicit module/protocol aliases, for example
`deviceshttp` for `internal/modules/devices/httpapi` and `devicesmcp` for
`internal/modules/devices/mcpapi`:

```go
// HTTP route assembly and MCP catalog assembly respectively:
deviceshttp.Register(v1, devicesService)
devicesmcp.Register(mcpServer, devicesService)
adaptershttp.Register(v1, adaptersService, functionsService)
adaptersmcp.Register(mcpServer, adaptersService, functionsService)
```

The current Adapters package has separate `Register` and `RegisterFunctions`
functions because management reads use `adapters.Service` while Functions use
`adapters.FunctionService`. That explains the old example, but does not require
two public registration names. In the new packages, `Register` installs both
sets using private helpers such as `registerFunctions`. Retain separate narrow
`Adapters` and `Functions` interfaces, service instances, admission gates, and
handler dependencies; do not merge their use cases or lifecycle policies.
Core calls Adapters registration once per protocol, and tests verify both
management and Function operations are included without a second call.

Both packages use ordinary local names such as `handler`, `functionHandler`,
`GetEntityInput`, and `Register`. Export registration and models/interfaces
genuinely consumed outside the package; keep handlers/mapping helpers private.
The protocol qualifier belongs to the package, not every Go identifier;
HTTP is not implicitly the default API. Migrate all old imports and registration
callers in the same module-sized unit without aliases back to the old package.

The domain-neutral typed tool façade belongs at `internal/platform/mcpapi`,
not beside product modules as an unclassified top-level internal package.
Move the existing `internal/mcpapi` implementation and its surviving tests there;
keep its Go package name `mcpapi`. It owns SDK registration, schema preparation,
result validation/encoding, generic ToolError handling, and server mechanics.
It imports no product modules and owns no tool catalog, module result model,
or domain failure classifier.

Inside a module's `mcpapi` package, import the platform package as `mcptools` to
distinguish its `Server`, typed registration functions, and schema overrides
from module registration. Core imports it as `mcpapi`, while module packages use
aliases such as `devicesmcp`. Migrate imports in all modules, the Agent bridge,
Core assembly, `internal/mcpecho`, and their tests in D4. Remove the old package
without compatibility re-exports. `internal/mcpecho` keeps its current location
and mounting API; this change only updates its framework dependency.

Each transport defines the narrow service interface it consumes, using domain
inputs/results. An interface may have the same name in both packages; do not
share it through HTTP, widen it to a union of both surfaces, or move it into the
domain solely to avoid duplication. The existing concrete service satisfies
both interfaces, and Core injects the same service instance into each.

The Agent module's MCP bridge is a client of the assembled catalog, not a
second product-facing MCP adapter. Keep its existing module layout; its flat
HTTP registration functions use the previously selected `RegisterHTTP` and
`RegisterHTTPStream` names because they lack a protocol-qualified package.
No generic application layer, service locator, or new DI framework is needed.

Production `httpapi` and module `mcpapi` packages must not import one another,
directly or through a helper that re-exports transport models. Domain services
must not import either transport. MCP handlers must not call Huma endpoint
methods, construct Huma request/output wrappers, or classify `huma.StatusError`
to reconstruct a domain outcome. MCP may use Huma's schema generator, constraint
tags, typed schema values, and native schema hooks on MCP-owned payloads. It must
not construct a Huma API or use Huma request decoding, response transformation,
or runtime validation for MCP calls. Canonical subdocuments remain imported from
their neutral owner rather than reflected or copied into another schema source.

### Operation-local file layout

Follow [the transport operation standard](../CODING_STANDARDS.md#keep-transport-operations-in-operation-local-files)
in every package created by this plan. The standard is normative; the change
map below lists representative operations, not every file in the final catalog.

- `register.go` is the explicit complete catalog. Its public `Register`
  constructs handlers and calls private registrars such as `registerGetEntity`.
  Any private registration groups only compose these calls; route/tool metadata
  and handler bodies stay in their operation files.
- `handler.go` owns private handler structs and consumer service interfaces.
  Adapter management and Function services remain separate dependencies there.
- Each HTTP operation ID or MCP tool name gets a `snake_case` file, such as
  `get_entity.go` or `execute_entity_command.go`. It contains that operation's
  registration/metadata, input/output wrappers, handler, and operation-specific
  decoding, mapping, and schema preparation/contract selection.
- Extract only actual multi-operation reuse into `<resource>_models.go`,
  `<resource>_mapping.go`, and `<resource>_contracts.go`. Shared protocol error
  translation belongs in `errors.go`; operation-only handling stays local.
  Other reused codecs/helpers get concrete responsibility names, not generic
  package-wide `inputs.go`/`models.go`/`handlers.go`/`contracts.go` catalogs.
- Operation tests normally use the operation's filename stem, with the existing
  test-level tags/suffixes. Cohesive registration, cross-operation, and lifecycle
  tests retain their behavioral names and boundaries; no artificial test quota.

For example, `devices/httpapi/get_entity.go` owns `registerGetEntity`,
`GetEntityInput`, `GetEntityOutput`, and `(*handler).getEntity`. The reused
`EntityBody` and `entityBody` mapping live in `entity_models.go` and
`entity_mapping.go`. MCP's `get_entity.go` independently selects its contracts
and implements its handler/result. Extracting shared schema builders into
resource-named files does not introduce cross-server caches or change the
registration-owned preparation lifetime.

This standard adds no per-operation subpackages, automatic registration, or
mandatory empty files. It does not require HTTP/MCP file parity, change wire or
schema names, or authorize unrelated repository-wide reorganization.

### Domain models and protocol payloads

Use specific terminology in code and documentation:

| Term | Owner and example |
| --- | --- |
| Domain input/result/read model | `devices.EntityID`, `devices.EntityWithState`, `automations.Run`. These cross service boundaries without Huma/MCP types. |
| HTTP request/response model | `deviceshttp.GetEntityInput`, `deviceshttp.EntityBody`, and `deviceshttp.GetEntityOutput`, defined in `devices/httpapi`. Preserve existing HTTP type names where possible; their wire shapes stay unchanged. |
| MCP arguments/result model | `devicesmcp.GetEntityInput`, `devicesmcp.EntitySummary`, `devicesmcp.EntityDetail`, and action receipts, defined independently in `devices/mcpapi`. |

Avoid the unqualified term DTO, which obscures which boundary owns a type.
MCP models must not alias, embed, or wrap HTTP response models. An independently
defined MCP type may have some identical fields, but its contract comes from
the MCP result catalog, not the corresponding HTTP response.

Protocol identity comes from the package for exported models and private
helpers alike. There is no blanket `HTTP...`/`MCP...` or `http...`/`mcp...`
rename. Keep domain types and genuinely neutral codecs unprefixed too. Moving
HTTP models to `httpapi` must not silently rename published OpenAPI components,
standalone `/schemas/...` URLs, `$schema`/Link headers, operation IDs, or canonical
schema URIs. Keep existing Go type names and schema publication where possible;
if resolving a move or name collision requires a rename, preserve the existing
wire name explicitly through Huma's registry and named publication, including
`publishOutputSchemas`. Capture these names in D1 and compare after D3; a new
package path is not permission to change the HTTP contract.

Share domain policies and canonical document codecs from their existing domain
owner. A package-private codec getter currently in `api`, such as Automation's
`definitionCodec`, must not become an exported HTTP API just so MCP can call it.
Each adapter can own a getter over the same domain `DefinitionCodec` and schema
authority without duplicating the canonical document. Fixed MCP contracts still
prepare once under the lifetime rules below.

For cursor code already needed by both transports, extract a small module-local
`internal/apicursor` package. It owns only pure scoped cursor encoding/decoding
over scalar/domain positions, imports no transport, and reuses the existing
`internal/platform/pagination` primitives. For example:

```go
// internal/modules/devices/internal/apicursor
// Proposed signatures; no HTTP or MCP request/response models cross here.
func DecodeEntities(cursor string, deviceID *devices.DeviceID) (*devices.EntityID, error)
func EncodeEntities(afterID devices.EntityID, deviceID *devices.DeviceID) (string, error)
```

Keep domain-aware cursor code inside its module rather than expanding platform
with product knowledge. Do not create helper packages without actual consumers;
Functions and Notifications can keep passing their existing domain-owned page
inputs/cursors through their services. Trivial scalar formatting and protocol
error mapping may be duplicated rather than creating a generic `api/shared`
package. No shared payload models, service wrappers, or common base handlers.

Both adapters establish their own input contracts before decoding a cursor.
HTTP retains Huma decoding/defaults. MCP retains its JSON input schema,
custom scalar decoders, and omission-versus-null behavior. Avoid reparsing
already validated MCP IDs through an HTTP request struct. Preserve cursor scope,
parent-existence checks, ordering, default page size 50, and maximum 200.

Each protocol builds its domain query and maps the resulting page to its own
payload. Neutral helpers return domain or plain input errors, never Huma
problems, HTTP status codes, or MCP results.
Do not extract a shared service-and-response wrapper that recreates the removed
HTTP dependency under a new name.

### Example handler change

The proposed Devices change calls the service and maps domain errors directly:

```diff
-func (handler *Handler) getEntity(ctx context.Context, input mcpGetEntityInput) (EntityBody, error) {
+func (h *handler) getEntity(ctx context.Context, input GetEntityInput) (EntityDetail, error) {
-    output, err := mcpRead(ctx, handler.GetEntity,
-        &GetEntityInput{EntityID: string(input.EntityID)}, mcpFailureEntityNotFound)
+    view, err := h.devices.GetEntity(ctx, devices.EntityID(input.EntityID))
+    // Map ErrEntityNotFound directly to entity_not_found.
+    // Keep unknown failures generic and retain their cause for safe diagnostics.
     if err != nil {
-        return EntityBody{}, err
+        return EntityDetail{}, entityFailure(err)
     }
-    return output.Body, nil
+    result, err := entityDetailFromDomain(view)
+    if err != nil {
+        return EntityDetail{}, mcptools.InternalToolError(err)
+    }
+    return result, nil
 }
```

The added lines live in `devices/mcpapi`, which imports the shared tool façade
as `mcptools`. Its operation-only detail mapper stays in
`devices/mcpapi/get_entity.go`. HTTP's reused `entityBody` mapper lives in
`devices/httpapi/entity_mapping.go` and produces `EntityBody` with the existing
HTTP JSON shape. Neither mapper belongs inside `devices.Service`.
`entityFailure` is a proposed MCP-local failure mapper, not a new domain API;
an inline switch is acceptable.

### Error ownership

| Owner | Required behavior |
| --- | --- |
| Devices | Classify domain sentinels directly. Preserve resource-specific not-found codes, invalid-request behavior, Command ID/status details, and the existing command failure classifier. Remove `mcpRead`, HTTP-status reconstruction, and cause-recovery helpers when their callers are gone. |
| Automations | Map domain sentinels and `ConditionsBlockedError` directly in each transport. Preserve revision conflict, busy/admission/snapshot failures, and committed Skip history coordinates. Do not share `automationProblemError` or export HTTP error internals for MCP. |
| Adapters | Map management errors without Huma problems. Keep Function codes and invocation identity details. Split the existing HTTP-dependent `functionFailure` handling into transport-local classifiers over domain errors; MCP never branches on HTTP status. Duplicated small switches are preferable to a shared transport-error package. |
| Notifications | Map `ErrInvalidInput`, missing destination/attempt, and closed admission directly to the current MCP codes and messages. Create one Attempt ID per send and call `SendPublic` once. Preserve independent public admission and no retry. |
| Shared `internal/platform/mcpapi` façade | Preserve ToolError text/structured fields, exact failure text for Function invocation, generic internal errors, and safe diagnostics. SDK argument rejection remains an `isError` result without structured domain content. |

No retry, replay, cancellation, deadline, admission, or durable side-effect rule
changes. Existing HTTP error responses retain their own status, headers, and
safe messages. Removing an HTTP wrapper must not leak a raw service error.

## Lean MCP discovery and results

### Discovery

- Continue serving stateless Streamable HTTP at `/mcp` on the existing listener.
- Keep MCP implementation version `1.0.0`. There are no consumers requiring a
  versioned migration. MCP protocol negotiation and HTTP `/v1` also stay unchanged.
- Keep the fixed 33-tool set from `mcpCatalogToolNames` in Core's catalog test.
- Keep portable, complete input schemas. In particular, Automation create and
  replace still publish strict recursive definition schemas and validate them
  before service invocation.
- Every tool omits the SDK `outputSchema` field. Do not replace it with `{}` or
  an unconstrained schema.
- Advertise no resource capability and register no resources or templates.
  The pinned SDK's resource-list methods may return empty collections; old
  resource reads must not resolve. Do not add a compatibility resource handler.
- Keep one Core catalog for the external endpoint and built-in agent. Neither
  the agent bridge nor startup builds a separate catalog.

Adapter Function descriptor fields named `input_schema` and `output_schema`
remain in tool results. Those describe Adapter Functions as data. They are not
MCP tool output-schema metadata and must not be removed. Function domain/SDK/NATS
schema validation is out of scope and remains unchanged.

### MCP result design

The proposed result contracts follow three rules:

- Collections return summaries sufficient to choose a target. They do not return
  complete Automation definitions, Function schemas, or invocation arguments.
- Detail tools return the evidence or canonical document needed to inspect that
  target. Histories with no separate entry-detail tool retain their diagnostic
  fields in the page rather than make those facts inaccessible.
- Actions return durable identity and the actual outcome supplied by the service.
  Do not perform an extra read, invent success, shorten an execution wait, or
  repeat an operation merely to construct a compact result.

Every collection uses `{items: [...], next_cursor?: string}`. Empty items are
`[]`; a terminal page omits `next_cursor`. Device detail retains its independent
`next_entity_cursor`. Ordering, filters, cursor scope, and limits do not change.
Times are UTC RFC3339Nano strings. In the tables, `?` means omit when absent and
`| null` means the field is always present but nullable. Other fields are required.
Result envelopes and fixed records are closed to undeclared properties. Opaque
JSON values and canonical nested documents retain their own schema semantics.

#### Result projections

These names describe MCP models only. Define them independently in each owning
module's `mcpapi` package: operation-only models stay in the operation file;
models used by multiple operations live in `<resource>_models.go`. No type
aliases to HTTP models are allowed.

| Projection | Exact fields and semantics |
| --- | --- |
| Entity summary | `entity_id`, `device_id`, `name`, `type`, `enabled`, `availability: {status, reason_code?}`, `state: {value, observed_at} \| null`. Null state means never observed, not unavailable. State values remain complete and exact. |
| Entity detail | Entity summary plus `adapter_id`, `support`. Its state additionally has `observation_id`, `adapter_received_at`, `source_updated_at?`; availability additionally has `source`, `since`, `evidence_at`, `source_observed_at?`. Support remains the full canonical capability document, not guessed operation metadata. |
| Device summary | `device_id`, `name`, `kind`. Name is the effective domain name, not a second HTTP name-override model. |
| Device detail | Device summary plus `entities: Entity summary[]`, `next_entity_cursor?`. |
| Command summary | `command_id`, `entity_id`, `operation`, `status`, `requested_at`, `completed_at?`, `failure_code?`. Values come from the retained Command record. |
| Command detail | Command summary plus `parameters`, `deadline_at`, `accepted_at?`, `outcome_observation_id?`. Parameters remain exact JSON. |
| Health transition | `status`, `source`, `observed_at`, `source_observed_at?`, `reason_code?`. Used by Entity availability and Adapter health histories. Each module owns its type and mapper. |
| State history entry | `observation_id`, `disposition`, `observed_at`, `adapter_received_at`, `source_updated_at?`, `value?`, `rejection_code?`. Rejected entries omit value; accepted JSON null is a present null value. |
| Entity Event history entry | `event_id`, `entity_id`, `name`, `disposition`, `emitted_at`, `received_at`, `recorded_at`, `rejection_code?`. No raw envelope, receive-order key, or private correlation/runtime data. |
| Adapter summary | `adapter_id`, `health: {status, reason_code?}`. Health is a service result, not a provider probe. |
| Adapter detail | Adapter summary with health `source`, `since`, `evidence_at`, `source_observed_at?`, and `runtime?` inside `health`. Runtime has `status`, `software_name`, `software_version`, `last_heartbeat_at: string \| null`, `lease_expires_at`. Fencing identity and claim timestamp remain available through HTTP rather than this MCP projection. |
| Function summary | `adapter_id`, `function_key`, `catalog_revision`, `description`, `callable`, `timeout_ms`. No input/output schemas. Description is untrusted data. |
| Function detail | Function summary plus `input_schema`, `output_schema`, retaining the exact Adapter descriptor documents as data. |
| Invocation summary | `invocation_id`, `adapter_id`, `function_key`, `catalog_revision`, `status`, `started_at`, `completed_at: string \| null`, `reason_code?`. No arguments or result. |
| Invocation detail | Invocation summary plus `arguments`, `deadline`, `result?`. Preserve all terminal alternatives, exact opaque numbers, and the domain rule that only success carries result. |
| Automation summary | `automation_id`, `name`, `enabled`, `revision`. No definition tree, synthetic execution counts, or extra service reads. |
| Automation detail | `automation_id`, `revision`, `created_at`, `updated_at`, `definition`. Definition is the complete canonical v2 document, including name and enablement. |
| Automation history summary | `entry_id`, `automation_id`, `kind`, `revision`, `recorded_at`, `source`, plus `status` for a Run or `reason` for a Skip. Source comes from the admission cause; detailed cause and Condition evidence stay in detail reads. |
| Notification destination | `destination_id`, `name`, `available`. Never expose the private Adapter route. |
| Notification summary | `notification_id`, `destination_id`, `destination_name`, `title`, `status`, `started_at`, `completed_at: string \| null`, `reason_code?`. No message echo or provider receipt. |
| Notification detail | Notification summary plus `message`, `deadline`, `receipt?`. Accepted means provider acceptance, not independently verified delivery. |

Automation history detail is a direct discriminated result, not an HTTP
`{kind, run}` or `{kind, skip}` wrapper. Both variants have `entry_id`,
`automation_id`, `automation_name`, `kind`, `revision`, `recorded_at`, `cause`,
and `condition_decision`:

- Run detail also has `status`, `started_at`, `completed_at?`, `failure_code?`,
  `snapshot`, `matched_trigger_ids`, `steps`, `branch_decisions`, `delays`, and
  `notifications`. Running omits completion/failure; success has completion but
  no failure; failed/interrupted has both. Arrays are always arrays, including
  not-yet-reached or empty evidence.
- Skip detail also has `reason`, `skipped_at`, and `matched_triggers`. It must
  not grow fake Run status or completion fields.

The nested definition, provenance, Condition, Step, branch, delay, and
notification evidence preserve the existing v2 document meanings and field
shapes. Map them directly from domain variants into MCP-owned types. Reuse
existing canonical document encoders where available, such as
`automations.EncodeDefinition`, `EncodeConditionDecision`, and
`EncodeBranchDecision`. Do not route through HTTP `*Body` models or add domain
JSON wrappers solely to share transport code. Explicit MCP validation schemas
reuse the canonical subdocument constraints, not the complete HTTP response
schema or its old response envelope.

#### Tool-to-result catalog

`Page<X>` below means the common collection envelope containing projection X.
This table covers all 33 current tools; no tool is renamed or added.

| Tool | Proposed successful MCP result |
| --- | --- |
| `list_entities` | `Page<Entity summary>` |
| `get_entity` | Entity detail |
| `update_entity` | `{entity_id, enabled}` from the service's returned Entity |
| `execute_entity_command` | `{command_id, status, observation_id?, value?}`. Satisfied requires observation/value, including a present null value; dispatched omits both. Keep the existing blocking-to-terminal behavior. |
| `list_entity_commands` | `Page<Command summary>` |
| `list_devices` | `Page<Device summary>` |
| `get_device` | Device detail |
| `get_command` | Command detail |
| `list_commands` | `Page<Command summary>` |
| `list_entity_availability_history` | `Page<Health transition>` |
| `list_entity_state_history` | `Page<State history entry>` |
| `list_entity_events` | `Page<Entity Event history entry>` |
| `list_adapters` | `Page<Adapter summary>` |
| `get_adapter` | Adapter detail |
| `list_adapter_health_history` | `Page<Health transition>` |
| `list_adapter_functions` | `Page<Function summary>`. Describe the selected Function before invocation. |
| `get_adapter_function` | Function detail |
| `invoke_adapter_function` | `{invocation_id, adapter_id, function_key, catalog_revision, status: "succeeded", result}`. Preserve non-success `isError` behavior; do not present failed/unknown execution as success. |
| `list_adapter_function_invocations` | `Page<Invocation summary>` |
| `get_adapter_function_invocation` | Invocation detail |
| `create_automation` | `{automation_id, revision}` from the committed Record |
| `list_automations` | `Page<Automation summary>` |
| `get_automation` | Automation detail |
| `replace_automation` | `{automation_id, revision}` from the committed Record |
| `delete_automation` | `{automation_id, deleted_revision, deleted: true}` only after service success |
| `start_automation_run` | `{automation_id, run_id, status, completed_at?, failure_code?}` mapped from the returned Run state. Admission is not completion. Read history with `entry_id = run_id` for evidence. |
| `list_automation_history` | `Page<Automation history summary>` |
| `get_automation_history_entry` | Automation history detail |
| `list_notification_destinations` | `Page<Notification destination>` |
| `get_notification_destination` | Notification destination |
| `send_notification` | `{notification_id, destination_id, status, reason_code?, receipt?}` from the single durable Attempt. Do not label rejected/not_sent/unknown attempts delivered or retry them. |
| `list_notifications` | `Page<Notification summary>` |
| `get_notification` | Notification detail |

Keep existing top-level failure codes, safe messages, Command ID/status details,
and Automation Skip history references. Function non-success details keep the
`invocation` key but use a MCP-owned compact receipt with `invocation_id`,
`adapter_id`, `function_key`, `catalog_revision`, `status`, and `reason_code?`.
This nested result shape is an intentional client change; the code, `isError`,
identity, and exact-number guarantees remain. Function persistence failures
still retain `invocation_id` where the current error contract supplies it.

#### Representative Go shapes

These are proposed definitions, not implementation. Other models follow the
field sets above. The `mcpapi` package supplies the protocol qualifier; HTTP
models move to `httpapi` with their existing names and JSON contracts.

```go
// internal/modules/devices/mcpapi/entity_models.go
type AvailabilitySummary struct {
    Status     string `json:"status"`
    ReasonCode string `json:"reason_code,omitempty"`
}

type StateSummary struct {
    Value      json.RawMessage `json:"value"`
    ObservedAt time.Time       `json:"observed_at"`
}

type EntitySummary struct {
    EntityID     string              `json:"entity_id"`
    DeviceID     string              `json:"device_id"`
    Name         string              `json:"name"`
    Type         string              `json:"type"`
    Enabled      bool                `json:"enabled"`
    Availability AvailabilitySummary `json:"availability"`
    State        *StateSummary       `json:"state"`
}

// internal/modules/automations/mcpapi/automation_models.go
type AutomationReceipt struct {
    AutomationID string `json:"automation_id"`
    Revision     int64  `json:"revision"`
}

// internal/modules/adapters/mcpapi/function_models.go
type FunctionSummary struct {
    AdapterID       string `json:"adapter_id"`
    FunctionKey     string `json:"function_key"`
    CatalogRevision string `json:"catalog_revision"`
    Description     string `json:"description"`
    Callable        bool   `json:"callable"`
    TimeoutMS       int    `json:"timeout_ms"`
}
```

Models containing opaque `json.RawMessage` or custom JSON variants require
explicit success schemas; do not infer byte-array schemas for their JSON values.
These schemas belong beside their operation or, when reused, in module
`mcpapi/<resource>_contracts.go` files. They reference canonical subdocuments
where appropriate and remain server-side only. Pure scalar result models can
use success contracts derived during registration.

### Shared typed tool façade (`internal/platform/mcpapi`)

Keep one typed registration API with optional input and success-schema overrides.
Registration owns derivation, portability, schema copies, and validator compilation;
callers do not prepare or wrap contracts. These are internal Go API changes, not
public Adapter SDK changes. `Register`
in this section is the shared typed tool registration function, not a module's
catalog-registration entry point.

```go
type Tool[I, O any] struct {
    Name string
    Description string
    InputSchema any   // Nil derives the ordinary argument shape.
    SuccessSchema any // Nil derives the ordinary result shape; never advertised.
    ExactFailureText bool
    Handler Handler[I, O]
}
```

Apply the same field changes to `ToolWithRequest[I, O]`. Registration signatures
remain `Register[I, O](server, tool)` and `RegisterWithRequest[I, O](server, tool)`.

There are no caller-facing `InputContract`, `OutputContract`, `PrepareInput`,
`PrepareOutput`, or module `mustContract` helpers. Later mutation of a caller's
schema maps or byte slices must not change a registered server's validation.
Preparation remains private to registration:

| Operation | Work and failure boundary |
| --- | --- |
| Omitted input override | Derive the existing object input schema, rewrite it for client portability, and retain owned JSON bytes. Preserve pointer-element and untyped input-object handling. |
| Explicit input override | Normalize and own the supplied schema. Do not compile a second input validator; the SDK owns input references, validation, and defaults. |
| Omitted success override | Derive and compile the result schema. Preserve pointer-element handling and validate actual marshaled output. Untyped `any` requires an explicit success schema. |
| Explicit success override | Compile the supplied schema, including opaque JSON and recursive contracts. Enforce an object result root. |
| Any setup failure | Registration fails startup through the existing panic convention. No uninitialized prepared-wrapper state or disabled-validation fallback exists. |

Every registered output has a non-nil success validator. Test-only untyped
outputs must provide an explicit schema or a concrete output type. Current Core
tools already use concrete output types, so this tightens only the internal
registration API.

The output compiler resolves local `$id`, recursive `$ref`, and relative
references to embedded resources without changing their meaning. Fixed
contracts are self-contained. Reject missing external resources rather than
loading files or network schemas. SDK input registration also rejects
unresolved references. Each failed preparation must reject registration rather
than produce an unvalidated tool.

`Register` supplies SDK `AddTool[I, any]` with the prepared input document and
nil `OutputSchema`. The wrapper invokes the typed `Handler[I, O]`, encodes its
result once, validates a decoded copy, and explicitly sets `StructuredContent`
to the original `json.RawMessage` plus the JSON text fallback. It returns nil
as the SDK's typed output value. This prevents SDK output-schema inference,
resolution, default application, and numeric re-encoding.

Use this one exact-output path for both registration functions and all tools.
Remove `ExactOutput`; retain `ExactFailureText` because it controls a distinct
failure contract. Success validation does not inject output defaults. Current
MCP result models supply their own fields. Independent MCP wire fixtures must
verify the declared projections and canonical subdocument meaning, not equality
with HTTP responses. Input schema defaults still run in the SDK before decoding.

Pin these result rules through both registration functions:

| Case | Required result |
| --- | --- |
| Valid success | Validate a decoded copy, then emit the original encoded JSON as structured content and text. Preserve nested numbers, null members, and opaque Function data. |
| Typed nil pointer/map when the selected success schema requires an object | Reject JSON null as an internal failure. Do not replace it with a zero struct or `{}`. Nullable members inside a valid object remain valid. |
| Missing required output property that has a schema default | Reject the success. Output validation does not insert defaults. The handler supplies required output fields. |
| Custom marshaler emits an invalid value | Reject the encoded value, even if the Go type appears valid. |
| Failure-shaped JSON returned as success | Validate against the success contract only. There is no success/failure union that can admit it accidentally. |
| Handler returns a modeled failure | Keep the existing `isError`, code/message/details, and exact-failure-text policy. Do not validate a modeled failure as success. |
| Encoding/validation error or unknown service failure | Return a generic internal tool failure and retain only safe diagnostic metadata. Never emit the private cause as client text. |

### Preparation lifetime

Prepare default and explicit contracts for each server during registration.
Keep the compiled validators in the registered tool closures so calls validate
without recompilation. Do not add process-wide type caches, content-hash caches,
`sync.Once` contract getters, or production hooks for test-only performance.
Production creates one catalog shared by the HTTP MCP endpoint and Agent.

Module operation registrars construct their explicit contracts directly. Within
one Automations registration, prepare the canonical definition/evidence documents
once and pass them to the operations that need them. Receipts, summaries, and
details retain independent result roots. Canonical schemas constrain only their
declared nested documents, not an old HTTP envelope.

Tests may prepare and reuse immutable fixtures in `_test.go` files. Such reuse
does not justify shared production state. Domain codecs or other runtime
facilities with repeated production consumers are outside this cache removal;
their actual request-time validation remains intact.

Keep canonical HTTP schema construction available in `httpapi`. Removing MCP
publication must not delete `automationOutputSchema` or model `Schema` methods
still used by Huma. MCP never imports these HTTP response schema builders as its
result authority; both adapters read canonical nested documents from the domain.
Input portability rewriting remains in `portable_schema.go`; output schemas no
longer need a portability rewrite or success/failure union.

The SDK still converts and resolves input schemas per server. Merely enabling
SDK `SchemaCache` does not solve this for portable JSON documents. This plan
does not fork the SDK, reimplement its input validation, or bypass the protocol
for built-in agent calls. Measure remaining input work after this cutover and
report it as follow-up evidence, not a reason to expand this implementation.

## File ownership and change map

```text
internal/
├── platform/
│   └── mcpapi/                          # move surviving internal/mcpapi code/tests here in D4
│       ├── tool.go                      # typed registration and optional schema overrides
│       ├── input_schema.go              # private input derivation and preparation
│       ├── schema_generation.go         # Huma generation, local definitions, document conventions
│       ├── output_validation.go         # private local-only success compilation
│       ├── output.go                    # move/modify exact_output.go: one result encoder
│       ├── portable_schema.go           # retain input portability behavior
│       ├── doc.go, server.go            # tools-only ownership; no product-module imports
│       └── *_test.go                    # move/modify: validation, wire output, isolation
├── mcpecho/
│   └── echo.go, *_test.go               # update platform/mcpapi imports; retain location/API
├── modules/
│   ├── devices/
│   │   ├── httpapi/                     # move HTTP code/tests from api; preserve wire models
│   │   │   ├── register.go, handler.go   # explicit catalog; handler and Devices interface
│   │   │   ├── get_entity.go            # registration, wrappers, and handler for this route
│   │   │   ├── list_entities.go         # registration, wrappers, and handler for this route
│   │   │   ├── execute_entity_command.go # operation-local Command transport behavior
│   │   │   ├── entity_models.go, entity_mapping.go # reused HTTP Entity contracts/mapping
│   │   │   └── errors.go, *_test.go      # shared HTTP errors and appropriately scoped tests
│   │   ├── mcpapi/
│   │   │   ├── register.go, handler.go   # split api/mcp.go; explicit catalog and Devices seam
│   │   │   ├── get_entity.go            # tool metadata, wrappers, detail handler and local mapping
│   │   │   ├── list_entities.go         # tool metadata, wrappers, and summary-page handler
│   │   │   ├── execute_entity_command.go # tool metadata, wrappers, and Command receipt/failure
│   │   │   ├── entity_models.go, result_schema.go # reused projections and result derivation
│   │   │   └── errors.go, *_test.go      # MCP errors and operation/shared-contract tests
│   │   └── internal/apicursor/          # extract cursor codecs/tests; no transport imports
│   ├── automations/
│   │   ├── httpapi/                     # move HTTP registration/models/evidence and tests
│   │   │   ├── register.go, handler.go   # explicit catalog; handler and Automations interface
│   │   │   ├── create_automation.go, get_automation.go # one route per file, including registration
│   │   │   ├── get_automation_history_entry.go # local HTTP history-read wrappers and handler
│   │   │   ├── automation_models.go, history_models.go # reused HTTP payloads
│   │   │   └── automation_contracts.go, history_contracts.go, schema_publication.go # retain HTTP schema authority
│   │   ├── mcpapi/
│   │   │   ├── register.go, handler.go   # explicit catalog, service seam, private handlers
│   │   │   ├── create_automation.go, replace_automation.go # local inputs/registration/receipt mapping
│   │   │   ├── get_automation.go, start_automation_run.go # local detail/Run receipt operations
│   │   │   ├── get_automation_history_entry.go # local Run/Skip detail contract and handler
│   │   │   ├── definition_arguments.go, definition_contracts.go # exact decoding and canonical input schemas
│   │   │   ├── automation_models.go, automation_contracts.go # reused receipts/definition constraints
│   │   │   └── errors.go, *_test.go      # stable failures and operation/canonical evidence checks
│   │   └── internal/apicursor/          # extract Automation/history cursors and owner tests
│   ├── adapters/
│   │   ├── httpapi/                     # move Adapter/Function HTTP code and tests
│   │   │   ├── register.go, handler.go   # complete catalog; separate Adapters/Functions seams
│   │   │   ├── get_adapter.go, list_adapters.go # operation-local management routes
│   │   │   ├── get_adapter_function.go, invoke_adapter_function.go # operation-local Function routes
│   │   │   └── function_models.go, invocation_models.go, *_test.go # reused payloads and owner tests
│   │   ├── mcpapi/
│   │   │   ├── register.go, handler.go   # complete catalog; separate service/handler dependencies
│   │   │   ├── get_adapter.go, list_adapters.go # management tool metadata/inputs/handlers
│   │   │   ├── get_adapter_function.go, invoke_adapter_function.go # Function tool metadata/inputs/handlers
│   │   │   ├── function_models.go, function_contracts.go # reused Function projections/constraints
│   │   │   ├── invocation_models.go, invocation_contracts.go # reused retained-evidence contracts
│   │   │   └── errors.go, *_test.go      # domain classifiers and exact invocation evidence tests
│   │   └── internal/apicursor/          # extract management cursors; Function cursors stay domain-owned
│   ├── notifications/
│   │   ├── httpapi/                     # move HTTP registration/models/mapping and tests
│   │   │   ├── register.go, handler.go   # explicit catalog and Notifications seam
│   │   │   ├── send_notification.go, get_notification.go # operation-local HTTP contracts/handlers
│   │   │   └── notification_models.go, notification_mapping.go, *_test.go # reused HTTP contracts/tests
│   │   └── mcpapi/
│   │       ├── register.go, handler.go   # explicit catalog and Notifications seam
│   │       ├── send_notification.go, get_notification.go # operation-local receipt/detail tools
│   │       ├── destination_models.go, notification_models.go # actual multi-operation reuse
│   │       └── errors.go, *_test.go      # protocol failures and single-attempt guarantees
│   └── agent/
│       ├── register.go, stream.go     # rename HTTP registration functions for assembly
│       └── mcp_tools.go, *_test.go    # migrate platform import/fixture API; retain bridge
└── app/hearthd/
    ├── run.go                          # migrate platform Server dependency and assembly imports
    ├── http_handler.go                 # import sibling adapters; inject per-transport interfaces
    ├── http_handler_test.go            # modify HTTP-only catalog setup
    ├── automations_api_test.go         # modify HTTP-only catalog setup
    ├── *_integration_test.go, *_e2e_test.go # migrate model imports; preserve assembly/journeys
    ├── mcp_integration_test.go         # lean discovery and server-isolation coverage
    └── mcp_catalog_benchmark_test.go   # new: isolated catalog construction benchmark
docs/architecture.md                    # modify during implementation: accepted ownership
CODING_STANDARDS.md                      # codified operation-local transport file convention
docs/adr/0023-serve-mcp-from-core-http-server.md # keep transport decision; link cutover
specs/mcp.md                            # mark superseded ownership/results/resources
specs/automation-variants.md             # mark superseded whole-response parity/publication
specs/adapter-functions.md              # mark superseded results/resources/publication
README.md                              # document sibling packages, independent MCP contracts
```

The tree shows representative files; every remaining route/tool in scope also
gets its own operation-named file, including registration and local wrappers.
Move HTTP-only code/tests to `httpapi` and split the mixed MCP catalogs into
operation-local files in `mcpapi`, without redundant protocol filename prefixes.
Create resource-named support files only for definitions with real reuse; no
placeholder model/mapping/contract files. Preserve existing HTTP Go/wire/schema
names even where their declarations move to a more specific file.
D2 deletes resource-only code/tests, including shared `resource.go` and
`resource_query.go`, before these moves. D4 removes the obsolete published
`output_schema.go` union machinery while preserving any derivation needed by
success validation. Move surviving framework files/tests to platform in that
same unit. Remove each old mixed module `api` directory and the old
`internal/mcpapi` directory after their consumers migrate; no compatibility
imports remain. Historical evidence links retain their baseline paths.

Domain service inputs/results, behavior, database rows, migrations, NATS events,
configuration, frontend models, and public Adapter SDK APIs do not change.
Transport-owned service interfaces move or narrow at their new consumer; the
domain does not acquire transport dependencies. Generated code has no intended
change; review any output from the full validation gate.

## Ordered deliverables

Each deliverable ends at a green verification boundary. Estimates are relative
effort, not calendar promises. The registration API migration in D4 is one
isolated edit phase across the wrapper and all internal callers. Intermediate
compile failures may exist only within that phase or a D3 module move, never as
a published state.

| ID | Outcome and owner | Depends on | Size | Acceptance |
| --- | --- | --- | --- | --- |
| D1 | Capture current catalog, service effects/failure cases, HTTP/OpenAPI contracts including schema names/URLs/headers, representative MCP payload sizes, and timing baseline. Old MCP JSON is comparison input, not a future equality requirement. Owners are existing wrapper/module/Core tests and new Core benchmark. | None | M | Baselines for A1, A4, A8, A10 |
| D2 | Complete two independent removal checkpoints. First replace full catalogs in HTTP-only Core fixtures with fresh empty `mcpapi.New` servers. Then remove resource registration/machinery/assertions. Preserve real catalogs, unrelated mixed-test assertions, and current versions. Owners are Core fixtures, shared `internal/mcpapi/resource*`, module resource code, and mixed tests. | D1 | M | A2, A4, A9 |
| D3 | Replace each mixed module `api` with sibling `httpapi`/`mcpapi` packages using the operation-local file standard: one route/tool per file, explicit register.go catalog, dependencies in handler.go, and resource-named files only for actual reuse. Preserve HTTP contracts; implement independent MCP results/mappings/validators and consumer service seams. One public Register includes all Adapter/Function operations with separate dependencies. Migrate helpers, assembly/tests, and old directories as green module-sized units. New MCP results and validators land together through the existing wrapper API. | D2 | XL | Module portion of A3; A4, A5, A8 |
| D4 | Move the surviving framework/tests to `internal/platform/mcpapi`; introduce registration-owned contracts, omit advertised output schemas, and use one exact result path. Migrate imports/typed registration calls across module adapters, Agent, Core assembly, and `internal/mcpecho`, then remove old `internal/mcpapi` and superseded fields/helpers. Owners are platform `mcpapi`, module operation registrars, and all listed consumers. | D3 | L | A1, A3, A5, A6, A7, A8 |
| D5 | Compare cold assembly, repeated catalog construction, and HTTP-only fixture timing against D1. Profile recurring work and retained preparation separately. Owner is Core's benchmark and existing assembly/HTTP tests. Do not change workloads during comparison. | D4 | M | A9, A10 |
| D6 | Reconcile docs and supersession links, complete the managed simulator smoke with retained evidence/cleanup, perform code/prose/comment cleanup, and run completion validation. Owner is the complete scoped change and Core/simulator journey verification. | D5 | M | A1, A11, A12 |

D1 records current behavior and adds only passing characterization checks.
Change discovery/resource expectations with D2 and D4, when their new behavior
exists. D1 does not assert that the unmodified baseline already meets the final
lean-discovery or performance criteria.

D2 and D3 still use the existing `internal/mcpapi` path. D4 relocates it with
the registration API migration in one green unit; the examples and focused
commands below describe the final platform path. Do not migrate imports early
without the implementation or leave a forwarding package between checkpoints.

Before deleting or rewriting a mixed test, identify each surviving assertion and
its owner. Function terminal-evidence/discovery tests and Automation
branching/delay/notification tests keep their service-safety and exact-evidence
checks. Replace whole-response HTTP/MCP equality with independent expected
protocol payloads and canonical-subdocument assertions. Preserve plain fixtures
still used by tool tests when deleting resource-only helpers.

Move owner-level HTTP and MCP assertions/helpers with their respective packages;
do not keep MCP tests importing HTTP response models or expose private handlers
just to preserve an old fixture. Split mixed-protocol tests into independent
protocol expectations. Any surviving assertion about both assembled surfaces
belongs in Core's integration/E2E tests, not in a sibling adapter import. Share
only transport-neutral domain fixtures where there is actual reuse, respecting
existing build tags and test-helper boundaries.

## Acceptance criteria

| ID | Checkable result | Boundary and method |
| --- | --- | --- |
| A1 | Exactly the current 33 tools remain with existing names/inputs, descriptions explaining summary/detail/receipt use, and complete input schemas. No tool advertises `outputSchema`; implementation version stays `1.0.0` and protocol negotiation is unchanged. | Core `/mcp` catalog and built-in agent bridge tests. Inspect wire metadata and omitted output-schema fields. Keep full identity coverage. |
| A2 | Initialize omits resource capability; resource/template lists contain no entries; old `hearth://` reads do not resolve. | One assembled SDK-client/raw-wire regression in Core. Delete obsolete resource-success matrices instead of recreating them as negative matrices. |
| A3 | The four mixed `api` packages become sibling `httpapi`/`mcpapi` packages without cross-imports or a legacy façade. Each follows the operation-local file standard, owns its transport contracts/seams/tests, and exposes one public Register. Adapters registers management and Functions with separate dependencies. The shared framework is in `internal/platform/mcpapi` with no product-module imports; domain services import neither transport. MCP never reuses HTTP endpoints/responses or reconstructs Huma errors. | Review each operation file against the explicit register.go catalog and CODING_STANDARDS.md. Check that wrappers/metadata/handler/local helpers stay together, extracted definitions have multiple operation consumers, and handler.go owns dependencies. Review imports/callers across Core, Agent, and Echo; old paths must be gone. Verify one Adapters Register includes both surfaces. No shared transport models or product logic in platform; cross-protocol assembly tests stay in Core. |
| A4 | HTTP routes, OpenAPI schemas/component names/URLs, response headers, payloads, and errors stay unchanged after the package moves. MCP results match the new result catalog, including identifier names, summary/detail selection, terminal receipts, arrays/null/omission, and scoped cursors. | Independent protocol fixtures and D1 metadata comparisons. Verify summaries omit bulky fields and details retain them. Exercise create/get/replace/read and Function describe/invoke/read with recording services, preserving effects/revision/identity. No whole-response parity assertion. |
| A5 | Invalid IDs, nulls, limits, malformed cursors, strict Automation definitions, and Function outer inputs never reach an inappropriate service call. Parent existence checks and omission defaults survive. | Owner-level negative and positive transport tests using recording service doubles; keep SDK rejection and domain ToolError contracts distinct. |
| A6 | Every output has a success validator. `any` without an explicit success contract, derivation failure, and invalid references fail startup. Reject values that violate the selected success contract, including nil object results, missing required properties with defaults, bad custom marshalers, recursive invalid output, and failure-shaped success. Unknown failures reveal no private cause text. | Wrapper registration and raw-wire tests through both Register paths, with valid controls. Test-only untyped outputs select an explicit schema or concrete type. Verify that absent external resources fail without file/network loading. |
| A7 | Server contracts own their schemas and services remain isolated. Two servers return their own stub's distinguishable results. Concurrent valid/invalid calls do not contaminate validators or inputs. Caller-schema mutation has no effect. Explicit reuse in test fixtures remains safe. | Wrapper/Core isolation tests under race instrumentation. Verify callable behavior and no production cross-server contract caches; do not add cache pointer-equality tests. |
| A8 | Selected canonical values preserve large integers, supported extreme numeric literals, explicit nulls, recursive snapshots, Function schemas/results, Command IDs/status, and Skip history references. The agent can discover summaries, fetch detail, execute once, and inspect the returned identity through MCP. Function failure details use the new MCP receipt rather than an HTTP Invocation body. | Raw JSON-RPC and scripted agent bridge tests with the new payloads. Inspect raw structured/text bytes before SDK float64 decoding. Compare canonical subdocuments and domain effects, not HTTP result envelopes. No live model required. |
| A9 | HTTP-only fixtures do not construct a full catalog. Production and protocol tests still build and exercise the real catalog. | Review setup chains and run Core unit/integration/E2E selections. Keep `TestMCPEndpointExposesTheFullCatalog` and production notification/Function/agent assembly guarantees. |
| A10 | Each server prepares its contracts during registration, without advertised output unions or production cross-server caches. Calls reuse only their registered validators. Tests own any fixture reuse. | Inspect preparation ownership and production state; keep validation/isolation tests green. The current construction benchmark includes preparation for every server. Historical cached-revision timing is not a current performance claim or acceptance gate. |
| A11 | Documentation describes sibling module adapters, platform-owned MCP infrastructure, uniform registration, and the operation-local convention in CODING_STANDARDS.md. Explain resource/output-schema removal, redesigned results, list-to-detail, and receipt-to-history use. Obsolete ownership/parity requirements/comments are gone; required checks pass or a specific blocker is reported. | Diff/link review, all three cleanup passes, and `mise run validate` with `GO_PACKAGES` unset. |
| A12 | The worktree-managed default simulator and production Core exercise the real `/mcp` endpoint. Discovery/inspection, satisfied and rejected Commands, Automation receipt/history, exact Function echo/failure, and native notification results agree with durable Core evidence by identity. No live household devices, model calls, or mutation retries are used. | Run the simulator smoke procedure below after final integration. Save raw requests/results, exact publication/Command/Run/invocation/notification IDs, URLs/config, expected/observed outcomes, and cleanup status. All scenarios must pass; unit/integration tests or simulator-control snapshots alone do not satisfy this criterion. |

### Focused commands

Use the repository tasks. These commands target the proposed package layout;
they are planned checks, not checks already run for an implementation. The
module selections include extracted cursor helpers and affected domain
consumers; an individual `httpapi` or module `mcpapi` package can be selected
while iterating. Include `internal/mcpecho` because it consumes the relocated
framework. D1 uses the existing mixed/framework paths before the moves.

```sh
transport_packages='./internal/platform/mcpapi ./internal/mcpecho ./internal/modules/devices/... ./internal/modules/automations/... ./internal/modules/adapters/... ./internal/modules/notifications/... ./internal/modules/agent'
GO_PACKAGES="$transport_packages" mise run test
GO_PACKAGES="$transport_packages" mise run test -- -tags=integration
GO_PACKAGES='./internal/app/hearthd' mise run test -- -tags=integration,e2e
GO_PACKAGES="$transport_packages ./internal/app/hearthd" mise run test -- -tags=integration -race
GO_PACKAGES="$transport_packages" mise run --skip-deps lint
GO_PACKAGES="$transport_packages ./internal/app/hearthd" mise run --skip-deps vet
```

Socket-backed SDK client tests remain integration tests. Pure preparation,
mapping, error classification, and recorder-only wire checks can remain units.
Follow [docs/testing.md](../docs/testing.md) for tags and helper dependencies.

### Managed simulator smoke (A12)

This is a required post-implementation check, not work to run while refining
this plan. Use the default [simulator configuration](../configs/scripted.simulator.yaml)
and repository lifecycle tasks. The [control reference](../.agents/skills/simulator-validation/references/http-control.md)
and [scenario reference](../.agents/skills/simulator-validation/references/validation.md)
document publication identities, native Functions/notifications, and cleanup.
No custom Adapter provider, config replacement, database reset, or live model
call is needed. The stack requires nonempty `HEARTH_OPENAI_API_KEY` in its
environment; do not print, persist, or capture the credential.

1. Record `mise --version` (requires 2026.9.12+) and the candidate commit/diff.
   Run `mise run simulator-start`; discover `SIM_CORE_PORT`, `SIMULATOR_PORT`,
   and the other worktree ports through selected inherited variables in
   `mise exec`, never fixed ports or a captured credential-bearing environment.
   Verify the managed Core is running the candidate, not an old daemon binary;
   if necessary restart through the managed lifecycle with retained storage.
   Save non-secret URLs, effective config, readiness, and an evidence directory
   under `/tmp/opencode/hearth-mcp-simulator/<run-id>/`.
2. Before mutation, record each scenario's stimulus, expected Core result,
   evidence lookup, and wait budget. Discover Entity IDs by `adapter_id` +
   `binding_key` + `key` from `GET /v1/sim/entities`, and destination IDs from
   `GET /v1/sim/notifications/destinations`; confirm identities through Core.
   Save pause states, then pause the healthy power and button sources and the
   rejecting power source. Confirm default notification behaviors and ensure
   no unrelated Automation would react to scenario inputs. Report interfering
   retained state as a blocker rather than deleting or disabling someone else's
   definitions. Save notification capture cursors before sends.
3. Initialize a real Streamable HTTP client at the discovered Core `/mcp` URL.
   Drive all tool discovery, inspections, and actions in the table through this
   endpoint. Save raw JSON-RPC requests/responses, including expected `isError`
   results. Use HTTP GETs only as independent Core evidence and simulator
   controls only to prepare input; neither substitutes for a tool call.

| Scenario and stimulus | Required result and evidence |
| --- | --- |
| Discovery and inspection | Initialize reports `1.0.0`, with no resource capability. `tools/list` has exactly the 33 tools, complete inputs, and no `outputSchema`; resource/template lists are empty. Use list-to-detail reads for a Device, Entity, Adapter, Function, and notification destination. Summary fields are compact and detail exposes the declared support/schema/evidence. Retain a terminal page and one cursor follow-up with a small limit where the default inventory has multiple items. Compare selected identities/domain values with Core HTTP reads, not complete response equality. |
| Satisfied Command | For `sim-healthy` / `simulated-light` / `power`, publish `false` once while paused. Save its `observation_id` and wait for that exact Core State/history record before executing `set` with `{"value":true}` once through `execute_entity_command`. Require a `satisfied` receipt. `get_command`, `list_entity_commands`, HTTP `/v1/commands/{id}`, and State history must identify the same Command and its outcome Observation; the receipt's value is `true`. Changed State without the verified Command/Observation linkage is insufficient. |
| Rejected Command | For `sim-rejecting` / `simulated-rejecting-plug` / `power`, confirm the retained `true` baseline, then execute `set` with `{"value":false}` once. Require `isError`, code `upstream_rejected`, and details with the durable Command ID/status. MCP `get_command` and HTTP `/v1/commands/{id}` must show that exact ID as `rejected` with the same failure code. Do not accept a transport timeout or unrelated history row as rejection evidence. |
| Automation receipt to history | Reset healthy power to `false` with an exact-ID publication check. Create one uniquely marked v2 Automation through MCP, with a Trigger on the paused button and one Command Step setting healthy power `true`. Retain its exact definition and revision. Verify compact list/create results and full `get_automation` detail, then call `start_automation_run` once. Follow its `run_id` using `get_automation_history_entry` until terminal; require a succeeded Run, the immutable definition/Condition evidence, and a verified satisfied Command ID resolving through `get_command`. The same Run/Command IDs must appear in HTTP history. Do not treat the initial Run receipt as completion. |
| Exact Function echo and failure | List/describe `sim-healthy` Functions and save each current `catalog_revision`. Invoke `inspect` once with `{"sequence":9007199254740993,"tiny":1e-10000,"nullable":null}`. Require a succeeded receipt with those exact result values/tokens, and the same `invocation_id` and arguments/result in MCP detail and HTTP `/v1/adapter-function-invocations/{id}`. Invoke `fail` once with `{}`; require `isError`, code `failed`, and the MCP-owned `invocation` receipt carrying the exact ID, status `failed`, and reason `simulated_failure`. Both histories retain the appropriate summaries and details; no failure result is presented as success. Inspect raw structured/text JSON with number-preserving tooling, not float64 coercion. |
| Native notifications | Discover/list/describe the default `accepted`, `rejected`, `not-sent`, and `unknown` destinations, then call `send_notification` once per destination with unique message markers. Require respectively `accepted`, `rejected/provider_rejected`, `not_sent/adapter_busy`, and `unknown/provider_error` in the receipts and exact-ID MCP/HTTP notification detail. Follow `notification_id` through the unchanged `get_notification` input key `id`. Match each ID to simulator capture `attempt_id`, destination, and immutable message. Follow capture/history pages and verify an untruncated, loss-free cursor window before asserting one receive per send. Acceptance is not proof of external delivery; these are explicit Adapter outcomes, not timeout tests. |

Ordinary reads/control requests have a two-second timeout. A synchronous
Command/Function/send tool call has a separate 60-second request deadline.
Each read-poll has a 90-second wall-clock budget with one-second spacing.
Only reads are polled: issue mutations once, stop subsequent mutations on an
unexpected result, and diagnose ambiguous responses by retained identities or
the unique marker. Never automatically retry a timed-out POST/tools call or
raise deadlines until it passes. Record a missing identity or lost capture
window as unresolved evidence, not a successful no-effect assertion.

In a cleanup/finally path, wait for owned Runs to stop and delete only the
temporary Automation at its recorded unchanged revision. If creation was
ambiguous, locate its exact marked definition across pages without retrying
create; report any unresolved ownership or changed definition. Restore saved
pause states, preserve SQLite/JetStream and all evidence, then run
`mise run simulator-stop` after validation or failure diagnosis unless the user
asked to leave it running. Save expected/observed results and cleanup status;
inspect managed Core/simulator logs on failure. A12 remains incomplete if any
scenario, evidence lookup, or required cleanup is unresolved. This bounded smoke
does not replace owner-level matrices, the built-in agent bridge tests, recovery
tests, or the full completion gate.

### Performance protocol

Do not combine these workloads into one speedup claim:

| Workload | Measurement and interpretation |
| --- | --- |
| Cold assembly/listing | Run `TestMCPEndpointExposesTheFullCatalog` in a fresh test process with `-count=1`. Includes first-use preparation, Huma registration, connection, and listing. Excludes Go compilation and full production startup. |
| Repeated catalog construction | `BenchmarkMCPCatalogConstruction` calls `newMCPServer` with fresh service doubles, including each server's preparation. Keep each server observable and report ns/op, B/op, and allocs/op. Excludes Huma and MCP session setup. Historical cached-revision runs warmed preparation first; those are a different, superseded workload. |
| HTTP-only fixture | Run `TestHTTPHandlerServesHealthReadinessAndDeviceOperations` in a fresh process. Retain its HTTP requests and assertions. Report removal of irrelevant catalog setup, not faster execution of the same internal work. |

Use the identical construction benchmark workload on the D1 baseline and
candidate; package moves may update imports/type qualifiers, not timed work.
Keep HTTP requests/assertions stable. Update discovery and MCP result
expectations with their owning deliverables; the two versions deliberately do
not return identical metadata or result payloads. Verify tool identity, selected
domain values, and successful calls outside the timed loop so an incomplete
catalog fails. Do not attribute smaller tool results to faster encoding of an
unchanged payload.

```sh
GO_PACKAGES='./internal/app/hearthd' mise run test -- -run '^$' -bench '^BenchmarkMCPCatalogConstruction$' -benchmem -benchtime=5x -count=1
GO_PACKAGES='./internal/app/hearthd' mise run test -- -run '^$' -bench '^BenchmarkMCPCatalogConstruction$' -benchmem -benchtime=5x -count=1 -race
GO_PACKAGES='./internal/app/hearthd' mise run test -- -tags=integration -run '^TestMCPEndpointExposesTheFullCatalog$' -count=1 -v
GO_PACKAGES='./internal/app/hearthd' mise run test -- -run '^TestHTTPHandlerServesHealthReadinessAndDeviceOperations$' -count=1 -v
```

For each workload and instrumentation mode, collect at least five runs per
revision, alternating baseline and candidate on an otherwise idle machine.
Repeat both test commands with `-race` for the race comparison. Report medians
and ranges. Record exact commits, Go version, cache state, CPU count, load, and
commands alongside raw evidence under `/tmp/opencode`.

Use separate `-cpuprofile` and `-memprofile` captures, with the binary `-o` and
all artifacts outside the checkout. Compare allocation volume and retained
memory separately. Registered validators live with their owning server;
check that repeated server creation does not retain extra services or sessions.
Profiler timing is not a benchmark sample. Output-schema bytes removed are a
protocol/work reduction, not a same-payload encoding speedup. Report residual
SDK input resolution without extending this plan into an SDK rewrite.

For the full completion gate, use a shell with `GO_PACKAGES` unset:

```sh
mise run validate
```

The full gate includes real-boundary tests and needs reachable Docker for
Mosquitto integration. No live model calls or household device actions are
required to validate this transport change. Missing infrastructure is a named
blocker, not permission to skip the gate silently.

## Alternatives and risks

| Alternative | Advantage | Cost or reason not selected |
| --- | --- | --- |
| Cache everything without changing MCP | Least external change | The user chose tools-only discovery and no advertised output schemas. Caching unused metadata preserves unnecessary work and resource machinery. |
| Decouple handlers only | Fixes error/ownership coupling | Does not remove repeated schema construction and SDK output resolution. |
| Drop all output validation | Smallest wrapper | Would lose custom-marshaler, recursive variant, and canonical success checks. Removing metadata does not justify accepting invalid output. |
| Replace SDK typed input handling | Could reuse more compiled input validation | Reimplements defaults/decoding/protocol behavior. Deferred until residual profiling establishes a need. |
| Shared HTTP/MCP response models | Less mapping code today | Couples independent protocols and obscures result ownership. The user explicitly chose protocol-specific payloads and result redesign. Share canonical documents and domain behavior instead. |
| Separate protocol files/prefixes inside one `api` package | Avoids import moves | Does not provide a package boundary around private handlers, mappings, and models. With independent contracts, sibling `httpapi`/`mcpapi` packages justify the move and avoid repetitive identifier prefixes. |
| New shared application layer or per-operation packages | Could centralize workflows | Existing services already own use cases. Protocol-owned mappings and neutral helpers suffice; another layer would add navigation and risk recreating HTTP wrappers. |
| Regroup or rename tools around tasks | Potentially different agent workflow | The approved expansion is result redesign. Existing tool names and inputs are sufficient for summary/detail/receipt journeys; regrouping is outside this plan. |

| Risk | Mitigation |
| --- | --- |
| Documentation or built-in agent examples retain old MCP contracts | Update resource/schema/result descriptions and detail/history follow-ups together. There are no external consumers to migrate; retain implementation `1.0.0` without compatibility machinery. Exercise the built-in agent bridge with the new payloads. |
| A compact result drops evidence with no remaining MCP read | Keep detail tools and full diagnostic history entries where no detail tool exists. Preserve canonical nested evidence and verified identities; never infer delivery/completion from an acknowledgment. |
| Independent packages remain coupled through aliases, shared seams, or schemas | Neither adapter imports the other. Each defines its consumed service interface and maps domain results independently. Test closed MCP roots and independent expected payloads; share only canonical subdocuments and genuinely neutral helpers. |
| Moving from HTTP errors loses safe messages or durable evidence | Capture representative failures in D1 and map original domain errors at each transport. Verify Command, Skip, and Function identity details on real wire calls. |
| Prepared documents or validators accidentally carry mutable state | Own schema bytes at preparation, keep fields private, share only read-only preparation, and test caller mutation plus concurrent cross-server isolation. |
| Common exact-output handling changes canonical numeric/null meaning | Preserve input defaults and selected canonical values under the new projections. Do not inject output defaults. Check raw numeric/null bytes and the documented omission rules. |
| Resource test deletion removes unrelated coverage | Inventory mixed assertions before deletion and retain HTTP/tool/domain checks with their owners. |
| Canonical HTTP schema builders are deleted as apparently unused MCP code | Trace Huma consumers. Keep HTTP schema publication and standalone schema tests green. |
| Package moves or incidental Go renames change published HTTP schema names | Keep existing HTTP type names; explicitly preserve wire names where a rename is unavoidable. Compare D1 components/references, standalone URLs, and schema headers after D3, including `publishOutputSchemas`. |
| Package moves lose private helpers or mixed-test assertions | Inventory consumers before each module move. Extract cursor codecs to module-local internal helpers; retain separate transport mappings/classifiers. Move owner tests and keep cross-protocol assembly checks in Core, without compatibility façades or test-only transport dependencies. |
| Uniform Adapters registration accidentally drops management or Function operations | Keep both service dependencies explicit and test that one Register call installs each protocol's complete Adapter/Function surface. Private helpers preserve separate handlers and admission gates. |
| Platform MCP infrastructure absorbs product contracts or leaves stale consumers | Keep the framework free of product-module imports, tool names, payloads, and domain error classifiers. Move surviving framework tests and migrate Core, Agent, module adapters, and Echo mounting imports together in D4; remove the old package rather than forwarding through it. |
| SDK input resolution remains expensive | Profile it after D5 and report the remaining share. Do not claim elimination of all schema work or expand into an SDK rewrite. |
| Concurrent test load hides gains or triggers deadlines | Serialize measurements, separate profiling from timings, and investigate failures instead of raising deadlines or retrying until green. |

## Rollout and stopping condition

Implement in the D1 through D6 order after approval, in one ordinary unmerged
PR. Preserve the selected delivery branch and unrelated work. Use coherent
commits at green checkpoints. Do not merge, deploy, or change a live household.

The redesign needs no database migration, retained-data conversion, version
bump, or external consumer rollout. Existing read tools replace resource reads;
compact lists lead to detail tools, and action receipts carry identifiers for
history reads. The built-in agent keeps its MCP bridge and tool identities, with
descriptions and scripted tests updated for the new results. Retained
conversation traces remain their original JSON text; do not rewrite historical
tool results. The prior development binary remains a diagnostic fallback, not a
second supported contract.

Stop when A1 through A12 hold, the benchmark comparison supports the stated
performance claim, and completion validation passes. A performance result
within noise, failed compatibility check, or unavailable required infrastructure
is an explicit unresolved result. Do not keep layering speculative optimizations
onto this plan. A different tool workflow or SDK input-validation architecture
requires a revised proposal.

## Design rationale

Keep service inputs/results in the domain and request/result payloads in their
own protocol adapters. Domain behavior can be shared without making HTTP and MCP
return the same JSON. Keep input validation in the SDK and prepare only reusable
schema data outside server binding. Removing unused resource/output-publication
requirements is preferable to caching their old machinery. Explicit output
contracts keep that simplification from disabling success validation.

Sibling protocol packages provide the intended ownership boundary without
changing module/service ownership. Domain codecs, protocol-owned mappers, and
small neutral cursor helpers are enough; no shared payload/application layer.
Operation-local files keep the route/tool contract and its service translation
discoverable together. Resource-named support files represent actual reuse,
not a mandatory horizontal split into inputs, handlers, models, and schemas.
One public module registration function need not imply one service: Adapters
accepts its two existing services explicitly. The generic typed tool framework
belongs in platform, while tools and their contracts remain with each module.
Test callable behavior and server isolation rather than cache pointer identity.
Deliver each change at a green checkpoint and report the three measurement
workloads separately. These rules do not depend on a developer's installed agent
skills.

## Approval state

The user confirmed both tracks, resource removal, no advertised output schemas,
and independently owned protocol payloads with redesigned MCP result shapes.
Review also approved removing duplicate input preflight resolution, requiring
explicit contracts for untyped outputs, and clarifying delivery/measurement.
The latest annotations retain implementation `1.0.0` and require managed
simulator verification. The subsequent package-split decision replaces the
prefix-based naming approach: sibling `httpapi`/`mcpapi` packages now provide
explicit protocol qualification for registration, payloads, and private helpers.
This review refinement makes module registration uniform, including Adapters'
separate service dependencies, and places the shared MCP framework in platform.
The user also approved the operation-local file convention demonstrated in the
example; it is now codified in CODING_STANDARDS.md and applied throughout this
plan's target layout and review criteria.
The explicit request on 2026-10-21 to implement this named, complete spec approves
the result catalog and implementation contract. Delivery remains one unmerged PR;
completion requires the evidence and stopping conditions above.
The user subsequently selected environment-only agent credentials through
`HEARTH_OPENAI_API_KEY`, explicitly removing key-file configuration and fallback.
This is an approved startup/configuration follow-up, not a change to MCP,
persistence policy, or the requirement to complete A12.
After the retained-database startup failure was explained, the user requested
"fix the upgrade". That separately authorizes the migration repair: defer the
old version-14 conversion to version 17 and expand the definition/snapshot byte
cap by exactly 544 bytes (32 new 17-byte Command discriminators). Raw JSON,
normalized JSON, and SQLite use the same 66,080-byte cap. This is a documented
size-policy exception to the original unchanged-domain/persistence scope, not a
wire-shape change or permission to reset storage or discard retained records.
The later request to "fix all those" approves the four-area simplification
described above. It preserves the implemented wire behavior and does not
authorize another managed scenario run or changes to household devices.
The latest "approved implement this" accepts Huma as a schema-only utility on
MCP-owned payloads, with constraint tags and native hooks. This supersedes the
no-Huma dependency restriction while retaining independent transports, exact
JSON, per-registration preparation, all 33 input documents, and the existing
validation owners. It does not approve Huma routes or runtime validation for MCP.

## Implementation evidence

### Deliverable coverage

| Deliverable | Observed result |
| --- | --- |
| D1 | Baseline `cab1bc0564dfa3ac299c1d21d4411e6c901de098` adds the construction benchmark and complete HTTP/OpenAPI digest. Captures include all 33 input schemas, initialization, representative Entity results, headers, and 48 standalone schema responses. |
| D2 | HTTP-only fixture removal and resource removal landed as separate verified checkpoints. Production, protocol, Function, notification, and Agent assembly retain real catalogs. |
| D3 | Four sibling transport pairs implement the selected results and operation-local ownership. Independent module reviews and integrated Core tests passed. Review found missing canonical history collection bounds; max, max+1, and malformed-item controls now cover all five affected arrays. The overflow cases reproduced the defect before correction. |
| D4 | Framework and consumers use `internal/platform/mcpapi`, registration-owned preparation, success-only validation, and one exact-result path. Independent review of the original implementation found no defects. Both registration paths cover invalid outputs, defaults, references, caller mutation, concurrent isolation, and exact JSON. |
| D5 | Five alternating pairs per workload and instrumentation mode show separated timing/allocation ranges. Profiles identify residual SDK input work and bounded retained preparation at sampled resolution. Measurements below use candidate `f950639cc852294f0d5a6d5330cd235cc0cc4227`. These measurements predate the separate credential, upgrade, and schema-builder cleanup follow-ups; the latest revision was not rebenchmarked. |
| D6 | Code, prose, and comment cleanup completed. `env -u GO_PACKAGES mise run validate` passed, including all test levels, generation, format, tidy, lint, vet, 69 frontend tests, and frontend build. All six managed A12 scenario groups passed on retained storage, with exact-ID evidence and cleanup below. |

The captured candidate HTTP/OpenAPI document, Entity response and headers, and
all 48 standalone schema responses are byte-identical to D1. All 33 MCP input
schema documents are also unchanged. Catalog names and implementation `1.0.0`
remain fixed; output-schema metadata and resource capability are absent.
Independent MCP tests assert the new payloads rather than HTTP response parity.
The scripted built-in Agent bridge follows summary, detail, one Command, and
the returned Command identity without a live model.

An earlier user-requested cleanup removed Huma's schema types from
the Adapter Function MCP contract builders at `ff4802c`. At that checkpoint, all
four module MCP packages and `internal/platform/mcpapi` had no Huma dependency,
including transitively. This historical restriction is superseded by the latest
approved schema-authoring refinement above. Plain
JSON Schema maps retain all five Function input documents and five server-side
success contracts: normalized before/after captures are byte-identical under
`/tmp/opencode/hearth-mcp-huma-removal/`. Explicit input-document fixtures and
existing acceptance/rejection, exact-number, reference, default, and isolation
tests cover the replacement. No HTTP schema, tool name, input/result shape,
failure classification, or preparation lifetime changes. Managed A12 evidence
below remains pinned to `7ba99ca`; it is not represented as a rerun of this
schema-only cleanup.

The subsequent user decision removes cross-server MCP caching entirely from
production. The default per-type cache and module-level `sync.Once` contract and
schema getters are gone. Tool registration directly prepares each server's
contracts; its closures retain validators for calls. Canonical Automation schema
documents are shared only within one module registration, not across servers.
Contract-test document reuse lives solely in `_test.go`; there are no production
test hooks. Existing domain codecs with repeated request-time consumers remain.

All 33 advertised MCP input schemas match the successful managed-run capture;
output schemas remain unadvertised. The comparison is retained in
`/tmp/opencode/hearth-mcp-no-cache-contract-comparison.json`. Focused unit,
all-level, and uncached race checks passed across all module MCP adapters,
platform MCP, Core, and Agent. Local logs are
`/tmp/opencode/hearth-mcp-no-cache-{unit,all-levels,race,lint,validate}.log`.
The current construction benchmark includes contract preparation for each server;
the historical cached-performance results below no longer describe this code.

### Registration and schema simplification

The refactoring baseline is `17a2c951710891563a35e875f93f49da61b38d91`.
Uncached all-level tests passed on that original code across the four module MCP
packages, platform MCP, Core, and Agent before structural edits. The log is
`/tmp/opencode/hearth-mcp-simplify-baseline.log`.

The changes landed in three implementation checkpoints:

- `067567f` moves preparation into registration and removes the public prepared
  wrappers, constructors, and all four module `mustContract` helpers. Both
  registration paths still copy schemas and retain a success validator.
- `e37d767` deletes ten Devices input `UnmarshalJSON` forwarding methods. The SDK
  validates and defaults arguments before typed decoding. Scalar ID and limit
  decoders retain their text-only failure classifications. The two optional ID
  filter guards remain because explicit null must not silently widen a query.
- `a2d454e` derives ordinary Devices success records, Function inputs, and three
  Notification inputs from their payload types. Object-only raw fields, non-null
  arrays and optional strings, page bounds/defaults, and message bounds remain
  explicit. Closed lifecycle unions and recursive contracts remain handwritten.
  Notification list schemas retain their published `required:null` member.

The new malformed optional-ID cases passed before decoder removal. Success
acceptance/rejection fixtures passed against the handwritten schemas before
derivation, then against their replacements through registered tool calls.
They protect required fields, closed records, nullable State, exact opaque
numbers/nulls, object-only support/parameters, and non-null arrays/optional strings.
Existing checks retain service isolation, single calls, defaults, exact raw
request extraction, and independent service/repository validation.

A temporary test compared raw `tools/list` responses with `UseNumber`, including
every numeric token, required member, and nullability choice. All 33 advertised
input documents matched `/tmp/opencode/hearth-mcp-no-cache-catalog.sse`.
The new capture is `/tmp/opencode/hearth-mcp-simplified-catalog.sse`; the temporary
test source and result remain at
`/tmp/opencode/hearth-mcp-simplify-equivalence_test.go` and
`/tmp/opencode/hearth-mcp-simplify-equivalence.log`. No test hooks or cross-server
production caches were added. Managed A12 evidence remains pinned to `7ba99ca`.

The reader no longer follows a separate preparation API before registration or
maintains ordinary field lists in two places. Necessary constraints stay with
their operations, and SDK validation remains distinct from independently
callable domain validation. No schema registry, DSL, generator, dependency, or
HTTP/MCP coupling was introduced at that checkpoint. Focused all-level tests and lint passed.
`env -u GO_PACKAGES mise run validate` passed against source `a2d454e`, including
all test levels, generation, formatting, module tidying, lint, vet, 69 frontend
tests, and frontend build. Unchanged-package results used normal Go test caching.
Uncached all-level race checks passed across all module MCP packages, platform
MCP, Echo, Core, and Agent with `mise run test -- -tags=integration,e2e -race -count=1`.
Logs are `/tmp/opencode/hearth-mcp-simplify-{all-level,lint,validate,race}.log`.
Independent read-only review of `17a2c95..a2d454e` found no blocking correctness
or standards issues. Its optional default-source suggestion was accepted so
the Function schema default continues using `defaultPageLimit`; a stale test
comment fragment was also removed. Follow-up inspection confirmed both changes.
The complete validation gate and affected Adapter/platform uncached all-level
race checks passed again after this cleanup. Final logs are
`/tmp/opencode/hearth-mcp-simplify-final-validate.log` and
`/tmp/opencode/hearth-mcp-simplify-review-race.log`.

### Huma schema authoring with independent MCP transport

The original-code pin for this refinement is `4102d9b87c7b0316fc1e16953a56f6c6843d4e1a`.
Uncached all-level tests passed across all module MCP packages, platform MCP,
Core, and Agent before structural edits. The log is
`/tmp/opencode/hearth-mcp-huma-authoring-baseline.log`. A temporary candidate
also reproduced the existing notification send shape and bounds using Huma tags.

Source checkpoint `c63354e` adopts Huma for schema generation only:

- A private platform generator owns a temporary built-in Huma registry for each
  derivation and bundles generated local references. It creates no Huma API,
  application schema registry, cross-server cache, or production test hook.
- Ordinary fields and constraints live on MCP payloads. Native Huma hooks retain
  object-only raw fields, nullable State, optional-ID unions, and Run lifecycle
  rules. Explicit closed unions use typed Huma schemas rather than untyped JSON
  maps. Canonical Automation documents retain their existing owner and exact raw
  values through the composition boundary.
- Notification bounds now come from tags; its two list hooks preserve
  `required:null`. Unexported pagination embeds became exported embeds because
  Huma otherwise skips them. Invocation identity fields are explicit to preserve
  required-member order. The distinct non-null Entity page has an operation-local
  payload rather than a type-dependent policy hidden in the generic page.
- The generator removes inferred Go-width numeric formats and retains null-first
  unions. The existing input portability walker handles empty schema objects as
  unconstrained values, just as it already handled boolean true, without treating
  empty instance-valued constants as schemas. The canonical-property helper
  retains raw JSON rather than decoding numeric values through a schema struct.
- SDK input validation/defaults, scalar decode failures, exact original-request
  extraction, domain entry points, and Tekuri success validation are unchanged.
  Encoding still happens once, and only the original bytes reach clients.

An initial equivalence replay identified six representation differences, all
empty operand schemas inside the two canonical definition inputs. Extending the
existing schema-node portability rule preserved the documents without a separate
compatibility framework. The final replay matched all 33 input documents with
`UseNumber`, including descriptions, defaults, required ordering, and nulls.
Captures are `/tmp/opencode/hearth-mcp-huma-authoring-{before,after}.sse`; the
temporary test source and passing result are
`/tmp/opencode/hearth-mcp-huma-authoring-equivalence_test.go` and
`/tmp/opencode/hearth-mcp-huma-authoring-equivalence.log`. The temporary test was
removed from the repository after retaining its evidence.

Existing module fixtures preserve success acceptance/rejection and exact wire
values. Additional cases cover optional-ID shape, legacy nullable read records,
closed lifecycle variants, native Run hook enforcement through registration,
and empty schema versus empty instance objects. Focused all-level tests and lint
passed. Full validation passed against `c63354e`, including every test level,
generation, formatting, module tidying, lint, vet, 69 frontend tests, and frontend
build. Unchanged packages used normal Go test caching. Uncached all-level race
checks also passed across all module MCP packages, platform MCP, Echo, Core, and
Agent. Logs are `/tmp/opencode/hearth-mcp-huma-authoring-{all-level,lint,validate,race}.log`.

Independent read-only review found no blocking defects and identified two stale
comments, which were corrected. Its note about tighter Huma defaults on Adapter
optional pointers prompted an additional compatibility correction: source times
and Runtime evidence retain their previous nullable acceptance. The new controls
failed before correction and passed afterward, including valid/invalid Runtime
objects. The evidence is
`/tmp/opencode/hearth-mcp-huma-nullability-{before,after}.log`. Follow-up review
confirmed the correction and comment cleanup. The final full gate passed again,
as did uncached all-level Adapter/platform race checks. Final logs are
`/tmp/opencode/hearth-mcp-huma-authoring-final-validate-2.log` and
`/tmp/opencode/hearth-mcp-huma-authoring-review-race.log`. The preceding final-gate
attempt stopped on a literal-schema-key lint finding in the new hook; its local
suppression was corrected before the passing gate. Managed A12 remains pinned to
`7ba99ca`, with no new device actions
or live-model calls. The cached performance results below remain historical and
do not describe this generator.

### Complete schema migration audit

The follow-up audit starts at published `26e2cb4e618526f6a665e91f2ac5d1d6a5b80040`.
It covers every tool registration, payload tag, native hook, explicit success
schema, and shared preparation helper. Core assembles these registrations; the
Agent consumes their advertised inputs rather than authoring another catalog.

| Owner | Tools audited |
| --- | --- |
| Devices, 12 | `list_entities`, `get_entity`, `update_entity`, `execute_entity_command`, `list_entity_commands`, `list_devices`, `get_device`, `get_command`, `list_commands`, `list_entity_availability_history`, `list_entity_state_history`, `list_entity_events` |
| Adapters, 8 | `list_adapters`, `get_adapter`, `list_adapter_health_history`, `list_adapter_functions`, `get_adapter_function`, `invoke_adapter_function`, `list_adapter_function_invocations`, `get_adapter_function_invocation` |
| Automations, 8 | `create_automation`, `list_automations`, `get_automation`, `replace_automation`, `delete_automation`, `start_automation_run`, `list_automation_history`, `get_automation_history_entry` |
| Notifications, 5 | `list_notification_destinations`, `get_notification_destination`, `send_notification`, `list_notifications`, `get_notification` |

The audit removes these remaining duplications:

- Automation deletion receipts now derive their closed object, required fields,
  nonempty identity, positive revision, and `deleted:true` from payload tags.
- Automation History summaries and pages derive ordinary fields and constraints
  from tags. Only the Run/Skip field-presence rule remains in a native hook. The
  unused explicit page builder is gone. Collections remain non-null; emitted
  cursors and identities remain nonempty; revisions and enums retain their bounds.
- Optional scalar Device and Entity IDs use `nullable:"true"` instead of hooks.
  Registered discovery still emits the exact null-first union. The separate
  explicit-null decoder guards remain, so null cannot silently broaden a query.
- Command detail overrides only the two inherited nullable summary fields.
  Huma already derives its other optional string pointers as non-null.
- Each invocation-union builder derives Function identity properties once and
  reuses them without mutation across its five variants. Reuse stays inside one
  registration, with no shared cache or new prepared-contract type.
- Canonical input composition uses normalized documents in Huma `Extensions`
  rather than encoding and decoding the root and property maps again. The
  normalizer owns the document and preserves exact numeric tokens and nulls.

The remaining complexity has a concrete contract responsibility:

| Retained code | Responsibility |
| --- | --- |
| Command receipt, State history, and Function invocation unions | Close each variant and require or forbid outcome-specific fields. |
| Automation detail and History detail schemas | Compose canonical definitions and bounded evidence without creating another authority for those documents. |
| Function schema profile | Preserve canonical input/output schema documents, recursive definitions, and arbitrary exact constants. |
| Nullable object hooks | Preserve State and Runtime null acceptance that scalar nullable tags cannot express. |
| Raw-object hooks and raw JSON fields | Require objects where specified without decoding opaque values through float64. |
| Lifecycle hooks and Notification list hooks | Enforce related fields and preserve the published `required:null` representation. |
| Optional-ID null guards and scalar decoders | Preserve pre-service rejection and stable failure classifications independently of advertised schema acceptance. |

The original-code uncached all-level pin passed before edits. History rejection
controls passed against the explicit builder before moving its constraints.
Discovery-level optional-ID and null/malformed-input controls passed before
removing those hooks. The canonical ownership/precision test passed before and
after simplifying composition. It mutates the caller's buffer before
registration and checks `9007199254740993`, `1.0000000000000001`, and `null` through
discovery. Deletion receipt fixtures also reject false or missing `deleted`,
empty identity, zero revision, and extra members.

After integration, focused uncached all-level tests passed across every module
MCP package, platform MCP, Core, and Agent. The final replay matches all 33
advertised input documents with `UseNumber`, preserving ordering, descriptions,
defaults, exact numeric tokens, and omission/null semantics. Its source is
`/tmp/opencode/hearth-mcp-schema-audit-equivalence-test.go`; captures are
`/tmp/opencode/hearth-mcp-huma-authoring-after.sse` and
`/tmp/opencode/hearth-mcp-schema-audit-after.sse`. The temporary replay test was
removed from the repository after retaining its evidence. Focused and replay
logs are `/tmp/opencode/hearth-mcp-schema-audit-final-focused.log` and
`/tmp/opencode/hearth-mcp-schema-audit-equivalence-final.log`.

Full validation passed, including all test levels, generation, formatting, module
tidying, lint, vet, 69 frontend tests, and frontend build. Unchanged packages used
normal Go test caching. Uncached all-level race checks passed across the affected
modules, platform MCP, Echo, Core, and Agent. Logs are
`/tmp/opencode/hearth-mcp-schema-audit-final-validate.log` and
`/tmp/opencode/hearth-mcp-schema-audit-final-race.log`. A focused lint attempt
identified a now-unused literal-schema suppression after the scalar hooks were
removed. That suppression was deleted before the passing full gate.

Independent read-only review found no defects or required corrections across all
changed code and tests. Code, comment, and prose cleanup retain the distinct
validation responsibilities above. The input replay proves advertised input
equivalence; existing output fixtures and runtime rejection tests cover success
validation separately. No managed-device scenarios, deployments, or live-model
calls are part of this audit. Managed A12 remains pinned to `7ba99ca`.

### Schema readability follow-up

The user requested a readability pass after the complete migration audit.
The baseline is `b585e4e12cd5ab88161abacc40322a0ea56ff603`. This pass preserves
contracts rather than changing schema generation or validation owners.

- Inline Huma literals replace the scalar, enum, reference, array, closed-object,
  and page shorthand helpers in Devices, Automations, and Adapters. The Devices
  `result_schema.go` helper file is removed. Repeated literals are intentional
  when they let readers understand an operation without following helper calls.
- Function summary and detail contracts no longer pass a boolean through a
  shared builder. Detail composes the summary with canonical documents directly.
- Invocation summary-page and detail contracts have separate operation-local
  builders. Each shows its terminal-evidence rules without a detail-mode switch.
  They retain shared invocation identity constraints and per-builder reuse of
  derived Function properties, not shared mutable registration state.
- Automation History detail declares both complete closed alternatives. Their
  required fields, canonical references, and evidence-array bounds are visible
  beside each property instead of assembled through a common-fields closure,
  map mutation, and required-array appends. The shared Run lifecycle contract
  remains a named builder because it expresses a substantial invariant.

Uncached focused all-level tests passed before and after the edits across all
module MCP packages, platform MCP, Core, and Agent. Temporary captures compare
ten explicit success/lifecycle schemas before and after the change. They match
structurally, including required-member and variant order. All 33 advertised
inputs also match with exact numeric tokens through `UseNumber`. Existing
success-validation, null, closure, bounds, and exact-wire fixtures remain in
place. No production test hooks or permanent snapshot framework were added.

Evidence is under `/tmp/opencode/hearth-mcp-readability-*`. The `baseline.log`,
`focused.log`, `output-equivalence.log`, and `input-equivalence.log` record these
checks. Per-module `*-before.json` and `.json` files retain output captures;
`*-snapshot_test.go` and `input-equivalence_test.go` retain the temporary test
sources, which were removed from the repository. Focused lint passed after
adding local literal-schema suppressions. The first lint attempt is retained in
`lint.log`; the passing result is `lint-final.log`.

Full validation passed, including all test levels, generation, formatting, module
tidying, lint, vet, 69 frontend tests, and frontend build. Unchanged packages used
normal Go test caching. Affected uncached all-level race checks passed across
the modules, platform MCP, Echo, Core, and Agent. Logs are
`/tmp/opencode/hearth-mcp-readability-validate.log` and
`/tmp/opencode/hearth-mcp-readability-race.log`. Independent read-only review
found no defects or required structural changes and independently confirmed the
ten output captures. Its stale invocation-identity suppression comment was
corrected. This pass adds no dependencies, deployments, managed-device scenarios,
or live-model calls. Managed A12 remains pinned to `7ba99ca`.

### Historical matched performance results — cached revision, superseded

These measurements describe the earlier cached implementation, not the current
code. The user subsequently removed production caching for repeated test-server
construction. They are retained as investigation history, not evidence of the
current runtime, allocation volume, retained heap, or a required speedup. The
current benchmark includes fresh preparation for every constructed server.

Linux/amd64, Go 1.27.1, Ryzen 9 3900X, 24 available CPUs, warm Go build caches.
Each cell is the median of five fresh test-process runs, followed by the range.
Baseline and candidate alternate within each workload and instrumentation mode.
No other validation or simulator workload ran during measurement. Ordinary
desktop processes were not stopped; observed one-minute load ranged from 1.27
to 4.60. The differences substantially exceed the observed run variation.

| Workload | Instrumentation | Baseline | Candidate |
| --- | --- | --- | --- |
| Warm catalog construction | Normal | 272.49 ms/op, 271.11–273.21 | 14.22 ms/op, 13.84–14.84 |
| Warm catalog construction | Race | 2474.17 ms/op, 2467.48–2482.98 | 148.09 ms/op, 147.57–149.00 |
| Cold assembly/connection/listing test | Normal | 0.31 s, 0.31–0.32 | 0.09 s, 0.09–0.09 |
| Cold assembly/connection/listing test | Race | 2.72 s, 2.70–2.73 | 0.58 s, 0.57–0.58 |
| HTTP-only fixture test | Normal | 0.29 s, 0.29–0.30 | 0.02 s, 0.02–0.02 |
| HTTP-only fixture test | Race | 2.55 s, 2.55–2.57 | 0.11 s, 0.11–0.11 |

| Catalog allocation metric | Baseline median | Candidate median |
| --- | --- | --- |
| Normal B/op | 120,228,696 | 7,960,504 |
| Normal allocs/op | 1,974,498 | 98,658 |
| Race B/op | 151,261,438 | 10,611,211 |
| Race allocs/op | 2,059,941 | 104,408 |

These are separate workload results, not a CI-wide or full startup speedup.
HTTP-only setup deliberately removes irrelevant work. Catalog discovery and
results intentionally change, so this is not faster encoding of an unchanged
payload. Indented catalog metadata captures shrink from 934,057 to 79,956 bytes;
those capture sizes are not measurements of network transfer.

Separate CPU and allocation profiles show SDK input remarshal and schema
resolution as prominent remaining costs. Fixed success preparation is no longer
repeated per server. Sampled retained heap was about 11.1 MiB after 200 catalog
constructions and 10.1 MiB after 1,000 in separate fresh processes. No per-server
growth was visible at this sampling resolution. Retained validators and fixed
documents are intentional; cache inspection found no handler, service, or
session ownership. Profile durations are not benchmark samples.

Raw commands, all 60 measurement logs, revisions, ranges, captures, and separate
profiles remain in `/tmp/opencode/hearth-mcp-evidence/`. The runnable comparison
driver is `/tmp/opencode/hearth-mcp-measure.py`. These local artifacts are
evidence, not checked-in runtime dependencies.

### Managed acceptance, upgrade repair, and validation caveats

The first `mise run simulator-start` attempt stopped at the former key-file
prerequisite without starting daemons. The user then approved environment-only
credentials. Commit `bba4416` removes the key-file field, flag, environment
binding, and fallback; startup reads only `HEARTH_OPENAI_API_KEY`. Focused unit,
all-level integration/race, lint/vet, and full completion validation passed;
independent read-only review found no defects. Tests replace inherited
credentials with synthetic values and do not call a live model.

Managed startup of `bba4416` passed credential preflight and started NATS, but
Core PID `3050341` exited at `stage=migrate_database`. Read-only inspection and a
rolled-back migration reproduction on a disposable SQLite backup identify
migration 14: adding Command Step kinds expands two retained Automation
definitions from 65,536 to 65,553 bytes, exceeding the existing 65,536-byte
constraint. The affected IDs are `aut_01a1042e-e82f-7b32-99e6-aaf333100473` and
`aut_01a1042e-e83c-74f2-8aa4-4514b6de66b6`. The database passes `quick_check` and
reports migrations through 13 applied. Startup applied 11–13 before failing;
diagnosis did not edit retained records or reset SQLite/JetStream. This blocker
was subsequently resolved by the user-authorized upgrade repair below.

The failed attempt's owned NATS process was stopped. No A12 actions or model
calls had been issued at that point. Historical failure and cleanup logs remain
in the evidence directory; they are not the final acceptance outcome.

Commit `7ba99ca` repairs the upgrade with a 544-byte bounded allowance, a no-op
version-14 checkpoint, and atomic version-17 table rebuild/conversion. Fresh
databases, pending legacy upgrades, already-current JSON, child-row preservation,
repeated Up, no-op Down, max/max+1 boundaries, exact numbers, and rollback are
covered. The one/32 Command maximum-size cases fail before the fix and pass
afterward. Raw decoding, normalized encoding, and SQLite agree on 66,080 bytes.
The existing limits on tree nodes and side-effect leaves are unchanged.

A consistent version-13 backup is retained at
`/tmp/opencode/hearth-mcp-evidence/before-upgrade-repair.db`. Managed startup
applied version 17 successfully, and all 187 retained Automations preserve their
IDs, revisions, timestamps, and exact parameter values; only executable Command
kinds were added. Both former size-limit rows now occupy 65,553 bytes.
`quick_check` passes and `foreign_key_check` reports no violations. The comparison
is retained as `retained-upgrade-result.json` in the evidence directory. A
post-A12 comparison also confirms all 150 retained Run snapshots preserve their
exact values apart from executable Command kinds (`retained-history-check.json`
in the successful run directory).

The bounded driver `/tmp/opencode/hearth-mcp-smoke.py` completed A12 using the
default managed configuration and real `/mcp` endpoint, without model calls,
household devices, storage replacement, or mutation retries. Mise 2026.10.17
started candidate `7ba99ca6950ade23fd35621cb29833416e04400a` from current source.
Core was `http://127.0.0.1:8080`, simulator control was
`http://127.0.0.1:8181`; these were discovered worktree allocations, not assumed
ports. Raw requests, results, scenarios/budgets, non-secret configuration,
publication IDs, exact JSON tokens, history lookups, and cleanup are retained at
`/tmp/opencode/hearth-mcp-simulator/20261021T195723014910Z/`.
`result.json` reports all six groups passed; `identity-index.json` indexes every
action and identity. A preceding driver-only initialize attempt used incorrect
2026 protocol framing and returned HTTP 400 before any actions; it stopped its
daemons cleanly. The corrected successful run negotiated supported protocol
`2025-11-25` and is not a retry of any side effect.

| A12 group | Observed Core evidence |
| --- | --- |
| Discovery | All 33 names and complete input documents; no output schemas or resources; summary/detail identity and descriptor checks, pagination, unchanged implementation `1.0.0`. |
| Satisfied Command | `cmd_01a1600a-322c-7bc8-957b-ab7d8d95e753`, linked to `obs_01a1600a-3231-7a9b-a0b2-bf070625b699` and true State in MCP and HTTP history. |
| Rejected Command | `cmd_01a1600a-3244-7a7b-b814-4fb7d943f8c4`, durable `rejected` / `upstream_rejected` in MCP and HTTP. |
| Automation | Temporary `aut_01a1600a-3283-752c-832c-9face5099597`, Run `arn_01a1600a-32de-746d-893a-b7ca338cff80`, succeeded with canonical snapshot and verified Command linkage. |
| Functions | `afi_01a1600a-36d7-763f-aaef-5d4208378eec` echoes `9007199254740993`, `1e-10000`, and null exactly; `afi_01a1600a-36e4-79fe-a664-df42c3ccf6c9` retains `failed` / `simulated_failure`. Compact receipts agree with MCP/HTTP detail. |
| Notifications | Accepted `ntf_01a1600a-36ee-78b5-9c5c-54806346c75b`, rejected `ntf_01a1600a-36f8-7346-81b0-00bf4b3d9440`, not-sent `ntf_01a1600a-3701-7402-90c8-008b7df57427`, unknown `ntf_01a1600a-370a-73f6-b29e-959bff2cae75`; matching immutable messages and exactly four loss-free simulator captures. |

Each scenario action was issued once. Cleanup deleted only the owned revision-1
Automation; its absence was independently checked in retained SQLite. All three
saved pause states were restored and checked against control responses. The
driver ran `mise run simulator-stop`; subsequent daemon status confirms NATS,
Core, simulator, and web all stopped. SQLite and JetStream remain intact.

The upgrade follow-up passed focused all-level checks, lint/vet, uncached
all-level affected-package race checks, and full validation. Initial race runs
exposed two lifecycle tests replaying full migration history inside their short
scenario timers. The cancellation failure reproduced in the full Core package;
isolated controls passed. Both tests now use the existing isolated migrated
database fixture before their timers begin. Cancellation/readiness deadlines and
production behavior were not relaxed. Final uncached race output is
`/tmp/opencode/hearth-upgrade-final-race.log`, and the full gate is
`/tmp/opencode/hearth-upgrade-final-validate.log`. The repair's structural and
correctness self-review is `/tmp/opencode/hearth-upgrade-review.md`; it is not an
independent-review claim. No generated or module changes resulted.
The post-A12 closing completion-gate output is retained in
`/tmp/opencode/hearth-mcp-complete-validate.log`.

An earlier D4 full gate observed `SQLITE_BUSY` in the unchanged
`TestCoreHealthDeliveryFailureDoesNotSuspendLeaseExpiry`. Three isolated
diagnostic runs and subsequent full and race gates passed without persistence,
retry, or timeout changes. The intermittent contention was not repaired or
explained away by those passes. Final integration's full gate passed; unchanged
packages may use the normal Go test cache.

Final transport, Agent, and Core validation also passed with
`CI=true GO_PACKAGES='<affected packages>' mise run test -- -count=1`, which
executes all levels under race instrumentation without cached test results.
The exact package selection and output are retained in
`/tmp/opencode/hearth-mcp-final-race.log`; final full-gate output is
`/tmp/opencode/hearth-mcp-final-validate.log`.
The post-credential, final integrated completion-gate log is
`/tmp/opencode/hearth-mcp-env-final-validate.log`; it does not substitute for A12.
