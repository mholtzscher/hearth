# Feature: Semantic numeric measurements

**Status:** APPROVED — ready for task breakdown.
**Approved:** 2026-09-12
**Effort:** XL

## Problem Statement

**Who:** Hearth users, adapters, automation authors, and UI clients that consume numeric readings from environmental sensors and future weather stations.

**What:** Hearth gives ambient temperature a dedicated `hearth.temperature/v1` type while humidity, illuminance, and battery level use `hearth.numericsensor/v1`. A generic numeric sensor identifies only a range and unit, so `%` cannot distinguish relative humidity from battery level and consumers must infer meaning from mutable names.

**Why it matters:** A weather station adds pressure, wind, precipitation, irradiance, and other readings. Continuing with unit-only numeric sensors would spread name-based inference through adapters, APIs, automations, and UI code. Adding one Entity type per physical quantity would instead multiply nearly identical contracts and generated facades.

**Evidence:** The Zigbee2MQTT adapter already special-cases temperature and catalogs humidity, illuminance, and battery as ambient numeric sensors. UI code branches on `hearth.temperature/v1` and otherwise renders generic numeric values from `support.state.unit`. Home Assistant addresses the same problem with sensor device classes, openHAB with quantity dimensions, Google Home with named sensor states, and Matter/SmartThings with measurement-specific clusters or capabilities.

## Proposed Solution

Add read-only `hearth.measurement/v1`, whose scalar JSON-number State is described by support containing a `measurement_kind`, a canonical UCUM `unit`, and the Entity's accepted `minimum` and `maximum`. The contract schema is authoritative for valid kind/unit pairs and kind-wide physical envelopes. Generated cross-document behavior validates each State against the registered Entity bounds.

Because `measurement_kind` defines Entity meaning while ordinary support may evolve, extend Entity-type manifests with generic `immutable_support_paths`. Generated catalog behavior compares those fields during re-registration; Core rejects a changed immutable support value before replacing the descriptor. `measurement/v1` marks `/state/measurement_kind` immutable. Unit needs no separate immutable path because the schema permits exactly one unit per kind.

Migrate existing temperature, relative humidity, illuminance, and battery-level Entities to this type. Remove `hearth.temperature/v1`; retain `hearth.numericsensor/v1` for numeric readings that intentionally carry no first-class measurement semantics, initially link quality and smart-plug electrical diagnostics. This is a direct breaking change: Hearth has no deployments, so all in-repository callers change together and development databases are recreated rather than migrated through compatibility aliases.

The v1 measurement-kind catalog is closed at any released revision. A new kind requires an explicit contract change, canonical unit, physical envelope, examples, adapter evidence, and UI review. Additive kinds may be added to v1 when they preserve every existing kind's meaning and representation; changing an existing kind, unit, envelope, or State representation requires a new Entity-type version.

## Scope and Deliverables

| Deliverable | Outcome | Effort | Owning Paths | Depends On | Acceptance |
|---|---|---:|---|---|---|
| D1 | Add generic immutable-support identity policy to manifests, generated catalog definitions, and registration reconciliation | L | `entitytypes/entitytype-manifest.schema.json`, `internal/cmd/entitytypegen/`, `internal/modules/devices/{catalog.go,registration.go,repository.go,sqlite_repository.go}`, `contracts/v1/registration-response.schema.json`, generated contract code and tests | — | A1–A3 |
| D2 | Add the authoritative measurement contract, generated SDK/catalog artifacts, and remove temperature/v1 | M | `entitytypes/measurementv1/`, `entitytypes/temperaturev1/`, `sdk/adapter/{measurementv1,temperaturev1}/`, `internal/modules/devices/zz_generated_entitytypes*` | D1 | A4–A7 |
| D3 | Migrate Zigbee2MQTT temperature, humidity, illuminance, and battery planning and decoding while retaining diagnostic numericsensors | M | `internal/adapters/zigbee2mqtt/entity_measurement.go`, `capability_catalog.go`, `planner_sensor.go`, affected tests | D2 | A8–A12 |
| D4 | Render and chart semantic measurements from descriptor support | M | `web/src/pages/DeviceFactsPage.tsx`, `web/src/pages/EntityDetailPage.tsx`, `web/src/components/entity-state-history.tsx` | D2 | A13–A14 |
| D5 | Update canonical documentation and migration guidance | S | `CONTEXT.md`, `README.md`, `docs/architecture.md`, `docs/zigbee2mqtt-capabilities.md`, affected `specs/`, real-device validation reference | D1–D4 | A15 |
| D6 | Regenerate, validate, build the web client, and review the complete breaking cutover | S | repository-wide generated outputs and tests | D1–D5 | A16 |

