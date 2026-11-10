# Agent-first dashboards

Home is a conversation workspace with a live canvas. Ask Hearth for a temperature
view, open it, adjust it, then explicitly save it. Dashboards reopen independently
of their originating conversation. Development tools retain their existing URLs
under secondary navigation.

Without an open view, the conversation fills Home as a single column. Each view
Hearth presents renders live inside its reply; a card loads readings only while
it is on screen. Presenting a view does not open the canvas. **Open in canvas**
shows it beside the conversation for editing, asking Hearth to change it, and
saving; that card then stays compact so the view is not drawn twice. While a
view is open, Hearth's revisions to it update the canvas. Closing a draft with
unsaved changes asks first, except an unedited generated view without a saved
base, which remains recoverable from its conversation. Recent conversations live
in the sidebar, or in a drawer on narrow screens.

The conversation uses assistant-ui's external-store runtime while Hearth owns
HTTP/SSE and retained history. Native composer and suggestion primitives handle
draft text and sending. Replies use native tool-call parts and `GroupedParts`,
with no browser-side tool execution. Missing results remain pending or unknown
rather than becoming completed calls when history is loaded.

`ConversationsProvider` owns each browser conversation's history, active request,
and recovery state. A new conversation gets its URL and Recent entry as soon as
Core creates it. You can open another conversation, start a new one, or visit
Dashboards and Development while a reply runs. Returning shows its live activity
or completed reply. Background views remain in their conversation and do not
replace the currently displayed canvas. Unsaved-view confirmations still apply.
Closing or reloading the tab, or changing the Core connection, cancels the
browser's requests; turns are not detached server jobs.

If a delayed creation returns an ID that already has an open session, that
session remains the owner. The submitted prompt waits for its current request
to finish. A history-recovery failure preserves the waiting prompt for retry
instead of sending it against unreconciled history. If the earlier message
fails, retry or explicitly discard its prompt before sending the queued message.
Successful sessions release their cached history after the last page
unsubscribes; active work and errors remain available until completion or recovery.

Assistant text renders as Markdown (including tables and fenced code), with raw
HTML skipped. **Copy** copies the original reply text rather than tool results
or rendered markup. **Jump to latest** appears when the message viewport is
scrolled away from the bottom. These controls do not save dashboards or send
household Commands.

## Drafts and saves

`present_view` returns an immutable `hearth.view` artifact. Its successful agent
tool-result commit retains it in conversation history. Presentation alone never
saves a dashboard, including when an external MCP client calls the tool.
A rejected definition returns `invalid_definition` with a message naming the
violated rule, such as the 16-reference limit or one chart's shared type, field,
and window, so the author can correct it. Schema violations share one fixed
reason because schema diagnostics can quote submitted values; the dashboards
HTTP API keeps its fixed `invalid dashboard input` detail.

Manual edits and agent revisions change a browser draft. **Save dashboard**
creates an independent record; **Save changes** replaces the selected saved base
at its expected revision; **Save as new** creates another record. A conflict
keeps the draft and offers reload or save-as-new. Refreshing or opening a view
does not change its saved revision or execute device Commands.

The selected draft accompanies customization as user context. The browser owns
the base dashboard ID/revision; model output cannot redirect a save. Unsaved
manual edits are session state. Original generated artifacts remain recoverable
while their conversation is retained.

## Live readings and history

Definitions store canonical Entity references, never captured readings. Visible
views share a connection-scoped request cache. Current State refreshes every ten
seconds; charts refresh at UTC minute boundaries. Focus and restored connectivity
refresh immediately, hidden pages pause timers, and unused subscriptions stop.

