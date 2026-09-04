# Zigbee2MQTT color temperature

## Problem

Hearth discovers power and brightness for tunable-white Zigbee lights but drops Zigbee2MQTT's `color_temp`. For example, `internal/adapters/zigbee2mqtt/testdata/state-3rcb01057z.json` reports `"color_temp":370`, and the matching inventory expose declares a 153–500 mired range, but Hearth creates no Entity for it.

## Decision and scope

Add `hearth.colortemp/v1` as a separate Entity for each eligible light expose, following `hearth.brightness/v1`. Its State and `set.value` are integer mireds, Zigbee2MQTT's native unit. Direct passthrough avoids conversion loss and lets command satisfaction use exact equality.

A light's color-temperature range is Entity support discovered from Zigbee2MQTT. A valid power expose remains the eligibility gate, so `color_temp` cannot create a Device by itself. One expose may therefore produce power, brightness, and color-temperature Entities.

This slice supports `color_temp` only. It defers `color_xy`, `color_hs`, `color_rgb`, `color_mode`, and `color_temp_startup`. Kelvin conversion belongs in a future presentation layer.

The feature uses the existing Entity command route, Zigbee2MQTT configuration, Device reconciliation, availability rules, NATS contracts, and entity-type-driven OpenAPI. It adds no route or configuration option.

## Entity type contract

Create package `entitytypes/colortempv1` for type `hearth.colortemp/v1`. The outer State range is 100–1000 mireds. Entity support narrows that range per light and declares a `set` step of 1. State and parameters must fall within both bounds. The `set` deadline is 10 seconds, and a command is satisfied when observed State exactly equals `set.value`.

### `entitytype.json`

```json
{
  "manifest_version": 1,
  "type": "hearth.colortemp/v1",
  "state_schema": "state.schema.json",
  "support_schema": "support.schema.json",
  "state_validation": [
    {"op": "gte", "left": {"root": "state", "path": ""}, "right": {"root": "support", "path": "/state/minimum"}},
    {"op": "lte", "left": {"root": "state", "path": ""}, "right": {"root": "support", "path": "/state/maximum"}}
  ],
  "operations": {
    "set": {
      "parameters_schema": "set-parameters.schema.json",
      "deadline_ms": 10000,
      "parameter_validation": [
        {"op": "gte", "left": {"root": "parameters", "path": "/value"}, "right": {"root": "support", "path": "/state/minimum"}},
        {"op": "lte", "left": {"root": "parameters", "path": "/value"}, "right": {"root": "support", "path": "/state/maximum"}},
        {"op": "multiple_of", "left": {"root": "parameters", "path": "/value"}, "right": {"root": "operation_support", "path": "/step"}}
      ],
      "satisfied_when": {"op": "eq", "left": {"root": "parameters", "path": "/value"}, "right": {"root": "state", "path": ""}}
    }
  },
  "examples": "examples.json"
}
```

### `state.schema.json`

```json
{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"urn:hearth:schema:entity-type:colortemp:v1:state","title":"Hearth colortemp/v1 State","type":"integer","minimum":100,"maximum":1000}
```

### `support.schema.json`

```json
{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"urn:hearth:schema:entity-type:colortemp:v1:support","title":"Hearth colortemp/v1 Entity support","type":"object","additionalProperties":false,"required":["state","operations"],"properties":{"state":{"type":"object","additionalProperties":false,"required":["minimum","maximum"],"properties":{"minimum":{"type":"integer","minimum":100,"maximum":1000},"maximum":{"type":"integer","minimum":100,"maximum":1000}}},"operations":{"type":"object","additionalProperties":false,"required":["set"],"properties":{"set":{"type":"object","additionalProperties":false,"required":["step"],"properties":{"step":{"type":"integer","minimum":1,"maximum":100}}}}}}}
```

### `set-parameters.schema.json`

```json
{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"urn:hearth:schema:entity-type:colortemp:v1:set-parameters","title":"Hearth colortemp/v1 set parameters","type":"object","additionalProperties":false,"required":["value"],"properties":{"value":{"type":"integer","minimum":100,"maximum":1000}}}
```

### `examples.json`

```json
{"cases":[{"name":"fixture-153-500-step-1","support":{"state":{"minimum":153,"maximum":500},"operations":{"set":{"step":1}}},"states":[{"value":370,"valid":true},{"value":153,"valid":true},{"value":500,"valid":true},{"value":152,"valid":false},{"value":501,"valid":false}],"operations":{"set":{"parameters":[{"value":{"value":370},"valid":true},{"value":{"value":152},"valid":false}],"outcomes":[{"parameters":{"value":370},"state":370,"satisfied":true},{"parameters":{"value":370},"state":369,"satisfied":false}]}}}]}
```

Generation must produce the `colortempv1` contract types with `int64` State, support bounds, step support, and `SetParameters.Value`; the typed SDK facade; conformance tests; and core catalog registration. Generated files are not hand-written.

## Generator change

