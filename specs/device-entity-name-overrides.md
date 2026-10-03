# Device and Entity name overrides

The HTTP and TypeScript PATCH request contracts below are superseded by
[Device and Entity JSON Merge Patch migration](device-entity-merge-patch.md).
The current HTTP payload uses flat `name_override` and requires
`application/merge-patch+json`. Domain `NameEdit`, persistence, and naming behavior
remain as specified here.

**Status:** Approved; backend implementation and validation complete
**Type:** Feature plan
**Effort:** L, approximately 1 day
**Created:** 2026-09-29
**Revised:** 2026-09-29

## Problem

Household operators need readable Device and Entity names without editing Adapter configuration or external systems. Today, registration supplies names and overwrites them on re-registration. Hearth already separates those names from canonical identity, but has no household-owned name editing.

## Implementation sequence

The user approved this implementation contract on 2026-09-29. Keep work inside the existing `devices` domain, HTTP, SQLite, and web boundaries. Read `internal/modules/devices/README.md` for ownership and `AGENTS.md` for validation commands.

Use the sections and owning paths below. Complete local checks before proceeding and cross-layer checks once their dependencies exist.

| ID | Action and reference | Effort | Depends on | Completion criterion |
| --- | --- | --- | --- | --- |
| D0 | Verify proposed PATCH request models with Huma. Use API models and HTTP contract | S | None | A7's omission/null/schema cases pass against the actual decoder and exported OpenAPI, or a minimal presence-aware solution for demonstrated gaps is recorded before D1–D3 proceed |
| D1 | Add the migration and naming reads. Use Domain models and Persistence schema | M | D0 | Empty/populated migration checks pass; Device and Entity reads expose coherent naming fields. A1 and the SQLite portion of A3 pass |
| D2 | Add validation and atomic metadata writes. Use Domain edits, Persistence interface, and Transaction rules | M | D1 | Direct-repository and Service checks pass for normalization, registration/reset, last-write-wins and mixed-update rollback. Repository portions of A2, A4, A5, A6 pass |
| D3 | Extend Entity PATCH and add Device PATCH. Use API models and HTTP contract | M | D2 | Actual HTTP decoding and exported OpenAPI agree; existing enablement clients work. HTTP/MCP portions of A2–A7 pass |
| D4 | Add detail-page editors. Use Web behavior and TypeScript types | M | D3 | Save/reset/cancel, error retention, and request-identity tests pass. A8 and A9 pass |
| D5 | Integrate and validate all layers. Use Acceptance and validation | M | D1–D4 | A1–A10 pass; generated output and scoped diff are reviewed; `mise run validate` passes or an environment blocker is recorded |

## Backend completion, 2026-09-29

- D1 adds migration 8, nullable overrides and coherent naming reads. Real SQLite tests cover populated version-7 upgrade and override-losing Down/Up; existing migration checks cover empty database creation.
- D2 adds shared normalization and Service/repository input boundaries. SQLite tests cover same-text pinning, no-op timestamps, last-write-wins, re-registration, reopen/reset, offline disabled edits, and mixed-update rollback on write and deferred-constraint commit failure. Existing owner/runtime and idempotent enablement regressions run through the shared workflow.
- D3 replaces the temporary Entity guard with atomic PATCH and registers Device PATCH. Actual HTTP and MCP integration tests cover naming read parity, mixed updates, reset, omission/null/malformed input rejection, error statuses and nullable response schemas. D0's scoped decoder repair remains unchanged.
- Backend A1–A7 and A10 checks are implemented. A10 checks compare nonempty State and pending Device Facts, Commands, canonical IDs, availability and persisted Automation references across metadata edits. D4 and A8/A9 remain outside the backend scope. D5 still requires integrated frontend checks and full validation results.
- `sqlite/metadata.go` owns Entity PATCH and the shared Entity mutation transaction; `sqlite/enablement.go` delegates. `sqlite/device_metadata.go` owns Device PATCH. Nullable conversions reuse `sqlite/null_mapping.go`.

Focused race-enabled Go tests and lint passed. `mise run validate` passed with generation, formatting, module tidying, full lint/vet, race-enabled Go tests, generated Entity-type fixture execution, and existing web tests, 6 files and 32 tests. No web files were edited in this backend change. Frontend acceptance remains pending D4. No isolated mutation/negative-control run was performed for the new backend tests; D0 retains its demonstrated before/after decoder regression evidence.

## Frontend completion, 2026-09-29

