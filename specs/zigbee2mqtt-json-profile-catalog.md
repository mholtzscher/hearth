# Zigbee2MQTT JSON profile catalog

**Status:** Ready for task breakdown
**Approved by:** User
**Type:** RFC / architectural refactor
**Effort:** XL, 7–12 focused days at 60% confidence
**Date:** 2026-09-09
**Baseline:** `6633d31`

## Summary

Replace handwritten Zigbee2MQTT Entity-mapping planners with embedded JSON profiles validated by JSON Schema. Profiles map expose shapes to stable Hearth Entity identities and select a closed set of Go planning strategies. Version 1 migrates current discovery for lights, relays, smart plugs, ambient sensors, and link quality.

The Adapter compiles the full catalog before connecting to NATS or MQTT. Catalog errors fail startup. Malformed runtime inventory remains isolated by device and expose.

## Problem

Hearth maintainers must change repetitive Go planners and Entity-specific helpers to add standard scalar capabilities. The `3RSP02028BZ` work added mapping code for six numeric sensors, three numeric settings, one enum setting, and one enum action, although existing Hearth Entity types already implement their runtime behavior.

Current mapping policy spans `planner_light.go`, `planner_relay.go`, `planner_sensor.go`, device-root helpers in `entity_*.go`, and tables such as `smartPlugElectricalSensors` and `smartPlugNumericSettings`. The existing expose index and generic `entityPlan` runtime provide the mechanisms needed behind the catalog.

## Goals

- Move current Entity mappings and family contribution assembly into embedded profiles.
- Let maintainers add a mapping covered by an existing strategy with profile JSON and fixture tests.
- Validate the full catalog with an authoritative JSON Schema and semantic compiler before external connections.
- Preserve every current Binding key, Device kind, Entity key, external ID, name, type, support document, deterministic order, rejection code, State conversion, Command payload, refresh behavior, and outcome policy.
- Match expose capabilities first and use exact vendor, model, and software-build overrides for proven quirks.
- Keep behavior in a closed set of Go strategies rather than executable profile syntax.
- Preserve malformed-inventory isolation and compile an immutable catalog safe for concurrent reconciliation.

## Non-goals

- Version 1 loads only repository-owned embedded profiles at startup. It accepts no operator or runtime catalog input and generates no effective catalog.
- Profile syntax is limited to the selectors, dependencies, identities, and strategy parameters defined here. It cannot express executable behavior.
- Existing Entity types and Core, persistence, HTTP, NATS, MQTT, and SDK contracts remain unchanged.
- State freshness, cross-message assembly, Command deadlines, matching, FIFO order, and evidence disposition remain unchanged.
- Only catalogued mappings are eligible.
- The cutover may change private handwritten planner interfaces.

## Decision

Build one profile-catalog module inside the existing `zigbee2mqtt` package. Its external interface loads an opaque immutable catalog and passes it to the Adapter constructor. Profile evaluation remains private to the package.

```go
// ProfileCatalog is an immutable, validated catalog of embedded Zigbee2MQTT
// Entity mapping profiles. Its zero value is invalid.
type ProfileCatalog struct {
    profiles   []compiledPlannerProfile
    overrides  []compiledProfileOverride
    strategies profileStrategyRegistry
}

// LoadEmbeddedProfileCatalog validates and compiles every repository-owned
// profile before the Adapter makes any external connection.
func LoadEmbeddedProfileCatalog() (*ProfileCatalog, error)
```

The catalog replaces mapping policy only. Go retains:

- tolerant `bridge/devices` decoding and Device identity validation;
- endpoint resolution, expose indexing, property ownership, and unique root or feature selection;
- color property exceptions, foreign color-mode claim detection, canonical scalar handling, and exact numeric conversions;
- typed Entity and Observation construction, Command translation, outcome matching, and same-message completeness;
- contribution merging and the runtime coordinator, including route fencing, MQTT generations, FIFO queues, deadlines, refresh, and evidence publication.

Profiles state mapping intent. Go implements protocol and Entity behavior.

## Architecture

```text
repository-owned *.profile.json / *.override.json
                    │
                    ▼
        JSON Schema validation
                    │
                    ▼
       semantic catalog compiler
     IDs, references, strategy params,
     ordering, dependencies, overrides
                    │
                    ▼
       immutable ProfileCatalog
                    │
bridge/devices ─► exposeIndex ─► profile evaluation
                    │                 │
                    │                 ▼
                    │       Go strategy registry
                    │       ├─ eligibility rules
                    │       ├─ typed descriptor/support
                    │       ├─ State decoder
                    │       └─ Command translator/matcher
                    │                 │
                    └─────────────────▼
                         []plannerContribution
                                  │
                                  ▼
                      existing deterministic merge
                                  │
                                  ▼
                         validated []entityPlan
                                  │
                                  ▼
                      existing reconciliation/runtime
```

