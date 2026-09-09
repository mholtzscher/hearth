# Zigbee2MQTT bulb color

**Status:** Draft for human review; not authorization to implement.
**Effort:** XL, about several days for generated contracts, caller updates, tests, and hardware validation.
**Outcome:** End-to-end native XY and hue/saturation capabilities with observable active mode.

## 1. Decisions and scope

The user selected:

- Support XY-only, HS-only, and dual-representation bulbs.
- Separate native XY and HS Entities, plus a read-only color-mode Entity.
- Native coordinate commands. No RGB or hex input, conversion, or picker.
- Scaled integer values with bounded numeric matching tolerance.
- Reported values plus an `active` flag for XY, HS, and color temperature.
- Mode-aware color-temperature confirmation, including the breaking scalar State change with direct updates to all repository callers.
- Color Commands kept separate from power and brightness.
- Automated coverage for every representation combination, plus at least one real-bulb color and temperature mode-switch path. Physical HS verification is optional, but the report must record the gap when skipped.

Product decisions are settled. Remaining approval covers this implementation contract, especially the satisfaction-list DSL, strict missing-mode behavior, temperature-only fallback, and canonical hue range.

### Problem

Hearth exposes power, brightness, and color temperature for eligible Zigbee2MQTT lights, but ignores color composites and `color_mode`. Users cannot command colored light through Hearth. A bulb can report temperature and several color representations at once while only one is active, so confirming against an inactive value alone can falsely report success for a mode-changing Command.

### Non-goals

RGB, hex, and value input, color-space conversion, perceptual matching, gamut tables and clipping, effects, transitions, move and step commands, startup settings, combined power, brightness, and color Commands, new HTTP routes, cross-message State assembly, provenance stronger than Zigbee2MQTT provides, and compatibility shims or persisted-data migration.

## 2. Architecture and seam

`entitytypes/` owns declarative schemas and generated validation, bindings, and satisfaction predicates. `sdk/adapter/` exposes generated typed facades. The devices module owns catalog registration, persistence, HTTP, and Command outcomes. Zigbee2MQTT per-kind `entity_*.go` modules own discovery inputs, decoding, translation, and adapter matching. Its runtime owns per-device FIFO, freshness gates, publication, and mandatory refresh. This feature adds color plans and generated contracts at those seams, with no color switches in the core or transport.

No SQL schema, HTTP envelope, NATS envelope, service assembly, or deployment change is required. JSON State content changes for temperature. Operators with local databases holding the old contract discard them explicitly. Automatic deletion and migration are out of scope.

## 3. Entity contracts

Every object below is closed (`additionalProperties: false`) and every shown field is required. Numeric fields use bounded JSON integers and generated Go `int64`. New types follow existing generated package conventions. Schemas and manifests are authoritative. Generated Go is never hand-edited.

### `hearth.colorxy/v1` in `entitytypes/colorxyv1/`

```go
// State reports XY chromaticity in ten-thousandths and whether XY mode is active.
type State struct {
    Active bool  `json:"active"`
    X      int64 `json:"x"` // 0..10000; 3125 means 0.3125
    Y      int64 `json:"y"` // 0..10000
}

type SetParameters struct {
    X int64 `json:"x"` // 0..10000
    Y int64 `json:"y"` // 0..10000
}
```

Support is `{"state":{},"operations":{"set":{}}}`. The `set` deadline is 10000 ms.

Satisfaction needs `state.active`, `abs(parameters.x-state.x) <= 1`, and the same Y condition.

Per-axis ranges are protocol bounds, not a promise that every pair is physically realizable. The adapter enforces no gamut or chromaticity-triangle restriction. Unachievable requests can time out.

### `hearth.colorhs/v1` in `entitytypes/colorhsv1/`

```go
// State reports hue in whole degrees and saturation in whole percentage points.
type State struct {
    Active     bool  `json:"active"`
    Hue        int64 `json:"hue"`        // canonical 0..359
    Saturation int64 `json:"saturation"` // 0..100
}

type SetParameters struct {
    Hue        int64 `json:"hue"`        // 0..359; use 0 rather than 360
    Saturation int64 `json:"saturation"` // 0..100
}
```

