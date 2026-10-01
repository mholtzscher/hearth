# Device and Entity JSON Merge Patch migration

Status: Implemented and locally validated. Deployment pending.

## Decision

Replace the nested `name_edit` HTTP payload with flat JSON Merge Patch requests on the existing Device and Entity PATCH routes. Require `Content-Type: application/merge-patch+json`. Update the backend and web client together, with no compatibility period.

This is a transport migration within the Devices module. Existing domain edits, persistence, name normalization, transactions, response models, and MCP behavior remain unchanged.

## Current implementation

- `api/metadata_types.go` uses `NameEditBody{Override *string}` inside an optional `name_edit` pointer to distinguish omission from reset.
- `api/patch_device.go` and `api/patch_entity.go` translate request bodies into domain `devices.NameEdit` values.
- `devices/metadata.go` validates and normalizes edits. Its service boundary rejects empty patches.
- `api/register.go` registers both operations through Huma.
- `web/src/components/NameEditor.tsx` sends the nested shape. `EntityDetailPage.tsx` sends enablement PATCH requests.
- `web/src/api/client.ts` defaults body-bearing requests to `application/json`, but callers can override that header.

## HTTP contract

| Route | Allowed request properties |
|---|---|
| `PATCH /v1/devices/{device_id}` | `name_override` |
| `PATCH /v1/entities/{entity_id}` | `name_override`, `enabled` |

Example rename:

```http
PATCH /v1/devices/{device_id}
Content-Type: application/merge-patch+json

{"name_override":"Kitchen switch"}
```

Example atomic Entity update:

```http
PATCH /v1/entities/{entity_id}
Content-Type: application/merge-patch+json

{"name_override":null,"enabled":false}
```

Rules:

- An omitted property is unchanged.
- A string `name_override` sets the override using existing normalization. Preserve UTF-8 validation, control-character rejection, trimming, and the 1–128 Unicode code point limit.
- `name_override: null` removes the override. The canonical GET/PATCH response continues emitting `name_override: null`, and `name` falls back to `adapter_name`.
- A boolean `enabled` sets enablement. `enabled: null` attempts to remove required enablement and is invalid.
- `{}` is a successful no-op for an existing resource. Return its current full representation without mutation. An absent resource still returns 404.
- Reject unknown and read-only properties, including `name_edit`, `name`, `adapter_name`, `state`, and Device `enabled`. Property names are case-sensitive.
- The patch must be a JSON object. RFC 7396 also defines whole-document replacement by non-object values; these resources reject replacement because it cannot produce a valid resource with its immutable fields intact.
- No resource creation through PATCH. Preserve the existing 404 behavior.
- Preserve 200 responses and their current Device/Entity shapes. The new request media type does not change response media types.
- Parse Content-Type as a media type rather than comparing raw header text. Accept valid parameters, such as `application/merge-patch+json; charset=utf-8`. Missing, malformed, or other Content-Type values return 415 before request decoding or service invocation.
- Advertise `Accept-Patch: application/merge-patch+json` on these PATCH responses. Adding OPTIONS routes is outside scope.

### Errors

| Status | Meaning |
|---|---|
| 400 | Invalid canonical ID, malformed JSON, or invalid normalized name |
| 404 | Resource not found |
| 415 | Missing, malformed, or unsupported Content-Type |
| 422 | Non-object patch, unknown/read-only property, incorrect property type, or `enabled: null` |
| 500 | Existing internal failure mapping |

All failed requests leave metadata and enablement unchanged. An invalid name combined with valid enablement must not partially change enablement. Preserve existing problem response conventions.

## Implementation shape

### API types and decoding

Keep omission tracking local to HTTP request models. Do not introduce a shared generic optional-value framework or change domain models solely to match JSON.

```diff
diff --git a/internal/modules/devices/api/metadata_types.go b/internal/modules/devices/api/metadata_types.go
@@
-type NameEditBody struct {
-    Override *string `json:"override" required:"true" nullable:"true"`
-}
 type PatchDeviceBody struct {
-    NameEdit *NameEditBody `json:"name_edit,omitempty"`
+    NameOverride *string `json:"name_override,omitempty" nullable:"true"`
+    nameOverridePresent bool
 }
diff --git a/internal/modules/devices/api/types.go b/internal/modules/devices/api/types.go
@@
 type PatchEntityBody struct {
     Enabled *bool `json:"enabled,omitempty"`
-    NameEdit *NameEditBody `json:"name_edit,omitempty"`
+    NameOverride *string `json:"name_override,omitempty" nullable:"true"`
+    nameOverridePresent bool
 }
```