## Non-Goals

- No weather-station adapter, protocol selection, discovery mapping, or live-device capture.
- No pressure, wind, precipitation, irradiance, UV, or other speculative production kind in the initial catalog; add those with adapter evidence.
- No writable numeric settings or actions; `measurement/v1` has no Operations.
- No qualitative, enum, binary, event-source, composite, or multi-axis measurements.
- No uncertainty, calibration, accuracy, precision, sampling interval, or data-quality metadata.
- No locale or user preference model and no canonical-to-display unit conversion framework.
- No replacement of link quality or smart-plug electrical readings currently represented by `numericsensor/v1`.
- No compatibility shim, dual registration, alias type, persisted-data rewrite, or mixed-version support.
- No general immutable Entity metadata document; immutability applies only to manifest-declared paths within normalized support.

## Domain Language

**Measurement:** A numeric reading of a defined physical or device property. Its measurement kind identifies what the number means independently of the Entity's name and unit.

**Measurement kind:** The stable semantic category of a Measurement, such as `temperature` or `relative_humidity`. It is not an individual sensor, Device role, location, display label, or unit.

**Canonical unit:** The sole wire/storage unit assigned by the measurement contract to one measurement kind. Adapters convert native values into it; clients may format or convert it only for presentation.

Use `measurement_kind`, not `quantity`, `device_class`, `metric`, `property`, or `phenomenon`, in Hearth contracts and code.

## Types

### Generic immutable support identity

Ownership: manifest schema and generator under `entitytypes/` and `internal/cmd/entitytypegen/`; erased runtime policy in `internal/modules/devices/catalog.go`; persistence enforcement in registration reconciliation.

```diff
 {
   "manifest_version": 1,
   "type": "hearth.measurement/v1",
+  "immutable_support_paths": ["/state/measurement_kind"],
   "state_schema": "state.schema.json"
 }
```

`immutable_support_paths` is optional and defaults to an empty list. Each entry MUST be a unique canonical JSON Pointer to a required scalar leaf in the support schema. The generator rejects malformed pointers, missing paths, optional traversal, object/array targets, duplicate paths, and unsupported scalar bindings. It emits a typed comparison over normalized decoded support. Existing Entity types declare no paths and therefore permit all currently valid support changes.

Generated implementation shape:

```go
// SameSupportIdentity reports whether support fields that define an Entity's
// stable semantic identity are unchanged.
func SameSupportIdentity(previous, next Support) bool {
    return previous.State.MeasurementKind == next.State.MeasurementKind
}
```

The generated built-in catalog passes this callback into `DefineEntityType`; concrete runtime code contains no `measurement/v1` type-ID branch.

### `hearth.measurement/v1`

Ownership: handwritten authoritative inputs in `entitytypes/measurementv1/`; generated Go in that package, `sdk/adapter/measurementv1/`, and the built-in Core catalog.

State is a finite JSON number after JSON decoding. Generated Go binds it to `float64`; therefore the contract intentionally inherits binary64 behavior: integers through 2^53 are exact, distinct higher-precision decimals may normalize to the same value, overflow is rejected, and underflow may normalize to zero. Equality and range validation operate on normalized binary64 values.

```go
// Generated from the schemas; shown here as the required implementation shape.
type State float64

type StateSupport struct {
    MeasurementKind string  `json:"measurement_kind"`
    Unit            string  `json:"unit"`
    Minimum         float64 `json:"minimum"`
    Maximum         float64 `json:"maximum"`
}

type Support struct {
    State      StateSupport     `json:"state"`
    Operations OperationSupport `json:"operations"`
}

type OperationSupport struct{}
```

