# Zigbee2MQTT JSON Profile Catalog

**Status:** Ready for task breakdown
**Approved by:** User
**Type:** RFC / architectural refactor
**Effort:** XL, 7–12 focused days at 60% confidence
**Date:** 2026-09-09
**Baseline:** `6633d31`

## Summary

Replace handwritten Zigbee2MQTT Entity-mapping planners with a repository-owned catalog of embedded JSON profiles validated by JSON Schema. Profiles define which expose shapes produce which stable Hearth Entity identities and select a closed set of audited Go planning strategies. The Go implementation retains tolerant Zigbee2MQTT wire decoding, endpoint and property-ownership rules, typed Entity construction, State conversion, Command translation, outcome matching, and the runtime coordinator.

The first version migrates all current Entity discovery for lights, relays, smart plugs, ambient sensors, and link quality. Profiles are compiled once during Adapter startup, before any NATS or MQTT connection. Any invalid or semantically conflicting catalog fails startup as a whole. Malformed devices or exposes remain runtime inventory concerns and continue to be isolated without suppressing unrelated devices or valid sibling Entities.

The catalog is build-owned configuration, not operator configuration. Version 1 has no external profile path, remote profile source, hot reload, expression language, or runtime Entity-type registration.

## Problem Statement

### Who

Hearth maintainers adding or correcting Zigbee2MQTT device capabilities.

### What

Adding a standard Zigbee2MQTT scalar capability currently requires repetitive Go changes across family planners and Entity-specific planning helpers. The repeated implementation usually performs the same work:

1. locate a root expose or nested feature;
2. validate endpoint resolution, access, property ownership, unit, values, and bounds;
3. derive a stable Entity key, external ID, and display name;
4. invoke an existing typed Entity implementation;
5. gate the result on a surviving family capability; and
6. isolate malformed siblings.

The Third Reality `3RSP02028BZ` work required new mapping code for six numeric sensors, three numeric settings, one enum setting, and one enum action even though their runtime behavior fits existing Hearth Entity types.

### Why it matters

A data-driven mapping catalog concentrates device-support policy in reviewable JSON. Once a generic strategy exists, adding an ordinary binary, numeric, or enum capability should require one profile edit and fixture coverage rather than another planner implementation. Runtime correctness remains protected by Go-owned strategies and existing command-evidence invariants.

### Evidence

Current mapping policy is distributed across `planner_light.go`, `planner_relay.go`, `planner_sensor.go`, device-root helpers in `entity_*.go`, and fixed tables such as `smartPlugElectricalSensors` and `smartPlugNumericSettings`. The existing expose index and generic `entityPlan` runtime already provide the deeper mechanisms needed behind a profile-catalog seam.

## Goals

- Move all current Zigbee2MQTT Entity-mapping definitions and family contribution assembly into embedded JSON profiles.
- Make a new mapping that fits an existing strategy require only JSON plus tests/fixtures.
- Validate profiles with an authoritative JSON Schema and semantic compiler.
- Fail Adapter startup before external connections when the embedded catalog is invalid or conflicting.
- Preserve every current Binding key, Device kind, Entity key, external ID, name, type, support document, deterministic order, rejection code, State conversion, Command payload, refresh behavior, and outcome policy.
- Preserve capability-first discovery: profiles match expose shape, not a fixed list of device models.
- Support explicit vendor/model/software-build overrides for proven upstream quirks.
- Keep the profile language declarative and bounded; profiles select Go strategies but never define executable behavior.
- Preserve per-device and per-expose isolation for malformed upstream inventory.
- Keep the compiled catalog immutable and safe for concurrent inventory reconciliation.

## Non-Goals

- Operator-provided profile files or Adapter YAML keys for profile paths.
- Runtime profile reload, NATS distribution, remote downloads, or device-database synchronization.
- JavaScript, CEL, JSONata, regular expressions, templates, arbitrary boolean expressions, or another general-purpose rule language.
- New Hearth Entity types or runtime Entity-type registration.
- Changes to Core registration, persistence, HTTP, NATS wire contracts, MQTT behavior, or SDK contracts.
- Changes to State freshness, cross-message assembly, Command deadlines, matching, FIFO ordering, or evidence disposition.
- Automatically exposing every unknown Zigbee2MQTT expose. Only catalogued mappings are eligible.
- Preserving private handwritten planner interfaces after cutover.
- A generated effective-catalog artifact. Embedded source JSON remains the single source of truth.

