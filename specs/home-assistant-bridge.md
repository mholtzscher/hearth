# Home Assistant Bridge Foundation - Implementation Spec

**Status:** Draft; behavior approved, two mapping details open
**Type:** Product capability and contract replacement
**Effort:** XL (independent staged delivery)
**Approved by:** User design confirmation
**Date:** 2026-08-25
**Baseline:** `main` at `e7003d3`

## Problem

The working Home Assistant process is documented and configured as disposable migration code. It registers one configured power Entity, has no durable Adapter-instance model, exposes no process or upstream health, cannot distinguish stale State from an available Entity, and has no supported packaging or Home Assistant compatibility policy.

Home Assistant is instead a valid permanent source of device ownership. Hearth needs a first-party inbound Bridge that any Household can run indefinitely while preserving the existing isolation rule: Home Assistant identifiers, payloads, service calls, and lifecycle assumptions must not enter the core domain or generic wire contracts.

This milestone productionizes the existing vertical slice. It does not pursue Home Assistant parity.

## Decision

Ship `hearth-adapter-homeassistant` in lockstep with Hearth as a permanent inbound Bridge. One Bridge process connects one configured Home Assistant instance to one Household. Home Assistant owns pairing, names, configuration, capability discovery, and external object lifecycle. Hearth owns canonical Device and Entity identity, current State, command history, generic lifecycle, and API representation.

Users select up to 500 registry-backed `light.*` entity IDs in strict YAML. Each selected HA light maps to one Hearth `light` Device, one `hearth.power/v1` Entity, and, when HA reports brightness support, one `hearth.brightness/v1` Entity. A bridged object may remain in Home Assistant forever. A later transfer to native ownership must be possible without changing canonical IDs, but the transfer operation is outside this milestone.

Make Adapter presence, upstream status, current issues, Entity availability, inventory reconciliation, and retirement generic Hearth capabilities. Keep them in the cohesive `devices` module because they currently exist to govern Device ownership, State, and Commands. Do not create a speculative adapters product module or a general third-party plugin system.

Replace the pre-release v1 Registration contract in place. No deployed compatibility obligation exists: the old Home Assistant YAML shape, local SQLite contents, and current SDK Registration API may break. Developers recreate local databases and config. Do not add aliases, conversion commands, dual wire versions, or one-time data rewrites.

## Product Contract

### Support promise

The first-class Bridge is:

- built and versioned with each Hearth release;
- distributed as Linux amd64, Linux arm64, macOS amd64, and macOS arm64 binaries with SHA-256 checksums;
- documented for direct use plus managed `systemd` and `launchd` operation;
- compatibility-tested against actual containers for the declared Home Assistant release lines;
- visible through Hearth's Adapter and Entity health APIs.

Each Hearth release declares support for the latest two Home Assistant monthly release lines and tests their latest patch versions. The initial declaration is Home Assistant `2026.8.x` and `2026.7.x`; the implementation must pin the latest available patches when its compatibility workflow lands. A newer or older HA version is attempted rather than blocked, but the Adapter exposes `unsupported_upstream_version` until the running version is in the declared matrix.

Bridge and core releases are lockstep-supported. The in-place v1 replacement deliberately cannot interoperate with the pre-foundation Adapter. Starting with the first Bridge-foundation release, later release-version mismatches that still implement this same v1 contract continue operating and expose `version_mismatch`; this milestone does not promise that mixed releases are tested or supported.

### Explicit non-goals

- Bidirectional synchronization or exposing Hearth-native Devices to Home Assistant.
- Home Assistant areas, devices, scenes, automations, services, or arbitrary domains in Hearth's model.
- Import-all, discovery/approval UI, HA labels, or runtime allowlist editing.
- Home Assistant add-on or OCI packaging.
- Hot config reload; allowlist changes require process restart.
- Availability history, Adapter retirement, archived-resource search, or native ownership transfer.
- HTTP network authentication; the API remains unauthenticated and loopback-only.
- Stable third-party Adapter APIs, manifests, runtime Entity types, or feature negotiation.
- A durable SDK outbox, queued Commands, or Command replay.
- Compatibility with the disposable Adapter's YAML, wire Registration API, or local database.