Support is `{"state":{},"operations":{"set":{}}}`. The `set` deadline is 10000 ms. Both coordinates are mandatory. Partial HS Commands do not exist.

With `d = abs(parameters.hue-state.hue)`, satisfaction needs `state.active`, `min(d, 360-d) <= 2`, and `abs(parameters.saturation-state.saturation) <= 1`.

The Command schema rejects hue 360 instead of rewriting persisted Parameters. Decoding canonicalizes observed 360 to 0, so State keeps one canonical form without parameter normalization.

### `hearth.colormode/v1` in `entitytypes/colormodev1/`

```go
// State identifies the reported color-control mode, not whether power is on.
type State string // enum: "xy", "hs", "color_temp"
```

Support is `{"state":{},"operations":{}}`. The type has no operations and no command route. Hearth publishes reported known enum values even when the matching representation is not an advertised capability. Unknown values are invalid observations, not a new State and not an inferred mode.

### Change `hearth.colortemp/v1`

Regenerate `entitytypes/colortempv1/zz_generated_types.go` from its changed State schema:

```diff
-type State int64
+type State struct {
+    Active bool  `json:"active"`
+    Value  int64 `json:"value"`
+}
```

`value` stays integer mireds, schema range 100..1000, constrained by the discovered minimum and maximum. Support and `set {"value":370}` Parameters are unchanged. State-validation references move from the State root to `/value`. Satisfaction is `state.active AND parameters.value == state.value`. Temperature tolerance stays zero.

The type ID stays the same for this unreleased breaking change. Every manifest, example, SDK user, simulator scenario, fixture, UI consumer, and test moves to the object form together. There is no v2 beside v1 and no compatibility path for the old scalar.

### Activity and observation semantics

`active` reports the mode selected by the reported color-control mode. It says nothing about power, freshness, or physical accuracy.

For color-capable light roots, XY is active exactly when the same-message mode is `xy`, HS exactly when it is `hs`, and temperature exactly when it is `color_temp`. Each coordinate Entity needs its full value and the mode in one MQTT message.

A message missing the mode or the top-level value skips that Entity observation. Hearth manufactures no `active:false` and carries no mode forward from an earlier message. A present but malformed mode or value yields a per-Entity decode issue with valid siblings intact, and complete but inactive values publish with `active:false`.

A mode-only message updates only the mode Entity. Coordinate Entities keep their prior complete State, flags included, until a new complete observation arrives. Readers must not treat separately updated Entities as one atomic snapshot. The mode Entity is the mode signal.

Roots with temperature but no advertised color composite stay active when mode is absent, because temperature is the only advertised color-control mode. When mode is present, validation runs and activity derives normally. An advertised but unplannable color composite keeps the root mode-sensitive, with no silent always-active fallback.

Hearth discovers a mode Entity for every eligible root with a usable color or temperature capability. A temperature-only device that never reports mode may leave that Entity stateless. Temperature still works through the fallback above. No mode observation is ever synthesized.

## 4. Generated behavior extension

The current `satisfied_when` slot holds one scalar comparison. It becomes a nonempty array of conjunctive rules, matching the existing validation arrays. Every in-repository manifest and generator fixture moves to the array form. The old object shape does not remain.

Example XY manifest fragment:

```json
{
  "satisfied_when": [
    {"op":"is_true","left":{"root":"state","path":"/active"}},
    {"op":"near","left":{"root":"parameters","path":"/x"},"right":{"root":"state","path":"/x"},"tolerance":1},
    {"op":"near","left":{"root":"parameters","path":"/y"},"right":{"root":"state","path":"/y"},"tolerance":1}
  ]
}
```

HS uses `circular_near` with `tolerance:2, modulus:360` for hue, plus a saturation `near` and an `is_true`. Temperature uses `is_true` plus `eq`. Power and brightness wrap their existing rule in a one-element array.