- D4 adds shared `NameEditor` to Device and Entity details, required naming read fields, and typed PATCH shapes. The editor sends only `name_edit`, counts Unicode code points, retains drafts and errors through background refresh, and offers explicit retry, cancel, and reset. Labels, status/error roles, and native form submission support keyboard use. Server validation remains authoritative.
- Each editor instance is keyed by object kind, canonical ID, and API-base generation. Unmounted instances ignore late success and failure responses, including selection/server switches away and back. A direct base-URL check also rejects responses after a server change before React cleanup.
- Detail pages immediately merge returned naming fields, then refresh their reads. Device PATCH never replaces the Entity collection. `useApi` exposes its existing data setter for these production update callbacks. Existing typed frontend fixtures now include the required naming fields.
- A8/A9 have 10 HTTP-stub React tests in `web/src/components/NameEditor.test.tsx`. They cover cancel without HTTP, failure/draft retention and explicit retry, null reset, 128 astral code points, pending controls, late success/error after ID/kind/server switches away and back, real Device selection races, and both detail pages preserving drafts through refresh. Device integration keeps its Entity link visible while the post-PATCH detail read remains pending. Entity integration updates the heading while offline/disabled and keeps enablement unchanged.
- `mise run web-test` passes, 7 files and 42 tests. `mise run web-build` passes TypeScript and Vite; Vite reports its large-chunk warning. Integrated `mise run validate` passes generation, formatting, module tidying, lint/vet, race-enabled Go tests, generated Entity-type fixtures, and web tests. Go test results were cached in this run. Generated/backend files received no frontend-owned edits; scoped handwritten diff and whitespace checks were reviewed.
- A9's manual browser/simulator walkthrough passed as recorded below. No frontend mutation run was performed. No commits were made.

### A9 manual browser evidence, 2026-09-29

- Started the real worktree-local stack with `mise run simulator-start`. NATS, Core, simulator and Vite were ready on loopback ports 4222, 8080, 8181 and 5173. The required agent key file was available; no credentials were missing. Used isolated `agent-browser` session `a9-hearth-detail` against `http://127.0.0.1:5173/#/devices`. All edits used existing simulated local objects, not household hardware or remote data. No mock HTTP server was used.
- Selected `Simulated light`, Device `dev_01a0e0bc-bc0e-764d-8060-1c5c88f5527d`. Cancel discarded `A9 canceled device`. Saving `A9 browser light` updated both the list and detail name, kept `Adapter name: Simulated light`, and showed `Name override active` and `Name saved.`. Reset restored `Simulated light`, showed `Using Adapter name` and `Name reset.`, and disabled reset. While editing, Refresh preserved the exact `A9 browser light` draft. Pending save disabled the field, save and cancel controls and showed `Saving name…`.
- After Device save and reset, the detail retained all eight Entity links and their canonical IDs: Power, Temperature, Brightness, Color temperature, Color XY, Color HS, Color mode and Effect. The Power link navigated to Entity `ent_01a0e0bc-bc0e-7654-bd84-aa93e51248f0`.
- On that Entity detail, Cancel discarded `A9 canceled entity`. Refresh preserved the exact `A9 browser power` draft. Save updated the displayed name to `A9 browser power`, kept `Adapter name: Power`, and showed `Name override active` and `Name saved.`. Reset restored `Power`, showed `Using Adapter name` and `Name reset.`, and disabled reset. The Entity remained enabled.
- `/tmp/opencode/hearth-a9/walkthrough.har` records 47 requests. Its only four PATCH requests were Device save/reset and Entity save/reset, each HTTP 200. Save bodies contained only `name_edit.override` with the names above; both resets sent `{"name_edit":{"override":null}}`. No canceled draft produced a PATCH. Responses contained the unchanged canonical IDs and Adapter names, the expected string/null override, and `enabled:true` for the Entity. Scripted State continued changing independently; this walkthrough does not claim A10 State/fact isolation.
- Also delayed delivery of one real, successful Device PATCH response with a temporary browser-page `fetch` wrapper. Saved `A9 delayed device`, waited until Core's response was held, switched to `Simulated climate`, then released it. Climate still displayed `Simulated climate`, its own Adapter name and `Using Adapter name`, with no old success status. Re-selecting Light read the committed `A9 delayed device`; reset restored its original Adapter name. The wrapper changed only response-delivery timing, not HTTP responses or production files. Old-server responses and late error variants retain the automated integration evidence above; they were not manually repeated.
- Screenshots are `/tmp/opencode/hearth-a9/device-save.png`, `device-reset.png`, `entity-save.png`, `entity-reset.png` and `device-late-response.png`. Final Device metadata and all eight links are recorded in `device-final.txt` and `device-entities-final.json` in the same directory. Both edited objects finished with null overrides. Closed the isolated browser and stopped all four started daemons with `mise run simulator-stop`; local simulator storage remains intact.