Replace the existing request-specific `UnmarshalJSON` implementations. Inspect raw top-level properties to validate the object shape, enforce the exact allowed keys, detect `name_override` presence, and reject explicit null enablement. Decode supported values with their concrete types. Reset the entire receiver on each successful decode so reuse cannot retain presence state.

Schema validation and decoding must agree, including Huma's handling of optional null properties. OpenAPI must describe nullable optional `name_override`, optional non-nullable boolean `enabled`, and `additionalProperties: false`. Remove the obsolete `NameEditBody` request schema. Verify runtime `/openapi.json`, not only Go struct tags.

### Handler mapping and interfaces

Replace `domainNameEdit(*NameEditBody)` with the following transport-local mapping:

```go
func domainNameEdit(present bool, override *string) *devices.NameEdit
```

Return nil when absent; otherwise return `&devices.NameEdit{Override: override}`. The service interfaces remain:

```go
PatchDevice(context.Context, devices.DeviceID, devices.DevicePatch) (devices.Device, error)
PatchEntity(context.Context, devices.EntityID, devices.EntityPatch) (devices.EntityWithState, error)
```

For nonempty patches, keep using these mutation methods. For `{}`, use existing `GetDevice`/`GetEntity` methods after ID validation and map the current object to the existing PATCH output. Do not call mutation methods or relax domain empty-patch validation. Device no-op responses remain `DeviceBody`, not the Device detail aggregate.

Keep media-type enforcement scoped to these two operations in the module's registration layer. Run it before Huma decodes the body. Configure request documentation separately from decoder support; merely advertising a media type in OpenAPI is not enforcement. Inspect the installed Huma version's extension points during implementation and verify behavior with requests through the real application router. Do not change application-wide JSON decoding or command content types.

### Web types and callers

```diff
diff --git a/web/src/api/types.ts b/web/src/api/types.ts
@@
-export interface NameEdit {
-  override: string | null;
-}
 export interface DevicePatch {
-  name_edit?: NameEdit;
+  name_override?: string | null;
 }
 export interface EntityPatch {
   enabled?: boolean;
-  name_edit?: NameEdit;
+  name_override?: string | null;
 }
```

NameEditor sends `{ name_override: override }`. Both NameEditor and EntityDetailPage's enablement caller explicitly set `content-type: application/merge-patch+json`. Keep `apiFetch`'s default `application/json` for other operations. Update request assertions and fetch fakes; test enablement-only requests as well as rename/reset requests.

### Project layout

```text
internal/
├── modules/devices/api/
│   ├── metadata_types.go        # modify: presence-aware flat decoding
│   ├── merge_patch.go           # new: operation-scoped MIME enforcement and JSON decoding
│   ├── types.go                 # modify: Entity patch model
│   ├── patch_device.go          # modify: mapping and no-op read
│   ├── patch_entity.go          # modify: mapping and no-op read
│   ├── register.go              # modify: media policy and OpenAPI
│   ├── metadata_types_test.go   # modify: decode/schema contract
│   ├── metadata_test.go         # modify: persisted HTTP behavior
│   └── patch_entity_test.go     # modify: existing request fixtures
└── app/hearthd/
    └── http_handler_test.go     # modify: real-router/OpenAPI checks
web/src/
├── api/types.ts                 # modify: flat request types
├── components/
│   ├── NameEditor.tsx           # modify: payload and header
│   └── NameEditor.test.tsx      # modify: rename/reset request assertions
└── pages/
    ├── EntityDetailPage.tsx     # modify: enablement request header
    └── related tests/fakes     # modify: affected request contracts
specs/
├── device-entity-merge-patch.md  # new: this migration proposal
└── device-entity-name-overrides.md # modify during implementation: superseded HTTP contract note
```

Domain, repository, migration, event, and configuration types do not change. The module API package owns merge-patch decoding and HTTP policy. No generic merge engine is needed because all currently writable properties are scalars.

## Deliverables

