# Quantity metadata and read units

Hearth stores State, history, Commands, and Automation operands in canonical
units. Ordinary Entity and numeric aggregate-history reads return household units
using Core's configured `household_unit_system` (`metric` or `us`). This changes
presentation, not stored data. Clients format the returned numbers without
converting them again.

## Read representations

These operations return household-unit readings by default:

- `GET /v1/entities/{entity_id}` and `GET /v1/entities`.
- `GET /v1/devices/{device_id}`, for each embedded Entity.
- `POST /v1/entities/{entity_id}/state/history`, after canonical aggregation.
- `PATCH /v1/entities/{entity_id}`, for the returned Entity.

Add `?units=canonical` to select the original canonical numbers instead. MCP
reading tools accept the equivalent optional `units: "canonical"` argument.
Omit the option for household units; there is no display opt-in flag or per-read
choice of metric versus US. An unknown option is invalid.

Both modes retain the full response envelope, evidence, and pagination, and
return only one set of readings. Cache entries must distinguish the requested
representations. Raw observation audit history, Device Facts, Automation
definitions, and internal evaluations remain canonical.

Entities include a `quantities` object keyed by the numeric field's State JSON
Pointer. The empty key denotes a scalar root. Each entry describes the number
at that field's existing path; it does not duplicate the number:

```json
{
  "quantities": {
    "": {
      "unit": "[degF]",
      "label": "°F",
      "fraction_digits": 1
    }
  }
}
```

This is an excerpt for a temperature Entity, not a replacement Entity body.
Canonical `21500 mCel` is returned as approximately `70.7` in `state.value` for a
US household. The same read with `units=canonical` returns `21500` and metadata
describing `mCel`. Integer milli-Celsius schemas describe canonical State, not
the display representation. Support remains canonical and must not be used to
interpret converted values or bounds; use the response metadata.

The metadata remains available when `state` is null. Optional numeric fields in
choice-valued settings remain absent. Non-numeric fields are unchanged.
Declared `range` bounds use the same unit as their reading. `fraction_digits`
is formatting advice, not measurement accuracy or numeric rounding in the API.
State validation limits alone do not declare a presentation range. For example,
the fixed temperature schema has validation limits but no declared range.
History selects one field and returns one `quantity` metadata object beside its
metrics or buckets. Null metrics remain gaps, never zero.

### Forecast snapshots

Hourly and daily forecast Entities also use backend household projection.
Their measurement names are unit-neutral and identical in canonical and household
reads. Canonical units are declared by the contracts, not encoded in the names:

| Period property | Canonical unit |
| --- | --- |
| `temperature`, `apparent_temperature`, `temperature_min`, `temperature_max` | `Cel` |
| `precipitation_probability`, `humidity`, `cloud_cover` | `%` (0–100) |
| `precipitation_rate` | `mm/h` |
| `pressure` | `hPa` |
| `wind_speed`, `wind_gust` | `m/s` |
| `wind_direction` | `deg` |
| `uv_index` | `index` |

Metadata uses concrete period pointers, such as
`quantities["/periods/0/temperature"]`. Each entry describes that returned field;
there are no wildcard pointers or duplicate readings. Omitted measurements stay
absent. Timestamps, dates, conditions, source summaries, period order, and
Observation evidence are unchanged. A null forecast State has an empty quantities
map because there are no period indices yet.

For example, a US household read returns `temperature: 36.5` with Fahrenheit
metadata; `units=canonical` returns `temperature: 2.5` with Celsius metadata at
the same pointer. Canonical reads preserve stored numeric tokens. SDK ingestion
and retained State use these same neutral names and canonical units. Forecast
Function results also use neutral names, but remain canonical and are not projected.

Forecast canonical temperatures use `Cel`, unlike current-condition temperature
Entities' `mCel`. Both use the shared affine engine and household policy. Forecast
temperature and precipitation-probability metadata recommend zero fraction digits
for compact card labels without rounding API values. Forecast arrays do not
become selectable scalar fields or aggregate-history sources.

## Writes are always canonical

The `units` option controls responses only. Command parameters and Automation
operands must satisfy their canonical contracts. Request `units=canonical` before
using a State value as a write input; never copy a household-unit reading into a
canonical write. Metadata PATCH does not write State and returns the same
representation an equivalent GET would return. Receipts without Entity readings
do not gain projection behavior.