## Domain Behavior

### Independent lifecycle dimensions

These dimensions must remain separate in domain types, persistence, wire DTOs, and HTTP output:

| Resource | Dimension | Values | Meaning |
|---|---|---|---|
| Adapter instance | presence | `online`, `offline` | Whether the core has a current SDK lease |
| Adapter instance | upstream status | `connected`, `disconnected` | The Adapter's latest report about its external system |
| Adapter instance | upstream reason | absent, `authentication_failed`, `unreachable`, `protocol_error` | Why a disconnected upstream cannot be used |
| Device/Entity | lifecycle | `active`, `retired` | Whether the resource remains in the Adapter's committed inventory |
| Entity | availability | `available`, `unavailable` | Whether the active Entity can currently observe and accept Operations |

Adapter presence uses a ten-second SDK heartbeat and expires after 30 seconds. `hearthd` pessimistically marks every Adapter offline at startup; persisted last-seen time does not carry an online lease through a core restart. A graceful disconnect may mark presence offline sooner, but correctness cannot depend on it.

The core persists current upstream and reported Entity status plus transition timestamps, but no status history. Effective Entity availability is derived in this precedence order:

1. retired -> `unavailable/retired`;
2. owner offline -> `unavailable/adapter_offline`;
3. upstream disconnected -> `unavailable/authentication_failed` or `unavailable/upstream_disconnected`;
4. no status snapshot accepted for the current Inventory revision -> `unavailable/status_pending`;
5. Adapter-reported unavailable -> its bounded reason;
6. otherwise -> `available`.

An unavailable Entity retains its last State and timestamps. An available Entity may still have `state: null` when no value has ever been observed.

### Command behavior

An Operation invoked on an active but unavailable Entity creates a durable Command and completes it without NATS dispatch:

- owner presence offline -> `adapter_unavailable`;
- owner online but upstream or object unavailable -> `entity_unavailable`.

Both map to HTTP 503 and include `command_id`. Add `entity_unavailable` to the command status/failure invariants rather than misclassifying an online Adapter as absent. The Adapter command responder must also be able to return `entity_unavailable` for a race after the core's availability check.

An Operation invoked on a retired Entity creates no Command and returns HTTP 409 `entity_retired`. Retirement is deliberate lifecycle, not a transient failed attempt.

### Retirement

A successful inventory omitting a previously configured selector retires its Device and every child Entity. Retired resources are excluded from normal lists, remain directly retrievable by known canonical ID with their last State, reserve all canonical IDs and mappings, and reject Commands.

If an active light loses brightness support, retire only its Brightness Entity. Keep the Device and Power Entity active. If brightness support returns, restore the same Brightness Entity ID. A missing, disabled, `unknown`, or `unavailable` HA object is not retired; it remains active and unavailable.

## Generic Adapter Contracts

### Adapter instance registration

Before inventory, an SDK Session registers its Adapter instance using its configured slug and reports:

```go
type InstanceDescriptor struct {
	Implementation string `json:"implementation"`
	Version        string `json:"version"`
}
```

`Implementation` is stable for a slug (`homeassistant`, `simulator`). `Version` is the Hearth release/build version and may change on re-registration. Reusing a slug for another implementation is a permanent identity conflict. A version mismatch is accepted and recorded as an issue.

Registration creates the queryable Adapter resource before any HA connection or inventory succeeds. The SDK starts presence renewal after accepted registration. A Bridge with an empty allowlist is valid and may be fully healthy.

### Inventory

Replace one-Binding Registration with one complete, schema-validated Inventory request containing at most 500 configured entries. The SDK must reject a request above the count limit or above a conservative encoded-size limit below NATS's default 1 MiB payload. A generated maximal fixture must prove the documented 500-entry shape fits without changing NATS `max_payload`.

Each entry is either resolved or unresolved:

```go
type Inventory struct {
	Entries []InventoryEntry `json:"entries"`
}

type InventoryEntry struct {
	ConfiguredSelector string                `json:"configured_selector"`
	BindingKey         *string               `json:"binding_key,omitempty"`
	Device             *DeviceDescriptor     `json:"device,omitempty"`
	Entities           []EntityDescriptor    `json:"entities,omitempty"`
	Issues             []InventoryEntryIssue `json:"issues,omitempty"`
}

type InventoryEntryIssue struct {
	Code string `json:"code"`
}
```

The authoritative JSON Schema expresses the resolved/unresolved union and bounded strings. `configured_selector` is exactly the value in Adapter configuration and remains unchanged until that configuration changes. Current external IDs remain separate fields in Device/Entity descriptors and may change while the configured selector does not. Both are generic external-system strings, not Home Assistant concepts.

A resolved entry has Binding, Device, and Entity descriptors and may also carry non-blocking issues. An unresolved entry omits all descriptors and has at least one blocking issue. Generic entry issue codes are:

- `object_not_found`;
- `stable_identity_unavailable`;
- `unsupported_object`;
- `capability_unknown`;
- `identity_conflict`;
- `invalid_descriptor`.

The core applies one complete Inventory in one SQLite transaction:

- create or update the Adapter's resolved Bindings, Devices, Entities, names, support, and external mappings;
- restore previously retired resources that return with compatible identity and Entity types;
- retire existing Bindings and child resources whose selectors are omitted;
- retain a known unresolved selector as active but unavailable only when `(adapter_id, configured_selector)` identifies exactly one existing Binding;
- record current issues for unresolved or rejected entries;
- return canonical IDs for every accepted resolved entry without exposing partial commit.

One bad entry does not prevent valid entries from operating. Entry-level rejection is part of the committed Inventory result. A newly unresolved selector creates an Adapter issue but no synthetic Device or Entity. An unresolved selector matching no or multiple prior Bindings similarly creates an issue without guessed association. Infrastructure or malformed-envelope failure rejects the whole request. An empty Inventory is complete and retires all previously selected Bindings for that Adapter.

An intentionally empty configured allowlist sends a complete empty Inventory even when the upstream is disconnected. A non-empty allowlist must never become an empty or partial Inventory merely because the upstream is disconnected, authentication failed, or snapshot acquisition failed. Once upstream acquisition succeeds, individually missing configured objects are submitted as unresolved entries so known Bindings remain active and unavailable.

### Identity reconciliation

For the Home Assistant Bridge, the Binding key is HA's opaque entity-registry entry ID, `configured_selector` is the entity ID currently written in YAML, and each Entity descriptor's external ID is the entity's current HA entity ID. The same external HA entity maps multiple typed Hearth Entities, so external-ID uniqueness is scoped by Entity key rather than prohibiting Power and Brightness mappings in one Binding.

If a registry entry is deleted and recreated with the same external entity ID, automatically re-key exactly one existing Binding only when its mapped Entity-type shape is compatible with the proposed shape. Names do not participate. If zero or multiple candidates exist, or the proposed registry identity is already mapped elsewhere, retain existing resources and expose `identity_conflict` for explicit reconciliation.

The reconciliation operation is a generic loopback HTTP action, not HA-specific core logic. Its exact request identifier/body remains an open question below. Approval must be bound to the current issue/proposal and applied transactionally; clients may not submit an unconstrained database rewrite.

### Current-status delivery

Do not turn unavailability into a fake Observation and do not add a lifecycle history stream. Add Core NATS request/reply subjects and schemas for:

- Adapter instance registration;
- heartbeat/upstream status;
- complete Inventory reconciliation;
- current Entity-status snapshot.

Accepted instance registration returns a core-generated `session_id`; every later lifecycle request carries it, and a newer registration invalidates the prior session. A successful Inventory returns a monotonically increasing `inventory_revision` and the canonical IDs for all known entries, including a known unresolved entry retained from prior inventory.

An Entity-status snapshot contains the `session_id`, `inventory_revision`, a monotonic session-local `sequence`, and exactly one status for every active canonical Entity returned by that Inventory. It is a complete replacement, not a delta: omission or an unknown Entity rejects the snapshot, and a new Inventory revision makes the prior snapshot ineligible. The core accepts only the current session and revision with a sequence newer than the last accepted sequence. Until it accepts a matching snapshot, active Entities are effectively `unavailable/status_pending`.