## Naming semantics

- Device and Entity overrides are independent. Neither changes canonical IDs, Binding keys, Entity keys, external IDs, ownership, or upstream names.
- The effective display name is the override when present, otherwise the latest Adapter-supplied name. Registration always continues updating the Adapter-supplied name.
- Reset removes the override and immediately follows the latest Adapter name. Saving text equal to the Adapter name creates an explicit, pinned override.
- Renaming and resetting work while an Adapter is offline or an Entity is disabled. They do not change State, availability, enablement, Commands, or Automation definitions.
- Names shown for existing references use current metadata rather than historical name snapshots.
- Name edits use last-write-wins, as defined in Transaction rules.

Canonical terms are recorded in `GLOSSARY.md` as Display name and Name override.

## Types

### Domain models, D1

Focused additions in `internal/modules/devices/model.go`:

```diff
 type Device struct {
     ID   DeviceID
     Kind DeviceKind
     Name string
+    AdapterName string
+    NameOverride *string
 }
@@
 type Entity struct {
@@
     Name      string
+    AdapterName string
+    NameOverride *string
```

`Name` remains the effective name. Read mapping must populate all three naming fields coherently. Clone `NameOverride` in `CopyEntityWithState` and any other copy boundary that returns independent domain values.

### Domain edits, D2

New definitions in `internal/modules/devices/metadata.go`:

```go
type NameEdit struct {
    Override *string // nil resets; non-nil is an explicit override
}

type DevicePatch struct {
    NameEdit *NameEdit // nil leaves naming unchanged; empty patches are invalid
}

type EntityPatch struct {
    Enabled *bool // nil leaves enablement unchanged
    NameEdit *NameEdit // nil leaves naming unchanged
}

func NormalizeNameOverride(value string) (string, error)
func (service *Service) PatchDevice(ctx context.Context, id DeviceID, patch DevicePatch) (Device, error)
func (service *Service) PatchEntity(ctx context.Context, id EntityID, patch EntityPatch) (EntityWithState, error)
```

Validate canonical IDs, patch shape, and override text at the Service boundary. The repository is independently callable, so it must enforce the same freely constructed input contract using shared domain validation.

`NormalizeNameOverride` rejects invalid UTF-8 and Unicode control characters before trimming surrounding Unicode whitespace, then requires 1–128 Unicode code points. Blank text is invalid, not a reset. Preserve internal spaces and Unicode spelling without case-folding or normalization. Duplicate names are valid.

Add `ErrInvalidMetadataPatch` in `repository.go`. Invalid override text wraps it. Preserve existing not-found errors.

### API models, D3

Add these fields explicitly to `EntityBody`, `DeviceBody`, and `DeviceDetailBody` in `devices/api/types.go`:

```go
AdapterName string  `json:"adapter_name"`
NameOverride *string `json:"name_override"`
```

`name_override` is always emitted, including JSON null. OpenAPI must mark it nullable.

```diff
 type PatchEntityBody struct {
-    Enabled bool `json:"enabled"`
+    Enabled *bool `json:"enabled,omitempty"`
+    NameEdit *NameEditBody `json:"name_edit,omitempty"`
 }
```

New transport types in `devices/api/metadata_types.go`:

```go
type NameEditBody struct {
    Override *string `json:"override" required:"true" nullable:"true"`
}
type PatchDeviceBody struct {
    NameEdit *NameEditBody `json:"name_edit,omitempty"`
}
```

Before persistence and handler implementation, verify these proposed request models against the actual Huma decoder and exported OpenAPI using the omission/null/schema cases in A7. This D0 check needs no database or mutation implementation. Cover omitted fields, explicit null for `enabled` and `name_edit`, missing `override`, null reset, and string override for both applicable endpoints.

Ordinary Go pointers do not retain the difference between omission and explicit null after decoding. Huma schema validation may reject invalid forms before that distinction is needed. Use the standard request models when runtime validation and exported OpenAPI enforce the contract. Add presence-aware decoding only for a demonstrated gap, scoped to these request models; record the gap and the chosen solution here before proceeding. Preserve the nested `name_edit` contract and the application's general decoding policy. D3 completes A7's mutation-safety and error-mapping checks against the implemented handlers.