`state.schema.json`:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "urn:hearth:schema:entity-type:measurement:v1:state",
  "title": "Hearth measurement/v1 State",
  "description": "Numeric reading expressed in the canonical unit declared by Entity support.",
  "type": "number"
}
```

`support.schema.json` is a closed object. Its `state` object requires all four fields and selects exactly one kind branch. The initial catalog is:

| `measurement_kind` | Canonical UCUM `unit` | Kind-wide envelope | Existing source |
|---|---|---:|---|
| `temperature` | `Cel` | `-273.15..1000` | Zigbee2MQTT `temperature` in `°C` |
| `relative_humidity` | `%` | `0..100` | Zigbee2MQTT `humidity` in `%` |
| `illuminance` | `lx` | `0..1000000000` | Zigbee2MQTT `illuminance` in `lx` |
| `battery_level` | `%` | `0..100` | Zigbee2MQTT `battery` in `%` |

`Cel` is the accepted public canonical UCUM identifier for degrees Celsius; UI presentation renders it as `°C`.

Required schema shape:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["state", "operations"],
  "properties": {
    "state": {
      "type": "object",
      "additionalProperties": false,
      "required": ["measurement_kind", "unit", "minimum", "maximum"],
      "properties": {
        "measurement_kind": {
          "type": "string",
          "enum": ["temperature", "relative_humidity", "illuminance", "battery_level"]
        },
        "unit": {"type": "string", "minLength": 1, "maxLength": 32},
        "minimum": {"type": "number"},
        "maximum": {"type": "number"}
      },
      "oneOf": [
        {
          "properties": {
            "measurement_kind": {"const": "temperature"},
            "unit": {"const": "Cel"},
            "minimum": {"minimum": -273.15},
            "maximum": {"maximum": 1000}
          }
        },
        {
          "properties": {
            "measurement_kind": {"const": "relative_humidity"},
            "unit": {"const": "%"},
            "minimum": {"minimum": 0},
            "maximum": {"maximum": 100}
          }
        },
        {
          "properties": {
            "measurement_kind": {"const": "illuminance"},
            "unit": {"const": "lx"},
            "minimum": {"minimum": 0},
            "maximum": {"maximum": 1000000000}
          }
        },
        {
          "properties": {
            "measurement_kind": {"const": "battery_level"},
            "unit": {"const": "%"},
            "minimum": {"minimum": 0},
            "maximum": {"maximum": 100}
          }
        }
      ]
    },
    "operations": {
      "type": "object",
      "additionalProperties": false,
      "maxProperties": 0
    }
  }
}
```

The manifest uses existing relation-DSL operations:

```json
{
  "manifest_version": 1,
  "type": "hearth.measurement/v1",
  "immutable_support_paths": ["/state/measurement_kind"],
  "state_schema": "state.schema.json",
  "support_schema": "support.schema.json",
  "state_validation": [
    {
      "op": "gte",
      "left": {"root": "state", "path": ""},
      "right": {"root": "support", "path": "/state/minimum"}
    },
    {
      "op": "lte",
      "left": {"root": "state", "path": ""},
      "right": {"root": "support", "path": "/state/maximum"}
    }
  ],
  "support_validation": [
    {
      "op": "gte",
      "left": {"root": "support", "path": "/state/maximum"},
      "right": {"root": "support", "path": "/state/minimum"}
    }
  ],
  "operations": {},
  "examples": "examples.json"
}
```

The JSON Schema, not a second Go table, owns kind/unit/envelope coherence. The current generator may ignore `enum`, `const`, and `oneOf` when emitting Go field types, but its generated codecs embed and enforce the complete raw schema. No generator or relation-DSL extension is required.

`examples.json` MUST cover:

- one schema-valid support and valid lower, middle, and upper State for every initial kind;
- invalid State below and above support bounds;
- non-number State;
- inverted bounds in `invalid_supports` (it schema-decodes and then fails generated `support_validation`).

