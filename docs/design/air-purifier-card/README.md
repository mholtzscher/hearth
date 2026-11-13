# Air purifier card prototype

[prototype.html](prototype.html) is a disposable design prototype for the Levoit
`air-purifier` Device. It is not wired to Core. It uses synthetic data for two
purifiers and simulates each Command: about one second pending, followed by the
optimistic Observation that the Adapter publishes after VeSync acknowledges the
change.

It compares three directions:

| Direction | Lead | Controls |
| --- | --- | --- |
| A · Controls first | Power On/Off at the reading scale | Power switch, Mode, Fan level, Display, Child lock |
| B · Air first | PM2.5 with its EPA band, range, and trend | Same set, beside the reading once the card is wide |
| C · Quick tile | Power ToggleTile with PM2.5 and category | Whole-card power press only |

Review toggles cover the card state (ready, Auto, Off, pending, rejected, no
reply, rolled back after a poll, fan level unknown, filter low, poor air,
offline, loading), the Mode and fan control style, the air-category treatment,
the trend window, and the width. The state and options can be set with the URL
hash, for example `prototype.html#preset=offline&style=menu&width=phone`.

## Chosen (November 13, 2026)

- **A · Controls first** for dashboards and **C · Quick tile** for grids of quick switches. B was not chosen.
- **Air category:** words plus tone. Good has a green dot, Moderate and Unhealthy for sensitive groups use warn, and Unhealthy or worse uses error. The category sits beside PM2.5, not in the Status slot.
- **Fan level:** a stepper, with Mode as segmented buttons. In Auto and Sleep, the stored manual level is drawn hollow. Stepping sets that level and switches to Manual.

- **Status line:** each card reserves one shared line, and the message names its control. DESIGN.md will allow this for cards with several controls.
- **VeSync rollback note:** deferred. Cards follow State. Explaining a poll that reverts an accepted change needs cards to read the stored Command result, which is out of scope for now.

The prototype now opens with these defaults.

## Verification

These captures show the production React dashboard and Core on November 13,
2026, from `feat/views-air-purifier-card`. The worktree-local simulator's
`simulated-air-purifier` Device supplied all seven Entities. A dashboard created
over `POST /v1/dashboards` held an AirPurifier card beside a PM2.5 TimeSeries,
and a Grid with an AirPurifier tile and the simulated light's ToggleTile. No
Levoit account, household Devices, or model turns were used.

| Capture | What it shows |
| --- | --- |
| [Desktop](desktop.png) | 1440×1000; the card in Auto with the stored fan level, and the tile beside a ToggleTile |
| [After controls](desktop-after-controls.png) | After pressing Sleep, fan +, and the tile's power: Off, Sleep selected, **Level 3 kept for Manual** |
| [Phone card](phone-card.png) | 390×844; the card in one column |
| [Phone tiles](phone-tiles.png) | 390×844; the tile and ToggleTile keep two compact columns |

Each press produced one `satisfied` Command (`sleep`, `"3"`, `false`, then
`true` from the card's switch), and Core's State matched afterward. Binding
filter life as PM2.5 was rejected with `400`. The page had no horizontal
overflow at either width and no console errors. The browser was driven through
the browse tool rather than agent-browser, which isn't installed here.
