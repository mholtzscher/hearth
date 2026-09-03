# Zigbee2MQTT Entity planning refactor

**Status:** Ready for task breakdown
**Approved by:** User
**Type:** Refactoring with proof features
**Effort:** XL, 5 to 9 focused days at 60% confidence
**Date:** 2026-09-03
**Baseline:** `f1e628a`

## Problem

The Zigbee2MQTT Adapter supports power, brightness, and color temperature. Each Entity kind adds another branch to discovery, descriptor construction, State decoding, Observation construction, Command translation, and outcome matching. Kind-specific data also accumulates in `discoveredEntity`, `decodedEntityState`, and `desiredState`, where most fields are invalid for most values.

Color temperature required coordinated production changes in six adapter files. Planned plugs, switches, bulb variants, and sensors would make those files and switches grow together. Missing one branch would compile in several cases and fail only at runtime.

The runtime coordinator has different change pressure. Its MQTT generation fencing, per-IEEE FIFO queues, deadlines, PUBACK handling, active refresh, and command-evidence disposition apply to every Entity kind. This refactor must keep those rules in one generic coordinator.

## Decision

Refactor the Adapter into two private in-process modules:

1. An expose index normalizes Zigbee2MQTT expose structure, endpoint resolution, property claims, access flags, units, and raw scalar metadata.
2. An Entity planning module runs explicit light, relay, and sensor planners. They produce immutable Entity plans that contain descriptor construction results and the functions needed to decode State and plan Commands.

Discovery merges planner results for one normalized IEEE address into one registration. Light and relay are mutually exclusive primary families. A valid light result wins over relay to preserve current behavior for Devices that expose both shapes. Sensor Entities are supplemental and join either primary family. A Device with no light or relay result becomes `sensor` when the sensor planner contributes at least one Entity.

A relay with a temperature measurement is therefore one `relay` Device with power and temperature Entities. Each planner removes same-family key ambiguity before merge. Complete-plan validation rejects any remaining duplicate Entity key rather than relying on Core descriptor validation.

The refactor includes two proof features:

- a relay-only Zigbee2MQTT plug that registers one `hearth.power/v1` Entity;
- a read-only Zigbee2MQTT temperature sensor that registers one new `hearth.temperature/v1` Entity.

`hearth.temperature/v1` uses integer milli-Celsius State. An upstream value of `21.375` becomes `21375`. The type has no Operations.

One normalized IEEE address remains one Hearth Device. Endpoint identity remains part of Entity keys and external IDs. Child Devices are deferred.

## Goals

- Adding a scalar Entity kind changes one Entity implementation or description, one explicit planner assembly point, and focused tests.
- Adding an Entity kind does not change generic State publication, Command coordination, or evidence disposition.
- Existing light Binding keys, Entity keys, external IDs, support, State, and Command behavior remain unchanged.
- Publish, get, and set access distinctions survive discovery and control startup refresh and Command routing.
- One Entity plan may claim several State properties and write several Command properties.
- Multi-property State uses same-message evidence only. The Adapter does not assemble Entity State across MQTT messages.
- One Device registration contains all non-conflicting plans for one IEEE address.

## Non-goals

- RGB, XY, HS, effects, transitions, or atomic color Commands.
- Electrical power, current, voltage, or cumulative energy Entities on the plug.
- Button remotes, action events, or a Hearth event Entity type.
- Child Devices for multi-gang switches or power strips.
- Cross-message State assembly or a State cache in the Adapter.
- Runtime planner loading, plugins, reflection, or `init` registration.
- A Zigbee2MQTT translation schema or generator.
- MQTT, NATS, HTTP, persistence, or SDK Session contract changes.
- Fixing the existing sequential sibling-publication behavior in `publishDeviceState`. That concurrency concern needs a separate change with its own tests.

## Current constraints

- `internal/modules/devices/registration.go` accepts only Device kind `light`.
- The Entity-type generator rejects JSON Schema `number` bindings because `float64` would lose decimal meaning.
- An operation-free Entity manifest loads, but `render_sdk.go` emits unused command-only imports and an empty `Handlers` type.
- Registration accepts 1 through 64 Entities and treats Entity type as immutable.
- Binding keys and Entity keys preserve canonical identity across restart and friendly-name changes.
- Startup currently sends `/get` for every discovered Entity. Read-only publish-only sensors require separate observable and gettable metadata.
- Commands on one IEEE address share a FIFO queue across Entity kinds.
- A command outcome requires fresh, non-retained, post-dispatch State from the same Entity, MQTT generation, and route revision.

## Architecture

```text
bridge/devices JSON
    -> tolerant Zigbee2MQTT wire decoding
    -> expose index
    -> light, relay, and sensor planners
    -> merged Device plan
    -> one Registration per IEEE
    -> bound runtime Entities
       -> generic State decoding and Observation publication
       -> generic Command planning and coordinator execution
```

