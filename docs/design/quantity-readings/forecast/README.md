# Forecast conversion verification

These screenshots show real saved Forecast cards against production Core and
worktree-local simulated Devices. Both runs use US household units and the same
checked-in `simulated-forecast` periods: six hours and three days in January 2099.
The distant dates keep the fixed fixture upcoming. No provider or model call is
needed.

| Viewport | Main before | Backend projection after |
| --- | --- | --- |
| 1440 × 1000 | [Desktop](before-desktop.png) | [Desktop](after-desktop.png) |
| 390 × 844 | [Mobile](before-mobile.png) | [Mobile](after-mobile.png) |

Before is main `ac8aadf`. After is `0408fad` plus the forecast projector, vocabulary,
and conversion changes committed with these captures. Desktop images crop the
leftmost 216 pixels of navigation from both versions to exclude an unrelated
conversation title. Mobile images are unmodified. The original captures remain
in the local verification directories.

The card retains compact degree-only cells. The legend and accessible summaries
retain the backend's full unit label. The first hourly source value, `2.5 Cel`,
now returns `36.5 [degF]` and formats as `37°`; the legacy frontend conversion
displayed `36°` at that rounding boundary. API values are not rounded.

The live after check establishes:

- Hourly default reads return `temperature: 36.5` with Fahrenheit metadata;
  canonical reads return `temperature_c: 2.5` with Celsius metadata.
- Daily default reads return `temperature_min: 30.92`; canonical reads retain
  `temperature_min_c: -0.6`.
- HTTP and MCP return the same forecast values and quantity metadata. Raw audit
  matches the canonical snapshot by exact Observation ID.
- The production automated journey checks both types across a US-to-metric
  restart, including an independent retained `21.5` numeric-token assertion.
- Frontend tests cover supplied values and precision, omitted measurements,
  metadata failures, expired-period filtering, zero precipitation, temperatures
  around 100°F, compact labels, and current-weather coexistence.

Capture uses Chromium headless with `--window-size`,
`--virtual-time-budget=10000`, `--screenshot`, and `--dump-dom`. The installed
agent-browser guide remains unavailable, so this is static real-browser evidence,
not an interactive or HAR-based journey. The isolated baseline stack is stopped.
The primary US preview remains running at the user's request.