New rule contracts:

| Operator | Operands | Constants | Meaning |
|---|---|---|---|
| `is_true` | required Boolean `left` only | none | left is true |
| `near` | required integer `left`, `right` | integer tolerance >= 0 | absolute difference <= tolerance |
| `circular_near` | required integer `left`, `right` | positive integer modulus; 0 <= tolerance < modulus/2 | shortest circular distance <= tolerance |

Generation rejects unknown operators, per-operator unsupported fields, missing operands or constants, fractional constants, optional or non-scalar references, invalid types, empty lists, and roots other than parameters and state. Satisfaction still references Parameters and State only, never mutable Support. Distance operators read only bounded nonnegative integer schemas, and circular operands stay below the modulus. The compiler carries these bounds into rule compilation and rejects schemas that cannot establish them. Generated code subtracts in an order that keeps operands nonnegative, so supported extremes cannot overflow.

Manifest constants use optional pointers during parsing so absent and zero stay distinct. Operation models store `[]ruleModel` for satisfaction and render logical conjunction. Generated public signatures stay unchanged:

```go
func SetSatisfied(parameters SetParameters, state State) bool
```

Structural State equality remains exact. A within-tolerance coordinate change is an applied State update, not `unchanged`. Activity changes are meaningful State changes too.

## 5. Discovery and identity

Power remains the light-family eligibility gate, with existing optional-feature isolation.

A color candidate is a direct light feature of type `composite` named `color_xy` or `color_hs`, with a nonempty property and state, set, and get access (7). It carries exactly one numeric child per required coordinate, `x` and `y` or `hue` and `saturation`, each with a matching child property and the same access. Bounds are optional, since standard upstream children omit them, but explicit bounds must match the upstream domain: XY 0..1, hue 0..360, saturation 0..100. Anything else omits that candidate. Extra unrelated children are harmless; duplicate required children disqualify.

Root endpoints resolve through the existing expose index. Discovery honors discovered color and temperature property names instead of assuming unsuffixed names. Mode is the Zigbee2MQTT companion property `color_mode` at root, or `color_mode_<endpoint-label>` for a scoped root, using the exact upstream endpoint label rather than the numeric Hearth identity. When companion-property ownership is ambiguous across roots or collides with another exposed property of a different meaning, discovery omits the affected color and mode capabilities instead of guessing, and mode-sensitive temperature never silently falls back to always-active behavior.

### Shared-property exception

Existing `PropertyUnique` counts would reject dual color composites because both claim `color`. One XY composite and one HS composite may each claim a property, sharing it only within the same resolved light root. Duplicate same-representation claims, claims in other roots, and unrelated claims disqualify the affected candidates, and uniqueness is otherwise unchanged. Each color candidate validates independently after ownership checks, so an invalid HS child never suppresses valid XY support.

Dual bulbs plan both native Entities. Profile evaluation never keeps the first expose and discards the other. There is no representation conversion and no hidden HS fallback on XY-only bulbs.

Identities follow existing conventions:

| Type | Base key / external-ID suffix | Display name |
|---|---|---|
| XY | `colorxy` | Color XY |
| HS | `colorhs` | Color Hue/Saturation |
| mode | `colormode` | Color Mode |

Root external ID is `<ieee>/root/<suffix>`, scoped form `<ieee>/ep<N>/<suffix>`, with keys `<suffix>-ep<N>`. Construction uses `scopedIdentity` and existing name validation. Color-temperature identity is unchanged.

## 6. Adapter interfaces and data flow

New facades stay confined to per-kind modules. Proposed private constructors:

```go
// entity_colorxy.go
func newColorXYPlan(metadata adapter.EntityMetadata, colorProperty, modeProperty string) (entityPlan, error)

// entity_colorhs.go
func newColorHSPlan(metadata adapter.EntityMetadata, colorProperty, modeProperty string) (entityPlan, error)

// entity_colormode.go
func newColorModePlan(metadata adapter.EntityMetadata, modeProperty string) (entityPlan, error)
```