## Profile documents

### File organization

Each embedded file contains one document. Filenames determine deterministic diagnostic order but never planning precedence.

```text
internal/adapters/zigbee2mqtt/profiles/
├── profile.schema.json
├── light.profile.json
├── relay.profile.json
├── sensors.profile.json
├── linkquality.profile.json
└── overrides/
    └── <vendor>-<model>.override.json
```

Profiles use explicit unique `order` values. Current behavior is represented by:

| Order | Profile | Role | Device kind |
|---:|---|---|---|
| 10 | `light` | primary | `light` |
| 20 | `relay` | primary | `relay` |
| 30 | `ambient-sensors` | supplemental | `sensor` |
| 40 | `linkquality` | supplemental | `sensor` |

The existing merge rule remains authoritative: the first non-empty primary contribution wins; later primaries are discarded; every supplemental contribution appends in order; the first non-empty supplemental establishes Device kind when no primary survives.

### Go wire types

The JSON Schema is authoritative. The following private Go types show the required implementation shape.

```go
type profileDocumentKind string

const (
    profileDocumentPlanner  profileDocumentKind = "planner-profile"
    profileDocumentOverride profileDocumentKind = "profile-override"
)

type plannerProfileDocument struct {
    Version        int                     `json:"version"`
    Kind           profileDocumentKind     `json:"kind"`
    ID             string                  `json:"id"`
    Order          int                     `json:"order"`
    Contribution   profileContribution     `json:"contribution"`
    CandidateGroups []profileCandidateGroup `json:"candidate_groups"`
    DeviceEntities []profileDeviceEntity   `json:"device_entities"`
}

type profileContribution struct {
    DeviceKind string             `json:"device_kind"`
    Role       profilePlannerRole `json:"role"`
}

type profileCandidateGroup struct {
    ID       string                  `json:"id"`
    Root     profileExposeSelector   `json:"root"`
    GateRule string                  `json:"gate_rule,omitempty"`
    Entities []profileCandidateEntity `json:"entities"`
}

type profileCandidateEntity struct {
    ID          string                `json:"id"`
    Source      profileEntitySource   `json:"source"`
    RequiresAny []string              `json:"requires_any,omitempty"`
    Identity    profileEntityIdentity `json:"identity"`
    Strategy    profileStrategyRef    `json:"strategy"`
}

type profileDeviceEntity struct {
    ID                    string                `json:"id"`
    Expose                profileExposeSelector `json:"expose"`
    RequiresGroupSurvivor string                `json:"requires_group_survivor"`
    Identity              profileEntityIdentity `json:"identity"`
    Strategy              profileStrategyRef    `json:"strategy"`
}

type profileExposeSelector struct {
    Type string `json:"type"`
    Name string `json:"name,omitempty"`
}

type profileEntitySource struct {
    Kind string `json:"kind"` // root | feature | derived
    Type string `json:"type,omitempty"`
    Name string `json:"name,omitempty"`
}

type profileEntityIdentity struct {
    Key  string `json:"key"`
    Name string `json:"name"`
}

type profileStrategyRef struct {
    Name       string          `json:"name"`
    Parameters json.RawMessage `json:"parameters"`
}
```

Rules use globally unique IDs such as `light.power`, `relay.power`, and `sensor.temperature`. Entity `identity.key` is always the unscoped stable base key. Go applies the existing `scopedIdentity` and `entityLocation` functions; profiles cannot construct endpoint suffixes or external IDs.

### Candidate-group semantics

A candidate group evaluates roots in retained inventory order. `root` selects upstream roots by exact expose `type` and optional exact `name`.

`source` is a closed JSON Schema `oneOf`. The selected form forbids fields not shown below:

```json
{"kind": "root"}
{"kind": "feature", "type": "numeric", "name": "brightness"}
{"kind": "derived", "name": "color-mode"}
```

- `root` permits only `kind` and passes the selected root expose to the strategy.
- `feature` requires non-empty exact `type` and `name`, then performs the existing exact-one `UniqueFeature(type,name)` lookup inside the selected root.
- `derived` requires `name`, forbids `type`, and accepts only `color-mode` in version 1. It passes no expose to the strategy. The strategy receives the selected root, expose index, and plans from its declared `requires_any` rules.
- If `gate_rule` is present, that rule must be the first Entity rule. Optional siblings are evaluated only when the gate produces a valid plan.
- Candidate groups are deduplicated by the scoped key produced by their gate. Every candidate sharing a duplicate gate key is dropped with all its siblings, preserving current light and relay behavior.
- `requires_any` may reference only earlier rules in the same candidate group. The evaluator runs the rule only if at least one referenced sibling produced a plan. This supports read-only color mode.
- A group without a gate evaluates each rule independently; malformed roots or rules do not suppress valid siblings.

### Device-entity semantics