The expose index and planners are in-process dependencies. Tests use their real implementations. MQTT remains behind the existing private `mqttConnection` seam.

## Types

### Canonical Device kinds

Owner: `internal/modules/devices/model.go`.

```diff
diff --git a/internal/modules/devices/model.go b/internal/modules/devices/model.go
@@
 type DeviceKind string
 
-const DeviceKindLight DeviceKind = "light"
+const (
+	DeviceKindLight  DeviceKind = "light"
+	DeviceKindRelay  DeviceKind = "relay"
+	DeviceKindSensor DeviceKind = "sensor"
+)
```

Owner: `internal/modules/devices/registration.go`.

```diff
diff --git a/internal/modules/devices/registration.go b/internal/modules/devices/registration.go
@@
-	if normalized.Device.Kind != DeviceKindLight {
-		return Registration{}, errors.New("device kind must be light")
+	switch normalized.Device.Kind {
+	case DeviceKindLight, DeviceKindRelay, DeviceKindSensor:
+	default:
+		return Registration{}, errors.New("device kind must be light, relay, or sensor")
 	}
```

The wire schema and SQLite schema already store Device kind as a bounded string. No migration is required.

### Expose index

Owner: `internal/adapters/zigbee2mqtt/expose_index.go`.

```go
type exposeIndex struct {
	roots          []indexedExpose
	propertyCounts map[string]int
}

type indexedExpose struct {
	expose   upstreamExpose
	endpoint int
	scoped   bool
	resolved bool
	order    int
}

type featureQuery struct {
	Type string
	Name string
}

func newExposeIndex(device upstreamDevice) exposeIndex
func (index exposeIndex) Roots(exposeType string) []indexedExpose
func (index exposeIndex) UniqueFeature(parent indexedExpose, query featureQuery) (upstreamExpose, bool)
func (index exposeIndex) PropertyUnique(property string) bool
```

`newExposeIndex` traverses every root and nested feature once. `propertyCounts` includes supported and unsupported exposes because one MQTT object property cannot safely identify two planned Entities. Root order remains inventory order.

`indexedExpose` stores the effective numeric endpoint for its root. An unresolved scoped endpoint remains in the index with `resolved == false`; planners omit it without affecting unrelated roots.

Add unit decoding to the private wire DTO:

```diff
diff --git a/internal/adapters/zigbee2mqtt/discovery_wire.go b/internal/adapters/zigbee2mqtt/discovery_wire.go
@@
 type upstreamExpose struct {
 	Type        string           `json:"type"`
 	Name        string           `json:"name"`
 	Property    string           `json:"property"`
+	Unit        string           `json:"unit"`
```

The tolerant custom unmarshaler must copy `unit` independently so malformed optional metadata still cannot discard sibling exposes.

### Entity plans

Owner: `internal/adapters/zigbee2mqtt/entity_plan.go`.

```go
type entityPlan struct {
	Descriptor       adapter.EntityDescriptor
	StateProperties  []string
	GetProperties    []string
	DecodeState      stateDecoder
	TranslateCommand commandTranslator
}

type runtimeEntity struct {
	plan     entityPlan
	entityID string
}

type stateDecoder func(
	entityID string,
	properties map[string]json.RawMessage,
	receivedAt time.Time,
) (stateReport, bool, error)

type stateReport struct {
	Observation adapter.Observation
	semantic    any
}

type stateDecodeIssue struct {
	Properties []string
	Err        error
}

type commandTranslator func(
	context.Context,
	string,
	adapter.Command,
	adapter.Responder,
) (plannedCommand, error)

type plannedCommand struct {
	SetValues     map[string]json.RawMessage
	GetProperties []string
	Deadline      time.Time
	Matches       func(stateReport) bool
}

func validateEntityPlans([]entityPlan) error
func validatePlannedCommand(plannedCommand) error
```

`stateDecoder` returns `present == false` when the MQTT message does not contain every State property required by that Entity. It does not save partial values for a later message. If every required property is present but invalid, it returns an error that becomes one `stateDecodeIssue` naming all claimed properties. Valid sibling Entities still publish.

`TranslateCommand == nil` marks a read-only Entity. Reconciliation includes it in registration, availability, and State routing but does not add it to the coordinator command route map.

`GetProperties` is an ordered subset of `StateProperties`. A publish-only sensor has no get properties. A controllable Entity must have a non-empty get list because accepted Commands require active refresh.

`validateEntityPlans` runs before registration. It checks descriptor completeness, non-empty and unique State properties, ordered get subsets, decoder presence, duplicate Entity keys, and the controllable refresh invariant. Command-specific output does not exist at discovery time. `validatePlannedCommand` therefore runs after typed Command translation and before MQTT publication; it rejects empty set values, duplicate or empty refresh properties, a zero deadline, or a nil matcher.

`stateReport.semantic` is private type erasure. Each Entity implementation creates it and the matching closure consumes it with a checked type assertion. A mismatch returns false and never panics. A generic private helper may implement exact matching for comparable typed State:

```go
func exactMatcher[T comparable](target T) func(stateReport) bool
```

The Observation has already passed through the generated typed SDK facade before it enters the runtime coordinator.

### Device planners

Owner: `internal/adapters/zigbee2mqtt/device_planner.go`.

```go
type devicePlanningInput struct {
	Device upstreamDevice
	IEEE   string
	Exposes exposeIndex
}

type plannerContribution struct {
	Kind     string
	Entities []entityPlan
}

type devicePlanner interface {
	Plan(devicePlanningInput) (plannerContribution, error)
}

func defaultDevicePlanners() []devicePlanner
func planDevice(devicePlanningInput, []devicePlanner) (devicePlan, error)

type devicePlan struct {
	Kind     string
	Entities []entityPlan
}
```

`defaultDevicePlanners` returns this fixed order:

```go
[]devicePlanner{
	lightPlanner{},
	relayPlanner{},
	sensorPlanner{},
}
```

All planners may inspect the same index. `planDevice` chooses one primary family. A non-empty light result wins; otherwise a non-empty relay result wins. The sensor result is supplemental to either family and is the primary result only when neither actuator family contributes. This preserves current light behavior when a Device also has a switch root. It also lets relay and light Devices gain read-only measurements without another Device registration.

Each planner removes every same-family Entity key that occurs more than once. `planDevice` preserves planner and expose order, then validates the complete result before registration.

Planner implementations are immutable and concurrency-safe. They perform no I/O and retain no Device state.

### Discovered and runtime Entities

Owner: `internal/adapters/zigbee2mqtt/discovery.go`.

```diff
diff --git a/internal/adapters/zigbee2mqtt/discovery.go b/internal/adapters/zigbee2mqtt/discovery.go
@@
 type discoveredDevice struct {
 	IEEEAddress  string
 	FriendlyName string
 	Model        string
 	Registration adapter.Registration
-	Entities     []discoveredEntity
+	Entities     []entityPlan
 }
-
-type discoveredEntity struct {
-	Descriptor        adapter.EntityDescriptor
-	Property          string
-	Kind              entityKind
-	Endpoint          int
-	Scoped            bool
-	PowerOn           scalarValue
-	PowerOff          scalarValue
-	BrightnessMaximum float64
-	ColorTempMinimum  int64
-	ColorTempMaximum  int64
-}
```

Owner: `internal/adapters/zigbee2mqtt/reconcile.go`.

```diff
diff --git a/internal/adapters/zigbee2mqtt/reconcile.go b/internal/adapters/zigbee2mqtt/reconcile.go
@@
 type runtimeEntity struct {
-	discovered discoveredEntity
-	entityID   string
+	plan     entityPlan
+	entityID string
 }
```

`runtimeDeviceFromBinding` pairs each immutable plan with the canonical Entity ID returned by registration. Decoder and Command functions receive that ID when invoked, so binding cannot introduce a new failure after Core commits registration.

### Command route and attempt

Owner: `internal/adapters/zigbee2mqtt/command.go` and `runtime_commands.go`.

```diff
diff --git a/internal/adapters/zigbee2mqtt/command.go b/internal/adapters/zigbee2mqtt/command.go
@@
 type commandRoute struct {
 	entityID             string
 	ieeeAddress          string
 	friendlyName         string
-	entity               discoveredEntity
+	entity               runtimeEntity
 	connectionGeneration uint64
 	routeGeneration      uint64
 }
-
-type desiredState struct {
-	power      bool
-	brightness int64
-	colorTemp  int64
-}
```

```diff
diff --git a/internal/adapters/zigbee2mqtt/runtime.go b/internal/adapters/zigbee2mqtt/runtime_commands.go
@@
 type commandAttempt struct {
@@
-	desired      desiredState
+	matches      func(stateReport) bool
```

`matcherMatches` is removed. Runtime matching invokes the attempt's closure only after all existing Entity, generation, revision, retained, dispatch-time, and deadline checks pass.

## Planner behavior

### Light planner

Owner: `internal/adapters/zigbee2mqtt/planner_light.go`.

The light planner moves current light discovery without changing its contract:

- consider `light` root exposes;
- require one unambiguous binary `state` feature with publish, set, and get access;
- require unique non-empty property and distinct valid `value_on` and `value_off` scalars;
- require a resolvable endpoint for scoped roots;
- make valid power the light eligibility gate;
- add brightness and color temperature only when each optional feature is valid;
- isolate malformed optional features;
- preserve current root and endpoint keys, names, external IDs, support, and ordering.

The planner uses Entity constructors from `entity_power.go`, `entity_brightness.go`, and `entity_colortemp.go`. Those files own all generated SDK facade use and Zigbee2MQTT value conversion for their Entity type.

### Relay planner

Owner: `internal/adapters/zigbee2mqtt/planner_relay.go`.

The relay planner considers `switch` root exposes. Each eligible root requires exactly one binary `state` feature with the same rules as light power:

- `access & 7 == 7`;
- one unique non-empty property;
- valid and unequal `value_on` and `value_off` JSON scalars;
- a resolvable endpoint when scoped.

Each accepted root produces a `hearth.power/v1` Entity through the same constructor used by lights. This keeps State and Command behavior identical.

For a normalized IEEE `0x00124b0024abcdef`, the root identity is:

```text
Binding key:       z2m-00124b0024abcdef
Device kind:       relay
Device external:   0x00124b0024abcdef
Entity key:        power
Entity external:   0x00124b0024abcdef/root/power
Entity type:       hearth.power/v1
```

Endpoint-scoped relays use `power-epN` and `epN/power`. One IEEE still produces one registration.

The Adapter calls the Device kind `relay`, not `plug` or `switch`, because the Zigbee2MQTT expose proves a controllable relay but does not reliably distinguish a plug from an in-wall switch.

### Sensor planner

Owner: `internal/adapters/zigbee2mqtt/planner_sensor.go`.

This slice supports only numeric ambient temperature. An eligible expose has:

- `type == "numeric"`;
- `name == "temperature"`;
- `unit == "°C"`;
- a non-empty Device-unique property;
- publish access present with `access & 1 == 1`;
- set access absent with `access & 2 == 0`;
- a resolvable endpoint when scoped.

Get access is optional. `access & 4 == 4` adds the property to `GetProperties`; otherwise startup sends no `/get`.

Root identity is:

```text
Entity key:        temperature
Entity external:   <ieee>/root/temperature
Entity name:       Temperature
Entity type:       hearth.temperature/v1
```

Endpoint identity uses `temperature-epN`, `<ieee>/epN/temperature`, and `<endpoint label> Temperature` under the existing endpoint naming rules.

A Device with only temperature Entities has kind `sensor`. A light or relay that also exposes temperature keeps the higher-priority Device kind and receives the non-conflicting temperature Entity.

## Temperature Entity type

Owner: `entitytypes/temperaturev1` and generated `sdk/adapter/temperaturev1`.

### Manifest

```json
{
  "manifest_version": 1,
  "type": "hearth.temperature/v1",
  "state_schema": "state.schema.json",
  "support_schema": "support.schema.json",
  "operations": {},
  "examples": "examples.json"
}
```

### State schema

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "urn:hearth:schema:entity-type:temperature:v1:state",
  "title": "Hearth temperature/v1 State",
  "description": "Temperature in milli-Celsius; one degree Celsius equals 1000 milli-Celsius.",
  "type": "integer",
  "minimum": -273150,
  "maximum": 1000000
}
```

The inclusive range is -273.15 through 1000 degrees Celsius. The type identifier fixes the unit, so State support does not repeat it.

### Support schema

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "urn:hearth:schema:entity-type:temperature:v1:support",
  "title": "Hearth temperature/v1 Entity support",
  "type": "object",
  "additionalProperties": false,
  "required": ["state", "operations"],
  "properties": {
    "state": {
      "type": "object",
      "additionalProperties": false,
      "maxProperties": 0
    },
    "operations": {
      "type": "object",
      "additionalProperties": false,
      "maxProperties": 0
    }
  }
}
```

Normalized support is:

```json
{"state":{},"operations":{}}
```

### Examples

```json
{
  "cases": [
    {
      "name": "fixed-range-milli-celsius",
      "support": {"state": {}, "operations": {}},
      "states": [
        {"value": 21500, "valid": true},
        {"value": -273150, "valid": true},
        {"value": 1000000, "valid": true},
        {"value": -273151, "valid": false},
        {"value": 1000001, "valid": false},
        {"value": 21.5, "valid": false}
      ],
      "operations": {}
    }
  ]
}
```

### Operation-free generation

Owner: `internal/cmd/entitytypegen/render_sdk.go` and `render_catalog_conformance.go`.

When an Entity type has no Operations, generation must:

- omit `encoding/json` and `sdk/adapter/typed` imports from its SDK facade;
- omit the empty `Handlers` type;
- omit `NewCommandHandler`;
- retain descriptor and Observation constructors;
- omit `time` from aggregate generated conformance tests when the entire catalog has no Operations.

No manifest schema change is required. Empty manifest, support, and example operation maps already validate.

## Temperature normalization

Owner: `internal/adapters/zigbee2mqtt/entity_temperature.go`.

```go
const (
	temperatureMinimumMilliCelsius int64 = -273_150
	temperatureMaximumMilliCelsius int64 = 1_000_000
	milliCelsiusPerCelsius              = 1_000
)

func normalizeTemperature(payload json.RawMessage) (int64, error)
```

Normalization must:

1. decode a JSON number with `UseNumber`;
2. parse it exactly with `big.Rat`;
3. multiply Celsius by 1000;
4. require an integral `int64` result;
5. enforce the Entity-type range;
6. reject rather than clamp, truncate, or round.

Required examples:

| Upstream JSON | Hearth State |
|---|---:|
| `21.5` | `21500` |
| `21.5000` | `21500` |
| `2.15e1` | `21500` |
| `-0.125` | `-125` |
| `-273.15` | `-273150` |
| `0.0001` | rejected |
| `"21.5"` | rejected |
| `1e10000` | rejected |

The generated temperature SDK facade constructs the descriptor and typed Observation. No adapter code builds raw Hearth Observation JSON.

## State processing

Owner: `internal/adapters/zigbee2mqtt/state.go` and `observation.go`.

`decodeDeviceState` decodes the MQTT payload into one `map[string]json.RawMessage`. It walks bound Entities in registration order.

For each Entity:

1. If none of its State properties are present, skip it.
2. If only a subset is present, skip it without caching.
3. If every required property is present, call `DecodeState`.
4. On failure, append one issue naming the claimed properties.
5. On success, submit the typed Observation and private semantic value to the coordinator.

The coordinator publishes `stateReport.Observation` directly. Remove `newObservation` and its Entity-kind switch.

A future multi-property Entity therefore requires one MQTT report containing every property needed for a complete Hearth State. This is an explicit limitation of the stateless Adapter.

## Command processing

Owner: `internal/adapters/zigbee2mqtt/command.go` and split runtime files.

`translateCommand` delegates to `runtimeEntity.plan.TranslateCommand` with the canonical Entity ID, marshals `plannedCommand.SetValues` as one JSON object, validates the result, and returns the matcher and absolute deadline.

A plan may write several properties. It may also request several properties after acceptance. `publishGet` changes to accept an ordered property list and publishes one object:

```go
func publishGet(
	ctx context.Context,
	connection mqttConnection,
	base string,
	friendly string,
	properties []string,
) error
```

For `[]string{"x", "y"}`, the payload is logically:

```json
{"x":"","y":""}
```

Production code must build the payload deterministically for tests and diagnostics. Duplicate or empty properties fail Entity-plan validation before route activation.

Only Entities with `TranslateCommand != nil` enter `routeSnapshot.routes`. Startup refresh includes only non-empty `GetProperties`. Every accepted Command retains the existing mandatory refresh rule.

The following coordinator rules remain unchanged:

- FIFO queue per IEEE address;
- queue time consumes the absolute deadline;
- different IEEE Devices may progress concurrently;
- `/set` runs outside the event loop;
- acceptance follows QoS 1 PUBACK;
- handler return does not wait for refresh or evidence publication;
- retained, pre-dispatch, stale-generation, stale-revision, wrong-Entity, and late reports cannot satisfy a Command;
- one matched report has one linked or ordinary disposition;
- route invalidation and MQTT failure preserve current fallback behavior.

## Discovery and merge rules

Device-level eligibility checks remain before planning:

- coordinator and group exclusion;
- supported and enabled Device;
- successful interview;
- non-nil definition;
- normalized IEEE address;
- route-safe friendly name;
- valid Device descriptor name.

After planners run:

- a non-empty light result is the primary family and relay results are ignored, preserving current light behavior;
- relay is primary only when light contributes nothing;
- sensor results supplement the primary family or form a sensor-only Device;
- no Entity plans from a Device with a light root uses `no_eligible_light`;
- no Entity plans from a Device with a switch root uses `no_eligible_relay`;
- no Entity plans from other roots uses `no_eligible_entity`;
- each planner omits all same-family plans whose Entity key is duplicated;
- any duplicate key left after primary and supplemental merge rejects the Device as `ambiguous_entity_plan`;
- more than 64 merged plans uses `too_many_entities`;
- invalid optional plans do not suppress independent valid plans;
- every descriptor is complete before registration.

The merge issues exactly one registration for the IEEE address.

## Identity and reconciliation

Existing identity is immutable under this refactor:

- Binding key remains `z2m-<normalized IEEE without 0x>`.
- Device external ID remains normalized IEEE.
- Existing light keys remain `power`, `brightness`, `colortemp`, and their `-epN` forms.
- Existing external IDs remain `<ieee>/root/<kind>` and `<ieee>/epN/<kind>`.
- Friendly name and endpoint labels remain mutable routing and display metadata.
- Property names never enter Binding or Entity keys.

New relay power deliberately uses the same power key scheme. A physical Device changing between a light and relay expose retains its power Entity identity if IEEE and numeric endpoint stay the same. Core may update the Device kind because kind is mutable registration metadata.

Owned mappings absent from the merged current plan retain canonical identity and receive the existing `capability_missing` availability reason. Missing and disabled Device handling remains unchanged.

Read-only temperature Entities receive the same explicit per-Device availability evidence as controllable Entities.

## Interfaces unchanged outside the package

The following interfaces do not change:

```go
func New(session Session, config Config, logger *slog.Logger) (*Adapter, error)
func (z2m *Adapter) Run(context.Context) error
func (z2m *Adapter) HandleCommand(context.Context, adapter.Command, adapter.Responder) error
```