The temperature constructor changes explicitly:

```diff
 func newColorTempPlan(
     metadata adapter.EntityMetadata,
     property string,
+    modeProperty string,
+    requireMode bool,
     minimum, maximum int64,
 ) (entityPlan, error)
```

Color plans claim `StateProperties: [colorProperty, modeProperty]` and `GetProperties: [colorProperty]`. The mode plan claims `[modeProperty]` and has no translator and no independent get properties, because sibling color and temperature refreshes already request mode upstream. Temperature claims `[property, modeProperty]` when mode is required, otherwise `[property]`, and its decoder optionally inspects mode. Temperature gets `[property]` in either case. These claims conform to the current ordered-get-subset invariant.

### Decoding

Parsing uses exact JSON numbers (`json.Number` and `big.Rat`, as existing number decoders do). Strings, null, Booleans, nonfinite values, and raw out-of-range values fail. Nonnegative values round half-up:

- XY accepts raw 0..1 per axis and stores `round(raw * 10000)` in 0..10000.
- Hue accepts raw 0..360, stores `round(raw)`, and folds 360 to 0.
- Saturation accepts raw 0..100 and stores `round(raw)`.
- Temperature keeps strict integral mired decoding with discovered-range checks.

Pairs validate as complete before producing observations. A partial pair inside a present `color` object is invalid for that representation, while another representation in the same object may still decode. Extra upstream fields are harmless. Missing top-level required properties skip without an issue, matching current `decodeDeviceState` behavior.

Shared XY/HS numeric normalization lives in `entity_color.go` when genuinely reused; mode decoding lives in `entity_colormode.go` and serves every mode-sensitive plan.

### Commands and refresh

HTTP remains `POST /v1/entities/{id}/commands`:

```json
{"operation":"set","parameters":{"x":3125,"y":3291}}
```

publishes:

```json
{"color":{"x":0.3125,"y":0.3291}}
```

HS `{"operation":"set","parameters":{"hue":120,"saturation":80}}` publishes `{"color":{"hue":120,"saturation":80}}`. XY encodes exact base-10 decimal JSON from scaled integers, with no float arithmetic in translation. Endpoint payload keys use discovered properties.

The temperature request shape is unchanged and publishes the existing mired scalar upstream. Set payloads never include power, brightness, transition, or unrelated properties. Bulb or converter side effects surface as observations through existing capabilities; nothing here promises their absence.

After set PUBACK or accept, the existing mandatory get refresh runs: `{"color":""}` for color and `{"color_temp":""}` for temperature, substituting discovered properties. Upstream standard converters read mode under these attributes.

Each matcher type-asserts the report semantic value to the corresponding generated State and calls the generated `SetSatisfied` captured with typed Parameters. A type mismatch returns false. The 10-second deadline, per-IEEE FIFO, one-claim handling, route and generation fences, and retained and pre-dispatch exclusions all remain. Missing mode cannot satisfy a color or mode-sensitive temperature Command. Devices that never produce enough complete reports after refresh time out under existing semantics. A native HS request answered in XY mode is not HS success.

### Error behavior

Malformed Parameters fail existing catalog and typed validation before MQTT publication. Invalid discovery omits the affected optional capabilities, and invalid observations use existing per-Entity decode issues without suppressing valid siblings, prefixed `color XY value`, `color HS value`, or `color mode value`. Transport errors, rejection, and timeouts follow existing adapter behavior with no new public error taxonomy.

## 7. Evidence limits

The real-device environment keeps `optimistic:false`, which suppresses optimistic command echoes but not Zigbee2MQTT caching or `color_sync` conversions. A fresh non-retained publication is not a per-field measurement receipt: mode and coordinates may carry upstream cached information, and Hearth receives no per-field provenance. Satisfaction means `active:true` plus a within-tolerance value under the existing freshness gates, an operational contract rather than a promise of exact physical color or newly measured attributes. Post-set gets add evidence but cannot tie one read receipt to one field through this MQTT contract.