#### D0 findings, verified 2026-09-29

Decoder-only Echo/Huma operations using the production request models and Huma v2.39.1 demonstrated that the proposed pointers export the intended schemas. `enabled` is an optional non-null boolean, `name_edit` is an optional non-null object, and its required `override` accepts string or null. Omission, false enablement, missing override rejection, null reset, and string override decode as intended.

The unmodified proposed models nevertheless accepted `enabled:null` and `name_edit:null` with HTTP 200. Mixed requests with one null field also reached the handler. Huma's object validator skips null optional properties independently of the property's nullability, so pointers and `nullable:"false"` cannot enforce this contract alone.

The minimal repair is scoped `UnmarshalJSON` methods on `PatchEntityBody` and `PatchDeviceBody`. They decode the ordinary pointer models, inspect raw top-level property presence, and reject explicit null for `enabled` and `name_edit`. Huma reports those typed-decoding errors as 422 before calling the handler. No global decoder configuration, general unknown-field policy, or persistent presence flags are changed. Matching follows the existing case-insensitive JSON field policy. Required nested `override` remains schema-enforced; null reset remains valid.

`api/metadata_types_test.go` verifies runtime rejection and handler non-invocation plus schemas fetched through `/openapi.json`. The same cases failed before the request-local repair and passed afterward. Empty objects intentionally pass structural decoding with both pointers nil. D2/D3 must reject unsupported/empty patches semantically with 400, enforce freely constructed inputs independently, and finish A7 against real mutation handlers. D0 does not add the Device PATCH route or implement naming mutations. The existing Entity handler only has a temporary guard for nil enablement or unimplemented name edits and pointer adaptation; D3 must replace its enablement-only call with atomic `PatchEntity`, retaining MCP enablement-only behavior.

### TypeScript types, D4

Add `adapter_name: string` and `name_override: string | null` to the existing TypeScript `Device` and `Entity` interfaces in `web/src/api/types.ts`. New request shapes:

```ts
export interface NameEdit {
  override: string | null;
}
export interface DevicePatch { name_edit?: NameEdit }
export interface EntityPatch { enabled?: boolean; name_edit?: NameEdit }
```

## Persistence interface, D2

New persistence seam in `devices/repository.go`:

```go
type PatchDeviceParams struct {
    DeviceID DeviceID
    Patch DevicePatch
    UpdatedAt time.Time
}
type PatchEntityParams struct {
    EntityID EntityID
    Patch EntityPatch
    UpdatedAt time.Time
}
type MetadataRepository interface {
    PatchDevice(context.Context, PatchDeviceParams) (Device, error)
    PatchEntity(context.Context, PatchEntityParams) (EntityWithState, error)
}
```

Add `Metadata MetadataRepository` to `Stores` in `service.go`, a compile-time implementation check and `Metadata: repository` to `sqlite/repository.go`.

Preserve the Service's `SetEntityEnabled` and `SetOwnedEntityEnabled` entry points and the repository's `SetEntityEnabled` operation. The owning Adapter's NATS/SDK enablement contract remains unchanged and does not gain naming edits.

The repository's `SetEntityEnabled` and `PatchEntity` must delegate to one private Entity mutation workflow in `sqlite/metadata.go`. That workflow owns transaction begin/rollback/commit, optional runtime and owner checks, current-row loading, requested-field comparison, writes, and final row mapping. Existing enablement operations supply an enablement-only edit and their existing fencing requirements; management PATCH supplies its requested fields without Adapter fencing. Preserve the existing runtime-before-Entity and owner-check ordering. Validate freely constructed inputs at each public boundary using shared domain validation.

Do not retain a parallel transaction implementation in `sqlite/enablement.go`, duplicate enablement policy, or call an independently committing public operation from PATCH. Preserve existing owner/runtime, not-found, and idempotency regressions, and add mixed-PATCH rollback checks against the shared workflow.

## HTTP contract, D3

| Endpoint | Request | Success |
| --- | --- | --- |
| `PATCH /v1/devices/{device_id}` | Optional `name_edit`; at least one supported field required | 200 Device body |
| `PATCH /v1/entities/{entity_id}` | `enabled`, `name_edit`, or both | 200 Entity body with current State/availability |

Example rename: `{"name_edit":{"override":"Kitchen ceiling"}}`.
Example reset: `{"name_edit":{"override":null}}`.
Existing `{"enabled":false}` requests remain valid.

