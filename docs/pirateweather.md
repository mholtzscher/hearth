# Pirate Weather Adapter

`hearth-adapter-pirateweather` publishes modeled current conditions and hourly and
daily forecasts for one configured location, and exposes fresh forecasts on demand
as Adapter Functions. It runs separately from Core and uses the public Hearth
Adapter SDK.

Pirate Weather is a modeled weather source, not your Ecowitt station. Keep its
Device and Entities separate from station measurements. Forecast Functions never
publish Entity State; the polled forecast Entities do.

## Configure and run

1. Subscribe to the forecast API at [Pirate Weather](https://pirateweather.net/).
   A new subscription may take up to 20 minutes to activate.
2. Store the API key in a private regular file readable by the companion. Do not
   put it in YAML, shell arguments, source control, or Function arguments.
3. Copy `configs/pirateweather.example.yaml` to a private configuration file.
   Replace the example coordinates and `api_key_file` path. Set `nats_url` to your
   trusted Hearth NATS endpoint and choose a unique stable `adapter_id`.
4. Start the companion while Core is running:

```sh
go run ./cmd/hearth-adapter-pirateweather --config /path/to/pirateweather.yaml --log-format json
```

The release image is `ghcr.io/mholtzscher/hearth/hearth-adapter-pirateweather`.
Mount configuration and the key file read-only. The companion needs outbound
HTTPS to `api.pirateweather.net` and access to the configured NATS server. It has
no database, local cache, inbound HTTP server, or Home Assistant dependency.

Configuration fields are:

| Field | Requirement |
| --- | --- |
| `adapter_id` | Stable subject-safe slug; one active process per ID |
| `nats_url` | NATS URL; CLI default is `nats://127.0.0.1:4222` |
| `api_key_file` | Regular file, at most 4096 bytes, nonempty single-line key after trimming |
| `location.name` | Trimmed display name, 1–128 characters |
| `location.latitude` | Required finite number between -90 and 90; zero is valid |
| `location.longitude` | Required finite number between -180 and 180; zero is valid |
| `poll_interval` | Go duration, default `15m`, range `15m`–`24h` |

Restart to apply configuration or credential changes. The CLI also accepts
`--adapter-id`, `--nats-url`, `--log-level`, and `--log-format`. Environment sources
use `HEARTH_ADAPTER_PIRATEWEATHER_` followed by `CONFIG`, `ADAPTER_ID`, `NATS_URL`,
`LOG_LEVEL`, or `LOG_FORMAT`. There is no API-key environment variable or CLI option.

The stable Binding key is `location`. Renaming the location or correcting its
coordinates preserves canonical IDs and Entity enablement. For a different
meaningful location, use a different Adapter ID so histories do not mix.

## Current-condition Entities

One virtual Device of kind `sensor` contains these read-only Entities:

| Entity key | Canonical unit |
| --- | --- |
| `temperature`, `apparent-temperature` | Integer milli-Celsius |
| `relative-humidity` | Percent |
| `pressure` | hPa, sea-level pressure |
| `wind-speed`, `wind-gust` | m/s |
| `wind-direction` | Degrees |
| `precipitation-rate` | mm/h, liquid-water equivalent, not snow depth |
| `precipitation-probability`, `cloud-cover` | Percent |
| `uv-index` | Index |

The companion requests SI data; household presentation converts canonical values
for display. Existing numeric cards, State history, and automations can use the
Entities. There are no Commands.

## Forecast Entities

The same Device also contains two read-only forecast Entities:

| Entity key | Entity type | State |
| --- | --- | --- |
| `hourly-forecast` | `hearth.hourlyforecast/v1` | Up to 48 hourly periods, starting with the provider's current hour |
| `daily-forecast` | `hearth.dailyforecast/v1` | Up to 8 daily periods, starting with today at the location |

Each canonical State is one forecast snapshot, `{"periods": [...]}`, and the newest
snapshot replaces the previous one. Period fields match the Function results
below, with two differences: the provider `icon` becomes the Hearth `condition`
(`clear-day`, `clear-night`, `partly-cloudy-day`, `partly-cloudy-night`,
`cloudy`, `rain`, `snow`, `sleet`, `thunderstorm`, `wind`, or `fog`; any other
icon omits it), and State has no `icon`, `precipitation_type`, location name,
retrieval time, or timezone. An empty provider summary is omitted. The Forecast
dashboard card reads these Entities; see [Dashboards](dashboards.md).

Ordinary Entity reads project forecast measurements into household units.
Both read modes use the same unit-neutral period fields, such as `temperature`;
per-period `quantities` metadata describes the returned numbers. Request
`units=canonical` for values in the contract's canonical units. Ingestion,
retained State, raw audit, and Function results remain canonical. See
[forecast read representations](units.md#forecast-snapshots).

Forecast Observations carry the response receive time and no source time: the
provider gives no forecast issue time, and `currently.time` describes current
conditions. A missing or invalid `hourly` or `daily` block publishes nothing for
that Entity and reports it unavailable with `forecast_unavailable`; its last
forecast remains, and current conditions and Adapter health are unaffected.

Forecast Entities have no scalar State fields, so they have no numeric history
charts, State change history, or automation triggers.

Every forecast Observation is retained under Core's ordinary Observation history
policy. A synthetic full forecast with every field present encodes to about
14.2 KB (48 hours) plus 2.5 KB (8 days) per poll, about 1.6 MB per day at the
15-minute default, or about 48 MB over a 30-day retention window. Real forecasts
that omit fields are smaller.

## Polling

The first poll runs immediately after registration. Later polls wait one interval
after the preceding poll completes, with no overlapping polls or catch-up work.
Each poll is one provider request for the `currently`, `hourly`, and `daily`
blocks.
Equal readings still produce new Observation evidence. Missing, null, sentinel,
wrong-type, or out-of-range values do not become zero; their Entities become
unavailable while Core retains last State. Valid sibling values still publish.

Provider `currently.time` is the conditions-valid time, not the model ingestion
time. It appears in `source_updated_at` with that limitation. The companion rejects
a current response more than two poll intervals old or more than one minute in
the future. Successful publication acknowledges JetStream storage, not physical
truth or synchronous Core acceptance.

## Query hourly and daily forecasts

Discover the Functions through the existing HTTP or MCP interface. The agent can
use `list_adapter_functions`, `get_adapter_function`, and `invoke_adapter_function`.

| Function | Required arguments | Result |
| --- | --- | --- |
| `get_hourly_forecast` | `{"hours":24}`, integer 1–48 | Up to that many hourly periods, starting with the provider's current hour |
| `get_daily_forecast` | `{"days":8}`, integer 1–8 | Up to that many daily periods, starting with today |

Every successful invocation uses a fresh API response, not a local forecast cache.
Shorter provider horizons remain shorter. Results contain `location_name`,
`retrieved_at`, an IANA `timezone`, and `periods`. Period timestamps are UTC;
daily `date` is local to the returned timezone. Unit-neutral measurement fields
include `temperature` (degrees Celsius), `humidity` (percent, 0–100), and
`wind_speed` (m/s). The published output schema documents each canonical unit;
Function results do not use household projection. Optional missing
measurements and text are omitted. A malformed or empty requested block fails instead
of returning an empty successful forecast.

Daily `temperature_min` and `temperature_max` describe midnight-to-midnight
extremes, not the daytime high and overnight low. Daily precipitation probability is the
provider's maximum; today's value may concern only the remaining hours. A
precipitation rate is liquid-water equivalent, not accumulated depth. Function
results keep the provider's summary and icon text unchanged.

```sh
CORE=http://127.0.0.1:8080
ADAPTER=pirateweather-home
FUNCTION=get_hourly_forecast
curl -fsS "$CORE/v1/adapters/$ADAPTER/functions"
REVISION=$(curl -fsS "$CORE/v1/adapters/$ADAPTER/functions/$FUNCTION" | jq -er .catalog_revision)
jq -n --arg revision "$REVISION" \
  '{catalog_revision:$revision,arguments:{hours:24}}' |
  curl --fail-with-body -X POST \
    "$CORE/v1/adapters/$ADAPTER/functions/$FUNCTION/invocations" \
    -H 'Content-Type: application/json' --data-binary @-
```

Inspect the returned `status` and retained invocation ID, not just HTTP 200. Core
retains successful results under its ordinary Function-history policy. Rediscover
the catalog after an Adapter restart. See [Adapter Functions](adapter-functions.md).

## Quota and failure recovery

A 15-minute interval uses approximately 96 scheduled requests per day, plus
forecast invocations and startup polls. Your subscription quota must cover both.
The provider recommends no faster than 15 minutes. This adapter has no automatic
forecast retry or response cache; repeated agent questions make additional calls.

Each HTTP attempt has a 10-second deadline and a 1 MiB response limit. The
Function deadline is 15 seconds and the encoded result limit is 64 KiB. Oversized
or invalid results fail safely rather than returning partial JSON. Requests use
the `apikey` header, not a secret in the URL. Redirects are refused.

HTTP 429 starts a shared cooldown for polls and Functions. It honors `Retry-After`
and, when remaining quota is explicitly zero, `Ratelimit-Reset` seconds until
reset, choosing the later valid time. Without usable bounded timing evidence,
cooldown lasts one poll interval. Requests already in progress may finish.

| Evidence | Meaning and action |
| --- | --- |
| `provider_authentication` | Check subscription activation and the secret file; restart after key changes |
| `provider_throttled` | Wait for cooldown/quota reset; do not repeatedly invoke forecasts |
| `provider_unavailable` | Network, timeout, or provider refusal; later polls/calls can recover |
| `invalid_response` | Wrong units, malformed/oversized data, or unusable requested block |
| `measurement_unavailable` | A current measurement is missing or invalid; last State remains readable |
| `forecast_unavailable` | The poll's hourly or daily block is missing or invalid; the last forecast remains readable |

Polls alone establish Adapter health and Entity availability. A failed forecast
Function does not rewrite the previous current-condition evidence; the next poll reassesses
upstream health. Recovery reports fresh availability after healthy evidence is
acknowledged. A disabled Entity stays disabled across restart; the companion does
not gate publication locally or change user enablement.

Shutdown cancels outstanding provider reads, joins Functions and polling, then
releases the Session. No pending forecast or poll is replayed on restart.

## Verification

Unit tests cover conversions, partial data, forecast publication, bounded polling,
and provider errors. Synthetic HTTP integration tests cover transport behavior. The
production-Core journey checks retained current and forecast State, Function
discovery and results, shutdown, stable identity, and preserved enablement through
public boundaries:

```sh
GO_PACKAGES='./internal/adapters/pirateweather ./internal/app/pirateweather ./cmd/hearth-adapter-pirateweather' mise run test -- -tags=integration,e2e
```

Synthetic tests do not establish live Pirate Weather compatibility. For a live
smoke test, supply an authorized key file and location, start the companion, read
its Device and thirteen Entities through `/v1/devices` and `/v1/entities`, including
both forecast States after the first poll, invoke both Functions,
and inspect `/v1/adapter-function-invocations/<id>` for the retained result. Confirm
units, timestamps, and availability before relying on it for household automations.
