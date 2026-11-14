# Quantity metadata and read units

Hearth stores State, history, Commands, and Automation operands in canonical
units. Entity-detail and numeric aggregate-history reads return canonical and
display representations together. Display uses Core's configured
`household_unit_system` (`metric` or `us`). This changes
presentation, not stored data. Clients format the returned numbers without
converting them again.

## Read representations

These operations return household-unit readings by default:

- `GET /v1/entities`.
- `GET /v1/devices/{device_id}`, for each embedded Entity.
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
These pointer-keyed quantities apply to collection, embedded Device, and PATCH
responses. Entity-detail and aggregate-history responses use the inline paired
shape below. Null metrics remain gaps, never zero.

### Paired canonical and display reads

`GET /v1/entities/{entity_id}` always returns both representations
from one retained Observation. It replaces `state.value` and
top-level `quantities` with two trees inside the same State evidence envelope:

```json
{
  "state": {
    "observed_at": "2026-11-14T21:02:40Z",
    "canonical": {"value": 4.4704, "unit": "m/s"},
    "display": {"value": 10, "unit": "[mi_i]/h", "label": "mi/h", "fraction_digits": 1}
  }
}
```

This synthetic fragment omits Entity metadata and the State's shared
`observation_id`, `adapter_received_at`, and optional `source_updated_at`.
Those fields remain on State, not on either representation. A never-observed
or stateless Entity still returns `state: null`.

Composite Entities have matching field trees under `canonical` and `display`.
Each numeric field becomes a reading object with `value` and `unit`; display
readings also carry `label`, `fraction_digits`, and a declared `range` when
available. Non-numeric values remain plain strings and booleans. Optional fields
stay absent in both trees. Forecasts preserve their period arrays and wrap each
available numeric measurement in the same way.

`POST /v1/entities/{entity_id}/state/history` aggregates once
and returns both forms of those statistics. Each bucket retains one `range`
and contains `canonical: {unit, metrics}` and
`display: {unit, label, fraction_digits, metrics}`. An unbucketed summary has
those same groups on its shared result envelope. Metric names, null gaps,
coverage, and bucket boundaries are shared; the ordinary top-level `quantity`
and bucket `metrics` are not returned. The encoded-size limit includes both
representations.

Neither representation is rounded for display. Clients use `fraction_digits`
only when formatting. Keep the complete response as one cache entry so a
refresh cannot pair a new displayed reading with an older formula input.

These two operations have one response shape and no `include` or `units`
selector. Clients select `canonical` for calculations and `display` for
presentation. Entity collections, embedded Device Entities, PATCH responses,
and MCP retain their existing representation-selection behavior.
This is a breaking detail/history contract change: deploy matching Core and web
together, and migrate external consumers before upgrading. Removed query flags
do not restore the previous response shape.

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

Collection, embedded Device, and PATCH metadata uses concrete period pointers, such as
`quantities["/periods/0/temperature"]`. Each entry describes that returned field;
there are no wildcard pointers or duplicate readings. Omitted measurements stay
absent. Timestamps, dates, conditions, source summaries, period order, and
Observation evidence are unchanged. A null forecast State has an empty quantities
map because there are no period indices yet.

For example, a US detail read returns `display.periods[0].temperature` as
`{value: 36.5, unit: "[degF]", label: "°F", fraction_digits: 0}` alongside
`canonical.periods[0].temperature` as `{value: 2.5, unit: "Cel"}` inside State.
Collection and embedded Device reads retain pointer-keyed quantities and the
`units=canonical` selector. Canonical reads preserve stored numeric tokens. SDK ingestion
and retained State use these same neutral names and canonical units. Forecast
Function results also use neutral names, but remain canonical and are not projected.

Forecast canonical temperatures use `Cel`, unlike current-condition temperature
Entities' `mCel`. Both use the shared affine engine and household policy. Forecast
temperature and precipitation-probability metadata recommend zero fraction digits
for compact card labels without rounding API values. Forecast arrays do not
become selectable scalar fields or aggregate-history sources.

## Writes are always canonical

The `units` option controls responses only. Command parameters and Automation
operands must satisfy their canonical contracts. Use the detail response's
`state.canonical`, or request `units=canonical` on collection/Device reads, before
using a State value as a write input; never copy a household-unit reading into a
canonical write. Metadata PATCH does not write State and retains its existing
single-representation response. Receipts without Entity readings
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

Weather cards use paired reads. They display household values and evaluate
condition words, Beaufort forces, and pressure tendencies against the canonical
values from the same response. The frontend does not convert units, and the
cards show no feels-like or dew-point temperatures.
See [weather cards](dashboards.md).

## PM2.5 unit migration

Migration 33 rewrites numeric sensors registered under the `pm25` Entity key
with unit `ug/m3` and no quantity kind to `µg/m³` and `particulate_matter`.
This allows re-registration despite unit immutability; other keys, including
`voc`, are untouched. Stored Observation and rollup values do not change: only
the displayed unit label changes. Down refuses because it cannot distinguish
migrated rows from sensors originally registered with the canonical unit. Back
up before upgrading and upgrade Core before the Levoit Adapter. Custom scripted
`pm25` descriptors using the old spelling must also be updated after migration.

## Deployment and rollback

This is a breaking default-read contract change. Canonical and household reads
use identical field paths, including forecasts. Deploy matching Core, Adapters,
SDK consumers, and web together, and update
external numeric consumers to use the paired detail/history trees, or returned
metadata and `units=canonical` on collection/Device reads.
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