Schema-invalid support cannot appear in `invalid_supports`, because generator loading intentionally requires those examples to decode before behavior validation. A focused handwritten `support_schema_test.go` MUST instead prove rejection of wrong kind/unit pairs, out-of-envelope bounds, unknown kinds, missing fields, and extra properties. A focused `precision_test.go`, modeled after `entitytypes/numericsensorv1/precision_test.go`, MUST prove binary64 overflow and documented precision behavior.

### Removed `hearth.temperature/v1`

Delete its handwritten schemas/examples and generated package/facade. The new temperature State is canonical Celsius as a JSON number, not integer milli-Celsius:

```diff
- type: hearth.temperature/v1
- support: {"state":{},"operations":{}}
- state: 21500
+ type: hearth.measurement/v1
+ support: {"state":{"measurement_kind":"temperature","unit":"Cel","minimum":-273.15,"maximum":1000},"operations":{}}
+ state: 21.5
```

## Interfaces

### Adapter planning boundary

Replace temperature-specific and ambient-numeric planning with one searchable measurement constructor in `internal/adapters/zigbee2mqtt/entity_measurement.go`:

```go
// newMeasurementPlan translates one read-only numeric property into its
// canonical measurement unit and validates it through measurement/v1.
func newMeasurementPlan(
    metadata adapter.EntityMetadata,
    property string,
    support measurementv1.Support,
    gettable bool,
) (entityPlan, error)
```

The constructor:

- creates descriptors and Observations only through the generated `sdk/adapter/measurementv1` facade;
- preserves finite fractional JSON numbers under the documented binary64 contract;
- performs no clamping, truncation, or rounding;
- carries no command translator and creates no command route;
- includes the upstream property in startup `/get` only when the expose grants get access;
- reports malformed or out-of-range values as per-property decode issues without suppressing valid sibling readings.

Replace `numericSensorMapping` for the ambient table with an explicitly named mapping type:

```go
type measurementMapping struct {
    exposeName     string
    key            string
    displayName    string
    upstreamUnit   string
    measurementKind string
    canonicalUnit  string
    minimum        float64
    maximum        float64
}
```

Initial Zigbee2MQTT mappings:

```go
[]measurementMapping{
    {exposeName: "temperature", key: "temperature", displayName: "Temperature",
        upstreamUnit: "°C", measurementKind: "temperature", canonicalUnit: "Cel",
        minimum: -273.15, maximum: 1000},
    {exposeName: "humidity", key: "humidity", displayName: "Humidity",
        upstreamUnit: "%", measurementKind: "relative_humidity", canonicalUnit: "%",
        minimum: 0, maximum: 100},
    {exposeName: "illuminance", key: "illuminance", displayName: "Illuminance",
        upstreamUnit: "lx", measurementKind: "illuminance", canonicalUnit: "lx",
        minimum: 0, maximum: 1000000000},
    {exposeName: "battery", key: "battery", displayName: "Battery",
        upstreamUnit: "%", measurementKind: "battery_level", canonicalUnit: "%",
        minimum: 0, maximum: 100},
}
```

The table repeats kind/unit data so adapters can construct self-describing descriptors, but it is not authoritative: the generated facade rejects a pair not admitted by `support.schema.json`. Exact upstream-unit matching remains adapter-owned. Native-to-canonical conversion is adapter-owned; these first mappings need only `°C` → numeric `Cel` relabeling because Celsius magnitude is unchanged.

`hearth.numericsensor/v1` and `newNumericSensorPlan` remain unchanged for:

- link quality (`lqi`), including exact-integer adapter decoding;
- smart-plug frequency, power, power factor, energy, current, and voltage until a separately evidenced semantic-measurement migration is approved.

### Core registration and catalog boundary

Extend the erased catalog policy:

```go
type EntityTypeDefinition struct {
    // existing fields omitted
    sameSupportIdentity func(previous, next EntitySupport) (bool, error)
}

func (catalog *TypeCatalog) SameSupportIdentity(
    typeID EntityTypeID,
    previous EntitySupport,
    next EntitySupport,
) (bool, error)
```

Both descriptors are decoded, normalized, and behavior-validated by the type's support codec before generated typed immutable-field comparison. For a type with no `immutable_support_paths`, valid previous and next support compare as the same identity. Decode or validation failure is an internal catalog/persisted-descriptor error, never silently treated as an identity change.