The SDK serializes lifecycle requests, retains only the latest complete status snapshot in memory, retries an unacknowledged snapshot through transient NATS/core disconnection, and republishes after reconnect. This in-memory coalescing is not a durable outbox. Heartbeats carry current upstream status and current runtime Adapter issues, but not the full Entity set. Inventory entry issues remain tied to the current committed Inventory.

Use these v1 subjects:

```text
hearth.v1.adapter.<adapter>.instance
hearth.v1.adapter.<adapter>.heartbeat
hearth.v1.adapter.<adapter>.inventory
hearth.v1.adapter.<adapter>.status
hearth.v1.adapter.<adapter>.observation.<entity>
hearth.v1.adapter.<adapter>.command.<entity>.<operation>
```

All four new interactions use Core NATS request/reply with correlated, causally linked envelopes. Observations retain their existing JetStream semantics; Commands retain ephemeral request/reply and no replay. Replace old Registration schema IDs, subjects, DTOs, fixtures, and SDK methods rather than retaining compatibility aliases.

## SDK Interface

Extend `sdk/adapter.Config` with required implementation and version metadata. Keep vendor concepts out of the SDK.

The intended public shape is:

```go
type Config struct {
	AdapterID      string
	Implementation string
	Version        string
	NATSURL        string
}

type UpstreamStatus struct {
	Status  string
	Reason  *string
	Version *string
	Issues  []AdapterIssue
}

type AdapterIssue struct {
	Code     string
	Selector *string
	Message  string
}

type EntityStatus struct {
	EntityID string
	Status   string
	Reason   *string
}

type EntityStatusSnapshot struct {
	InventoryRevision int64
	Entities          []EntityStatus
}

func Connect(context.Context, Config) (*Session, error)
func (s *Session) RegisterInstance(context.Context) error
func (s *Session) SetUpstreamStatus(UpstreamStatus) error
func (s *Session) ReconcileInventory(context.Context, Inventory) (InventoryResult, error)
func (s *Session) SetEntityStatuses(EntityStatusSnapshot) error
```

`RegisterInstance` starts the internal heartbeat/status loop after the core accepts identity. `SetUpstreamStatus` and `SetEntityStatuses` validate and replace current in-memory snapshots, trigger prompt delivery, and do not block vendor event loops on a temporarily missing core. The `Issues` slice is the complete current Adapter-reported runtime issue set; omission clears a prior reported issue, while core-computed and committed Inventory issues remain separate. The SDK owns session IDs and status sequence numbers; Adapter implementations use the Inventory revision returned by `ReconcileInventory`. `Close` stops renewal, drains handlers and NATS as today, and makes later setters return `ErrClosed`.

Extend the existing command response schema with `entity_unavailable`. Add `Unavailable(message string) error` to `Responder`; it emits a rejected response with that bounded code. Existing `Reject(message)` continues to mean `upstream_rejected`. The core maps either response to the corresponding terminal Command status/failure code, and typed SDK facades expose the same distinction.

The simulator moves to the same API and remains the generic failure harness. Keep interfaces in consuming packages and use concrete SDK types; do not add a plugin abstraction.

## HTTP API

The `devices` module owns Huma operation registration and explicit transport models. Application assembly continues to own Echo/Huma construction and loopback policy. Handlers translate only; lifecycle, inventory, listing, availability, and command decisions remain testable without HTTP.

Add:

```text
GET  /v1/adapters
GET  /v1/adapters/{adapter_id}
GET  /v1/devices
GET  /v1/devices/{device_id}
GET  /v1/entities
POST /v1/adapter-reconciliations/{reconciliation_id}  # provisional; open question
```

Keep existing Entity get and Command routes. Collection routes return deterministic active-only arrays and are unpaged in this milestone. A Household is limited to 2,500 Adapter instances, 2,500 active Devices, and 2,500 active Entities. Instance registration rejects creation of a 2,501st Adapter with `household_limit`; Inventory rejects a transaction that would exceed either Device/Entity limit. Lists never silently truncate. Archived resources are available only by known canonical ID.

Adapter output includes:

```json
{
  "id": "homeassistant",
  "implementation": "homeassistant",
  "version": "...",
  "presence": {"status": "online", "changed_at": "...", "last_seen_at": "..."},
  "upstream": {"status": "connected", "reason": null, "version": "2026.8.3", "changed_at": "..."},
  "issues": []
}
```

Issue objects contain a stable bounded `code`, optional external `selector`, safe message, first/current observation timestamps, and a reconciliation handle only when action is possible. Current issue codes include Inventory codes plus `selector_renamed`, `version_mismatch`, and `unsupported_upstream_version`. Internal errors and tokens never enter the response.

Device output adds `lifecycle`. Entity list/get output adds `lifecycle` and:

```json
"availability": {
  "status": "available",
  "reason": null,
  "changed_at": "..."
}
```

Use stable explicit Huma operation IDs and preserve the existing Hearth error envelope. API tests own methods, paths, validation, mappings, and runtime OpenAPI; module tests own behavior.

## Persistence

Because no deployment or persisted-data compatibility exists, reshape `00001_initial.sql` and regenerate sqlc. Do not add a migration that recognizes `homeassistant-migration` or the old single Binding.

The schema must represent:

- `adapter_instances`: stable slug, immutable implementation, current version, presence, lease timestamps, upstream status/reason/version, and timestamps;
- `adapter_issues`: current issue identity, code, selector, safe message, optional reconciliation proposal, and timestamps;
- Device and Entity `active|retired` lifecycle with transition timestamps;
- reported Entity availability/reason/timestamp, distinct from effective derived availability;
- configured Binding selector, unique by `(adapter_id, configured_selector)`, in addition to Binding key and current external IDs;
- multiple Entity mappings per Binding;
- repeated external entity IDs within a Binding when Entity keys differ;
- existing canonical State, Observation receipt, and Command invariants;
- the new `entity_unavailable` terminal Command status/failure code.

Inventory reconciliation is one repository-owned SQLite transaction. It first classifies every entry: new resolved, existing by Binding key, existing by unique configured selector, unambiguous compatible external-ID replacement, new unresolved, known unresolved, or conflict. It validates the complete proposed result before changing rows and then performs identity re-keying, restore/create/update, retirement, current issues, Inventory revision, and result loading atomically. Generated sqlc types remain inside the concrete repository. Repository tests run against migrated SQLite and prove rollback on every conflict path.

At core startup, interrupt active Commands as today and mark every Adapter presence offline before HTTP starts. Do not erase upstream reports, Entity reports, State, or issues during that startup transition.

## Home Assistant Bridge

### Configuration

Replace the old Binding object with a scalar entity-ID allowlist:

```yaml
adapter_id: homeassistant
nats_url: nats://127.0.0.1:4222
entities:
  - light.office
  - light.kitchen
home_assistant:
  url: http://homeassistant.local:8123
  token_file: .secrets/homeassistant-token
```

`adapter_id` remains a required stable subject-safe slug so multiple HA instances can be represented later. `entities` is required but may be empty, contains at most 500 unique `light.*` IDs, and permits no wildcard or per-entry names/keys. Unknown YAML fields, duplicate IDs, unsupported domains, unreadable token files, and malformed URLs fail startup. A readable but rejected token does not exit: register the Adapter first, report `authentication_failed`, re-read the token file, and retry with bounded exponential backoff. Other HA connection failures similarly keep the process present and retry.

Config changes require restart. The Bridge never rewrites YAML.

### HA identity and metadata

Resolve each configured entity ID with the entity-registry WebSocket API and use registry entry `id` as Binding key. Do not use `unique_id`, `platform`, or `config_entry_id` alone. Reject entities with no registry entry as `stable_identity_unavailable`.

Subscribe to state and entity-registry changes before acquiring registry and state snapshots. Buffer and reconcile concurrent events so startup and reconnect retain the existing no-lost-transition guarantee. Follow an entity-ID rename during the running session by registry ID: retain the old configured selector, update the descriptor's current external ID, report `selector_renamed`, and require YAML to contain the new ID before the next restart. A restart with the stale YAML value submits an unresolved entry whose configured selector matches the prior Binding, retaining the known Device as unavailable. After the user updates YAML, the unchanged registry Binding key updates the persisted configured selector without changing canonical IDs.

