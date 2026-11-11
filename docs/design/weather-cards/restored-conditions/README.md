# Restored weather conditions

These captures show the production React dashboard and Core on November 11,
2026, against the worktree-local simulator. A local copy of the scripted preset
adds six rain sensors to the weather station: a `precipitation_rate` sensor in
mm/h and five `precipitation_depth` counters in mm. The station's Entities are
paused, then receive these canonical readings through the simulator
publication API:

- Temperature: `9400 mCel`.
- Humidity: `94 %`.
- Wind and gust: `6.3 m/s` and `11.2 m/s`.
- Wind direction: `135 deg`.
- Rain rate: `6.8 mm/h`.
- Storm, day, week, month, and year totals: `18.6`, `16.2`, `22.9`, `71.4`, and `688.1 mm`.
- Pressure: `1003.2 hPa`.

Core applied each publication by its exact Observation ID. The dashboard was
saved over HTTP. The US run restarts Core with `household_unit_system: us`
against the same database and republishes the same readings.

| Capture | What it shows |
| --- | --- |
| [Metric desktop](metric-desktop.png) | 1440×1000; Outside, Rain, Wind, and Pressure |
| [US desktop](us-desktop.png) | The same dashboard in a US household |
| [Metric phone](metric-phone.png) | 390×900; Outside and Rain in one column |
| [Metric phone Wind](metric-phone-wind.png) | The Wind card at phone width |

Both households show the same words because they come from the canonical
readings. A US card shows **Moderate breeze** for 14.1 mi/h. Applying the
Beaufort thresholds to that mi/h value would give **Near gale**. In the US
household, Rain words a 0.27 in/h rate as **Rain now**. Applied to 0.27, the
mm/h thresholds would give **Light rain**.

The database is fresh, so Pressure has no reading from three hours earlier and
states **Tendency unavailable**. The simulator cannot backdate Observations.
Component tests cover the worded tendency in both unit systems.

The browser HAR contains history queries only; three are `units=canonical`.
It has no Commands or dashboard writes.
