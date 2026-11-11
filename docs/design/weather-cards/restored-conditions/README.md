# Weather cards with full synthetic history

These screenshots show production React and Core after the approved Wind-history
follow-up to `b44ab8c`, captured through Browse on November 11, 2026. Rain,
Pressure, and disabled Rain/Pressure close-ups retain their earlier `b44ab8c`
captures because those treatments are unchanged. The history is synthetic,
seeded directly into this worktree's simulator database to inspect complete trend lines. The browser does
not mock API responses, and no household Devices or model turns were used.

## Fixture

With the local stack stopped, its SQLite database was backed up before replacing
only the temperature, rain-rate, and pressure Entities' history. Each received
26 hours of canonical samples, three per minute: 4,680 Observations and 1,560
minute rollups per Entity. The rollups use exact rational sums and matching
sample counts, minima, and maxima. Small additional samples fill the restart
interval; the simulator repeats current values every 30 seconds thereafter.
Database integrity and foreign-key checks passed.

The series include a daily temperature cycle, several showers separated by dry
periods, and falling pressure. Rain samples vary around each minute's mean so
the min–max band is visible. Core's history API confirmed all 288 five-minute
buckets populated for each 24-hour series, and all 360 one-minute buckets for
the pressure tendency's six-hour history. Both household and canonical pressure
history reads had complete coverage.

The simulator supplies these current readings:

- Temperature: `9400 mCel`; humidity: `94 %`.
- Wind: `6.3 m/s`; gusts: `11.2 m/s`; direction: `135 deg`.
- Rain rate: `6.8 mm/h`.
- Storm, day, week, month, and year counters: `18.6`, `16.2`, `22.9`, `71.4`, and `688.1 mm`.
- Pressure: `1003.2 hPa`.

Counters are independent fixture readings, not integrals of the synthetic rain
rate. The saved dashboard has the same bindings in both unit systems. Core was
restarted with `household_unit_system: us` for the US captures.

The seed script, backup, API responses, and original Browse captures remain
locally under `.data/verification/weather-full-history/`. The database and
backup are not checked into Git.

The Wind follow-up backed up the stopped database again, then added 26 hours
of sustained-speed history to the existing wind Entity: 4,680 Observations and
1,560 matching minute rollups. The synthetic speed varies around the current
6.3 m/s. Core returned all 288 five-minute buckets, with a range of
3.489–9.1584 m/s in metric and 7.8047–20.4868 mi/h in US units.
Its backup, seed, and API evidence remain under
`.data/verification/weather-wind-history/`. No browser responses were mocked.

## Captures

| Capture | What it shows |
| --- | --- |
| [Metric desktop](metric-desktop.png) | 1440×1000; complete 24-hour rain, wind-speed, and pressure curves in the two-zone layout |
| [US desktop](us-desktop.png) | The same dashboard with household-projected readings and history |
| [Intermediate desktop](metric-intermediate.png) | 1100×1000; stacked content and full-width trends |
| [Metric three columns](metric-three-columns.png) | Three-column layout at 1440×1000 |
| [Metric four columns](metric-four-columns.png) | Four-column layout at 1440×1000 |
| [Metric phone](metric-phone.png) | 390×1000; the top of the single-column dashboard |
| [Phone Rain](metric-phone-rain.png) | Element capture showing the full 24-hour rain curve and totals |
| [Phone Pressure](metric-phone-pressure.png) | Element capture showing the falling curve and three-hour tendency |
| [Phone Wind](metric-phone-wind.png) | Current readings above the full-width sustained-speed trend |
| [US phone Wind](us-phone-wind.png) | The same stacked card, with speed and history projected to mi/h by Core |
| [US phone Pressure](us-phone-pressure.png) | Element capture with the tendency and range in inches of mercury |
| [Disabled trends](metric-disabled.png) | Real disabled fixture Entities retain dashed-gray lines and omit the bands |
| [Disabled Wind](wind-disabled.png) | Disabling the real wind-speed Entity dims the reading and gives its trend a dashed-gray line without a band |
| [Wind variants](wind-variants-boundary.png) | Optional max gust, speed-only, no-compass, and no-trend cards at exactly 460px content width, with a 100.0 mi/h reading |

## Observed result

Rain, Wind, and Pressure draw one continuous path across the full trend width,
with a nonempty min–max band. Wide cards use a 72px plot; narrow cards retain
the 44px plot. The curves, labels, and footers remain within their card bounds.
Pressure remains Falling; its exact three-hour change varies with capture time
as the steady-current fixture fills the recent interval. Browse also verified that
Source describes the five-minute sample means, min–max band, and tendency method.

Live Rain lines blend dusk blue (`--chart-4`) equally with `--gray-9`; Pressure
uses the same blend with pale gold (`--chart-5`). The bands use those chart
colors at 8% opacity. Computed line contrast against the card surface is about
11:1 for Rain and 13.2:1 for Pressure. Readings, labels, and surfaces remain
neutral. The final captures use the real stylesheet after removing the
temporary browser preview.

Wind adds an optional 1 h, 6 h, 24 h, or 7 d trend of sustained speed, not gusts
or direction. The editor saved 24 h on the local dashboard, and a Core read
confirmed the saved definition. At 1440px, its 577px-wide card is 220px tall
instead of the previous 215px. An 88px compass and current readings fill the
left zone; a neutral 72px trend and its range fill the right. Narrow cards stack
the trend below the readings. Source states the sample-mean and min–max method.
The disabled Wind trend recovered after re-enabling the Entity.

The 100.0 mi/h check used a temporary simulator publication matched by its
Observation ID in Core. Wind then returned to 6.3 m/s, with its ticker resumed.
That one high-speed fixture Observation was removed from the stopped local
database and its rollup rebuilt before the normal final captures.

The disabled-state check temporarily disabled the local rain-rate and pressure
Entities through Core's metadata API. Both trends rendered gray with 4px/3px
dashes and no band. Both Entities were re-enabled and confirmed before the
live screenshots were captured. This changes neither household data nor the
production status rules.

This fixture bypasses Adapter ingestion for historical samples. It verifies
Core's real history queries, household projection, and rendered cards, not
backdated ingestion or physical sensor behavior. These captures use the real
Wind component and stylesheet, not the earlier illustrative DOM preview.
Phone row images are lossless crops of Browse screenshots. The owned Browse
session and local stack were stopped; the seeded database is retained for inspection.
