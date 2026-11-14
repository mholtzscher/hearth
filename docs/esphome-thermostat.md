# ESPHome thermostats

Hearth maps an ESPHome climate entity to `hearth.thermostat/v1`, with Device kind
`thermostat`. Eligible sensors, switches, numbers, and selects remain separate
Entities on the same Device. Cover kind takes precedence on a mixed
cover/thermostat device.

Upgrade Core before the ESPHome Adapter. Configure credentials, discovery, and
static devices using the [ESPHome setup](../README.md#esphome-adapter).
No firmware change or Home Assistant bridge is required.

## Read the thermostat

Discover canonical IDs using the Entity or Device APIs. Thermostat State has a
required mode and optional readings:

```json
{
  "mode": "heat_cool",
  "activity": "idle",
  "current_temperature": 20000,
  "target_temperature_low": 18333,
  "target_temperature_high": 20556,
  "fan_mode": "Automatic",
  "current_humidity": 54,
  "target_humidity": 59
}
```

This example uses canonical units. Ordinary HTTP/MCP reads project temperatures
into household Celsius or Fahrenheit and provide pointer-keyed `quantities`.
Use `?units=canonical` for HTTP reads, or `units:"canonical"` in MCP, when
preparing a Command or Automation operand. Writes always use integer
milli-Celsius: 20 °C is `20000`; convert Fahrenheit with
`round((fahrenheit - 32) * 1000 / 1.8)`.

Mode is a setting, not current equipment activity. A thermostat in `heat` can
report `idle`. A missing reading means unknown, not zero, off, or idle.
Each native frame replaces the thermostat snapshot; the Adapter does not merge
earlier readings or sibling sensor State into it. Reported low/high targets may
be inactive in the current mode or temporarily reflect separately updated
equipment datapoints.

## Send explicit setting Commands

Read the Entity's `support.operations` before choosing an operation. Presence
means supported; absent setters cannot be used. All thermostat setters have
10-second observed deadlines.

| Operation | Canonical parameters | Meaning |
| --- | --- | --- |
| `set_mode` | `{"mode":"heat_cool"}` | Choose an advertised mode |
| `set_target` | `{"temperature":20000}` | Set a single-target device's target |
| `set_range` | `{"low":18333,"high":20556}` | Set both reported heating/cooling targets |
| `set_fan_mode` | `{"fan_mode":"Automatic"}` | Select an exact advertised fan choice |
| `set_humidity` | `{"humidity":59}` | Set the device-defined humidity target |

Supported canonical modes are `off`, `heat`, `cool`, `heat_cool`, `auto`, `dry`,
and `fan_only`. Each Entity advertises its own subset. `auto` is distinct from
`heat_cool`; do not substitute one for the other.

Temperature bounds live in `support.state.temperature_minimum` and
`temperature_maximum`. Humidity bounds use `humidity_minimum` and
`humidity_maximum`. These are write constraints, not clipping limits for
reported State. Their presence does not advertise a setter. Fan choices live
in `support.state.fan_modes`; copy the complete string, including case and spaces.

For a paired-target ESPHome device, use `set_range` even in heat, cool, off,
or fan-only mode. Do not send `set_target` as a heat/cool alias. Setting targets
does not select a mode, turn equipment on, or change a schedule. Range requests
require low ≤ high. One native request carries both values, but equipment may
apply and report the underlying datapoints separately.

Example HTTP request, using a discovered ID:

```sh
curl -X POST "$CORE/v1/entities/$THERMOSTAT/commands" \
  -H 'content-type: application/json' \
  -d '{"operation":"set_range","parameters":{"low":18333,"high":20556}}'
```

The existing developer Command form is manual: enter the supported operation
and its JSON parameters. Its generic default `set {"value":true}` is not a
thermostat operation. There is no dedicated thermostat control card. Existing
numeric and text cards can bind thermostat fields for readings and history.

## Rheem humidity and related settings

The household Rheem ESPHome/EcoNet device exposes two different humidity
controls:

- The climate Entity's `set_humidity` targets **dehumidification**.
- The separate **Humidification Target Percentage** number targets
  **humidification**.

Do not interchange these settings. **Follow Schedule**, humidification and
dehumidification enable switches, **Fan Duty Cycle**, and other eligible
settings remain independently addressable. The Adapter does not toggle them
as side effects of a thermostat Command. Firmware decides how a fan change
interacts with schedule and fan-only mode.

Numbers use `hearth.numericsetting/v1`:

```json
{"operation":"set","parameters":{"mode":"value","value":55}}
```

Selects use `hearth.enumsetting/v1`:

```json
{"operation":"set","parameters":{"value":"Continuous"}}
```

Read each Entity's bounds and choices. Numbers retain their advertised units;
an empty unit remains unspecified/dimensionless metadata. Hearth does not
infer percent or temperature from a setting's name.

The existing numeric-setting contract has no public step property. ESPHome
number SET validates bounds, not step alignment; Hearth forwards a valid value
without grid rounding. Values that cannot survive the Native API's shortest
decimal float32 representation are rejected before writing.

Configuration/diagnostic/default-disabled entities still require exact
include overrides, for example `include: ["number/Relative Humidity Offset"]`.
The other supported override forms include `climate/HVAC` and
`select/Fan Duty Cycle`. Exclusion and main-device eligibility still apply.
Use `--log-level debug` to inspect fixed `device.capability_omitted` reason
codes for unsupported entities or omitted thermostat control families.

## Evidence and limitations

A successful socket write permits Command acceptance. Satisfaction requires
an exact matching reported setting linked to that Command, not ambient
temperature reaching a target or proof that HVAC equipment started.
Intermediate reports can update State while a Command continues waiting.
For an already-matching request, the Adapter reuses its existing same-connection
subscription evidence after writing; the original receipt time remains visible.
This shortcut is not a fresh physical measurement. Reconnects never replay writes.

The Adapter does not round thermostat targets to ESPHome visual steps.
The Rheem's cached native metadata reported a 0.5 °C visual step while Home
Assistant displayed 0.5 °F; these are not equivalent and neither establishes
the actual equipment increment. Visual bounds are used conservatively for
write admission. Actual equipment rounding or ignored writes can make an
otherwise valid Command time out. Do not treat acceptance as success.

Activity is shown only when native discovery advertises it. Research found
that Home Assistant's cached action capability differed from the stock
EcoNet v3.8.2 source. Fresh discovery determines what Hearth exposes; neither
an advertised flag nor a reported action proves independently measured activity.

ESPHome parses standard fan names before custom names, ignoring case.
A custom label such as `Low` is therefore not safely addressable as a custom
choice; Hearth omits that ambiguous fan capability. The zero-valued standard
`on` mode is also indistinguishable from an unset optional fan on this wire.
Hearth omits `on` from advertised fan choices and treats such reports as unknown.
When a reserved custom label causes the entire fan capability to be omitted,
its otherwise unambiguous choices are omitted too. Without that collision,
other standard modes and custom speed labels can be exposed after successful
native discovery and registration.
No cached `on` default can satisfy a Command.

Protocol/production-Core tests use a synthetic peer. The read-only Rheem probe
did not complete native discovery: the endpoint rejected plaintext and presented
a Noise frame, and no encryption key was available. Version negotiation did not
occur. Physical discovery and readings remain unverified; their verification
is explicitly deferred for this delivery. Physical Commands,
deadband, equipment increments, fan/schedule effects, and write confirmation
remain unverified until explicitly authorized and tested.

## Home Assistant comparison and future devices

Home Assistant's climate model is a compatibility reference, not Hearth's
contract. The [climate developer guide](https://contributors.home-assistant.io/core/entities/climate/)
and [ESPHome translation](https://github.com/home-assistant/core/blob/df5a935ed1f297d051df655c8fdc5c505b7a7389/homeassistant/components/esphome/climate.py)
highlight these distinctions:

| Feature | Hearth v1 | Future gap |
| --- | --- | --- |
| Mode versus activity | Separate fields; activity optional | No inference of equipment stages |
| Single/range targets | Independent setters and optional readings | No mode-dependent single-target aliases |
| Custom fan choices | Exact unambiguous strings | Reserved standard names ignore case; ambiguous fan capabilities are omitted |
| Standard fan `on` | Not exposed | Zero-valued wire enum cannot establish that the optional fan value exists |
| Humidity | Measured and device-defined target; independent settings | No combined humidification/dehumidification range model |
| Presets | Not exposed by this Adapter | Eco, vacation, boost, sleep, and vendor presets |
| Swing | Not exposed | Vertical/horizontal AC and heat-pump swing |
| Schedules and holds | Firmware-owned siblings where exposed | No generic resume, hold, or schedule API |
| Auxiliary heat | Independent existing sensor/settings semantics | No generic emergency-heat control |
| Power | Explicit supported mode | No promise that “on” restores the previous mode |
| Dynamic constraints | Declared bounds and ordered range requests | Device-specific deadband, preset restrictions, and mode-dependent limits |

HA distinguishes schedule-managed `auto` from automatic heat/cool selection
and supports independent single/range capability flags. Some integrations
adjust another target or change mode before writing; Hearth does neither
implicitly. HA [deprecated climate auxiliary heat](https://developers.home-assistant.io/blog/2024/03/10/climate-aux-heater-deprecated/)
in favor of independent switch/sensor modeling.

Relevant upstream sources:

- [HA climate request validation](https://github.com/home-assistant/core/blob/df5a935ed1f297d051df655c8fdc5c505b7a7389/homeassistant/components/climate/services.py).
- [Nest device-specific constraints](https://github.com/home-assistant/core/blob/df5a935ed1f297d051df655c8fdc5c505b7a7389/homeassistant/components/nest/climate.py).
- [ESPHome 2026.10.2 climate flags](https://github.com/esphome/esphome/blob/2026.10.2/esphome/components/climate/climate_mode.h).
- [EcoNet v3.8.2 climate translation](https://github.com/esphome-econet/esphome-econet/blob/631859b2af7fdd51bf5c53ada9380c2801577ca5/components/econet/climate/econet_climate.cpp).