Add `gte` to the relation DSL in `entitytypes/entitytype-manifest.schema.json` and `internal/cmd/entitytypegen/behavior.go`. Like `lte`, it requires matching numeric operands. It generates `left >= right`. Update generator tests and `specs/generated-entity-type-behavior.md` to cover and document the operator. Existing Entity types must generate without behavioral changes.

## Adapter contract

### Discovery and identity

For each otherwise eligible light expose, discover an optional feature when all of these hold:

- `type == "numeric"` and `name == "color_temp"`;
- exactly one matching feature exists and its non-empty `property` is unique across the Device inventory;
- `access & 7 == 7`;
- `value_min` and `value_max` are finite integers within 100–1000, and `value_min < value_max`.

An invalid or ambiguous color-temperature feature is omitted without suppressing valid power or brightness siblings. An expose without valid power remains ineligible and reports through the existing `no_eligible_light` path.

Add `entityKindColorTemp`. Store the discovered integer minimum and maximum on `discoveredEntity`. Register support as:

```json
{"state":{"minimum":153,"maximum":500},"operations":{"set":{"step":1}}}
```

Use Entity key and external-ID kind `colortemp` at the root. For endpoint-scoped exposes, use key `colortemp-epN` and the existing endpoint naming convention. `makeEntityDescriptor` creates the descriptor through `sdk/adapter/colortempv1`.

### State and observations

Add `ColorTemp int64` to `decodedEntityState`. `normalizeColorTemp` accepts only a finite, integral JSON number within the discovered inclusive range. It performs no unit conversion. A rejected value creates a `stateDecodeIssue` for that property without dropping valid sibling States.

The observation branch publishes a typed `hearth.colortemp/v1` Observation with the mired value. Existing retained-State, availability, reconciliation, and observation-disposition rules apply unchanged.

### Commands and matching

Add `colortemp int64` to `desiredState` and a typed command branch using `sdk/adapter/colortempv1`. For `set {"value":370}`, publish this one-property payload to `<base>/<friendly_name>/set`:

```json
{"color_temp":370}
```

After QoS 1 acceptance, call the existing `publishGet` for `<base>/<friendly_name>/get` with `{"<property>":""}`. Keep the existing per-IEEE FIFO, absolute deadline, connection generation, route revision, and one-report disposition rules. `matcherMatches` requires exact mired equality. Only a fresh, non-retained, post-dispatch matching report may become command-linked evidence.

The HTTP API remains `POST /v1/entities/{entity_id}/commands` with `{"operation":"set","parameters":{"value":370}}`. Validation and command failures use the existing Entity-type and core error semantics.

## Implementation map

| Area | Change |
|---|---|
| `entitytypes/entitytype-manifest.schema.json`, `internal/cmd/entitytypegen`, generated-behavior spec | Add and test `gte` |
| `entitytypes/colortempv1`, generated SDK and catalog files | Add schemas, examples, generated types, behavior, facade, and registration |
| `internal/adapters/zigbee2mqtt/discovery*.go` | Discover the feature, range, identity, and descriptor |
| `internal/adapters/zigbee2mqtt/state.go`, `observation.go` | Decode strict mired State and publish typed Observations |
| `internal/adapters/zigbee2mqtt/command.go`, `runtime.go` | Pass through commands and match exact State |
| Zigbee2MQTT fixtures and tests | Cover discovery, State, commands, and concurrency |
| `README.md` | Document color-temperature discovery and command examples |

Estimated effort is 1–2 days. Implement generator support before the entity type, then discovery, State and commands, tests and documentation, and live verification.

## Acceptance and verification

- [ ] `mise run generate` and `mise run validate` pass. The catalog contains `hearth.colortemp/v1` with the schemas, 10-second deadline, range rules, step rule, and exact satisfaction rule above.
- [ ] The 3RCB01057Z fixture discovers power, brightness, and color-temperature Entities. Color-temperature support is 153–500 mireds with step 1. A temp-only fixture remains `no_eligible_light`.
- [ ] State `{"color_temp":370}` produces typed State `370`. Values `152` and `501`, non-numbers, and fractional numbers produce a property-level `stateDecodeIssue` while valid siblings still publish.
- [ ] A set to 370 publishes the exact `/set` and `/get` payloads above. It satisfies only from eligible linked evidence before the deadline.
- [ ] Commands for one IEEE Device remain FIFO across power, brightness, and color temperature. Different Devices may progress concurrently.
- [ ] Focused unit tests cover `gte`, generated conformance, discovery validation and endpoint identity, State decoding, passthrough, and exact matching. Integration tests cover `/set` through linked Observation and mixed-Entity concurrency. Run Gremlins on each changed Go package after strengthening its tests and investigate surviving mutants.
- [ ] On a live 3RCB01057Z with Zigbee2MQTT 2.13.0, setting 370 through the HTTP route succeeds only after the linked refresh. `GET /v1/entities` then reports State 370, and the Adapter remains healthy. Sweep both range endpoints, 153 and 500.

## Decisions confirmed

- Outer schema and discovery bounds 100–1000 mireds: approved.
- Live proof bulb: 3RCB01057Z: approved.