State schemas derive selectable scalar fields, including numeric fields inside
objects. The optional binding `field` is a JSON Pointer; omission and `""` select
the root. The asserted `entity_type` must match the actual Entity. Numeric fields
use the schema's `hearth` unit annotation, or dimensionless `1` by default.
Dynamic units reference support and cannot change on re-registration. Cards never
store a display unit. Core projects readings according to the household's
`household_unit_system` (`metric` or `us`) and supplies units, labels, precision,
and declared bounds in `quantities`, or `quantity` for aggregate history. The
web displays those numbers without converting them again or waiting for a
separate household-settings read. US precipitation is shown in inches or
inches per hour. Canonical-only consumers request
`units=canonical`; raw observation audit remains canonical. Current State and
historical Observations are distinct. History uses one-minute rollups through
the Entity history query: 1h has 60 one-minute buckets, 6h has 360 one-minute
buckets, 24h has 288 five-minute buckets, and 7d has 336 thirty-minute buckets.
Ranges start on the bucket grid and end at the requested instant. The final
minute is included whole, so reports after that instant but within its minute
can count in the last bucket. Min/max and arithmetic sample means include
both applied and unchanged values. Optional fields request `nulls:"gap"`: any reported absent selected value
makes all numeric metrics null for that bucket. Empty buckets are also gaps.
Later malformed values still fail the query; missing values are never averaged
away. No seed or inferred interval fills a gap. Pruned observations are not
reconstructed, and current State is never inserted into history. Sample means
are neither time-weighted nor circular means (including hue).

One chart shares a type, field, window and resolved canonical unit. Series whose
delivered unit codes differ are not plotted together, even when
their canonical units match; the incompatible series reports an error.
Brightness State and Commands use integer percentages from 0–100; cards display the actual
normalized observations without device-specific scaling. Adapters round Commands
to native levels, and Core satisfaction allows a one-percentage-point difference.
Hue/saturation share one current Entity read but use distinct history requests.
Color temperature stays in mireds; xy values display as four-decimal fractions.
Inactive color modes do not hide their numeric values.

Yes/no and text fields have their own history: Core records each change of
those fields, and `POST /v1/entities/{entity_id}/state/changes` returns them (see
[State changes](state-changes.md)). StateTimeline rows read a `timeline` over the
card's window and derive their totals in the browser: time per value, and changes
into a value counted only from a known earlier value. WeekPattern reads a
`summary` with one-hour buckets from local midnight six days ago. Both refresh at
UTC minute boundaries like charts. A changed field's interval starts at the
change's `observed_at`; dashed spans have no earlier record. A refused range
(`query_limit`) shows "Too many readings for this window. Choose a shorter one."
on its row while other rows still render.

An absent optional field shows **No value**, not zero or **Never observed**.
Availability, enablement, missing Entities, never-observed State, and request
failure are separate conditions. An old observation alone does not imply an
unavailable Entity. Failed refreshes preserve the last reading with a visible
failure indicator and observation time.

## Ownership and extension

| Owner | Responsibility |
| --- | --- |
| `contracts/views/v1` | Generated portable JSON Schema and shared examples |
| `contracts/state-fields/v1` | Generated scalar field catalog: pointers, presence, value types and unit semantics |
| `internal/modules/dashboards` | Strict write validation, bindings, presentation and saved definitions |
| `internal/modules/devices` | Canonical State, history queries, State changes, generated field lookups and immutable interpretation |
| `internal/modules/agent` | Durable tool results, selected user context, public history/SSE projections |
| `web/src/features/views/catalog` | Composed schemas and read compatibility |
| `web/src/features/views/cards` | Numeric/text rendering and local controls |
| `web/src/features/views/bindings` | One field adapter driven by the generated catalog |
| `internal/modules/household` | Read-only household settings (`GET /v1/household`) |
| `web/src/features/views/data` | Shared cache, refresh lifecycle, and the provider that owns the Command queue |
| `web/src/features/views/controls` | Light and power controls, the connection-scoped Command queue, and pending and failure lines |
| `web/src/features/conversation` | Provider-owned conversation sessions, history/SSE projections, native message rendering, and the shared Recent list |

To add a visual form, add its schema, binding declaration, component and editor
under `cards/`, then register it in the catalog and renderer. Add a server binding
extractor if it declares reads. Run `mise run generate` and update shared examples.

To expose a scalar field, declare it in the State schema; numeric fields are
automatically eligible. Add a `hearth` annotation when the field has a unit,
then run `mise run generate`; no dashboard allowlist changes.
See [State annotations](../entitytypes/README.md) for supported schema forms.
Boolean/string fields use State cards or ReadingList rows. State accepts optional
`words:{true,false}` for booleans only. Without custom words power and binary
sensors read On/Off; `/active` flags read Active/Inactive. Color modes use readable
names; other choices retain their text. A ReadingList has a title and 1–12 rows,
each with a State source, optional label, and optional boolean words. Its footer
states the oldest displayed reading; statuses attach to individual rows.