The SDK `Session`, `Responder`, `CommandEvidence`, MQTT connection, NATS subjects, wire envelopes, HTTP routes, and persistence stores are unchanged.

## Project layout

```text
entitytypes/
└── temperaturev1/
    ├── entitytype.json                     # new, operation-free Entity manifest
    ├── examples.json                       # new, State conformance cases
    ├── state.schema.json                   # new, milli-Celsius integer State
    ├── support.schema.json                 # new, empty State and Operation support
    └── zz_generated_*.go                   # generated, bindings, codecs, behavior, tests

sdk/adapter/
└── temperaturev1/
    └── zz_generated_*.go                   # generated, descriptor and Observation facade

internal/cmd/entitytypegen/
├── render_sdk.go                           # modify, omit command-only output for zero Operations
├── render_catalog_conformance.go           # modify, conditional time import
└── main_test.go                            # modify, operation-free generation tests

internal/modules/devices/
├── model.go                                # modify, relay and sensor Device kinds
├── registration.go                         # modify, validate three canonical Device kinds
├── registration_test.go                    # modify, kind acceptance and rejection
├── catalog_test.go                         # modify, operation-free command rejection
├── command_test.go                         # modify, reject temperature Operations before dispatch
├── zz_generated_entitytypes.go             # generated, temperature catalog entry
└── zz_generated_entitytypes_test.go        # generated, temperature conformance

internal/adapters/zigbee2mqtt/
├── discovery_wire.go                       # modify, retain expose unit
├── expose_index.go                         # new, normalized expose and property index
├── device_planner.go                       # new, planner interface, merge, validation
├── planner_light.go                        # new, current light family rules
├── planner_relay.go                        # new, relay family rules
├── planner_sensor.go                       # new, temperature sensor rules
├── entity_plan.go                          # new, plans, bound behavior, match helpers
├── entity_power.go                         # new, complete power translation
├── entity_brightness.go                    # new, complete brightness translation
├── entity_colortemp.go                     # new, complete color-temperature translation
├── entity_temperature.go                   # new, complete temperature translation
├── discovery_exposes.go                    # remove after behavior moves to index and planners
├── discovery.go                            # modify, build one merged Device plan
├── state.go                                # modify, generic plan execution and common JSON helpers
├── observation.go                          # modify, submit prebuilt typed Observations
├── command.go                              # modify, generic command planning
├── reconcile.go                            # modify, bind plans and separate State, get, command routes
├── runtime.go                              # modify, event types and coordinator loop only
├── runtime_routes.go                       # new, route activation and invalidation
├── runtime_commands.go                     # new, queues, dispatch, deadlines, acceptance
├── runtime_observations.go                 # new, matching and Observation disposition
├── testdata/
│   ├── bridge-devices-relay-plug.json      # new, sanitized real inventory capture
│   ├── state-relay-plug.json               # new, sanitized real State capture
│   ├── bridge-devices-temperature.json     # new, sanitized real inventory capture
│   └── state-temperature.json              # new, sanitized real State capture
└── *_test.go                               # modify or add focused planner, Entity, and runtime tests

internal/app/zigbee2mqtt/
└── run_integration_test.go                 # modify, relay and temperature SDK/Core flow

docs/architecture.md                       # modify, accepted kinds and planning rules
README.md                                   # modify, plug and temperature behavior
specs/zigbee2mqtt-adapter.md                # modify, replace light-only and deferred-temperature claims
```

All Zigbee2MQTT implementation files remain in one package. A subpackage would force private wire and runtime types into a wider interface without a second external caller.

## Deliverables

| Deliverable | Effort | Depends on |
|---|---:|---|
| D1. Sanitized plug and temperature captures plus characterization tests | M | - |
| D2. Operation-free temperature Entity type and relay/sensor Core kinds | L | D1 |
| D3. Expose index, Entity plans, current light migration, and generic execution | XL | D1 |
| D4. Relay and sensor planners with plug and temperature proof flows | L | D2, D3 |
| D5. Runtime file split, documentation, mutation checks, and full validation | L | D4 |

D1 is a contract input, not a late acceptance artifact. Use Zigbee2MQTT 2.13.0 and the household plug and temperature sensor intended for the first rollout. If several qualify, choose the simplest Device matching this slice. The checked-in inventory capture retains vendor and model so later work can reproduce the evidence source. Preserve expose nesting, type, name, property, endpoint, access, unit, scalar values, and numeric bounds. Sanitization changes IEEE address, friendly name, description, and other household identifiers only. If a capture contradicts the relay or Celsius assumptions above, implementation stops and returns this spec for review.

## Test strategy

Each meaningful test must state the protected behavior and plausible defect it detects.