Device Entities use the existing `UniqueRoot(type,name)` behavior and are evaluated once after candidate groups. `requires_group_survivor` must name a candidate group with a non-empty `gate_rule`; referencing an ungated group is a startup-fatal `invalid_group_reference`. A device Entity is evaluated only when that group retains at least one candidate after gate planning and duplicate-gate-key removal.

This supports light-level power-on behavior and effects, plus relay-level power-on behavior, electrical measurements, numeric settings, and reset action. A missing, duplicate, unresolved, or ineligible root omits only that device Entity.

### Example profile

```json
{
  "$schema": "./profile.schema.json",
  "version": 1,
  "kind": "planner-profile",
  "id": "relay",
  "order": 20,
  "contribution": {
    "device_kind": "relay",
    "role": "primary"
  },
  "candidate_groups": [
    {
      "id": "relay.roots",
      "root": {"type": "switch"},
      "gate_rule": "relay.power",
      "entities": [
        {
          "id": "relay.power",
          "source": {"kind": "feature", "type": "binary", "name": "state"},
          "identity": {"key": "power", "name": "Power"},
          "strategy": {"name": "binary-power", "parameters": {}}
        }
      ]
    }
  ],
  "device_entities": [
    {
      "id": "relay.power-on-behavior",
      "expose": {"type": "enum", "name": "power_on_behavior"},
      "requires_group_survivor": "relay.roots",
      "identity": {"key": "poweronbehavior", "name": "Power-On Behavior"},
      "strategy": {"name": "enum-setting", "parameters": {}}
    }
  ]
}
```

Each eligible optional expose produces its Entity independently. Profiles do not require a fixed device shape.

## Strategy registry

Profiles select from the closed Go strategy registry returned by `defaultProfileStrategyRegistry`.

```go
type profileStrategyDefinition struct {
    Name              string
    AllowedSourceKinds []string
    CompileParameters func(json.RawMessage) (any, error)
    Plan              func(profileStrategyInput, any) (entityPlan, bool)
}

type profileStrategyInput struct {
    IEEE     string
    Root     indexedExpose
    Expose   *upstreamExpose // selected root/feature; nil for a derived source
    Index    exposeIndex
    Identity profileEntityIdentity
    Prior    map[string]entityPlan // only declared, successfully planned dependencies
}

type profileStrategyRegistry map[string]profileStrategyDefinition

func defaultProfileStrategyRegistry() profileStrategyRegistry
```

`CompileParameters` runs once at catalog compilation. `Plan` receives typed compiled parameters, never unvalidated JSON. `bool == false` means the upstream expose is ineligible and only that candidate is omitted.

The registry owns each strategy's Hearth Entity type, State policy, operation outcome, access semantics, decoder, translator, matcher, and support construction. Those fields are not configurable independently.

### Version 1 strategies

| Strategy | Profile-controlled parameters | Go-owned behavior |
|---|---|---|
| `binary-power` | none | Full publish/set/get eligibility, discovered on/off scalars, `hearth.power/v1`, observed exact matching |
| `brightness` | none | Exact range/reversibility checks, percent scaling and rounding, observed matching |
| `color-temperature` | none | Exact mired bounds, same-message color-mode activity, observed active-value matching |
| `color-xy` | none | Composite/axis validation, shared property rules, exact rational scaling, tolerance matching |
| `color-hs` | none | Composite/axis validation, hue folding/scaling, circular tolerance matching |
| `color-mode` | none | Derived companion property, foreign-claim checks, read-only same-message mode State |
| `startup-color-temperature` | none | Exact mired bounds and `previous` ↔ `65535` sentinel handling |
| `temperature` | none | `°C` eligibility and exact milli-Celsius conversion |
| `numeric-sensor` | accepted upstream units, output unit, integer/float format, fixed or upstream/fallback bounds | Publish-without-set eligibility, property uniqueness, optional get refresh, typed numeric Observation |
| `numeric-setting` | accepted upstream units | Full publish/set/get eligibility, required finite discovered bounds, value-mode State, observed matching |
| `enum-setting` | none | Full publish/set/get eligibility, choices from expose, observed matching |
| `enum-action` | `access: set-only \| includes-set` | Choices from expose, stateless dispatched outcome, no State/get/matcher |

Parameters for `binary-power`, `brightness`, `color-temperature`, `color-xy`, `color-hs`, `color-mode`, `startup-color-temperature`, `temperature`, and `enum-setting` are exactly `{}`.

The generic `numeric-sensor` strategy covers humidity, battery, link quality, and electrical measurements. Its parameters have exactly this shape:

```json
{
  "accepted_units": ["", "lqi"],
  "unit": "lqi",
  "number_format": "integer",
  "bounds": {
    "mode": "fixed",
    "minimum": 0,
    "maximum": 255
  }
}
```