StateTimeline has a title, a `style`, a `window` (`1h`, `6h`, `24h` or `7d`), and
1–12 rows of yes/no or text fields with optional labels and boolean words. Each
Entity field appears once. `lanes` puts every row on one time axis in its chart
series color, with the current value and totals beside it; `ledger` leads each
row with its current value, a "since" time and totals above a neutral strip;
`feed` lists the changes newest first, grouped by part of the day; in text rows a
known absence reads "No value". The "since"
time appears only when the timeline's last value matches current State. The feed
folds more than three changes of one row in one part of the day into one line
with a count and pulse strip, shows 12 entries, and reveals 12 more per
**Show earlier changes**. A card glows when a row's latest value changes on a
history refresh.

WeekPattern binds one yes/no field with an optional label and words. It shades
7 local days by 24 hours at five levels (0, 1–5, 6–15, 16–30 and over 30
minutes true) and states each day's changes into true and time true. Future
hours are outlined; hours without a known value stay empty. It glows when a
history refresh changes a shaded level or a daily total. Each one-hour bucket
counts toward the local hour it starts in: a one-hour daylight-saving shift
merges or empties one cell, and a half-hour shift (as on Lord Howe Island)
attributes the rest of that week's hours half an hour late.

Metric accepts `trend:"1h"|"6h"|"24h"|"7d"` and `range:true`. Trends reuse the
rollup-backed history query, preserve gaps, and state the sampled range. Range is
available only when the field declares bounds through its `hearth.range`
annotation (literal bounds or required numeric support references). Bounds use
the reading's display unit. A value outside them remains visible and is named
Above/Below the range; the marker pins to the track's endpoint.

Device cards bind `device:{kind:"device",device_id}` and optionally override
their title with `label`. They read the Device's live membership, skipping event
sources and unknown types. Each State-bearing Entity contributes its primary
reading, with qualifiers such as color `active` flags omitted. Numeric settings
show their value or choice according to mode. A Device with exactly one power
and one brightness Entity uses a light composite, retaining per-Entity statuses.
Other readings become rows, at most 12; truncation is stated and Source links
every readable Entity plus Device details. View writes validate Device existence.
There is a combined limit of 16 distinct Entity and Device references per view.

ToggleTile binds one `source` with `kind:"entity-state"`,
`entity_type:"hearth.power/v1"`, and its root field. It may override the label.
The entire tile except **Source** is one power control when the Entity supports
`set`; there is no separate toggle. Its On/Off reading, checked state, and faint
violet On surface reflect observed State. Pending and failure lines express
Command intent separately. Unsupported Operations leave the reading read-only;
disabled Entities retain a disabled control, and missing Entities cannot send
Commands.

For a metering plug, optional `metric:{source,window,detail?}` selects one numeric
State field and a `1h`, `6h`, `24h`, or `7d` sparkline. The displayed number and
history always use that same explicit source. `metric.detail:{source,label?}`
adds one independently bound numeric reading, such as a total-energy counter;
it is not calculated from the sparkline. Both numeric sources must belong to
the power Entity's Device, checked on presentation and save. They count toward
the view's 16-reference budget. The editor offers same-Device numeric fields
and preserves an existing selection when discovery fails. A missing or
unavailable selected source never silently switches to another metric. Each
reading keeps its own statuses; the footer states the oldest displayed reading.

ToggleList has a `title` and 1–12 ordered `rows:{source,label?}`. Every row binds
a distinct power Entity's root and derives its switch from current support.
Source choices include the Entity name for multi-endpoint Devices and an ID
when names still collide. Replacing a source resets its row label to identify
the new target; the label remains editable.
Optional `all_off:true` offers **Turn all off**. One gesture requests a separate
`set(false)` Command for each enabled, supported row not already observed Off,
including never-observed and unavailable rows. It skips disabled and read-only
rows. The group action waits while any list Command is pending; individual
controls continue to use the shared queue. Its summary counts currently
observed Off readings and names failures, without claiming atomic execution or
physical outcomes. Each failed row retains its own recovery message.

