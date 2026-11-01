# Agent-first dashboards

Home is a conversation workspace with a live canvas. Ask Hearth for a temperature
view, open it, adjust it, then explicitly save it. Dashboards reopen independently
of their originating conversation. Development tools retain their existing URLs
under secondary navigation.

## Drafts and saves

`present_view` returns an immutable `hearth.view` artifact. Its successful agent
tool-result commit retains it in conversation history. Presentation alone never
saves a dashboard, including when an external MCP client calls the tool.

Manual edits and agent revisions change a browser draft. **Save dashboard**
creates an independent record; **Save changes** replaces the selected saved base
at its expected revision; **Save as new** creates another record. A conflict
keeps the draft and offers reload or save-as-new. Refreshing or opening a view
does not change its saved revision or execute device Commands.

The selected draft accompanies customization as user context. The browser owns
the base dashboard ID/revision; model output cannot redirect a save. Unsaved
manual edits are session state. Original generated artifacts remain recoverable
while their conversation is retained.

## Live readings and history

Definitions store canonical Entity references, never captured readings. Visible
views share a connection-scoped request cache. Current State refreshes every ten
seconds; charts refresh at UTC minute boundaries. Focus and restored connectivity
refresh immediately, hidden pages pause timers, and unused subscriptions stop.

Temperature is stored in milli-Celsius and converted only for display. Current
State and historical Observations are distinct. History has 288 buckets over
exactly six or 24 hours, with counts, min/max and mean from retained accepted
Observations. Both applied and unchanged values count. Empty buckets are gaps;
pruned observations are not reconstructed, and the current reading is never
inserted into history.

Availability, enablement, missing Entities, never-observed State, and request
failure are separate conditions. An old observation alone does not imply an
unavailable Entity. Failed refreshes preserve the last reading with a visible
failure indicator and observation time.

## Ownership and extension

| Owner | Responsibility |
| --- | --- |
| `contracts/views/v1` | Generated portable JSON Schema and shared examples |
| `internal/modules/dashboards` | Strict write validation, bindings, presentation and saved definitions |
| `internal/modules/devices` | Canonical Entity State and bounded temperature aggregation |
| `internal/modules/agent` | Durable tool results, selected user context, public history/SSE projections |
| `web/src/features/views/catalog` | Composed schemas and read compatibility |
| `web/src/features/views/cards` | Numeric/text rendering and local controls |
| `web/src/features/views/bindings` | Typed read adapters and display conversion |
| `web/src/features/views/data` | Shared cache and refresh lifecycle |
| `web/src/features/conversation` | Hearth history/SSE state and assistant-ui runtime |

To add a visual form, add its schema, binding declaration, component and editor
under `cards/`, then register it in the catalog and renderer. Add a server binding
extractor if it declares reads. Run `mise run generate` and update shared examples.

To add a numeric resource, compose its source/display contract and typed adapter
with the registry. Reuse Metric, TimeSeries, cache and shell. Its owning module
supplies any new query. A nonnumeric resource can define its own render model;
events and editors need not be forced into numeric cards.

The initial catalog advertises temperature only and rejects actions, expressions,
URLs and arbitrary properties. A future control needs a separate typed action
adapter invoking its owning service, with capability and outcome semantics.
Reading, rendering and dashboard saving must never dispatch such actions.

## Compatibility

`schema_version` identifies the envelope/graph format; `catalog_version` identifies
component and binding semantics. Additive changes preserve old definitions.
Breaking semantics require a new major version and retained old support or an
explicit migration preview. Reading never migrates or saves automatically.

Stored definitions remain raw JSON in read responses. Unsupported nodes become
placeholders; unsupported containers suppress their subtree. Unsupported majors
or invalid graphs produce a whole-view fallback. Original JSON is downloadable,
and definition edits and customization remain disabled while content is
unsupported. Explicit revision-checked deletion remains available.

## Agent integration

Eino owns iteration, parallel dispatch, native call identities, instructions,
skill loading and dangling-call repair. Hearth extends its existing trace hooks
with product projections. A view event follows the successful tool-result commit,
even if final prose later fails. Failed writes and input-only repair placeholders
cannot create view evidence. Private Responses continuation items stay private
and unchanged. See [agent skills](agent-skills.md) for provider migration.

## Persistence and deployment

Migrations 26 and 27 add the accepted-observation time index and independent
dashboard table. They do not rewrite existing conversation or device evidence.
Rolling back migration 27 drops saved dashboards; migration 26 drops only its
index. Keep a database backup when rolling back a deployment.

The checked-in schema is embedded in Go. Regeneration needs installed frontend
dependencies and runs through the normal mise generation task. The web container
build uses the repository root context (`mise run web-docker-build`) so shared
contract examples are available to typechecking.
