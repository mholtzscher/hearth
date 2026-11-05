# Canonical brightness evidence

Captured from the real dashboard at runtime `f27fdc0`, using a fresh isolated
Core/simulator database. Later test and evaluation-fixture corrections do not
change the captured runtime. No physical Device or existing demo was used.

| Artifact | Observed behavior |
| --- | --- |
| [Dashboard](dashboard.png) | The hosted model generated an unsaved brightness metric and one-hour history; both display actual canonical observations of 51% |
| [Command result](command-result.png) | A request for 50% is satisfied by observed 51%, and the result retains 51 |
| [Send a 50% Command](command.mp4) | Selecting the 50% preset, sending once, and inspecting the truthful result |

The fixture holds brightness at 51 and disables parameter application. This checks
Core's one-percentage-point tolerance and browser presentation, not native-device
rounding. Separate adapter/application tests prove Z-Wave request 50 → native 50 →
observed 51 and nearest-integer Zigbee payloads. Core E2E also checks that 52 does
not satisfy a request for 50.

The model was `gpt-6.1-terra` with medium reasoning. Generation and discard left
the seeded dashboard's revision, definition, and update time unchanged. The HAR
contains 36 requests, exactly one Command submission and no dashboard writes.
All owned fixture processes and browser sessions were stopped; databases,
configuration, receipts, and recordings remain in local verification storage.

The dashboard screenshot is 1280 × 900; the Command screenshot and original
recording are 1280 × 633. The MP4 pads the height to 634 and plays at one-eighth
speed for readability. No frames were reconstructed. Screenshots and the contact
sheet were inspected, and the exported MP4 played to completion in Chromium.

[Revision 4 captures](../state-fields/README.md) remain historical evidence. That
runtime requires the removed native-scale support, so it cannot run this same
empty-support fixture as an equivalent before capture.
