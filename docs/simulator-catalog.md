# Simulator catalog

`mise run simulator-start` loads [`configs/scripted.simulator.yaml`](../configs/scripted.simulator.yaml).
The default has 26 Devices and 105 Entities across five Adapters. `sim-healthy`
owns 22 Devices and 100 Entities. The four fault Adapters retain their original
Devices and behaviors; `sim-unavailable` also has a numeric temperature Entity.
No new Entity types or simulator runtime features are required.

## Household fixtures

Binding keys identify Devices within `sim-healthy`. Discover canonical Entity IDs
from the simulator control inventory rather than constructing them from keys.

| Device binding keys | Entity keys | Dashboard coverage |
| --- | --- | --- |
| `living-room-sensor`, `kitchen-sensor`, `office-sensor`, `bedroom-sensor`, `nursery-sensor`, `garage-sensor` | `temperature`, `humidity`, `battery`, `linkquality`, `occupancy` | Six indoor temperature series, humidity, duplicate Battery/Humidity labels, binary State |
| `outdoor-sensor` | `temperature`, `humidity`, `battery`, `linkquality` | Temperature crossing zero, empty battery |
| `freezer-sensor` | `temperature`, `battery`, `linkquality` | Negative temperature, weak link quality |
| `kitchen-metered-plug`, `office-metered-plug` | `power`, `consumption`, `energy`, `voltage`, `current`, `mode`, `countdown` | Boolean switches, watts, watt-hours, volts, amps, numeric/choice States |
| `air-quality-monitor` | `co2`, `pm25`, `voc`, `temperature`, `humidity`, `battery` | Parts per million, micrograms per cubic metre, long labels, Unicode names |
| `weather-station` | `temperature`, `humidity`, `pressure`, `wind`, `gust`, `illuminance` | Temperature, pressure, and speed unit conversions; lux with a wide range |
| `hallway-light` | `power` | On/off-only capability subset |
| `bedside-light` | `power`, `brightness` | Dimmable subset, initial brightness 0% |
| `kitchen-white-light` | `power`, `brightness`, `colortemp` | Tunable-white subset, initial brightness 100% |
| `living-room-color-light` | `power`, `brightness`, `colorhs`, `colortemp`, `colormode` | Structured State fields, hue/saturation boundaries, active/inactive color |

Use the eight `*-sensor` temperature Entities together for the chart's maximum
eight series. Their cadences range from 5 to 23 seconds. Indoor series include
overlapping values; outdoor and freezer readings expand the axis below zero.
Temperature State uses integer milli-Celsius, not degrees Celsius.

Metering readings are `hearth.numericsensor/v1`; `hearth.power/v1` is a boolean
switch, not a watt measurement. Energy totals publish once so a looping sequence
does not reset a cumulative total. The metered plug countdowns use seconds and a
`disabled` choice. The office countdown starts in choice mode with no `/value`;
the kitchen countdown alternates between numeric and choice modes.

The countdowns are display fixtures, not happy-path mode-changing Command fixtures.
The simulator cannot apply `choice` or change `mode`: a `set` requesting those
changes can be accepted and then time out. Numeric `set` works only while the
current State already has a `value` member. Use full-State control publications
to exercise numeric/choice display transitions. Those publications are input
Observations, not evidence that a Command completed.

New light power and brightness Entities have no output ticker, so Commands stay
visible until another Command or manual publication. Other Entity scripts are
independent, not a coupled physical simulation: changing a plug's switch does not
change its meter readings, and color mode does not rewrite its color Entities.
Generic simulator parameter application handles `value`, not XY/HS coordinates
or `active`. New color-temperature fixtures start active so numeric `set` can
satisfy its outcome. Use full-State control publications for coordinate or active
flag changes.

## Numeric diagnostics

These two Devices belong to `sim-healthy`. Their values deliberately exercise
formatting and chart behavior rather than model physical sensors.

| Device / Entity key | Output | Expected use |
| --- | --- | --- |
| `diagnostic-patterns/constant` | `42` once, then silence | Reading age and a single-point history |
| `diagnostic-patterns/repeated` | `42, 42` every 5 seconds | Fresh identical Observations without a new afterglow |
| `diagnostic-patterns/rounded` | `21.501, 21.502, 21.503` every 5 seconds | Values that all display as `21.5`; no formatted-value change |
| `diagnostic-patterns/slow` | `0, 25, 50, 75, 100` every 2 minutes | Slow updates, percentage boundaries, ramp |
| `diagnostic-patterns/rapid` | `0, 0, 100, 100, 0` every 2 seconds | Step changes and repeated values |
| `diagnostic-ranges/zero` | `0` once | Zero must not look like missing data |
| `diagnostic-ranges/signed` | `-100, -0.5, 0, 0.5, 100` every 5 seconds | Signed values and axes crossing zero |
| `diagnostic-ranges/tiny` | `0.001, 0.002, 0.009, 0.1` every 7 seconds | Rounding and small values |
| `diagnostic-ranges/large` | `987654321.5` watt-hours once | Large totals and card overflow |
| `diagnostic-ranges/spike` | `10, 10, 10, 1000, 10, 10` every 5 seconds | Short spikes and chart min/max bands |

In `sim-unavailable`, `simulated-unavailable-light/temperature` starts unavailable
and never publishes initial State. Bind it to a numeric card to inspect
unavailability with no reading. The original power Entity still repairs its own
availability on a `set` Command; it does not repair the temperature Entity.

## Suggested checks

1. Compare eight room temperatures on one chart. Check legend labels, overlapping
   lines, negative readings, and the Source links to the correct Entities.
2. Compare Metric and TimeSeries values for temperature, pressure, and wind in
   metric and US household unit systems. The Core unit-system setting requires a
   restart; stored canonical values do not change.
3. Place zero, tiny, and large readings in narrow cards. Include the air-quality
   Device's long Unicode name and repeated Battery labels in discovery views.
4. Compare constant, repeated, rounded, rapid, and slow diagnostics. New identical
   or identically formatted readings must not create a fresh afterglow.
5. Bind a Metric to the office countdown's `/value` field and another to the
   unavailable light temperature. Distinguish an absent field from unavailable,
   never-observed State.
6. Open the development Entity list and navigate past its first 50-item page.

State scripts publish their first value at startup, then advance and loop.
Single-value scripts publish only once. Pause tickers through the existing
control API before deterministic Command or manual-publication checks. Control
acknowledgment alone does not prove Core accepted a publication; match its exact
Observation ID in Core State or history.

## Limits

The catalog does not seed past history. A fresh stack only has samples collected
since startup, including on 24-hour and 7-day charts. Timestamp offsets do not
replace historical ingestion. A quiet Entity is not automatically unavailable.

Disabled/missing Entities, available-but-never-observed State, retained readings
after unavailability, transport failures, and lifecycle transitions require
separate scenarios or existing frontend fixtures. This expansion adds neither
scenario drivers nor history seeding. Existing notification destinations,
Functions, fault scenarios, and the minimal first-light example remain unchanged.