The Hearth Device name follows HA's friendly name on each successful reconciliation, falling back deterministically to the current entity ID. Child Entity names are `Power` and `Brightness`. Name changes never affect identity.

### Capability and State mapping

Every resolved HA light has:

- Entity key `power`, type `hearth.power/v1`, existing full support;
- Entity key `brightness`, type `hearth.brightness/v1`, maximum 100 and step 1, when `supported_color_modes` contains `brightness`, `color_temp`, `hs`, `xy`, `rgb`, `rgbw`, `rgbww`, or `white`.

An explicit `supported_color_modes` list containing only `onoff`/`unknown` means no brightness support. A missing or malformed capability list on a previously mapped light reports `capability_unknown` and preserves its current Entity shape; it never retires Brightness from incomplete evidence. For a new light, missing capability evidence registers Power only with the issue. Do not fall back to legacy feature bitmasks unless a supported-version compatibility test proves that a declared release requires it.

Reconcile capability/name changes from HA registry/state events using a fresh complete Inventory; never send a one-entry partial Inventory. This restores or retires Brightness according to the generic rules.

Map HA `on` and `off` to Power `true` and `false`. HA `unknown`, `unavailable`, a missing configured object, or upstream disconnection retains last State and reports affected Entities unavailable. A dimmable light that starts off without any brightness attribute has an available Brightness Entity with `state: null`; brightness Commands are allowed. Once HA reports brightness, retain that last level while the light is off. Setting brightness may turn the HA light on and must publish both resulting Power and Brightness Observations.

The exact `0-255` to `0-100` brightness conversion and inverse remain open below. No implementation may silently choose rounding behavior because Command satisfaction depends on deterministic round trips.

### Commands

Power uses HA `light.turn_on` and `light.turn_off`. Brightness uses `light.turn_on` with the approved converted brightness parameter. Preserve concurrent WebSocket request correlation and the subscribe-first snapshot algorithm.

After HA accepts a service call, actively refresh current state. Publish the target Entity's fresh Observation using the Command handler context so correlation, causation, and outcome satisfaction remain valid. Publish side-effect observations for sibling Entities without claiming they satisfy the target Command. HA rejection remains `upstream_rejected`; a target becoming unavailable before service dispatch returns `entity_unavailable`.

## Ownership and Expected Changes

Keep the existing dependency direction:

```text
Home Assistant WebSocket
  -> internal/adapters/homeassistant
  -> sdk/adapter
  -> versioned NATS contracts
  -> internal/modules/devices/nats
  -> devices Service/repository
  -> SQLite
  -> devices/api
```

Expected owners:

```text
contracts/v1/                         # authoritative instance/inventory/status schemas
internal/contracts/v1/natswire/       # generic subjects, envelope and route mechanics
sdk/adapter/                          # heartbeat, current status, inventory client
internal/modules/devices/             # domain behavior and SQLite repository
internal/modules/devices/nats/        # Core NATS servers and wire/domain mapping
internal/modules/devices/api/         # Adapter/Device/Entity Huma transport
internal/app/hearthd/                  # assembly, startup offline transition, readiness
internal/adapters/homeassistant/       # HA protocol, identity, mapping and commands
internal/app/homeassistant/            # strict config and process lifecycle
cmd/hearth-adapter-homeassistant/      # thin executable
.github/workflows/                     # HA matrix, repository gate and release artifacts
docs/ and README.md                    # product, architecture, installation and support
```

Do not move HA vendor code into `devices`, expose sqlc/vendor models, construct Echo/Huma outside application assembly, or add forwarding APIs for deleted Registration behavior.

## Delivery Stages

Each stage is independently mergeable and passes the full repository gate.

