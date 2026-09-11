# Extending Zigbee2MQTT capabilities

Hearth maps **capabilities, not device models**. Zigbee2MQTT already describes
most device differences through exposes. Another model with the same supported
exposes normally needs a captured regression fixture, not a new mapping.

## Where to change code

| Change | Location |
| --- | --- |
| Read-only ambient numeric capability | `ambientNumericSensors` in `internal/adapters/zigbee2mqtt/capability_catalog.go` |
| Read-only smart-plug electrical capability | `smartPlugElectricalSensors` in the same file |
| Read-only binary occupancy capability | `planOccupancy` in `internal/adapters/zigbee2mqtt/entity_binarysensor.go` |
| Observable relay numeric setting | `smartPlugNumericSettings` in the same file |
| Read-only `action` Event source | `planActionEvent` in `internal/adapters/zigbee2mqtt/entity_event.go` |
| Light/color composition or dependency | `planner_light.go` |
| Relay family gating | `planner_relay.go` |
| New State/Command conversion | The relevant `entity_*.go` constructor |

The catalog is ordinary typed Go data. There is no JSON schema, compiler,
strategy registry, evaluator, runtime catalog dependency, or override engine.
Changing a mapping requires rebuilding the adapter, just like other code.

## Add a numeric sensor

Add one record to the appropriate table, using captured expose evidence:

```go
{
    exposeName: "humidity", key: "humidity", displayName: "Humidity",
    upstreamUnit: "%", unit: "%", minimum: 0, maximum: 100,
},
```

- `exposeName` and `upstreamUnit` match exactly; an empty unit is not a wildcard.
- The State property and endpoint come from inventory, not the table.
- `key` determines stable Entity identity. Do not rename existing keys casually.
- Ambient sensors use fixed table bounds and visit all matching roots in inventory
  order. Electrical sensors require exactly one matching device-root expose;
  valid upstream bounds win, both absent use the table's fallback envelope, and
  malformed or partial bounds omit only that sensor.
- Both use `newNumericSensorPlan`: JSON numbers retain their fractions, support
  validates their range, publish is required, set is forbidden, and get access
  alone enables refresh. These rules do not become per-record options.
- Temperature keeps its milli-Celsius constructor. Link quality keeps exact-integer
  decoding and device-wide unique-root selection. A different conversion is code,
  not an expression in a table.

Ambient illuminance is a record in the same `ambientNumericSensors` table: exact
expose name `illuminance`, exact unit `lx`, and a fixed 0–1000000000 lx
validation envelope because Zigbee2MQTT omits ambient bounds. Its non-empty,
Device-unique State property still comes from inventory. It uses the same
`newNumericSensorPlan` rules (publish required, set forbidden, get access alone
enabling refresh), so it needs no new constructor.

`planDevice` calls `planLightFamily`, `planRelayFamily`, `planSensorFamily`,
`planOccupancy`, `planLinkquality`, and `planActionEvent` directly.
`mergeDeviceContributions` merges their returned values; there is no planner
interface, registry, or empty planner object.

Planner order is observable. The first non-empty light or relay family is
primary and keeps Device kind `light` or `relay`; every later family is
supplemental, so a light that also reports occupancy stays a light while an
occupancy-only Device is a sensor. Within the sensor family, ambient temperature
precedes the ambient table, whose capability records run humidity, illuminance,
then battery. Occupancy follows the sensor family, then link quality, then the
read-only action Event last, in that order. Relay power and power-on behavior
precede electrical sensors, then numeric settings, then reset. Family precedence
and color/power dependencies remain explicit in the existing planners.

## Add a binary occupancy sensor

Occupancy is a boolean reading, so it uses the generic read-only
`hearth.binarysensor/v1` type rather than a numeric table. `planOccupancy`
accepts one resolved root binary expose that is named and property-aliased
`occupancy`, with publish access, no set access, a Device-unique property, and
present distinct `value_on`/`value_off` scalars. Those declared scalars, not an
assumed boolean, define decoding: `true`/`false`, `"ON"`/`"OFF"`, and `1`/`0`
each decode exactly when the expose declares them, and any other value is a
per-property decode issue that leaves valid siblings intact.

The plan builds support `{"state":{},"operations":{}}` through
`newBinarySensorPlan`, requires publish access, forbids set access, and creates
no command route; get access alone controls startup `/get`. An absent,
non-scalar, or identical on/off pair, a duplicate root, a foreign claim on the
property, or set
access omits occupancy without affecting valid siblings. The contribution is
supplemental, so a light or relay that also reports occupancy keeps its actuator
kind and an occupancy-only Device is a sensor.

Occupancy is not a device-model special case and needs no model table. A new
model with the same declared binary expose needs only a captured regression
fixture.

