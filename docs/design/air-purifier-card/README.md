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
2026, from `feat/views-air-purifier-card`. The initial desktop capture is from
pre-rebase `05bd3df` (now `8feef17`); the other captures and video are from
pre-rebase `c042469` (now `00a3e41`). Rebase preserved the purifier behavior;
the later editor typing and comment cleanup does not change the demonstration.
The worktree-local simulator's
`simulated-air-purifier` Device supplied all seven Entities. A dashboard created
over `POST /v1/dashboards` held an AirPurifier card beside a PM2.5 TimeSeries,
and a Grid with an AirPurifier tile and the simulated light's ToggleTile. No
Levoit account, household Devices, or model turns were used.

| Capture | What it shows |
| --- | --- |
| [Desktop](desktop.png) | 1440×1000; the card in Auto with the stored fan level, and the tile beside a ToggleTile |
| [After controls](desktop-after-controls.png) | 1440×1000; Off, Sleep selected, **Level 3 kept for Manual**, Display off, and Child lock on |
| [Phone card](phone-card.png) | 390×844; the card in one column |
| [Phone tiles](phone-tiles.png) | 390×844; the tile and ToggleTile keep two compact columns |
| [Control sequence](controls.mp4) | 17 seconds at 1440×1000; Display off, Child lock on, Sleep, fan +, and tile power off |

The initial run produced four `satisfied` Commands (`sleep`, `"3"`, `false`,
then `true` from the card's switch). The recorded run produced five more,
one per press, and Core's State matched every requested value afterward. Its
browser network capture contains exactly five Command submissions, all satisfied.
Binding filter life as PM2.5 was rejected with `400`. The page had no horizontal
overflow at either width and no console errors. The browser was driven through
the browse tool rather than agent-browser, which isn't installed here. The video
was played through successfully and its frames inspected. No before capture is
included because AirPurifier is a new component; the generic Device card is unchanged.