## 8. Existing consumer updates

- Debug Entity detail gains capability-specific complete XY and HS example Parameters plus coordinate presets, with units and `active` beside values, keyed off type IDs and Support rather than model names. No picker and no implied calibrated swatch.
- Mode shows enum text and discrete history, with no command controls.
- Temperature reads `state.value` for existing bounds, presets, and history, and displays activity.
- XY and HS history use timestamped structured-value rows with coordinates, units, and activity, rather than a single-number chart. Existing scalar histories are unchanged.
- Simulator, app integration tests, SDK examples, and all temperature observations move to `{value,active}`. Simulated ordinary temperature state is active unless a scenario explicitly tests inactive behavior.
- README and architecture pick up the three new types, mode semantics, the temperature shape break, and freshness limits. Historical specs stay historical, with supersession noted where current-support statements would otherwise conflict.

## 9. Project layout and deliverables

```text
entitytypes/
├── entitytype-manifest.schema.json       # modify: satisfaction array and new operators
├── colorxyv1/                            # new: schemas, manifest, examples, generated bindings
├── colorhsv1/                            # new: schemas, manifest, examples, generated bindings
├── colormodev1/                          # new: enum State, empty operations, generated bindings
├── colortempv1/                          # modify: object State and active satisfaction
├── powerv1/entitytype.json               # modify: wrap satisfaction rule in array
└── brightnessv1/entitytype.json          # modify: wrap satisfaction rule in array
internal/
├── cmd/entitytypegen/
│   ├── main.go                          # modify: parsed/compiled satisfaction list and constants
│   ├── behavior.go                      # modify: operator checks and operand bounds
│   ├── render_behavior.go               # modify: conjunction and safe distance predicates
│   └── *_test.go, testdata/              # modify: schema failures, generated behavior fixtures
├── adapters/zigbee2mqtt/
│   ├── profiles/light.profile.json      # color/mode mappings and sibling dependencies
│   ├── profile_plan.go                  # evaluate light candidate groups and dependencies
│   ├── profile_strategy.go              # bind color strategies to typed Entity behavior
│   ├── expose_index.go                  # narrowly scoped color-property ownership check
│   ├── entity_color.go                  # new: shared exact numeric decode, if reused
│   ├── entity_colorxy.go                # new: XY plan and translation
│   ├── entity_colorhs.go                # new: HS plan and translation
│   ├── entity_colormode.go              # new: mode decode and read-only plan
│   ├── entity_colortemp.go              # modify: object State, mode policy, generated matcher
│   ├── *_test.go                        # new/modify , focused plan, decode, command, runtime tests
│   └── testdata/                        # new/modify , XY-only, HS-only, dual, endpoint fixtures
├── modules/devices/
│   ├── zz_generated_entitytypes.go      # regenerate: catalog registrations
│   └── *_test.go                        # modify: temperature State and outcome assertions
└── app/zigbee2mqtt/*_test.go              # modify: end-to-end contract coverage
sdk/adapter/
├── colorxyv1/                            # generate: typed facade
├── colorhsv1/                            # generate: typed facade
├── colormodev1/                          # generate: read-only facade
└── colortempv1/                          # regenerate/update callers , changed State
cmd/hearth-simulator/                     # modify callers as needed , new temperature State
web/src/
├── pages/EntityDetailPage.tsx            # modify: units, activity, coordinate examples/presets
└── components/entity-state-history.tsx   # modify: structured/discrete history and temperature extraction
specs/
├── z2m-bulb-color.md                     # new: this implementation contract
├── generated-entity-type-behavior.md     # modify during implementation: new DSL contract
└── zigbee2mqtt-adapter.md                # modify during implementation: current color scope
README.md                                # modify during implementation: usage and types
docs/architecture.md                     # modify during implementation: mode-aware State semantics
```

