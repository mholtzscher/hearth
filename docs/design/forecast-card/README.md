# Forecast card verification

These captures show the production React dashboard and Core on November 9, 2026,
from `feat/pirateweather-forecast-entities` at `12b7e6d` plus the chart-label
spacing change committed with this README. The worktree-local simulator's
`simulated-forecast` Device supplied both forecast Entities. Its checked-in
periods start in 2099, so the run paused both Entities and published synthetic
forecasts through the simulator's publish control: 48 hourly periods from the
current hour and 8 daily periods from today, in America/Chicago. The household unit
system is metric. No Pirate Weather requests, household Devices, or model turns
were used.

| Capture | What it shows |
| --- | --- |
| [Desktop](desktop.png) | 1440×1000; daily, 24-hour, and 48-hour Forecast cards from a dashboard created over HTTP |
| [Disabled](disabled.png) | The hourly Entity disabled through `PATCH /v1/entities/{id}`: both hourly cards keep the last forecast, dimmed, with a **Disabled** status |
| [Phone, daily](phone-daily.png) | 390×844; the daily list in one column |
| [Phone, hourly](phone-hourly.png) | 390×844; the 24-hour chart scrolled inside its card, 22px per hour |
| [Entities summary](entities-summary.png) | The Entities table shows forecast summaries instead of raw State JSON |

Core validated the dashboard on create, so the Forecast bindings passed both the
view schema and the Entity-type check. The page did not overflow horizontally at
either width. The browser HAR contains reads only: 26 reads of the two forecast
Entities and no mutations. Publishes, the dashboard, and the enablement change
were made with `curl`.

The first desktop capture showed the highest hourly temperature label touching
its condition icon. The chart now starts its temperature line lower; these
captures include that change.

[prototype.html](prototype.html) is the approved design prototype, with synthetic
data and review toggles for the hourly style, horizon, precipitation threshold,
icon tint, units, card state, and width. The user chose the hourly chart, a 10%
threshold, tinted icons, and a configurable count.