## Decision

Build one deep profile-catalog module inside the existing `zigbee2mqtt` package. Its external interface consists of loading an opaque immutable catalog and passing it to the Adapter constructor. Planning remains private to the package.

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

The profile catalog replaces only mapping policy. These mechanisms remain Go-owned:

- tolerant `bridge/devices` decoding;
- IEEE and friendly-name validation;
- endpoint resolution and scoping;
- expose indexing and recursive property-claim counting;
- unique-root and unique-feature selection;
- the color XY/HS shared-property exception;
- foreign color-mode claim detection;
- canonical scalar handling;
- exact integer and rational-number conversions;
- typed Entity descriptor and Observation construction;
- Command translation and generated outcome matching;
- same-message multi-property completeness;
- primary/supplemental merge semantics;
- runtime route fencing, MQTT generations, FIFO queues, deadlines, refresh, and evidence publication.

This separation keeps the catalog interface deep: profile authors describe mapping intent while the implementation hides protocol and Entity-type complexity.

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

## Profile Documents

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

A candidate group evaluates roots in retained inventory order.

- `root` selects upstream roots by exact expose `type` and optional exact `name`.
`source` is a closed JSON Schema `oneOf`; fields not named by the selected form are forbidden:

```json
{"kind": "root"}
{"kind": "feature", "type": "numeric", "name": "brightness"}
{"kind": "derived", "name": "color-mode"}
```

- `root` permits only `kind` and passes the selected root expose to the strategy.
- `feature` requires non-empty exact `type` and `name`, then performs the existing exact-one `UniqueFeature(type,name)` lookup inside the selected root.
- `derived` requires `name`, forbids `type`, and in version 1 accepts only `color-mode`. It passes no expose to the strategy; the strategy receives the selected root, expose index, and plans from its declared `requires_any` rules.
- If `gate_rule` is present, that rule must be the first Entity rule. Optional siblings are evaluated only when the gate produces a valid plan.
- Candidate groups are deduplicated by the scoped key produced by their gate. Every candidate sharing a duplicate gate key is dropped with all its siblings, preserving current light and relay behavior.
- `requires_any` may reference only earlier rules in the same candidate group. The rule is evaluated only if at least one referenced sibling produced a plan. This represents read-only color mode without creating a general dependency language.
- A group without a gate evaluates each rule independently; malformed roots or rules do not suppress valid siblings.

### Device-entity semantics

Device Entities use the existing `UniqueRoot(type,name)` behavior and are evaluated once after candidate groups. `requires_group_survivor` must name a candidate group with a non-empty `gate_rule`; referencing an ungated group is a startup-fatal `invalid_group_reference`. A device Entity is evaluated only when that group retains at least one candidate after gate planning and duplicate-gate-key removal.