ToggleTile and ToggleList since lines use the last 24 hours of State changes,
independently of current reads. A matching interval after a known opposite value may name its start;
a seed clamped to the window boundary only establishes **On/Off throughout last
24h**, not an exact change time. Missing, failed, or nonmatching history omits
the since time without preventing power control. A first observation after unknown
coverage does not establish a switch time. No On/Off sparkline is added.

Weather cards bind named slots, each read from an Entity's root. Slots that
accept `hearth.numericsensor/v1` also require its stored unit, checked when a
view is presented or saved: `mm/h` for rain rates, `mm` for rain totals, and
`deg` for wind direction. The editor offers only matching sensors.

- OutsideNow requires an outdoor `temperature` and optionally binds `humidity`,
  `wind`, `wind_direction`, `rain_rate`, and `rain_today`. It reads 24 hours of
  temperature for the sampled high and low. If temperature history fails to refresh, a
  warning appears and retained extrema are labelled **Last loaded 24 h**, not
  the current rolling window. Source states the history interval's exact start
  and end; current readings keep their independent observation times.
- Rain requires a `rate` and optionally binds `storm`, `totals` (`day`, `week`,
  `month`, `year`), and a `trend` window for the rate. The rate leads, including
  when it is zero.
  Totals are the station's own counters; the card never sums the rate.
- Wind requires `speed` and optionally binds `gust`, `direction`, and
  `max_gust`. Direction is shown as the selected Entity's reading, not a compass.
- Pressure binds one `hearth.pressure/v1` `source` and an optional `trend`; no
  history is requested when the trend is omitted.

Weather cards do not derive feels-like, dew point, conditions, or tendencies.
Each reading keeps its own statuses; the footer states the oldest displayed
reading. Weather cards count toward the 16-reference budget like other cards.

Forecast binds one `source` Entity of type `hearth.hourlyforecast/v1` or
`hearth.dailyforecast/v1` and reads its whole State; there is no `field`. Its
optional `periods` is 6–48 for hourly (default 24) and 3–8 for daily (default
8); the JSON Schema ties the range to the source type, and Core also checks on
present and save that the Entity exists with that type. The title defaults to
**Next 24 hours** or **Next 8 days** from the count. The card shows modeled
forecasts, not station readings:

- Daily lists one row per upcoming day: the day (**Today** first), a condition
  icon, the precipitation chance, the low, a bar spanning the shown days' range,
  and the high.
- Hourly charts upcoming hours: a temperature line with gaps where a period has
  no temperature, precipitation-chance bars, and icons and temperatures every 1,
  2, 3, or 6 hours for up to 6, 12, 24, or 48 hours. Below 22px per hour the plot
  scrolls inside the card.
