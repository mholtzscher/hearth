# Afterglow color system evidence

Captured on 2026-11-05 with the worktree-local Hearth application and scripted simulator. Before captures use `main` at `6ecb306` against the same simulator Core; after captures correspond to the Afterglow change. Both render the same generated view from one conversation.

## Comparable captures

| State | Before | After |
| --- | --- | --- |
| Desktop Home with canvas, 1440 × 900 | [Before](before-home.png) | [After](after-home.png) |
| Development Commands, 1440 × 900 | [Before](before-commands.png) | [After](after-commands.png) |
| Phone canvas, 390 × 844 | [Before](before-phone.png) | [After](after-phone.png) |

The captures show Home and Development sharing one true-black token set, violet for Hearth's voice and the unsaved state, orange for the single primary action, the freshness line on the live reading only, relative observation ages, and the chart's orange end-of-window edge. Before, Development used a separate neutral theme and links in blue.

## Additional verification

- Generating the view used one hosted-model turn against the simulator. No device Commands or dashboard saves were sent.
- Measured contrast for key pairs: secondary text on black 8.63:1, links 10.39:1, text on orange 8.03:1, status colors at least 7.06:1 on black.
- The Impeccable design detector reported no findings. An independent design review returned **ship** after its fixes were scored resolved.
- Unit tests cover the freshness thresholds, the relative-age wording, and showing the freshness key only for views with readings.

The browser was headless Chromium. Physical touch devices and Safari were not tested. Simulator readings are not evidence of physical household behavior.
