# Weather cards with full synthetic history

These screenshots show production React and Core at `421cea1`, captured through
Browse on November 11, 2026. The history is synthetic, seeded directly into this
worktree's simulator database to inspect complete trend lines. The browser does
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

## Captures

| Capture | What it shows |
| --- | --- |
| [Metric desktop](metric-desktop.png) | 1440×1000; complete 24-hour rain and pressure curves in the two-zone layout |
| [US desktop](us-desktop.png) | The same dashboard with household-projected readings and history |
| [Intermediate desktop](metric-intermediate.png) | 1100×1000; stacked content and full-width trends |
| [Metric three columns](metric-three-columns.png) | Three-column layout at 1440×1000 |
| [Metric four columns](metric-four-columns.png) | Four-column layout at 1440×1000 |
| [Metric phone](metric-phone.png) | 390×900; the top of the single-column dashboard |
| [Phone Rain](metric-phone-rain.png) | Element capture showing the full 24-hour rain curve and totals |
| [Phone Pressure](metric-phone-pressure.png) | Element capture showing the falling curve and three-hour tendency |
| [Phone Wind](metric-phone-wind.png) | Element capture retaining the compass and reading order |
| [US phone Pressure](us-phone-pressure.png) | Element capture with the tendency and range in inches of mercury |

## Observed result

Both rain and pressure draw one continuous path across the full trend width,
with a nonempty min–max band. Wide cards use a 72px plot; narrow cards retain
the 44px plot. The curves, labels, and footers remain within their card bounds.
The pressure card now shows **Falling · −3.1 hPa in 3 h**, or
**Falling · −0.09 in Hg in 3 h** in the US household. Browse also verified that
Source describes the five-minute sample means, min–max band, and tendency method.

This fixture bypasses Adapter ingestion for historical samples. It verifies
Core's real history queries, household projection, and rendered cards, not
backdated ingestion or physical sensor behavior. No production UI code changed
for these captures. The owned Browse session and local stack were stopped;
the seeded database is retained for inspection.