- Precipitation chance shows only at 10% or more. Core returns forecast values
  in household units with per-period quantity metadata. The card formats those
  values without conversion or a household-settings read; forecast temperature
  metadata recommends whole degrees. Canonical/source fields remain available
  through `units=canonical`; see [forecast read representations](units.md#forecast-snapshots).
- Periods that have ended drop at each minute without a refetch: an hour once it
  is over, a day once the viewer's local date passes it. With none left, the card
  says the last forecast has passed.
- The footer states the forecast's observation time. An unavailable or disabled
  Entity keeps its last forecast, dimmed.

Grid accepts 1–4 columns as a ceiling. Grids containing only ToggleTiles use a
160px minimum and can retain two columns on phones; other grids keep the
248px minimum and single-column phone layout. Metering ToggleTiles span the full
row on phones, with tighter spacing and the measurement opposite the power State.
Each metering card uses its content height rather than stretching to a neighbor
with an additional reading. Narrow ordinary ToggleTiles use the compact
reading scale and omit only the visual observation-label prefix; their time's
accessible label and Source retain the full evidence.

The view catalog advertises scalar field/type pairs and Device references, and rejects actions,
expressions, URLs and arbitrary properties. Definitions never declare arbitrary
Operations or Command parameters. ToggleList's `all_off` opts into native group
presentation; control eligibility still derives from live Entity support.

## Controls

A card derives its controls from the bound Entities: an Operation listed in an
Entity's current support is controllable. A light composite whose power Entity
supports `set` shows a power switch, and one whose brightness Entity supports
`set` shows a brightness slider from 1 to 100 %. Only the switch turns a light
off. A disabled Entity's control is disabled. Entity availability doesn't
disable a control, because Core still attempts a Command for an unavailable
Entity whose Adapter is healthy. Controls work wherever a view renders: saved
dashboards, the canvas, and conversation replies.

Only a gesture sends a Command: a switch press, the end of a slider drag, or
keyboard steps that have settled for half a second. Reading, rendering,
refreshing and saving never send one. Controls use the existing
`POST /v1/entities/{entity_id}/commands`, which waits for the Command outcome.

One `CommandQueue` per Core connection, beside the read cache, holds each
Entity's control state, so every card showing that Entity agrees. Each Entity
Operation has at most one Command in flight and one queued value; a newer value
replaces the queued one. While a Command is pending, the control shows the
requested value with **Turning on…** or **Setting brightness to 40 %…**, and
the reading keeps showing observed State. A satisfied Command re-reads every
State entry at once; a read already in flight is followed by one more, because
it may predate the change. The pending line clears when that read lands. Each
control reserves the row for that line, so the card doesn't move when the line
appears and clears. A
failure returns the control to observed State and names the reason under it
until the next request. After a timeout or a lost connection, the message says
the change may still apply. A changed reading glows, whatever caused the change.

Moving the brightness slider while a light is off sends only `brightness.set`.
Zigbee2MQTT and Z-Wave lights normally turn on as a result; a device that
doesn't stays Off on the card.

## Compatibility

`schema_version` identifies the envelope/graph format; `catalog_version` identifies
component and binding semantics. Additive changes preserve old definitions.
Breaking semantics require a new major version and retained old support or an
explicit migration preview. Reading never migrates or saves automatically.

Stored definitions remain raw JSON in read responses. Unsupported nodes become
placeholders; unsupported containers suppress their subtree. Unsupported majors
or invalid graphs produce a whole-view fallback. Original JSON is downloadable,
and definition edits and customization remain disabled while content is
unsupported. Explicit revision-checked deletion remains available.

## Agent integration

Eino owns iteration, parallel dispatch, native call identities, instructions,
skill loading and dangling-call repair. Hearth extends its existing trace hooks
with product projections. A view event follows the successful tool-result commit,
even if final prose later fails. Failed writes and input-only repair placeholders
cannot create view evidence. Private Responses continuation items stay private
and unchanged. See [agent skills](agent-skills.md) for provider migration.

## Persistence and deployment

Household-default Entity and history reads change the numeric representation at
existing JSON paths. Deploy Core and web together; older web clients would apply
their own conversion to the projected numbers. Request `units=canonical` for
canonical readers. This units change does not rewrite stored observations or
require a database reset. See [quantity metadata and read units](units.md) for
the Core-first Adapter rollout and rollback constraints.

The earlier State-field cutover required a fresh database and coordinated Core, SDK and
Adapter versions. Fixed units no longer appear in support JSON, and brightness
support contains neither native maximum nor step. Old support data
is unsupported; there is no compatibility decoder or migration for that historical
cutover. An operator upgrading from before that cutover must stop writers and
reset the database before restarting the new runtime (or
choose a separate fresh fixture path). Do not delete `.data/` wholesale: preserve
design assets, screenshots, recordings, verification evidence and local specs.
Recreate fixture Entities and explicitly seed saved dashboards; generated drafts
still never save themselves. A retained old backup requires its matching old
runtime/configuration, not the new runtime.

Migrations 26 and 27 add the accepted-observation time index and independent
dashboard table. They do not rewrite existing conversation or device evidence.
Rolling back migration 27 drops saved dashboards; migration 26 drops only its
index. Keep a database backup when rolling back a deployment.

The checked-in schema is embedded in Go. Regeneration needs installed frontend
dependencies and runs through the normal mise generation task. The web container
build uses the repository root context (`mise run web-docker-build`) so shared
contract examples are available to typechecking.