| Stage | Deliverable | Verification gate |
|---|---|---|
| S1 | Generic Adapter instance registration, persisted current health/issues, SDK heartbeat, Adapter list/get | Simulator proves online, expiry within 30 seconds, core-restart pessimism, upstream failure, and version issue |
| S2 | Atomic Inventory replacement, multi-Entity Bindings, lifecycle/availability, Device/Entity lists, unavailable Command outcomes | SQLite and service tests prove atomicity, retirement/restore, partial entry validity, identity re-key, and no-dispatch failures |
| S3 | HA config/registry refactor, multiple lights, Power and Brightness, live rename/capability/status reconciliation | Scripted WebSocket tests cover all mapping, reconnect, race, and causal-observation rules |
| S4 | Actual HA `2026.8.x`/`2026.7.x` container matrix and declared compatibility source | Matrix proves auth, registry-ID rename stability, snapshot/events, availability, and both Operations |
| S5 | Four release binaries, checksums, systemd/launchd docs, product/architecture cleanup | Release workflow artifacts install and report lockstep versions; clean-checkout docs match runtime config/API |

S1 and S2 update the simulator alongside each contract change. S3 does not land on a private or parallel lifecycle path. S4 may update exact HA patch tags but not weaken the two-line policy. S5 does not add containers or an HA add-on.

## Acceptance

- [ ] `CONTEXT.md`, ADR 0016, and ADR 0017 agree that Home Assistant is a permanent inbound Bridge with HA-owned external lifecycle and Hearth-owned canonical identity.
- [ ] Core/wire/SDK contain no Home Assistant identifiers, payloads, service names, registry field names, or version policy.
- [ ] The old disposable wording and old Home Assistant Binding config are absent from current product, architecture, README, and example config; historical first-light plans/specs remain historical rather than being rewritten.
- [ ] Adapter instance registration precedes Inventory, starts a 10-second SDK heartbeat, expires within 30 seconds, and reports offline after core restart until renewed.
- [ ] Adapter API distinguishes presence from upstream status and exposes only bounded stable issue codes and safe messages.
- [ ] One complete Inventory supports 0-500 entries under default NATS payload limits and commits valid entries, unresolved entries, issues, identity re-keying, capability retirement/restoration, and omitted-selector retirement atomically.
- [ ] One selected dimmable HA light retains one Device ID and stable Power/Brightness Entity IDs across restart, name change, entity-ID rename, capability loss/return, temporary absence, retirement/restoration, and unambiguous registry-entry recreation.
- [ ] Missing and `unknown`/`unavailable` HA objects preserve last State, report unavailable, and do not become retired.
- [ ] Active unavailable Commands are durably recorded without dispatch and distinguish `adapter_unavailable` from `entity_unavailable`; retired Commands return 409 without a record.
- [ ] Active-only Adapter, Device, and Entity lists are deterministic and bounded at 2,500; retired Device/Entity direct lookup retains last State and lifecycle.
- [ ] The Bridge runs with an empty allowlist, continues running across HA disconnect/auth rejection, re-reads a rotated token, and never retires inventory because upstream acquisition failed.
- [ ] Real HA tests pass against the pinned latest patches of `2026.8.x` and `2026.7.x`, including stable registry ID across rename.
- [ ] Unsupported HA and mixed Hearth versions operate with visible issues rather than hard rejection.
- [ ] Release CI emits the four selected binaries and SHA-256 checksums; installation docs cover token permissions, restart policy, logs, systemd, and launchd.
- [ ] Runtime OpenAPI contains every new route/status/error and preserves existing route contracts except approved additive Entity fields and Command failure values.
- [ ] `devenv test`, `git diff --check`, and generated-code drift checks pass from a clean recreated database/config.

## Open Questions

1. **Brightness conversion:** choose the exact deterministic mapping between HA integer `0-255` and Hearth integer `0-100`, including inverse conversion and the behavior of a `0` brightness Command. Recommended starting point: nearest-integer scaling in both directions, with a round-trip conformance table for every Hearth value.
2. **Reconciliation action shape:** choose whether the loopback action accepts an opaque short-lived issue/reconciliation ID or explicit constrained old/new Binding identities. Recommended starting point: an opaque ID bound to the current persisted proposal, returning 409 when stale.

The spec remains Draft until these two questions are resolved. They do not block S1, but S2 must settle the reconciliation shape and S3 must settle brightness conversion before implementation of the affected behavior.