Each new entity bundle contains `entitytype.json`, `state.schema.json`, `support.schema.json`, `examples.json`, generated outputs, and, for controllable types only, `set-parameters.schema.json`. Generator-driven changes to other checked-in outputs and conformance fixtures are intended. Search all `colortempv1.State`, `hearth.colortemp/v1`, and `satisfied_when` callers before concluding the breaking updates are complete.

| Order | Deliverable | Effort | Depends on |
|---|---|---|---|
| D1 | Generator conjunction, Boolean activity, bounded integer distance operators; update existing manifests and prove unchanged scalar behavior | L | none |
| D2 | New entity bundles and temperature State change, generated catalog/facades, all existing temperature callers updated | L | D1 |
| D3 | Color discovery, decoding, translation, mode-aware temperature, realistic fixtures and adapter/runtime integration | L | D2 |
| D4 | Debug UI and docs, structured history and request examples | M | D2, D3 |
| D5 | Full validation and real-bulb acceptance evidence | M, hardware-dependent | D3, D4 |

D2 and its callers stay buildable together. No intermediate scalar and object split ships.

## 10. Test and acceptance contract

Tests demonstrate fault detection, not just successful serialization.

### Generator and contract tests

- Power and brightness behavior is unchanged after the move to satisfaction arrays.
- Empty lists and each invalid operator, operand, and constant combination fail generation with contextual errors.
- `is_true` cannot read a non-Boolean field. Distance operators cannot read optional, negative-range, unbounded, or incompatible fields.
- Distance comparisons are inclusive and overflow-safe at supported integer extremes. Circular bound validation rejects invalid domains.
- XY target `(3125,3291)`: active readings within ±1 per axis succeed, ±2 in either axis fails, and an inactive exact match fails.
- HS hue 359 against 1 succeeds and against 2 fails. Saturation ±1 succeeds and ±2 fails. Changing either required coordinate independently can fail the match. Inactive always fails.
- Command hue 360 is invalid. Observed hue 360 and 359.6 decode to canonical zero. Fractional hue is accepted and rounded.
- Temperature succeeds only on an exact active match. Inactive exact matches and active off-by-one readings fail.
- Exact State equality distinguishes activity flips and within-tolerance coordinate changes.
- Core outcome tests exercise generated satisfaction, not only adapter matcher closures.

### Discovery and decode tests

- XY-only, HS-only, dual, temperature-only, and color-without-temperature devices discover exactly the intended optional Entities beside power and brightness.
- Dual composites sharing one property survive. Duplicate same-mode, cross-root, and unrelated claims fail narrowly.
- Required coordinate children and access validate. Absent standard child bounds are allowed, and invalid optional HS never suppresses valid XY, power, or temperature.
- Endpoint properties, mode suffixes, and stable numeric identities verify across different endpoint labels.
- Raw out-of-range values fail before rounding. Strings, null, and partial pairs fail locally. Extra upstream fields are harmless.
- Missing mode or value skips without cached assembly. Mode-only reports update only mode. Invalid mode never becomes false activity.
- Inactive complete pairs publish inactive State. Malformed XY never blocks valid HS, temperature, or power.
- The temperature-only missing-mode fallback works. Advertised-but-unplannable color blocks the fallback.

### Runtime and API integration

- Exact MQTT payload assertions prove no power, brightness, transition, or temperature keys leak into color sets, and no XY float-artifact digits appear.
- Mandatory gets follow set acceptance and carry discovered properties.
- Wrong-mode exact coordinates never link or satisfy. A later complete active within-tolerance observation does.
- Retained, pre-dispatch, stale-generation, wrong-route, and after-deadline matches stay ineligible.
- Per-IEEE FIFO serializes XY, HS, temperature, power, and brightness. Unrelated devices stay independent.
- API discovery, command submission, State and history reads, and outcome audit work through unchanged envelopes.
- No mode command is registered. Unsupported representation requests gain no fabricated route.
- Missing or incompatible reports time out. Success never follows from publication acceptance alone.