| Layer | Behavior | Failure it must detect | Oracle |
|---|---|---|---|
| Characterization | Existing light identity, support, normalization, and payloads survive the refactor | Planner migration changes canonical IDs or behavior | Existing accepted architecture and captured bulb fixtures |
| Generator | Zero-Operation manifests emit compiling contracts, SDK facades, and catalog entries | Unused command imports or accidental command interface | Manifest and generated SDK contract in this spec |
| Entity type | Milli-Celsius range and empty Operations validate | Fractional canonical State or out-of-range values enter Core | Temperature schema and unit definition |
| Expose index | Endpoint and property ambiguity remain Device-wide | Two plans claim one MQTT property or endpoint labels change identity | Zigbee2MQTT expose contract and existing adapter rules |
| Planner | One IEEE produces one registration with deterministic kind and Entity order | Separate planners overwrite or duplicate the Device | Hearth Binding invariant and this spec |
| Temperature | Exact decimal conversion accepts millidegrees and rejects finer precision | Float rounding, truncation, overflow, or numeric strings | Independent integer quotient and remainder calculation |
| Read-only access | Publish-only temperature creates no `/get` or command route | Generic startup treats every Entity as gettable or controllable | Zigbee2MQTT access bits and empty Entity Operations |
| Relay | Plug power uses discovered scalars and current command lifecycle | Hard-coded ON/OFF or a relay-specific coordinator branch | Sanitized plug capture and power/v1 contract |
| Multi-property seam | Complete same-message properties produce one State; split messages produce none | Accidental cross-message caching or partial synthetic State | Explicit stateless rule in this spec |
| Runtime | Existing FIFO, freshness, revision, deadline, and disposition rules remain | Refactor accepts stale evidence or duplicates Observations | Existing coordinator contract and deterministic tests |
| Process integration | Core accepts relay and sensor registration and projects temperature | Core kind validation, generated catalog, or SDK routing mismatch | SDK/Core contract |
| Fuzz | Inventory and State parsing do not panic or accept invalid milli-Celsius | Malformed JSON, huge exponents, and partial exposes escape isolation | Parser invariants in this spec |

Required focused tests include:

- Existing 3RCB01057Z and multi-endpoint light descriptors compare byte-for-byte before and after the refactor.
- Friendly-name, endpoint-label, and MQTT-property changes do not change canonical keys.
- The captured plug registers Device kind `relay` and one `hearth.power/v1` Entity.
- Plug State uses captured `value_on` and `value_off` scalars.
- Plug Commands preserve `/set`, PUBACK, acceptance, `/get`, and linked evidence behavior.
- The captured temperature sensor registers Device kind `sensor` and one `hearth.temperature/v1` Entity.
- Temperature `21.5` publishes State `21500` with support `{"state":{},"operations":{}}`.
- Publish-only temperature produces no startup `/get`.
- A gettable temperature expose produces startup `/get` but no command route.
- Core rejects every temperature Operation before adapter dispatch.
- A mixed relay and temperature fixture produces one Device and one registration with both Entities.
- A mixed light and temperature fixture remains Device kind `light` and preserves all light identities.
- A test-only two-property plan emits only when both properties occur in one message.
- A test-only two-property Command publishes both set values and refreshes all declared properties.
- A matcher semantic-type mismatch returns false without panic.
- Commands remain FIFO across all controllable Entities on one IEEE and concurrent across IEEE addresses.

Use exact decimal test values, exponent notation, range neighbors, strings, null, malformed JSON, and a huge exponent. A property test should generate integer milli-Celsius values, format exact Celsius JSON with integer arithmetic, and require conversion back to the source integer. Do not calculate the expected value with the production converter.

After focused tests pass, run Gremlins against `./internal/adapters/zigbee2mqtt` and `./internal/cmd/entitytypegen`. Investigate behavioral survivors rather than adding syntax-only assertions.

Finish with `mise run validate` as required by the repository workflow.

## Acceptance criteria

### Architecture

- [ ] Production Zigbee2MQTT code contains no `entityKind`, `desiredState`, kind-valued State union, or Entity-kind switch.
- [ ] Light, relay, and sensor planners are explicit private implementations assembled in one deterministic order.
- [ ] One primary planner result plus supplemental sensor plans merge before one registration.
- [ ] Generic State and Command runtime files import no generated Entity-type facade packages.
- [ ] Per-kind generated facade imports live only in `entity_*.go` files.
- [ ] Multi-property plans use same-message evidence and retain no cross-message State cache.

### Compatibility

- [ ] Existing light Binding keys, Entity keys, external IDs, names, support, State normalization, MQTT payloads, and command evidence behavior remain unchanged.
- [ ] Existing owned mappings omitted by a current plan become unavailable with `capability_missing` and are not deleted.
- [ ] MQTT, NATS, SDK Session, HTTP, and persistence interfaces do not change.

### New proof behavior