Registration reconciliation already loads `support_json` through `GetEntityMapping`. After confirming the type ID is unchanged, it calls `SameSupportIdentity` with persisted and proposed normalized support. A false result rejects the entire registration atomically:

```diff
 type RegistrationRejectionCode string
 const (
     RegistrationImmutableTypeChange RegistrationRejectionCode = "immutable_type_change"
+    RegistrationImmutableSupportChange RegistrationRejectionCode = "immutable_support_change"
 )
```

The NATS registration response schema adds `immutable_support_change`; generated wire bindings and adapter SDK decoding regenerate. The safe operator message is `"an existing entity cannot change immutable support"`. Tests prove names, external IDs, ranges, and Operations may still reconcile when valid, while a manifest-declared identity field may not.

Existing Observation, persistence, and HTTP State interfaces remain unchanged: they already carry opaque Entity type IDs, support JSON, and State JSON. Regeneration adds `EntityTypeMeasurementV1` to the built-in catalog and removes `EntityTypeTemperatureV1`.

Existing development databases containing migrated keys will first reject re-registration with `immutable_type_change`, because their persisted Entity type is `temperature/v1` or `numericsensor/v1`; operators MUST recreate those databases. No data migration or fallback is provided.

### UI boundary

The web client recognizes `hearth.measurement/v1`, reads `measurement_kind`, `unit`, `minimum`, and `maximum` from support, and accepts only finite numeric State for summaries and charts. It MUST:

- show a human label for known UCUM units (`Cel` displays as `°C`; `%` and `lx` display unchanged);
- include units in current-State, Device Fact, history-point, axis, and tooltip rendering;
- use registered support bounds where charts need a domain;
- never infer measurement meaning from Entity name or unit;
- fall back to structured raw rendering when support is malformed or a kind is unknown.

Preferred-unit conversion, such as Celsius to Fahrenheit, is deferred. Until that feature exists the UI displays canonical values; its `Cel` → `°C` label is presentation only and does not own conversion or kind/unit validity.

## Project Layout

```text
entitytypes/
├── entitytype-manifest.schema.json        # modify — immutable_support_paths contract
├── measurementv1/                         # new — authoritative semantic measurement contract
│   ├── entitytype.json                    # new — read-only behavior and range relations
│   ├── state.schema.json                  # new — scalar JSON-number State
│   ├── support.schema.json                # new — closed kind/unit/envelope catalog
│   ├── examples.json                      # new — contract and invalid-support examples
│   ├── precision_test.go                  # new — binary64 behavior contract
│   └── zz_generated_*.go                  # new/generated — bindings, codecs, behavior, conformance
└── temperaturev1/                         # delete — superseded dedicated type

sdk/adapter/
├── measurementv1/zz_generated_facade*.go  # new/generated — typed adapter construction
└── temperaturev1/                         # delete — removed facade

internal/adapters/zigbee2mqtt/
├── entity_measurement.go                  # new — one semantic measurement constructor
├── entity_temperature.go                  # delete — milli-Celsius specialization removed
├── entity_numericsensor.go                # modify — retain diagnostic/electrical use only
├── capability_catalog.go                  # modify — semantic measurement mapping catalog
├── planner_sensor.go                      # modify — plan unified measurement records
└── *_test.go                              # modify — literal type/support/State contracts

internal/cmd/entitytypegen/
├── main.go and behavior.go                 # modify — parse/validate immutable support paths
├── render_behavior.go                     # modify — emit typed identity comparison
├── render_catalog.go                      # modify — wire comparison into catalog definitions
└── *_test.go                              # modify — generator rejection and execution coverage

internal/modules/devices/
├── catalog.go                              # modify — erased support-identity policy
├── registration.go                        # modify — public rejection code/message
├── repository.go                          # modify — internal immutable-support sentinel
├── sqlite_repository.go                   # modify — enforce policy during reconciliation
├── dbqueries/registration.sql             # unchanged — already selects support_json
├── zz_generated_entitytypes.go            # modify/generated — measurement catalog registration
├── zz_generated_entitytypes_test.go       # modify/generated — catalog conformance
├── catalog_test.go                        # modify — measurement and identity assertions
└── command_test.go                        # modify — read-only operation rejection

contracts/v1/
├── registration-response.schema.json      # modify — immutable_support_change wire code
└── zz_generated_*.go                      # modify/generated — registration response binding

web/src/
├── pages/DeviceFactsPage.tsx              # modify — semantic measurement rendering
├── pages/EntityDetailPage.tsx             # modify — measurement State summaries
└── components/entity-state-history.tsx    # modify — measurement history charts and units

docs/ and specs/                           # modify — canonical type and adapter descriptions
CONTEXT.md                                  # modify — Measurement vocabulary
README.md                                   # modify — API examples and development DB reset
```