- `accepted_units` is required, contains 1–4 unique exact strings of at most 32 characters, and may contain `""` to mean the upstream unit must be absent. No implicit unit conversion occurs.
- `unit` is a required non-empty Hearth support unit of at most 32 characters.
- `number_format` is exactly `integer` or `float`. `integer` uses the existing exact-integer decoder; `float` accepts finite JSON numbers and preserves fractions.
- `bounds` is a discriminated union:
  - `{"mode":"fixed","minimum":N,"maximum":N}` always uses the configured finite bounds and ignores upstream bound metadata;
  - `{"mode":"upstream-or-fallback","minimum":N,"maximum":N}` prefers both present valid finite upstream bounds, uses the configured values only when both upstream bounds are genuinely absent, and makes the Entity ineligible for malformed, one-sided, non-finite, or inverted upstream bounds.
- Both forms require finite `minimum < maximum`.
- The strategy always requires publish access, forbids set access, requires a non-empty Device-unique property, and includes a startup get property only when upstream get access is present.

The `numeric-setting` parameters have exactly this shape:

```json
{
  "accepted_units": ["%"],
  "unit": "%"
}
```

- `accepted_units` follows the same exact-match rules as `numeric-sensor`.
- `unit` is optional. When omitted, Hearth support omits its optional unit; the profile must include `""` in `accepted_units` if an absent upstream unit is required. When present it is a non-empty string of at most 32 characters and is copied to Hearth support without conversion.
- The strategy always requires publish, set, and get access; a non-empty Device-unique property; and both present finite upstream bounds with `minimum < maximum`. It preserves finite fractional JSON numbers in numeric value mode and uses observed exact matching.

The `enum-action` parameters are exactly `{"access":"set-only"}` or `{"access":"includes-set"}`. The first requires access to equal the set bit; the second requires the set bit and ignores additional publish/get bits. Both require a non-empty Device-unique property and non-empty unique expose values. The strategy always produces a stateless dispatched action.

## Device overrides

Overrides address proven vendor/model/firmware quirks without making the generic catalog model-specific.

```go
type profileOverrideDocument struct {
    Version  int                   `json:"version"`
    Kind     profileDocumentKind   `json:"kind"`
    ID       string                `json:"id"`
    Selector profileDeviceSelector `json:"selector"`
    Patches  []profileRulePatch    `json:"patches"`
}

type profileDeviceSelector struct {
    Vendor           string   `json:"vendor"`
    Model            string   `json:"model"`
    SoftwareBuildIDs []string `json:"software_build_ids,omitempty"`
}

type profileRulePatch struct {
    Rule               string                 `json:"rule"`
    Enabled            *bool                  `json:"enabled,omitempty"`
    Source             *profileEntitySource   `json:"source,omitempty"`
    Expose             *profileExposeSelector `json:"expose,omitempty"`
    StrategyParameters json.RawMessage        `json:"strategy_parameters,omitempty"`
}
```

Rules:

- `vendor` and `model` are both required exact strings.
- `software_build_ids`, when present, is a non-empty unique list of exact strings. Version 1 does not interpret firmware as semantic versions.
- For one target rule, evaluation starts from the base rule, overlays at most one matching general vendor/model patch, then overlays at most one matching exact-build patch.
- Patch fields overlay independently. An omitted field retains the value from the previous layer. `source`, `expose`, and `strategy_parameters`, when present, each replace that complete field rather than deep-merging it. `enabled` defaults to true in the base; an exact-build patch may explicitly re-enable a rule disabled by the general patch.
- "At most one" applies per target rule, not per document. Two general patches for the same vendor, model, and target conflict. Exact-build selectors for the same vendor, model, and target must have disjoint build-ID sets. Duplicate patches for one target inside one document also conflict.
- Candidate-entity patches may specify `source` and must omit `expose`. Device-entity patches may specify `expose` and must omit `source`. Supplying the wrong field for the target rule kind is `override_target_kind_mismatch`.
- A patch may disable a rule, replace its candidate source or device expose match, or fully replace strategy parameters.
- A patch cannot change the rule ID, Entity key, display name, strategy name, contribution, group, dependency, or order.
- A strategy-parameter replacement must validate against the target strategy.
- Overrides cannot add rules in version 1. A new standard capability belongs in a planner profile. New behavior requires a Go strategy.

`upstreamDevice` gains tolerant decoding for top-level `software_build_id`. Vendor and model come from the existing definition. These strings only select overrides and never enter Binding or Entity identities.

## JSON Schema contract

`profiles/profile.schema.json` uses JSON Schema draft 2020-12 and an ID such as `urn:hearth:schema:zigbee2mqtt-profile:v1`.

Required schema properties:

- closed objects (`additionalProperties: false`) at every level;
- `version` exactly `1`;
- a discriminated `oneOf` for `planner-profile` and `profile-override`;
- profile and rule IDs matching `^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$`, maximum 64 characters;
- positive unique profile orders bounded to 1–1000;
- known Device kinds and planner roles;
- non-empty candidate groups and Entity lists within bounded maximum sizes;
- base Entity keys matching `^[a-z][a-z0-9]*$`;
- display names from 1 through 128 characters;
- expose type/name/unit strings bounded to 128/32 characters as appropriate;
- finite numeric bounds with minimum strictly less than maximum, enforced semantically where JSON Schema cannot compare fields;
- strategy references represented by a discriminated `oneOf`, so each strategy accepts only its documented parameter object;
- override selectors and patches with the constraints above, including mutually exclusive `source`/`expose` fields tied semantically to the target rule kind;
- no `null` as a substitute for an omitted optional field unless explicitly part of the strategy contract.

Each embedded document is limited to 256 KiB. The catalog permits at most 64 documents, 64 candidate groups per profile, 64 rules per group, 64 device rules per profile, 64 patches per override, and 512 total rules. The compiler checks aggregate limits that JSON Schema cannot express.

The implementation reuses `github.com/santhosh-tekuri/jsonschema/v6` and the repository's `entitytypes.CompileJSONCodec` pattern. The Adapter owns this schema under `internal/adapters/zigbee2mqtt/profiles/`.

## Catalog compilation and conflicts

`LoadEmbeddedProfileCatalog` performs all work before returning:

1. read embedded schema and profile files in lexical path order;
2. compile the authoritative profile schema;
3. schema-validate and decode every document as exactly one JSON value;
4. enforce file and aggregate limits;
5. sort planner profiles by explicit unique `order`;
6. validate unique document, group, and globally unique rule IDs;
7. validate contribution kind/role combinations;
8. validate gate, group-survivor, and `requires_any` references, including that every group-survivor target has a gate;
9. require `requires_any` to reference earlier rules in the same group;
10. resolve every strategy and compile its parameters;
11. validate strategy/source compatibility, including the closed root/feature/derived source forms;
12. reject duplicate base Entity keys within one planner profile;
13. validate override targets, target-rule kind, layered selector overlap, duplicate target patches, and replacement parameters; and
14. return an immutable catalog only if every document succeeds.

Catalog failures are deterministic typed errors:

```go
type ProfileCatalogError struct {
    Code       string
    Document   string
    ProfileID  string
    RuleID     string
    JSONPointer string
    Err        error
}

func (err *ProfileCatalogError) Error() string
func (err *ProfileCatalogError) Unwrap() error
```

Stable error codes include:

- `schema_invalid`
- `document_too_large`
- `catalog_limit_exceeded`
- `duplicate_document_id`
- `duplicate_profile_order`
- `duplicate_group_id`
- `duplicate_rule_id`
- `duplicate_entity_key`
- `invalid_contribution`
- `unknown_reference`
- `invalid_group_reference`
- `invalid_dependency_order`
- `unknown_strategy`
- `strategy_source_mismatch`
- `strategy_parameters_invalid`
- `override_target_unknown`
- `override_target_kind_mismatch`
- `override_selector_conflict`

A catalog conflict means two repository-authored declarations cannot be compiled deterministically. Runtime device data is not a catalog conflict. Duplicate upstream roots, property collisions, unresolved endpoints, unsupported units, and malformed expose metadata retain existing per-device/per-Entity isolation behavior.

## Planning semantics

For each device, the evaluator:

1. validates Device type, support, interview, definition, IEEE address, and friendly name;
2. builds the existing `exposeIndex`, including recursive claims from supported and unsupported exposes;
3. selects overrides by exact vendor, model, and software-build evidence;
4. evaluates profiles by `order`, then candidate groups and roots in retained inventory order;
5. resolves sources through `UniqueFeature` or `UniqueRoot`, applies any override, and invokes the strategy;
6. applies gate deduplication and sibling gating;
7. evaluates device Entities whose required candidate group survived; and
8. returns one `plannerContribution` per profile for the existing merge and validation logic.

An ineligible strategy result omits that candidate. Same-contribution duplicate keys are all omitted. A remaining cross-contribution key collision rejects the device as `ambiguous_entity_plan`. No eligible contribution retains the existing `no_eligible_light`, `no_eligible_relay`, or `no_eligible_entity` attribution. More than 64 Entities rejects the device as `too_many_entities`.

## Identity and compatibility contract

The migration must preserve these values byte-for-byte for every existing fixture:

- Binding key: `z2m-<normalized IEEE without 0x>`;
- Device external ID: normalized IEEE address;
- Device kind: `light`, `relay`, or `sensor` under current precedence;
- root Entity keys and all `-ep<N>` scoped variants;
- Entity external IDs: `<ieee>/root/<key>` or `<ieee>/ep<N>/<key>`;
- display names and endpoint-label prefixes;
- Entity type and normalized support JSON;
- registration and Entity order;
- State and get-property routes;
- stateless/read-only/controllable classification;
- discovery rejection codes.

