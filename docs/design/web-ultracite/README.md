# Ultracite migration verification

Captured on November 15, 2026, in headless Chromium at 700×600 using Storybook's
synthetic fixtures. The baseline wheel source comes from `4630a36bf1ef5d1d723a9ab2a6b4584d122e66cf`.
The after captures show this PR's migrated components.

| Capture | What it shows |
| --- | --- |
| [Wheel before](wheel-before.png) | Original custom slider, hue 0 and saturation 50 |
| [Wheel after](wheel-after.png) | Native range overlay with the same artwork and starting value |
| [Wheel focus](wheel-focus.png) | Visible keyboard focus after dragging to hue 90, saturation 80, then pressing ArrowUp |
| [Wheel interaction](wheel-interaction.mp4) | Trusted mouse drag and release, followed by keyboard focus and ArrowUp |
| [Forecast](forecast-after.png) | The hourly chart retains its labels and artwork as a named figure |
| [Status notice](status-after.png) | The semantic status output retains the notice styling |

The interaction clip is trimmed from the recording to exclude page-loading frames.
The release commits hue 90 and saturation 80. ArrowUp changes saturation to 81;
the committed-value readout still describes the last pointer release. The focus
outline measures 2 px.

The baseline source was mounted in a disposable Storybook story with the same
starting value and CSS. The disposable capture files are not shipped. These
captures demonstrate browser component behavior, not physical-device effects.