Unit-aware threshold authoring is a separate feature. This change does not
convert saved operands, change exact comparison/equality, or round Adapter input
to a common normalization policy.

## Quantity kinds

Numeric field schemas declare a fixed `quantity_kind` or reference
`support.state.quantity_kind` for generic numeric sensors and settings. The kind
describes meaning rather than just dimension: precipitation rate displays in
inches/hour while speed displays in miles/hour for US households.

Hearth validates the bounded kind vocabulary and compatible canonical unit codes.
Percent-valued humidity, brightness, saturation, battery, cloud cover, and
precipitation probability have distinct kinds. Color temperature remains in
mired, not absolute temperature; chromaticity x/y share a kind but have distinct
field pointers. Air-quality readings use `carbon_dioxide` in `ppm`,
`particulate_matter` in `µg/m³`, and `air_quality_index` in `index`. PM1, PM2.5,
PM4, and PM10 share `particulate_matter`; the Entity name identifies the particle
size, as it identifies the axis for chromaticity. VOC and NOx indices share
`air_quality_index`. Existing legacy codes such as `mired`, `ratio`, and `lqi`
retain their storage spelling. Unknown generic measurements may remain unclassified;
Core does not infer meaning from mutable names or unit dimensions.

An existing generic field can acquire a previously absent kind once. Thereafter
ordinary registration cannot change or remove it. Invalid kinds, incompatible
units, and immutable-interpretation changes reject the registration batch without
partial edits. Existing unit immutability remains unchanged. No value migration
or new metadata database column is required.

## Conversion boundary

The backend uses a small supported conversion table behind a positive-affine
interface (`y = scale*x + offset`). Scale must be positive and finite, and offset
and results must be finite. Household unit selection, labels, and precision are
separate from this mathematical engine. It can be replaced without changing
display policy. Display conversions do not alter Adapter ingestion constants or
source-specific rounding and rejection rules.

Core aggregates history canonically before conversion. Positive-affine conversion
preserves min/max ordering and arithmetic sample means. A sample mean for hue or
wind direction is still arithmetic; it is not a circular mean. Precipitation
counter summaries do not become precipitation totals over a window by summing
cumulative readings. Forecast Function outputs have separate canonical-unit schemas
and are not projected by this Entity-read feature.

Weather cards display household readings. Their condition words, Beaufort
forces, and pressure tendencies use fixed thresholds, so the cards evaluate them
against a separate `units=canonical` read of the same Entity. The frontend does
not convert units, and the cards show no feels-like or dew-point temperatures.
See [weather cards](dashboards.md).

## Deployment and rollback

This is a breaking default-read contract change. Canonical and household reads
use identical field paths, including forecasts. Deploy matching Core, Adapters,
SDK consumers, and web together, and update
external numeric consumers to use returned metadata or `units=canonical`.
Older clients that still perform household conversion can convert twice.

Forecast contracts correct the initial unit-suffixed names in place. Stop old
forecast producers and let their Observation queue drain before the coordinated
cutover; old suffixed payloads are not accepted aliases. Migration 32 renames keys
in current forecast State, its backing Observation, and pending Device Facts
linked to forecast Entities and their source Observations. Numeric tokens, IDs,
timestamps, period order, and saved dashboards are preserved. Older audit rows,
Automation evidence, and pinned Function results are not rewritten and may retain
the old names. New Function invocations use the corrected descriptor and result
shape. Review any saved Automation expressions or external consumers that
reference the old keys. The migration does not rewrite their definitions.

An orphaned pending fact has no retained Entity type to establish its contract.
The migration leaves such records untouched rather than infer a type from their
JSON field names.

Back up SQLite before upgrading. Migration 32 refuses automatic downgrade;
restore the pre-upgrade backup with matching Core, Adapters, and web binaries.

Upgrade Core before Adapters send the optional kind declaration: older Core
support schemas reject that property. Generic retained rows remain unclassified
until the Adapter re-registers explicitly. Fixed types derive their kinds from
the new schema catalog without rewriting retained support or State.

After a generic field acquires a kind, rolling its Adapter back to a version
that omits the kind fails registration. Preserve the declaration in the rollback
binary. To downgrade Core and the whole contract, restore a pre-upgrade backup
with matching Core, Adapters, and web versions, or use an explicitly reviewed
migration. There is no automatic kind erasure or support merging to make old
binaries accept the new metadata.