Property names, profile IDs, rule IDs, vendor, model, firmware, and friendly names never enter canonical Entity keys. Overrides may not change identity fields.

Because Hearth has no deployments, private planner types may change directly and no compatibility shim is required. Observable identities remain stable to preserve fixtures, mapping reconciliation, and future deployment expectations.

## Startup and composition

Catalog loading occurs after YAML config validation and logger construction but before `adapter.Connect` or any MQTT work.

```diff
diff --git a/internal/app/zigbee2mqtt/run.go b/internal/app/zigbee2mqtt/run.go
@@
 func Run(ctx context.Context, config Config, logger *slog.Logger) error {
     if err := config.Validate(); err != nil {
         return err
     }
+    catalog, err := zigbee2mqttadapter.LoadEmbeddedProfileCatalog()
+    if err != nil {
+        return failStage("load_profile_catalog", err)
+    }
     // adapter.Connect remains after successful catalog compilation.
@@
-    zigbeeAdapter, err := zigbee2mqttadapter.New(session, adapterConfig, logger)
+    zigbeeAdapter, err := zigbee2mqttadapter.New(session, adapterConfig, catalog, logger)
```

```diff
diff --git a/internal/adapters/zigbee2mqtt/adapter.go b/internal/adapters/zigbee2mqtt/adapter.go
@@
 type Adapter struct {
     session Session
     config  Config
+    profiles *ProfileCatalog
@@
-func New(session Session, config Config, logger *slog.Logger) (*Adapter, error)
+func New(session Session, config Config, profiles *ProfileCatalog, logger *slog.Logger) (*Adapter, error)
```

`New` rejects a nil catalog. Tests use a helper that compiles the embedded catalog or a supplied in-memory profile set; there is no process-global catalog.

The app module adopts the existing Hearth Core `runStageError`/`ErrorStage` pattern so `cmd/hearth-adapter-zigbee2mqtt` emits `process.failed` with `stage=load_profile_catalog` and `error_code=profile_catalog_invalid`. The Adapter logs one safe structured diagnostic with catalog error code, profile ID, rule ID, and JSON pointer. It never logs profile contents, MQTT payloads, URLs, or raw values.

## Discovery interface changes

Catalog dependency is explicit through discovery and tests:

```diff
diff --git a/internal/adapters/zigbee2mqtt/discovery.go b/internal/adapters/zigbee2mqtt/discovery.go
@@
-func discoverInventory(payload []byte) (inventoryDiscovery, error)
+func discoverInventory(payload []byte, profiles *ProfileCatalog) (inventoryDiscovery, error)
@@
-func discoverDevice(device upstreamDevice) (discoveredDevice, *deviceRejection)
+func discoverDevice(device upstreamDevice, profiles *ProfileCatalog) (discoveredDevice, *deviceRejection)
@@
-func buildDiscoveredDevice(device upstreamDevice, ieeeAddress string) (discoveredDevice, *deviceRejection)
+func buildDiscoveredDevice(
+    device upstreamDevice,
+    ieeeAddress string,
+    profiles *ProfileCatalog,
+) (discoveredDevice, *deviceRejection)
```

`connection.go` passes `z2m.profiles`. Existing tests update to use `mustEmbeddedProfileCatalog(t)` or compile a focused in-memory catalog. No compatibility wrapper retains implicit global planning.

## Migration plan

Implement deliverables D1 through D7 in dependency order. Handwritten planners may remain temporarily for differential tests while D2 through D6 establish parity. D7 makes profile planning the sole production path and deletes handwritten planner assembly and mapping tables. The final release has no dual path or runtime flag.

## Project layout