This represents light-level power-on behavior and effects, plus relay-level power-on behavior, electrical measurements, numeric settings, and reset action. A missing, duplicate, unresolved, or ineligible root omits only that device Entity.

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
    },
    {
      "id": "relay.electrical-power",
      "expose": {"type": "numeric", "name": "power"},
      "requires_group_survivor": "relay.roots",
      "identity": {"key": "electricalpower", "name": "Electrical Power"},
      "strategy": {
        "name": "numeric-sensor",
        "parameters": {
          "accepted_units": ["W"],
          "unit": "W",
          "number_format": "float",
          "bounds": {
            "mode": "upstream-or-fallback",
            "minimum": 0,
            "maximum": 1000000000
          }
        }
      }
    },
    {
      "id": "relay.reset-total-energy",
      "expose": {"type": "enum", "name": "reset_total_energy"},
      "requires_group_survivor": "relay.roots",
      "identity": {"key": "resettotalenergy", "name": "Reset Total Energy"},
      "strategy": {
        "name": "enum-action",
        "parameters": {"access": "includes-set"}
      }
    }
  ]
}
```

Missing voltage, current, energy, or any other optional expose simply produces no corresponding Entity. Profiles describe capability rules, not a mandatory device shape.

## Strategy Registry

Profiles select a closed registry of concrete Go strategies. The registry is not an extensibility interface and uses no `init` registration, reflection, or plugins.

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
| `enum-action` | `access: set-only | includes-set` | Choices from expose, stateless dispatched outcome, no State/get/matcher |

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

## Device Overrides

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
- “At most one” is per target rule, not per document. Two general patches for the same vendor/model and target conflict. Exact-build selectors for the same vendor/model and target must have disjoint build-ID sets; intersecting sets conflict. Duplicate patches for one target inside one document also conflict.
- Candidate-entity patches may specify `source` and must omit `expose`. Device-entity patches may specify `expose` and must omit `source`. Supplying the wrong field for the target rule kind is `override_target_kind_mismatch`.
- A patch may disable a rule, replace its candidate source or device expose match, or fully replace strategy parameters.
- A patch cannot change the rule ID, Entity key, display name, strategy name, contribution, group, dependency, or order.
- A strategy-parameter replacement must validate against the target strategy.
- Overrides cannot add rules in version 1. A new standard capability belongs in a capability profile; genuinely new behavior requires a Go strategy.

`upstreamDevice` gains tolerant decoding for top-level `software_build_id`. Vendor and model come from the existing definition. These strings are matching evidence only and never enter Binding or Entity identities.

## JSON Schema Contract

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

The implementation reuses `github.com/santhosh-tekuri/jsonschema/v6` and the repository's `entitytypes.CompileJSONCodec` pattern. It does not place Adapter-local schemas in `contracts/v1` or `entitytypes/`.

## Catalog Compilation and Conflicts

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

## Planning Semantics

For each device:

1. Existing discovery code validates Device type, support, interview, definition, IEEE address, and friendly name.
2. Build the existing `exposeIndex`, including recursive claims from supported and unsupported exposes.
3. Select matching overrides from exact vendor/model/software-build evidence.
4. Evaluate compiled planner profiles by `order`.
5. Within each profile, evaluate candidate groups and roots in retained inventory order.
6. Resolve rule sources through existing `UniqueFeature`/`UniqueRoot` behavior.
7. Apply an override, if any, before invoking the target strategy.
8. Invoke the Go strategy; ineligible candidates are omitted individually.
9. Apply candidate gate deduplication and sibling gating.
10. Evaluate device Entities only when their required candidate group survived.
11. Return one `plannerContribution` per profile.
12. Use the existing primary/supplemental merge and validation semantics.

The following behavior is unchanged:

- light is the first primary and wins over relay when both contribute;
- relay wins when light does not contribute;
- ambient sensors and link quality supplement either primary family;
- sensor-only devices use kind `sensor`;
- same-contribution duplicate keys are all omitted;
- a remaining cross-contribution key collision rejects the device as `ambiguous_entity_plan`;
- no eligible contribution uses the existing `no_eligible_light`, `no_eligible_relay`, or `no_eligible_entity` attribution;
- more than 64 Entities rejects as `too_many_entities`;
- malformed optional exposes never suppress unrelated valid siblings.

## Identity and Compatibility Contract

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

## Startup and Composition

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

## Discovery Interface Changes

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

## Migration Plan

Migration is incremental internally but ships only after complete parity. There is no runtime flag or dual behavior in the final state.

### Stage 1: characterize the baseline

Before cutover, create one differential test harness that compares handwritten planning with profile planning for every existing `bridge-devices-*.json` fixture. Compare Device kind, Entity order, descriptors, support bytes, State/get properties, stateless policy, translator presence, rejection code, and command plan behavior.

### Stage 2: catalog foundation

Add the schema, embedded documents, compiler, typed errors, strategy registry, startup stage, and in-memory test compiler. At this stage handwritten planners may remain the production path while the profile evaluator runs only in differential tests.

### Stage 3: read-only scalar profiles

Migrate ambient temperature, humidity, battery, link quality, and electrical numeric sensors. Preserve integer versus fractional decoding and upstream-get behavior.

### Stage 4: relay profile

Migrate shared relay power, power-on behavior, numeric settings, reset action, endpoint scoping, relay family gating, and smart-plug fixtures.

### Stage 5: light profile

Migrate power, brightness, color temperature, XY, HS, color mode, startup color temperature, effects, shared-color-property rules, same-message mode activity, and multi-endpoint fixtures.

### Stage 6: cutover and deletion

Make profile planning the sole production path. Delete handwritten planner assembly and mapping tables rather than retaining compatibility shims. Keep Go strategy implementations, expose indexing, generic merge, Entity plan validation, and runtime coordination.

## Project Layout

```text
cmd/hearth-adapter-zigbee2mqtt/
└── main.go                                      # modify — reports load_profile_catalog startup failures
internal/app/zigbee2mqtt/
├── run.go                                       # modify — loads catalog before external connections
├── run_stage.go                                 # new — startup-stage error classification
└── run_test.go                                  # modify — proves catalog failure prevents connection
internal/adapters/zigbee2mqtt/
├── adapter.go                                   # modify — requires and stores ProfileCatalog
├── connection.go                                # modify — passes catalog into inventory discovery
├── discovery.go                                 # modify — explicit catalog dependency
├── discovery_wire.go                            # modify — decodes software_build_id for overrides
├── device_planner.go                            # modify — merges compiled profile contributions
├── expose_index.go                              # retain — endpoint/property ownership implementation
├── entity_plan.go                               # retain — immutable plan and command invariants
├── profile_catalog.go                           # new — embed, opaque catalog, public loader
├── profile_types.go                             # new — private profile and compiled types
├── profile_compile.go                           # new — schema and semantic compilation
├── profile_plan.go                              # new — deterministic candidate/profile evaluation
├── profile_strategy.go                          # new — closed strategy registry and parameter compilation
├── profile_catalog_test.go                      # new — schema, limits, errors, conflicts, overrides
├── profile_plan_test.go                         # new — precedence, gating, endpoint, identity behavior
├── profile_parity_test.go                       # new — differential fixture parity during migration
├── profiles/
│   ├── profile.schema.json                      # new — authoritative closed JSON Schema
│   ├── light.profile.json                       # new — current light mappings
│   ├── relay.profile.json                       # new — current relay/smart-plug mappings
│   ├── sensors.profile.json                     # new — temperature/humidity/battery mappings
│   ├── linkquality.profile.json                 # new — link-quality supplemental mapping
│   └── overrides/                               # new — proven vendor/model/build patches
├── planner_light.go                             # delete after parity — mapping replaced by light profile
├── planner_relay.go                             # delete after parity — mapping replaced by relay profile
├── planner_sensor.go                            # delete after parity — mapping replaced by sensor profile
├── entity_*.go                                  # modify selectively — retain strategies, remove plan/mapping tables
└── testdata/                                    # retain — captured and synthetic device/state fixtures
internal/adapters/zigbee2mqtt/fixture_helpers_test.go # modify — explicit catalog helper
internal/app/zigbee2mqtt/run_integration_test.go      # modify — startup and profile-backed flow
README.md                                             # modify — profile author workflow and supported behavior
docs/architecture.md                                 # modify — accept embedded profile planning and narrow deferred runtime manifests
docs/adr/0019-use-embedded-zigbee2mqtt-profiles.md   # new — architecture decision and consequences
specs/zigbee2mqtt-entity-planning.md                  # modify — mark handwritten-planner design superseded
specs/zigbee2mqtt-adapter.md                          # modify — profile-backed discovery/startup contract
```

Profile code stays in the existing Adapter package because it must construct private `entityPlan` values and reuse private expose-index semantics. A separate Go subpackage would either create an import cycle or expose broad internal runtime types, producing a shallower interface.

## Deliverables

| ID | Outcome | Effort | Owning paths | Depends on | Acceptance |
|---|---|---:|---|---|---|
| D1 | Record the architecture decision and authoritative profile contract | M | `docs/adr/0019-*`, `docs/architecture.md`, `profiles/profile.schema.json`, `profile_types.go` | — | A1, A2 |
| D2 | Compile and validate the embedded catalog with fail-fast startup diagnostics | L | `profile_catalog.go`, `profile_compile.go`, `profile_catalog_test.go`, `internal/app/zigbee2mqtt/run*.go`, command main/tests | D1 | A3, A4, A5 |
| D3 | Build the closed Go strategy registry around existing Entity behavior | L | `profile_strategy.go`, selected `entity_*.go`, strategy-focused tests | D1 | A6, A7 |
| D4 | Migrate read-only sensor and link-quality discovery with differential parity | L | `sensors.profile.json`, `linkquality.profile.json`, `profile_plan.go`, parity tests | D2, D3 | A8, A9 |
| D5 | Migrate relay and smart-plug discovery with differential parity | L | `relay.profile.json`, relay/entity mapping helpers, plug tests/fixtures | D2, D3 | A8, A10 |
| D6 | Migrate light/color discovery with differential parity | XL | `light.profile.json`, color strategy bindings, color/bulb fixtures and tests | D2, D3 | A8, A11 |
| D7 | Cut over, delete handwritten planners, and update author documentation | L | `device_planner.go`, `planner_*.go`, `discovery.go`, `README.md`, existing specs | D4, D5, D6 | A12, A13, A14 |

## Acceptance Criteria

- [ ] **A1:** `profile.schema.json` validates every embedded profile and rejects unknown fields, wrong document versions/kinds, malformed strategy parameters, and out-of-contract limits.
- [ ] **A2:** The catalog format, strategy list, override restrictions, ordering, and conflict definitions are documented without unspecified behavior or implementation-defined precedence.
- [ ] **A3:** Catalog compilation rejects every stable semantic error code with deterministic document/rule/pointer evidence and never returns a partial catalog.
- [ ] **A4:** A catalog load failure occurs before NATS session connection or MQTT dialing; executable logs report `stage=load_profile_catalog` and `error_code=profile_catalog_invalid` without raw profile content.
- [ ] **A5:** The embedded catalog compiles at startup and under `mise run validate`; no generated effective-catalog file or operator profile path exists.
- [ ] **A6:** Every strategy reference resolves to a closed Go registry entry, and strategy/source mismatches fail catalog compilation.
- [ ] **A7:** Generic numeric sensor, numeric setting, enum setting, and enum action strategies can plan a synthetic new scalar mapping using only an in-memory JSON profile and fixture—no mapping-specific Go branch.
- [ ] **A8:** Every existing `bridge-devices-*.json` fixture produces byte-identical Device kind, ordered descriptors/support, Entity keys/external IDs/names/types, State/get routes, and rejection codes under profile planning.
- [ ] **A9:** Sensor-only, supplemental sensor, link-quality unit/get behavior, exact integer handling, and malformed-sibling isolation remain unchanged.
- [ ] **A10:** Relay power, smart-plug electrical sensors/settings/actions, endpoint identity, duplicate-power gating, observed settings, dispatched reset, and captured `3RSP02028BZ` behavior remain unchanged.
- [ ] **A11:** Light precedence, brightness scaling, color-temperature activity, XY/HS shared properties, color-mode derivation, startup sentinel, effects, multi-endpoint identity, and same-message completeness remain unchanged.
- [ ] **A12:** Existing runtime Command, concurrency, MQTT, reconciliation, and observation tests pass without weakened freshness, matching, or dispatch assertions.
- [ ] **A13:** Final production discovery has one profile-backed path; handwritten planner mappings and duplicated mapping tables are deleted with no compatibility shim or feature flag.
- [ ] **A14:** `mise run validate` passes and final diff review shows no generated, formatted, or module-metadata drift beyond intended changes.

## Test Strategy

| Layer | Behavior protected | Approach |
|---|---|---|
| Schema | Closed/versioned profile documents and bounded fields | Compile authoritative schema; valid/invalid fixture matrix; trailing JSON and size-limit tests |
| Compiler | IDs, order, references, strategies, dependencies, overrides, conflicts | Table tests asserting exact `ProfileCatalogError.Code` and JSON pointer |
| Evaluator | Root order, feature/root selection, gating, supplements, overrides | Pure tests with synthetic `upstreamDevice` values and deterministic expected contributions |
| Differential | Observable parity with handwritten planners during migration | Run old and new planners over every existing inventory fixture; compare normalized plans and rejection codes |
| Strategy | Eligibility and conversion behavior | Keep existing entity tests; add generic-strategy tests with positive and negative reports/commands |
| Runtime | State, Command, freshness, dispatched outcomes, FIFO, route fencing | Run existing runtime tests against profile-produced plans without changing their oracles |
| Startup | Fail before external I/O | Inject catalog loader/connect functions in an internal app test; assert zero connection calls on invalid catalog |
| Fuzz | Malformed profile safety | Fuzz schema/compiler inputs; assert no panic, no partial catalog, and deterministic error classification |
| Real device | Profile matches actual upstream expose/state | Use the existing real-device-validation workflow for any new override; capture sanitized minimal fixtures |
| Repository | Formatting, generated code, lint, race tests, vet, profile validity | `mise run validate` |

Meaningful tests must state the protected behavior and plausible defect. Differential tests are temporary migration evidence; after handwritten planners are deleted, retained fixture expectations become the independent contract oracle.

## Alternatives Considered

| Option | Advantages | Disadvantages | Decision |
|---|---|---|---|
| Keep handwritten planners | Maximum Go type safety; no profile compiler | Repetitive mapping code and scattered policy continue | Rejected |
| Per-model device profiles | Simple lookup and easy exceptions | Duplicates shared capabilities; fails when firmware changes expose shape; large maintenance surface | Rejected |
| Generic expression/rule language | Maximum configurability | Turns JSON into executable code, weakens reviewability, and duplicates Go semantics | Rejected |
| Build-time generated Go catalog | Invalid profiles fail builds and runtime evaluation can be simpler | Introduces generated source/effective catalog as a second representation and does not remove startup validation requirement | Rejected for v1 |
| Separate profile Go package | Strong package boundary | Must expose private `entityPlan`/expose internals or create an import cycle | Rejected |
| Embedded profiles plus closed strategies | JSON-only ordinary mappings, exact startup validation, runtime remains typed Go | Front-loaded compiler/schema work and JSON debugging cost | Chosen |

## Trade-offs

| Chose | Over | Because |
|---|---|---|
| Capability profiles | Device-model manifests | Expose shape is the actual runtime contract and varies across models/firmware |
| Exact vendor/model/build overrides | Regex/semver selectors | Exact matching is deterministic and Zigbee firmware strings are not reliably semantic versions |
| Explicit profile order | Filename or priority tie-breaking | Current family precedence is an observable contract and must be obvious in review |
| Closed strategy registry | Configurable operations/outcomes | Command evidence and typed State behavior are safety-critical Go concerns |
| Startup failure | Skipping bad documents/rules | Profiles are shipped code; partial catalog behavior would hide repository defects |
| Device-level isolation | Treating upstream conflicts as catalog failures | Real inventories may be malformed without making the shipped catalog invalid |
| No generated catalog | Build-time materialization | Embedded source plus shared test/startup compiler avoids two sources of truth |
| Same package implementation | Separate subpackage | Preserves a small interface without exporting private planner/runtime types |

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---:|---:|---|
| Identity or ordering drift during migration | Medium | High | Byte-for-byte differential fixtures before each family cutover; no identity fields in overrides |
| Profile language grows into a programming language | Medium | High | Fixed document structure, closed strategies, no expressions/templates, schema-version review for new syntax |
| JSON becomes harder to debug than Go branches | Medium | Medium | Stable rule IDs, deterministic typed errors, JSON pointers, debug traces naming the first failed strategy eligibility check |
| Static conflict detection rejects valid mutually exclusive rules | Low | Medium | Limit catalog conflicts to structural contradictions; preserve device-dependent property/key ambiguity at evaluation time |
| Override accumulation obscures generic behavior | Medium | Medium | Require real-device fixture provenance; exact selectors; patches only; review generic fix before adding override |
| Complex color behavior is accidentally reimplemented in profiles | Low | High | Profiles only name color strategies; existing exact math and mode logic remain in Go and retain tests |
| Startup is bricked by a shipped profile typo | Low | High | Same compiler runs in tests and startup; `mise run validate` required; fail-fast is intentional rather than partial behavior |
| Temporary dual planner implementation fossilizes | Medium | Medium | D7 explicitly deletes handwritten planners; no release/merge until full parity and cutover are complete |

## Success Metrics

- Adding a standard numeric sensor/setting or enum setting/action supported by an existing strategy changes only a profile JSON file and fixture/tests.
- No production mapping allowlist or per-expose metadata table remains in Go after cutover.
- Every pre-migration fixture and runtime test passes with unchanged expected identities and behavior.
- Invalid repository profiles cannot establish NATS or MQTT connections.
- Device-specific overrides remain a minority of profile rules and always cite captured fixture evidence.

## Documentation Decisions

Implementation must add an ADR accepting repository-owned embedded Adapter profiles and update `docs/architecture.md` to replace “explicit light, relay, and sensor planners” with profile-backed planning. The existing statement deferring runtime manifest loading/general discovery must be narrowed explicitly: user-supplied/runtime-loaded manifests remain deferred, while build-owned embedded Adapter profiles are accepted.

`CONTEXT.md` does not change. “Profile,” “rule,” “strategy,” and “override” are Adapter implementation terms, not household domain concepts.

## Open Questions

None. The requested decisions are fixed for version 1:

- all current Entity discovery migrates;
- profiles are repository-owned embedded JSON;
- capability rules are primary and exact device overrides handle proven exceptions;
- invalid/conflicting catalogs fail startup;
- runtime behavior remains Go;
- operator configuration and hot reload are excluded.
