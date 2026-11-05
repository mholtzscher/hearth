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

State schemas derive selectable scalar fields, including numeric fields inside
objects. The optional binding `field` is a JSON Pointer; omission and `""` select
the root. The asserted `entity_type` must match the actual Entity. Numeric fields
use the schema's `hearth` unit annotation, or dimensionless `1` by default.
Dynamic units reference support and cannot change on
re-registration. Cards never store
a display unit. The household's `household_unit_system` (`metric` or `us`,
served by `GET /v1/household`) chooses it, and the web units module converts
with UCUM: temperature shows °C or °F, wind speed m/s or mi/h, pressure hPa or
in Hg. Units that aren't UCUM codes show as stored. Current State and
historical Observations are distinct. History has 288 buckets over exactly one,
six or 24 hours, or seven days, with min/max and arithmetic sample mean from retained accepted
Observations through the Entity history query. Both applied and unchanged values
count. Optional fields request `nulls:"gap"`: any reported absent selected value
makes all numeric metrics null for that bucket. Empty buckets are also gaps.
Later malformed values still fail the query; missing values are never averaged
away. No seed or inferred interval fills a gap. Pruned observations are not
reconstructed, and current State is never inserted into history. Sample means
are neither time-weighted nor circular means (including hue).

One chart shares a type, field, window and resolved canonical unit. Brightness
State and Commands use integer percentages from 0–100; cards display the actual
normalized observations without device-specific scaling. Adapters round Commands
to native levels, and Core satisfaction allows a one-percentage-point difference.
Hue/saturation share one current Entity read but use distinct history requests.
Color temperature stays in mireds; xy values display as four-decimal fractions.
Inactive color modes do not hide their numeric values.

An absent optional field shows **No value**, not zero or **Never observed**.
Availability, enablement, missing Entities, never-observed State, and request
failure are separate conditions. An old observation alone does not imply an
unavailable Entity. Failed refreshes preserve the last reading with a visible
failure indicator and observation time.

## Ownership and extension

| Owner | Responsibility |
| --- | --- |
| `contracts/views/v1` | Generated portable JSON Schema and shared examples |
| `contracts/state-fields/v1` | Generated scalar field catalog: pointers, presence, value types and unit semantics |
| `internal/modules/dashboards` | Strict write validation, bindings, presentation and saved definitions |
| `internal/modules/devices` | Canonical State, history queries, generated field lookups and immutable interpretation |
| `internal/modules/agent` | Durable tool results, selected user context, public history/SSE projections |
| `web/src/features/views/catalog` | Composed schemas and read compatibility |
| `web/src/features/views/cards` | Numeric/text rendering and local controls |
| `web/src/features/views/bindings` | One field adapter driven by the generated catalog |
| `web/src/features/units` | UCUM conversion and the household unit-system policy |
| `internal/modules/household` | Read-only household settings (`GET /v1/household`) |
| `web/src/features/views/data` | Shared cache and refresh lifecycle |
| `web/src/features/conversation` | Hearth history/SSE state and assistant-ui runtime |

To add a visual form, add its schema, binding declaration, component and editor
under `cards/`, then register it in the catalog and renderer. Add a server binding
extractor if it declares reads. Run `mise run generate` and update shared examples.

To expose a scalar field, declare it in the State schema; numeric fields are
automatically eligible. Add a `hearth` annotation when the field has a unit,
then run `mise run generate`; no dashboard allowlist changes.
See [State annotations](../entitytypes/README.md) for supported schema forms.
Boolean/string fields are catalogued but do not yet have cards. To change which
units a household system prefers, edit `web/src/features/units/systems.ts`.

The view catalog advertises numeric field/type pairs and rejects actions,
expressions, URLs and arbitrary properties. A future control needs a separate typed action
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

The State-field cutover requires a fresh database and coordinated Core, SDK and
Adapter versions. Fixed units no longer appear in support JSON, and brightness
support contains neither native maximum nor step. Old support data
is unsupported; there is no compatibility decoder or migration. The operator
must stop writers and reset the database before restarting the new runtime (or
choose a separate fresh fixture path). Do not delete `.data/` wholesale: preserve
design assets, screenshots, recordings, verification evidence and local specs.
Recreate fixture Entities and explicitly seed saved dashboards; generated drafts
still never save themselves. A retained old backup requires its matching old
runtime/configuration, not the new runtime.

Migrations 26 and 27 add the accepted-observation time index and independent
dashboard table. They do not rewrite existing conversation or device evidence.
Rolling back migration 27 drops saved dashboards; migration 26 drops only its
index. Keep a database backup when rolling back a deployment.

The checked-in schema is embedded in Go. Regeneration needs installed frontend
dependencies and runs through the normal mise generation task. The web container
build uses the repository root context (`mise run web-docker-build`) so shared
contract examples are available to typechecking.
