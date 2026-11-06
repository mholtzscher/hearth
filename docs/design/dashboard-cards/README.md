# Dashboard card evidence

Before screenshots use `2fe3faa`. The refreshed after screenshots and video
use the source in `6a21ba5`, with the worktree-local simulator stack and the
saved "Living room tonight" dashboard. No physical devices or hosted model
calls were used. Capture sizes are 1440×1000 and 390×844.

## Changed-value glow

[`changed-value-glow.mp4`](changed-value-glow.mp4) records the real dashboard
while simulator controls publish readings for the paused living room sensor:

1. Opening the dashboard at 21.5°C leaves its edge dark.
2. A new report of 21.5°C advances the observation time without a glow.
3. A report of 21.54°C still displays 21.5°C and leaves the edge dark.
4. A report of 21.6°C changes the displayed value and lights the orange edge.

The other healthy sources are paused during this check. Some retain glows
from changes just before they were paused. The video ends at the visible
21.6°C change; fake-time component tests cover expiry after 60 seconds and
verify that same-value reports do not extend that minute.

The raw capture is WebM; the committed MP4 is an H.264 transcode without cuts,
overlays, or redaction. Sampled frames and the final glowing frame were
inspected. The after screenshots show the settled dashboard, its charts,
and the Source disclosure. Their historical chart windows retain gaps from
earlier simulator runs.

## Validation

- `mise run validate`: passed, including 36 frontend test files / 330 tests.
- Component tests cover same-value suppression, changes below display
  precision, unit changes without newer data, and staggered chart refreshes.
- The browser check confirmed the same-value and rounding cases against
  Core's processed Observations, then confirmed the visible changed-value glow.
- No horizontal page overflow at 390px. The unchanged 44px coarse-pointer
  rule was not verified on a physical touch device.
- The browser CLI was 0.41.2; its installed reference guide was 0.39.0.
  Only supported open, wait, evaluate, screenshot, and record commands were used.
- The independent Opus review was unavailable because the provider usage
  limit was reached. The implementation owner reviewed the scoped diff.

Run-local receipts, browser measurements, and cleanup evidence remain in
`.data/verification/changed-value-glow/`; they are not published artifacts.
The owned browser session and simulator stack were stopped after capture.