```text
cmd/hearth-adapter-zigbee2mqtt/
└── main.go                                      # modify, report load_profile_catalog failures
internal/app/zigbee2mqtt/
├── run.go                                       # modify, load catalog before external connections
├── run_stage.go                                 # new, classify startup-stage errors
└── run_test.go                                  # modify, prove catalog failure prevents connection
internal/adapters/zigbee2mqtt/
├── adapter.go                                   # modify, require and store ProfileCatalog
├── connection.go                                # modify, pass catalog into inventory discovery
├── discovery.go                                 # modify, accept an explicit catalog
├── discovery_wire.go                            # modify, decode software_build_id
├── device_planner.go                            # modify, merge profile contributions
├── expose_index.go                              # retain endpoint and property ownership
├── entity_plan.go                               # retain plan and command invariants
├── profile_catalog.go                           # new, embed and load the opaque catalog
├── profile_types.go                             # new, define private profile and compiled types
├── profile_compile.go                           # new, validate and compile profiles
├── profile_plan.go                              # new, evaluate profiles
├── profile_strategy.go                          # new, define and compile closed strategies
├── profile_catalog_test.go                      # new, test schema, limits, errors, and overrides
├── profile_plan_test.go                         # new, test planning and identity behavior
├── profile_parity_test.go                       # new, compare old and new planners
├── profiles/
│   ├── profile.schema.json                      # new, authoritative JSON Schema
│   ├── light.profile.json                       # new, current light mappings
│   ├── relay.profile.json                       # new, current relay and smart-plug mappings
│   ├── sensors.profile.json                     # new, temperature, humidity, and battery mappings
│   ├── linkquality.profile.json                 # new, link-quality mapping
│   └── overrides/                               # new, proven vendor, model, and build patches
├── planner_light.go                             # delete after parity
├── planner_relay.go                             # delete after parity
├── planner_sensor.go                            # delete after parity
├── entity_*.go                                  # modify, retain strategies and remove mapping tables
└── testdata/                                    # retain captured and synthetic fixtures
internal/adapters/zigbee2mqtt/fixture_helpers_test.go # modify, provide explicit catalog helper
internal/app/zigbee2mqtt/run_integration_test.go      # modify, test startup and profile-backed flow
README.md                                             # modify, document profile authoring
docs/architecture.md                                 # modify, accept embedded profiles
docs/adr/0019-use-embedded-zigbee2mqtt-profiles.md   # new, record the decision
specs/zigbee2mqtt-entity-planning.md                  # modify, mark handwritten planning superseded
specs/zigbee2mqtt-adapter.md                          # modify, specify profile-backed discovery
```

Profile code stays in the Adapter package because it constructs private `entityPlan` values and uses private expose-index rules. A separate package would create an import cycle or require public planner types solely for profile evaluation.

## Deliverables

| ID | Outcome | Effort | Owning paths | Depends on | Acceptance |
|---|---|---:|---|---|---|
| D1 | Record the architecture decision and authoritative profile contract | M | `docs/adr/0019-*`, `docs/architecture.md`, `profiles/profile.schema.json`, `profile_types.go` | None | A1, A2 |
| D2 | Compile and validate the embedded catalog with fail-fast startup diagnostics | L | `profile_catalog.go`, `profile_compile.go`, `profile_catalog_test.go`, `internal/app/zigbee2mqtt/run*.go`, command main/tests | D1 | A3, A4, A5 |
| D3 | Build the closed Go strategy registry around existing Entity behavior | L | `profile_strategy.go`, selected `entity_*.go`, strategy-focused tests | D1 | A6, A7 |
| D4 | Migrate read-only sensor and link-quality discovery with differential parity | L | `sensors.profile.json`, `linkquality.profile.json`, `profile_plan.go`, parity tests | D2, D3 | A8, A9 |
| D5 | Migrate relay and smart-plug discovery with differential parity | L | `relay.profile.json`, relay/entity mapping helpers, plug tests/fixtures | D2, D3 | A8, A10 |
| D6 | Migrate light/color discovery with differential parity | XL | `light.profile.json`, color strategy bindings, color/bulb fixtures and tests | D2, D3 | A8, A11 |
| D7 | Cut over, delete handwritten planners, and update author documentation | L | `device_planner.go`, `planner_*.go`, `discovery.go`, `README.md`, existing specs | D4, D5, D6 | A12, A13, A14 |

## Acceptance criteria

- [ ] **A1:** `profile.schema.json` validates every embedded profile and rejects unknown fields, wrong document versions/kinds, malformed strategy parameters, and out-of-contract limits.
- [ ] **A2:** The catalog format, strategy list, override restrictions, ordering, and conflict definitions are documented without unspecified behavior or implementation-defined precedence.
- [ ] **A3:** Catalog compilation rejects every stable semantic error code with deterministic document/rule/pointer evidence and never returns a partial catalog.
- [ ] **A4:** A catalog load failure occurs before NATS session connection or MQTT dialing; executable logs report `stage=load_profile_catalog` and `error_code=profile_catalog_invalid` without raw profile content.
- [ ] **A5:** The embedded catalog compiles at startup and under `mise run validate`; no generated effective-catalog file or operator profile path exists.
- [ ] **A6:** Every strategy reference resolves to a closed Go registry entry, and strategy/source mismatches fail catalog compilation.
- [ ] **A7:** Generic numeric sensor, numeric setting, enum setting, and enum action strategies can plan a synthetic new scalar mapping using only an in-memory JSON profile and fixture. No mapping-specific Go branch is required.
- [ ] **A8:** Every existing `bridge-devices-*.json` fixture produces byte-identical Device kind, ordered descriptors/support, Entity keys/external IDs/names/types, State/get routes, and rejection codes under profile planning.
- [ ] **A9:** Sensor-only, supplemental sensor, link-quality unit/get behavior, exact integer handling, and malformed-sibling isolation remain unchanged.
- [ ] **A10:** Relay power, smart-plug electrical sensors/settings/actions, endpoint identity, duplicate-power gating, observed settings, dispatched reset, and captured `3RSP02028BZ` behavior remain unchanged.
- [ ] **A11:** Light precedence, brightness scaling, color-temperature activity, XY/HS shared properties, color-mode derivation, startup sentinel, effects, multi-endpoint identity, and same-message completeness remain unchanged.
- [ ] **A12:** Existing runtime Command, concurrency, MQTT, reconciliation, and observation tests pass without weakened freshness, matching, or dispatch assertions.
- [ ] **A13:** Final production discovery has one profile-backed path. Handwritten planner mappings and all production mapping allowlists or per-expose metadata tables are deleted, with no compatibility shim or feature flag.
- [ ] **A14:** `mise run validate` passes and final diff review shows no generated, formatted, or module-metadata drift beyond intended changes.