- [ ] Core accepts canonical Device kinds `light`, `relay`, and `sensor` and rejects unknown kinds.
- [ ] `hearth.temperature/v1` is generated from schemas and a manifest with integer milli-Celsius State and no Operations.
- [ ] The operation-free SDK facade has descriptor and Observation constructors and no command handler.
- [ ] The captured plug registers one relay Device and one power Entity.
- [ ] The captured temperature sensor registers one sensor Device and one temperature Entity.
- [ ] Temperature conversion is exact to one milli-Celsius and rejects values that require rounding.
- [ ] Read-only temperature State publishes normally and never creates a command route.
- [ ] Get access alone controls startup refresh.
- [ ] Mixed planner contributions still produce one Device per IEEE.

### Verification

- [ ] Focused planner, Entity, State, Command, reconciliation, generator, catalog, and process tests pass.
- [ ] Native fuzz tests preserve parser and milli-Celsius invariants.
- [ ] Existing deterministic concurrency tests retain their assertions and pass with the race detector.
- [ ] Gremlins survivors in changed packages are reviewed and material gaps are fixed.
- [ ] `mise run validate` passes with generated and formatted changes committed.
- [ ] README, architecture, and the base Zigbee2MQTT spec no longer claim light-only or defer implemented temperature support.

## Risks and mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Entity-plan migration changes existing canonical identity | Medium | High | Byte-for-byte characterization tests before structural changes |
| Generic matching weakens typed outcome semantics | Medium | High | Per-kind typed matcher closures and mismatch tests; keep runtime freshness gates unchanged |
| Read-only sensors receive unsupported `/get` or Commands | Medium | High | Separate State, get, and command metadata; process integration test |
| Planner execution produces two registrations for one IEEE | Medium | High | One primary-family merge function and mixed-capability registration test |
| Multi-property plans imply a cache that violates statelessness | Medium | Medium | Same-message-only contract and split-message negative test |
| Temperature unit or precision is misread | Medium | High | Require captured `°C`, parse with `big.Rat`, and reject sub-milli values |
| Operation-free generation breaks SDK or catalog output | High | Medium | End-to-end zero-Operation generator fixture and generated conformance |
| Runtime file movement obscures coordinator regressions | Medium | High | Move after behavior passes; retain deterministic concurrency and race tests |
| Captured devices differ from assumed expose shapes | Medium | Medium | Make sanitized captures D1 and stop for spec review on contradiction |

## Trade-offs

| Chose | Over | Reason |
|---|---|---|
| Private compiled Entity plans | Repeated central switches | Kind behavior changes in one file and the coordinator remains generic |
| Explicit planner list | Reflection or runtime registration | Ordering and supported families remain visible and validated at startup |
| Three planner implementations now | Waiting for the second Device family | Plug and sensor proofs make all three implementations real in this slice |
| Milli-Celsius integers | `float64` or a new decimal binding | Exact language-neutral State without expanding generator number semantics |
| Same-message multi-property State | Cross-message assembly | Preserves the stateless Adapter and avoids freshness ambiguity |
| One IEEE Device | Endpoint child Devices | Preserves current identity and keeps topology policy out of this refactor |
| Relay Device kind | Plug and wall-switch model tables | Zigbee2MQTT exposes prove relay behavior, not installation form |
| Handwritten translators | A translation generator | Four current kinds do not justify another schema and generator |

## Delivery order

1. Capture and sanitize real plug and temperature inventory and State payloads.
2. Add characterization tests for every existing light identity and behavior that the refactor can disturb.
3. Add `relay` and `sensor` Device kinds to Core.
4. Add zero-Operation generator support and `hearth.temperature/v1`; regenerate all checked-in code.
5. Add the expose index and Entity-plan validation.
6. Move power, brightness, and color-temperature behavior into per-kind files.
7. Make State decoding and Observation creation generic.
8. Make Command translation, refresh, and matching generic without changing coordinator lifecycle rules.
9. Add light, relay, and sensor planners and merge one primary family plus supplemental sensor plans per IEEE.
10. Add captured plug and temperature proof tests plus mixed-planner and multi-property seam tests.
11. Split `runtime.go` by responsibility without changing the coordinator type or event model.
12. Update README, architecture, and the base Zigbee2MQTT spec.
13. Run focused tests, mutation checks, and `mise run validate`; review the final diff.

## Success measure

After this slice, adding a scalar read-only sensor requires one Entity implementation or description, one planner assembly entry, captured fixtures, and focused tests. It does not require changes to generic State publication, Observation disposition, Command coordination, or route lifecycle code.

## Open questions

None. D1 uses Zigbee2MQTT 2.13.0 and selects the simplest qualifying household devices intended for rollout. Their vendor and model remain in the sanitized inventory fixtures. A payload that contradicts this spec returns the document to review rather than creating an implementation exception.

## Confirmed decisions

- Scope includes the refactor, one relay-only plug, and one read-only temperature sensor.
- Entity plans support multi-property State and Commands now.
- Light, relay, and sensor planner implementations ship in this slice.
- One IEEE address remains one Hearth Device.
- Temperature State is integer milli-Celsius.
- Plug and temperature contracts use sanitized real Zigbee2MQTT captures.