## Acceptance Criteria

- [ ] **A1:** Generator validation accepts unique immutable pointers to required scalar support leaves and rejects malformed, duplicate, missing, optional, object, and array targets.
- [ ] **A2:** Catalog comparison is generated without literal measurement type branching; changing `/state/measurement_kind` returns false while changing valid bounds returns true.
- [ ] **A3:** Re-registration atomically rejects an immutable support change as `immutable_support_change` across service, repository, NATS schema, and generated wire bindings; types without declared paths retain current reconciliation behavior.
- [ ] **A4:** `hearth.measurement/v1` accepts exactly the initial four measurement kinds, each only with its canonical unit and bounds inside its kind-wide envelope.
- [ ] **A5:** Support rejects unknown kinds, wrong units, inverted bounds, out-of-envelope bounds, missing fields, and additional properties through the appropriate schema or generated behavior boundary.
- [ ] **A6:** State accepts finite JSON numbers within Entity support bounds and rejects non-numbers and out-of-range values after binary64 normalization.
- [ ] **A7:** Generated SDK and Core catalog conformance passes; `hearth.temperature/v1` and its generated facade/catalog constant no longer exist.
- [ ] **A8:** Zigbee2MQTT temperature registers as `measurement/v1` with kind `temperature`, unit `Cel`, and Celsius State (for example upstream `21.5` produces `21.5`, not `21500`).
- [ ] **A9:** Humidity, illuminance, and battery register as `measurement/v1` with literal expected kinds, canonical units, bounds, and unchanged numeric magnitudes.
- [ ] **A10:** Temperature, humidity, illuminance, and battery remain read-only; get access alone controls startup refresh and no Operation reaches adapter dispatch.
- [ ] **A11:** Link quality and all existing smart-plug electrical readings remain `numericsensor/v1` with unchanged support, decoding, identity, and ordering.
- [ ] **A12:** Existing Entity keys, external IDs, Device grouping, planner ordering, availability, and sibling-isolation behavior remain unchanged across the type cutover.
- [ ] **A13:** Current State and Device Fact UI views render all four measurement kinds with their canonical values and human-readable unit labels.
- [ ] **A14:** History accepts and charts finite measurement States, includes units, uses support bounds where applicable, and safely falls back for malformed descriptors.
- [ ] **A15:** Canonical docs describe `measurement/v1`, immutable measurement kind, retained numericsensor scope, direct database recreation, and the process for adding a kind without retaining stale temperature/milli-Celsius claims.
- [ ] **A16:** `mise run validate` and `mise run web-build` pass; explicit manual UI review covers all four kinds plus unknown-kind and malformed-support fallbacks; the reviewed diff contains only intended source, generated, formatted, and module-metadata changes.

## Test Strategy

