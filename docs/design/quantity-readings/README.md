# Quantity read presentation evidence

Real saved dashboards captured against production Core with worktree-local
simulated Devices. These are screenshots of the application, not mockups.

| Viewport | Before | After |
| --- | --- | --- |
| 1440 × 1080 | [Desktop](before-desktop.png) | [Desktop](after-desktop.png) |
| 390 × 844 | [Mobile](before-mobile.png) | [Mobile](after-mobile.png) |

Before uses `710db18`. After uses `13145c3` plus the regenerated OutsideNow
descriptions and OpenAPI snapshot update. Subsequent documentation and evidence
commits do not change the rendered behavior.

Both runs use metric household settings and the same dashboard bindings. The
weather-station simulator Entities are paused, then receive these canonical
readings through the simulator publication API:

- Temperature: `25000 mCel`.
- Humidity: `50 %`.
- Wind and gust: `3.4 m/s` and `5 m/s`.
- Pressure: `1013.2 hPa`.

Core audit history matches each exact publication Observation ID. The dashboard
shows `25.0 °C`, `50.0 %`, `3.4 m/s`, and `1013.2 hPa` in both versions. After
removes frontend-derived dew point, weather classifications, and pressure
tendency. Retained high/low history and actual Entity readings remain visible.

The after run also verifies default, collection, Device, canonical, aggregate
history, and MCP reads. Default temperature is `25` with `Cel` metadata;
canonical HTTP and MCP return `25000` with `mCel` metadata. Raw audit remains
`25000`. HTTP and MCP aggregate metrics and quantity metadata agree. The
automated production journey separately checks a US-to-metric restart with a
classified generic sensor and unchanged canonical access.

Capture uses Chromium headless with `--window-size`, `--virtual-time-budget=10000`,
`--screenshot`, and `--dump-dom` against the saved dashboard URL. The installed
agent-browser CLI lacks its skill guide and the paired browser relay is
unreachable, so Chromium is the available fallback. No navigation, interaction,
HAR, hosted-model, or physical-device journey is claimed. Screenshots are
unmodified and contain only synthetic fixture data. Both owned stacks are stopped.
