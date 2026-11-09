# Weather card verification

These captures show the production React dashboard and Core on November 9, 2026,
with the worktree-local simulator plus a local preset that adds a simulated
weather station. The station has 13 Entities with Ecowitt's types and units,
including `hearth.numericsensor/v1` sensors in mm/h, mm, and deg. Sources are the
weather-card changes on `feat/views-weather-cards`, based on `e8cc97f`. The
household unit system is metric. No household Devices or model turns were used.

| Capture | What it shows |
| --- | --- |
| [Desktop](desktop.png) | 1440×1000; Outside now, Rain, Wind, and Pressure from a dashboard saved over HTTP |
| [Edited](edited.png) | Revision 2 after removing humidity in the editor: dew point disappears, wind-chill feels-like remains, changed rows glow |
| [Editor](editor.png) | Slot pickers name each Entity's Device and offer only sensors with the slot's unit |
| [Phone](phone.png) | 390×900; single column, compass beside the reading |
| [Source](source.png) | Rain's Source details list every contributing Entity |

The database is fresh, so history is sparse. Pressure therefore states that it
has no reading from three hours ago instead of guessing a tendency. The
Source capture predates a change that drops a role name when it matches the
Entity name apart from case, such as "Rain rate · Rain Rate".

Saving a definition with a rain total bound to the mm/h rain-rate sensor
returned 400 and created no dashboard. The browser HAR contains one dashboard
update and history reads only, with no Commands.