- Omitted fields mean unchanged. Both endpoints require at least one supported field; naming is currently the only editable Device field.
- Explicit null for `enabled` or `name_edit` is invalid.
- Within a present `name_edit`, `override` is required. Explicit null means reset; missing `override` is invalid.
- Preserve the application's existing unknown-field handling; do not change general API decoding policy for this feature.
- Invalid canonical ID: 400. Unknown canonical object: 404. Semantic metadata validation failure: 400. Structural/schema validation uses existing Huma 422 behavior. Unexpected storage failure: 500 without leaking internals.
- Responses and GET/list/MCP resources use the same naming-field mapping.
- Device PATCH returns metadata only. Its response is not the paginated Device GET detail body.

## Persistence schema, D1

Keep database `name` columns as the latest Adapter names. Derive effective names in read queries instead of persisting a second effective-name value.

Create migration `00008_device_entity_name_overrides.sql`. For both `devices` and `entities`, add:

```sql
name_override TEXT NULL
```

Add a nullable override length constraint where supported by the migration: null or 1–128 SQLite characters. Domain validation owns Unicode/control-character policy. Existing names and IDs are preserved, and all existing overrides start null.

Recreate `entity_read_projection` with `COALESCE(e.name_override, e.name) AS name`, `e.name AS adapter_name`, and `e.name_override`. Preserve every other projection column and join. Update Device SELECTs similarly. Leave registration descriptor writes and inserts using the stored Adapter name and column defaults.

Migration Down restores the original Entity view before removing the added columns. Downgrade discards overrides, restoring Adapter names; document this loss and require a database backup before downgrade.

## Transaction rules, D2

Each PATCH transaction loads the current object, writes all requested changes atomically, and returns the mapped current row. Update only fields present in the request, preserving unrequested metadata. Validation errors and commit failures leave both enablement and naming unchanged.

A no-op succeeds without changing metadata timestamps. Compare the stored nullable override, not just effective text: pinning text equal to the Adapter name and resetting that pin are real metadata changes. Adapter registration leaves override columns untouched. Metadata timestamps change only when an actual requested field changes.

The last successfully committed override edit or reset takes effect. Clients may submit from an old read without a precondition. SQLite busy/lock errors follow the repository's existing contention policy and are not successful edits. Do not add automatic conflict retries.

## Web behavior, D4

Create `NameEditor.tsx` shared by the selected Device detail panel and Entity detail page. Its inputs are canonical object ID, object kind, current naming metadata, and a successful-update callback. It owns edit text and pending/error state; it sends only a name edit through `apiFetch`.

- Details show the current effective name, Adapter name, and whether an override is active.
- Rename opens a labeled text field initialized to the effective name. Save submits the entered override. Cancel makes no request.
- A separate reset action sends null override. Disable reset if no override is active.
- Disable submission while a request is pending. Server validation is authoritative; client counts code points rather than UTF-16 code units.
- On success, update the displayed naming metadata from the response and refresh the owning detail/list data. Do not use a Device PATCH response to replace the Entity collection.
- On failure, retain the draft, show the error, and allow explicit retry without a preliminary re-fetch.
- Background data refresh preserves an open draft. Scope pending results to object ID and current API base URL; switching selection/server must not apply an old response to the new object.
- Provide accessible field labels, status/error text, and keyboard-operable actions.

## Project layout and ownership