### Real-device acceptance

Implementation-time validation follows `.agents/skills/real-device-validation/SKILL.md`. Discovery runs read-only first and records exposed modes, endpoints, model, firmware when available, and the Zigbee2MQTT version. The existing Third Reality fixture is minimal with an empty color child list and is not authoritative evidence of installed capabilities. Installed converters and firmware may differ from current upstream source, and observed fixture facts stay as observed.

Minimal realistic fixtures are hand-modeled from observed shapes without secrets, labeled synthetic where synthetic, with source, version, and firmware gaps on record.

Actuation names the physical bulb and the exact planned command sequence, gains explicit user confirmation, and agrees on restoration first. At least one supported native color path must pass: baseline read, approved color request, active mode plus bounded value confirmation, temperature request with active temperature confirmation, and return to color including a repeat or no-op request that still demands fresh evidence. Power and brightness side effects go on record rather than assumed away, and restoration is an authorized command rather than implicit cleanup.

A bulb without temperature uses a color-plus-temperature target to meet the mode-switch gate. Native HS runs too when available and approved, but missing physical HS evidence does not block when the report says so and automated HS coverage passes. Representative achievable coordinates are the success gate, not corner sweeps.

Implementation closes with `mise run validate`, a diff review of intended generated, formatting, and module changes, and repository mise tasks for UI checks. Blocked hardware gates and failing validation go on record as unresolved.

## 11. Trade-offs and risks

| Decision / risk | Consequence / mitigation |
|---|---|
| Native Entities rather than one tagged color Entity | Explicit native commands with no conversion layer; UI labels carry the distinction. |
| Fixed units and tolerances rather than mutable Support tolerances | Deterministic tests and stable Command meaning; unusual device deviations time out. |
| Strict activity confirmation | Incomplete mode reporting can time out; mandatory get plus live acceptance exposes it. |
| Upstream caching, color_sync, and separately projected mode | Section 7 defines the operational contract; freshness gates and the separate mode display carry it. |
| Breaking temperature State and manifest shape | Broad caller impact in one buildable slice; no compatibility burden in this undeployed project. |
| Converter and model version drift | Installed inventory and version are captured before hardware tests. |
| Gamut limits and device quirks | Numerically valid requests can stay unsatisfied; no perceptual promise. |

## 12. Sources and rationale

Official references (current upstream source is mutable; record the installed version during acceptance):

- [Expose format, access, dual color composites](https://www.zigbee2mqtt.io/guide/usage/exposes.html)
- [Example dual-capability device and native set/get payloads](https://www.zigbee2mqtt.io/devices/915005987501.html)
- [Optimistic configuration](https://www.zigbee2mqtt.io/guide/configuration/devices-groups.html)
- [Expose constructors](https://github.com/Koenkk/zigbee-herdsman-converters/blob/master/src/lib/exposes.ts)
- [Color set conversion](https://github.com/Koenkk/zigbee-herdsman-converters/blob/master/src/converters/toZigbee.ts)
- [Color report conversion](https://github.com/Koenkk/zigbee-herdsman-converters/blob/master/src/converters/fromZigbee.ts)
- [Read attributes, including mode](https://github.com/Koenkk/zigbee-herdsman-converters/blob/master/src/lib/light.ts)
- [Color synchronization/cache conversions](https://github.com/Koenkk/zigbee-herdsman-converters/blob/master/src/lib/color.ts)
- [Third Reality definitions](https://github.com/Koenkk/zigbee-herdsman-converters/blob/master/src/devices/third_reality.ts)

XY reporting uses four decimal places after a uint16 mapping, so one ten-thousandth bounds ordinary wire and report quantization. Non-enhanced hue uses 254 steps over 360 degrees plus integer reporting, so two degrees covers that quantization, and enhanced fractional hue rounds into the same integer contract. Saturation uses 254 steps with integer percentage reporting, so one percentage point is the bounded allowance. These tolerances reflect quantization only, not a perceptual metric or a model-specific gamut.