Evidence: `testdata/bridge-devices-3rsnl02043z.json` and
`testdata/state-3rsnl02043z.json` are handcrafted minimal sanitized shapes of a
2026-09-11 passive capture from the Third Reality `3RSNL02043Z` night light on
host Wanda (`software_build_id` `v1.00.86`). Sanitization changed only the IEEE
address, friendly name, and description; model, vendor, build, expose nesting,
expose type/name/property/access, units, enum values, declared binary
`value_on`/`value_off`, numeric bounds, and the reported values are retained. The
fixture discovers nine Entities on a light Device in planner order — power,
brightness, colorxy, colormode, poweronbehavior, effect, illuminance, occupancy,
linkquality — and the live payload reported illuminance `20` and occupancy
`true`. This is passive evidence for discovery, support, and State translation
only: no physical command was exercised, and occupancy and illuminance are
read-only.

## Add a numeric setting

Add one record to `smartPlugNumericSettings`:

```go
{
    exposeName: "led_brightness", key: "ledbrightness", displayName: "LED Brightness",
    expectedUnit: "%",
},
```

Settings require a unique root, exact unit, publish/set/get access, and both valid
upstream bounds. Empty `expectedUnit` requires no upstream unit and omits the
Hearth unit. The shared constructor handles fractional values, wire property,
refresh, and matching. No new translator is needed for this class of setting.

## Map a read-only action event

Buttons and remotes report named occurrences through a top-level `action` enum
expose. Hearth maps the single device enum expose named `action` — at the
device root or on one named endpoint, which yields an endpoint-scoped Entity —
with publish-only access (`access == 1`), a device-unique `action` or
`action_*` property covered by Zigbee2MQTT's cache exclusion, and a resolvable
endpoint to a `hearth.enumevent/v1` Entity. A second `action` expose
anywhere in the device is ambiguous and omits the capability. The discovered
`values` array passes through unchanged as the Event name support, so the
generated facade rejects a name outside the canonical slug pattern and the
Entity is omitted rather than registered with partial support. The Entity
claims no State, requests no startup `/get`, and creates no command route; the
contribution is supplemental, so a standalone button keeps the sensor kind.

Freshness depends on verified Zigbee2MQTT behavior. Zigbee2MQTT 2.14.1
`lib/state.ts` lists `action` and `action_.*` in `CACHE_IGNORE_PROPERTIES`, and
`State.set` removes those properties before persisting its State cache or
sending later cache-expanded and startup-cache messages. Broker-retained live
messages can still contain `action`, so Hearth independently ignores every MQTT
message marked retained. Each non-retained message with a present, supported
`action` is exactly one occurrence; repeated identical reports are never
value-deduplicated. An action report that arrives before MQTT routes are active
is queued as its own occurrence, in arrival order, and replayed exactly once
when routes activate; it is never coalesced with a sibling action report, and a
later battery-only report on the same topic cannot erase it. Queued occurrences
live only in that connection's memory: the pending queue is bounded (1024
entries, with ordinary topics coalesced per topic), so it is a short-lived
pre-activation buffer, not a durable outbox. If admitting a message would exceed
the bound, the adapter never drops a chosen occurrence silently; it ends the MQTT
generation, reports the adapter unhealthy, and reconnects with backoff so the
next generation resynchronizes from retained bridge topics. An absent, empty,
null, malformed, or unsupported action emits nothing and never suppresses a
valid sibling State observation from the same message. If a future Zigbee2MQTT
version caches actions again, later non-retained State reports could repeat a
stale action; update the compatibility evidence before claiming support for
that version.

Evidence: `testdata/bridge-devices-3rsb22bz.json` is a sanitized model of the
2026-09-10 Third Reality `3RSB22BZ` capture (Zigbee2MQTT 2.14.1,
`software_build_id` `v1.00.35`): identity fields are sanitized, while model,
vendor, build, expose shape, and values are retained. It exposes `action` with
values `[single, double, hold, release]` alongside numeric battery and
linkquality.

## Prove a change

1. Capture inventory and State/Command evidence for the device. Add or extend a
   fixture; never infer a vendor quirk solely from a model name.
2. Add literal expectations for identity, support, ordering, MQTT properties,
   representative values, and rejected malformed inputs. Do not calculate expected
   results from the production table.
3. Run `mise run validate`. Existing discovery, command, reconciliation, and
   race-enabled runtime tests must continue to pass.

`capability_catalog_test.go` uses synthetic, test-only sensor/setting mappings to
prove that these additions need data rather than a new translation implementation.
`discovery_contract_test.go` retains literal device contracts independent of the
tables. Synthetic extension examples do not add unverified production capabilities.

## Deliberate limits

The proposed JSON profile language in PR #76 was implemented and then replaced
before merge: describing gates, sources, dependencies, strategies, and overrides
made authors learn a second planning system without adding device support.
The final design keeps the useful repetitive data and removes that language.

There is no speculative vendor/model/build override mechanism. If a captured
quirk cannot be represented by existing exposes, add the smallest explicit Go
correction alongside the affected planning code and its fixture. Only extract
shared machinery once repeated real cases demonstrate what needs to vary.