| ID | Outcome | Effort | Owners | Dependencies | Acceptance |
|---|---|---|---|---|---|
| D1 | Flat decoding, mapping, and no-op behavior | M | Module API models, handlers, and tests | None | A1–A4 |
| D2 | Required media type, Accept-Patch, and accurate OpenAPI | M | `api/merge_patch.go`, `api/register.go`, module tests, `http_handler_test.go` | D1 | A5–A6 |
| D3 | Web rename/reset/enablement cutover | S | Web files above and affected fakes | D1, D2 | A7 |
| D4 | Contract documentation, full validation, coordinated release | S | Existing naming spec and all affected paths | D1–D3 | A8 |

Total estimate: M to L, roughly one working day including Huma integration and validation.

## Acceptance and validation

- A1: Rename, reset, and omission behave correctly on both endpoints. Unicode, trimming, and invalid-name behavior are preserved.
- A2: Combined Entity name/enablement edits are atomic. A rejected combined request changes neither field.
- A3: `{}` returns 200 with the current PATCH representation and no mutation; nonexistent resources return 404.
- A4: Old shape, unknown/read-only fields, case variants, non-object bodies, wrong types, and null enablement fail without mutation. Malformed JSON returns 400.
- A5: Only the merge-patch media type is accepted, including valid parameters. Unsupported/missing/malformed types return 415 before decode or service invocation. PATCH responses advertise Accept-Patch.
- A6: Runtime OpenAPI lists only the new request media type, exact writable properties and nullability, rejects additional properties, documents no-op behavior and errors, and preserves operation IDs and response schemas.
- A7: Actual web requests use the flat body and required header for rename, reset, and enablement. GET and Command requests retain their existing behavior.
- A8: No persistence/domain/MCP changes are required. Existing domain, event, and database regression checks pass. The old HTTP contract is marked superseded and backend/web release together.

| Criteria | Check | Command | Prerequisites |
|---|---|---|---|
| A1–A6 | API tests with real-router checks and SQLite-backed mutation assertions | `GO_PACKAGES='./internal/modules/devices/api ./internal/app/hearthd' mise run --skip-deps test` | Project Go toolchain |
| A7 | Web request assertions and UI regression suite | `mise run web-test` and `mise run web-build` | Web dependencies |
| A8 | Full validation and resulting diff review | `mise run validate` | Reachable Docker daemon for real-Mosquitto tests |

During implementation, apply the repository's test-authoring skills before modifying tests. Inspect generated, formatting, and module changes after validation; include only intended changes.

## Rollout and risks

1. Implement backend, OpenAPI, and web changes in one release unit.
2. Release backend and web together. Reload open browser sessions to load the new client.
3. Verify rename, reset, and enablement against the deployed routes with the required header.

| Risk | Mitigation |
|---|---|
| Huma defaults accept JSON or document the wrong media type | Operation-scoped early enforcement plus real-router and runtime OpenAPI tests |
| Omission and null collapse into the same Go pointer value | Explicit transport-local presence flag and reset/omission regression cases |
| Cached web clients or external callers use the old contract | Announce breaking change, release backend/web together, require reload; no silent fallback |
| General merge-patch semantics are mistaken for permission to edit all response fields | Explicit writable-field schemas and strict runtime key validation |

No compatibility decoder, new writable fields, generic patch framework, ETags, optimistic concurrency, database migrations, or new OPTIONS routes are included. Concurrency behavior remains the existing field-level last-writer behavior; do not implement read-modify-write of the whole resource.

Open questions: none for the proposed contract. Huma integration details must be verified against the installed version during implementation.

## Implementation evidence

- Huma v2.39.1 supports request media-type documentation through the Body field's `contentType` tag. Operation middleware enforces the required MIME type before decoding.
- The operation-local `mergePatchAPI` delegates JSON decoding to the existing decoder after MIME validation. It forwards `OperationDocumenter` to preserve route-group prefixes and modifiers in OpenAPI. Other operations retain their existing decoder and content types.
- Removed the synthetic decoder-route fixture. Real HTTP mutation tests now own payload rejection, omission/reset, and storage safety; a small decoder-reuse test independently checks that request models cannot retain old presence state.
- `mise run validate` passed, including race-enabled Go tests, lint, vet, generated fixture execution, all 43 web tests, and the web build. Generation and module tidying produced no unrelated changes. Vite still reports its bundle-size warning.
- No domain, persistence, configuration, or MCP production files changed. Backend/web deployment and deployed-route smoke checks remain release tasks.