| Layer | What | How |
|---|---|---|
| Manifest/generator | Immutable-pointer validation and typed identity comparison | Generator unit, regression, and isolated fixture-execution tests |
| Contract | Kind/unit pairing, envelopes, Entity bounds, closed shapes | Declarative examples for schema-valid behavior; handwritten schema-rejection tests for schema-invalid support |
| Precision | Binary64 normalization and equality | Focused `measurementv1/precision_test.go` |
| Registration | Immutable semantic identity across support updates | Repository/service/NATS contract tests for changed kind versus changed bounds |
| Adapter unit | Eligibility, descriptor support, State decoding, refresh, no commands | Rework temperature/percentage/capability catalog tests with literal expectations |
| Fixture contract | Captured temperature/humidity/battery and illuminance devices | Rebaseline existing sanitized inventory and State fixtures without deriving expectations from production tables |
| Core integration | Registration, Observation projection, command rejection | Catalog/command tests and Zigbee2MQTT proof-flow integration test |
| UI | Summary, fact, chart, tooltip, malformed fallback | Extract pure formatting/parsing helpers where needed and add focused frontend tests if the existing toolchain supports them; otherwise cover through build plus explicit manual review |
| Repository | Generated outputs, format, lint, vet, race tests, module tidiness | `mise run validate` |
| Web | TypeScript/build correctness and visible formatting/fallbacks | `mise run web-build` plus recorded manual cases |

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---:|---:|---|
| Binary64 weakens temperature's exact milli-Celsius contract | Certain | Medium | Make the break explicit, retain no scaling, add precision tests, and reject overflow/non-number input through codecs |
| Existing databases reject immutable type changes | Certain in development DBs | Medium | Require database recreation; add no compatibility path because there are no deployments |
| Mutable support could reinterpret retained State/history | Medium without enforcement | High | Generate immutable support identity comparison and reject changed measurement kind during registration |
| The kind catalog becomes a speculative ontology | Medium | High | Ship only four evidenced kinds; require evidence and review for each additive kind |
| Kind/unit knowledge drifts into adapters or UI | Medium | High | Keep authoritative pairing in `support.schema.json`; adapters submit descriptors, generated facade validates them, UI reads descriptors and owns presentation only |
| Battery classification is disputed as diagnostic | Medium | Low | Define `battery_level` as semantic device-property measurement while retaining link quality as generic diagnostic numeric data |
| Weather needs temporal semantics not represented here | High | Medium | Use distinct future kinds such as rate versus accumulation; specify them only with the weather integration |
| `oneOf`/`const` are enforced by codecs but not reflected as Go enums | Medium | Low | Document string bindings; rely on embedded schema validation; revisit generated enum bindings only after repeated need |

## Trade-offs Made

| Chose | Over | Because |
|---|---|---|
| One semantic measurement type | One Entity type per physical quantity | Shared read-only behavior remains uniform while meaning is explicit |
| `measurement_kind` | `quantity`, `device_class`, `property`, `metric` | It is precise, searchable, and does not import another platform's vocabulary |
| Explicit validated unit | Unit derived only by consumers | Descriptors remain self-contained without splitting semantic ownership |
| UCUM unit identifiers | Display strings or adapter-native units | Stable language-neutral identifiers support future conversion and validation |
| JSON number / binary64 SDK | Per-kind scaled integers | One State shape works for heterogeneous measurements and current generator behavior |
| Closed additive v1 catalog | Arbitrary namespaced kinds | Core can reject semantic/unit contradictions while still growing through reviewed additions |
| Direct breaking migration | Compatibility alias or dual registration | Hearth has no deployments and Entity type is immutable |
| Keep numericsensor/v1 | Replace every numeric reading | Generic diagnostics and electrical values are outside this evidenced semantic migration |

## Adding a Measurement Kind

A future change to the v1 catalog is complete only when it:

1. names one stable `measurement_kind` and distinguishes ambiguous temporal meanings such as rate versus accumulation;
2. selects one canonical UCUM unit;
3. defines a defensible kind-wide envelope;
4. adds a `support.schema.json` branch and positive/negative examples;
5. adds an adapter mapping backed by captured upstream expose and State evidence;
6. adds literal adapter and integration expectations;
7. verifies UI rendering and decides whether preferred-unit conversion is needed;
8. updates canonical docs and runs `mise run validate`.

Changing the representation of an existing kind is not additive and requires `hearth.measurement/v2`.

## Open Questions

None.

## Success Metrics

- Every migrated Entity can be classified from `type + support` without reading its name, external ID, Adapter, or upstream payload.
- No accepted descriptor can pair a known measurement kind with the wrong canonical unit.
- Adding the first weather-station capability requires an additive kind/mapping change rather than another generic numeric contract or dedicated Entity type.
- All existing migrated sensor fixtures preserve Device identity, Entity keys, numeric meaning, and read-only behavior under the new type.