```text
GLOSSARY.md                                     # modify: settled naming vocabulary
specs/
└── device-entity-name-overrides.md              # this implementation contract
internal/
├── platform/db/migrations/
│   └── 00008_device_entity_name_overrides.sql   # new D1: columns and view migration
└── modules/devices/
    ├── model.go                                # modify D1: naming read fields
    ├── metadata.go                             # new D2: validation and patch use cases
    ├── metadata_test.go                        # new D2: observable domain behavior
    ├── repository.go                          # modify D2: params, errors, persistence seam
    ├── service.go                             # modify D2: metadata capability
    ├── observation.go                         # modify D1: independent name pointer copies
    ├── README.md                              # modify D2: atomic metadata ownership
    ├── api/
    │   ├── types.go                           # modify D3: naming reads, optional enablement
    │   ├── metadata_types.go                  # new D3: name-edit request models/validation
    │   ├── patch_device.go                    # new D3: Device PATCH handler
    │   ├── patch_entity.go                    # modify D3: atomic Entity PATCH handler
    │   ├── mapping.go                         # modify D3: naming read mapping
    │   ├── register.go                        # modify D3: new route and error declarations
    │   └── metadata_test.go                   # new D3: HTTP decoding, updates, schema
    └── sqlite/
        ├── repository.go                     # modify D2: capability assembly
        ├── metadata.go                       # new D2: Entity PATCH and shared mutation transaction
         ├── device_metadata.go                # new D2: Device PATCH transaction
        ├── enablement.go                     # modify D2: delegate existing operation to shared Entity mutation
        ├── metadata_test.go                  # new D1/D2: migration, persistence, atomicity
        ├── device_reads.go                   # modify D1: Device naming row mapping
        ├── entity_mapping.go                 # modify D1: Entity naming row mapping
        ├── dbqueries/
        │   ├── state.sql                     # modify D1: naming read columns
        │   └── metadata.sql                  # new D2: metadata updates
        └── dbsqlc/                           # generated D1/D2: regenerate, never hand-edit
web/src/
├── api/types.ts                               # modify D4: naming reads/request types
├── components/
│   ├── NameEditor.tsx                         # new D4: shared editor and error handling
│   └── NameEditor.test.tsx                    # new D4: save/reset/error/race behavior
└── pages/
    ├── DevicesPage.tsx                        # modify D4: selected Device editor
    └── EntityDetailPage.tsx                   # modify D4: Entity editor
```

Preserve behavior in every row construction/mapping affected by generated projection changes. D5 integrates all deliverables and generated artifacts.

## Acceptance and validation

| ID | Boundary and expected result | Check and procedure |
| --- | --- | --- |
| A1 | Migrations preserve IDs, Adapter names and existing Entity data; overrides start null; Down/Up follows documented loss policy | Real SQLite tests under `devices/sqlite`: Up from empty and populated version 7, then Down/Up on a disposable database |
| A2 | Device and Entity override survive re-registration, Adapter name updates and database reopen; reset reveals latest Adapter name; same-text override stays pinned | Repository/API integration tests: register, edit, register changed descriptors, reopen DB, reset |
| A3 | All Device/Entity list, detail, PATCH and MCP reads expose coherent effective name, Adapter name and nullable override | HTTP/MCP read tests using migrated SQLite |
| A4 | Later accepted override edits/reset replace earlier ones without preconditions; no-ops preserve timestamps; Adapter and enablement updates preserve overrides | Real SQLite tests with successive writes from clients that read the same initial metadata |
| A5 | Mixed PATCH commits both fields or neither; name-only PATCH preserves enablement; existing enablement-only and Adapter-fenced calls retain behavior | HTTP and SQLite behavior tests plus existing enablement regressions |
| A6 | Unicode code-point bounds, whitespace trimming, blank/control/invalid-UTF-8 rejection, duplicate names and preserved internal spaces follow contract | Domain and direct-repository boundary tests, including 128/129 multibyte characters |
| A7 | Required/null/omitted field rules and 400/404/422 mappings work; exported OpenAPI matches runtime; malformed requests mutate nothing | HTTP table tests against real decoder plus exported OpenAPI assertions |
| A8 | Detail controls save, cancel and reset correctly; failures retain the draft, show an error, and allow explicit retry | React tests with HTTP boundary stubs |
| A9 | Editor ignores old object/server responses and retains its draft during refresh; successful Device edit preserves Entity collection | React integration tests and manual Device/Entity detail walkthrough |
| A10 | Renames work offline/disabled and create no Commands, State changes or Device Facts; IDs/Automation references remain unchanged; generated output reproducible | Integration tests comparing those public/persistent effects; regenerate and review generated diff |

Run `mise run --skip-deps test` for Go checks, `mise run web-test` and `mise run web-build` for web checks, and `mise run --skip-deps generate` for generation. Finish with `mise run validate` and review generated, formatting, and module changes.

Validation uses disposable local data, not production names. Go/full checks require Docker for existing Mosquitto tests. For an optional simulator walkthrough, follow the start/stop instructions in `AGENTS.md` and setup prerequisites in `README.md`.

When authoring or changing Go tests, load `test-audit` and `go-test-effectiveness`. Verify behavior through domain, HTTP, and real SQLite boundaries.

## Scope limits

This feature provides independent detail-page name overrides only. Derived Entity names, bulk/list editing, rename audit records, name-change events, name-triggered Automations, and Adapter protocol changes remain out of scope. Naming semantics defines identity and upstream-name preservation; Transaction rules defines concurrency.

## Approval

Domain scope and this implementation contract were approved by the user on 2026-09-29. No unresolved product questions remain. Requested contract changes must update this document.