## Test strategy

| Layer | Behavior protected | Approach |
|---|---|---|
| Schema | Closed/versioned profile documents and bounded fields | Compile authoritative schema; valid/invalid fixture matrix; trailing JSON and size-limit tests |
| Compiler | IDs, order, references, strategies, dependencies, overrides, conflicts | Table tests asserting exact `ProfileCatalogError.Code` and JSON pointer |
| Evaluator | Root order, feature/root selection, gating, supplements, overrides | Pure tests with synthetic `upstreamDevice` values and deterministic expected contributions |
| Differential | Observable parity with handwritten planners during migration | Run both planners over every inventory fixture; compare normalized plans, stateless policy, translator presence, rejection codes, and command-plan behavior |
| Strategy | Eligibility and conversion behavior | Keep existing entity tests; add generic-strategy tests with positive and negative reports/commands |
| Runtime | State, Command, freshness, dispatched outcomes, FIFO, route fencing | Run existing runtime tests against profile-produced plans without changing their oracles |
| Startup | Fail before external I/O | Inject catalog loader/connect functions in an internal app test; assert zero connection calls on invalid catalog |
| Fuzz | Malformed profile safety | Fuzz schema/compiler inputs; assert no panic, no partial catalog, and deterministic error classification |
| Real device | Profile matches actual upstream expose/state | Use the existing real-device-validation workflow for any new override; capture sanitized minimal fixtures |
| Repository | Formatting, generated code, lint, race tests, vet, profile validity | `mise run validate` |

Meaningful tests must state the protected behavior and plausible defect. Differential tests are temporary migration evidence; after handwritten planners are deleted, retained fixture expectations become the independent contract oracle.

## Decision rationale

| Decision | Alternative | Reason |
|---|---|---|
| Embedded planner profiles | Handwritten planners or per-model manifests | Expose shape is the runtime contract and varies across models and firmware. Profiles remove repeated Go mapping policy. |
| Closed Go strategy registry | A general expression language | Go retains typed State, Command, and outcome behavior. |
| Exact vendor, model, and build overrides | Regular expression or semantic-version selectors | Zigbee firmware strings are not reliably semantic versions. Exact matching is deterministic. |
| Explicit profile order | Filename precedence or tie-breaking | Family precedence is observable and must be explicit. |
| Whole-catalog startup failure | Skipping invalid documents or rules | Profiles ship with the binary. A partial catalog would hide repository defects. |
| Runtime inventory isolation | Treating upstream conflicts as catalog failures | One malformed device or expose must not suppress valid inventory. |
| Embedded source JSON | Generated Go or an effective-catalog artifact | One compiler validates the single source in tests and at startup. |
| Existing Adapter package | A separate profile package | A separate package would expose private `entityPlan` and expose-index types or create an import cycle. |

## Risks and mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---:|---:|---|
| Identity or ordering drift during migration | Medium | High | Byte-for-byte differential fixtures before each family cutover; no identity fields in overrides |
| Profile syntax grows beyond mapping policy | Medium | High | Closed documents, closed strategies, and schema-version review for new syntax |
| Profile errors are hard to diagnose | Medium | Medium | Stable rule IDs, typed errors, JSON pointers, and traces that name the first failed eligibility check |
| Static conflict checks reject device-dependent alternatives | Low | Medium | Restrict catalog conflicts to structural contradictions and evaluate property or key ambiguity per device |
| Overrides obscure generic behavior | Medium | Medium | Require captured fixture evidence, exact selectors, patches only, and review a generic fix first |
| Temporary dual planners remain after migration | Medium | Medium | D7 deletes handwritten planners, and release waits for parity and cutover |

## Success metrics

Acceptance criteria A7, A8, A13, and A14 measure the cutover. Device-specific overrides remain a minority of profile rules and cite captured fixture evidence.

## Documentation decisions

Implementation must add an ADR accepting repository-owned embedded Adapter profiles. It must update `docs/architecture.md` to replace "explicit light, relay, and sensor planners" with profile-backed planning. The current deferral narrows to user-supplied and runtime-loaded manifests.

`CONTEXT.md` does not change. "Profile," "rule," "strategy," and "override" are Adapter implementation terms, not household domain concepts.

## Open questions

None.
