# Home workspace refinement evidence

Captured on 2026-11-05 with the real worktree-local Hearth application and scripted simulator. Before captures use revision `717a245`; after captures correspond to the accompanying UI changes. No model requests or device Commands were sent.

## Comparable captures

| State | Before | After |
| --- | --- | --- |
| Desktop Home, 1440 × 1000 | [Before](before-desktop.png) | [After](after-desktop.png) |
| Phone Home, 390 × 844 | [Before](before-phone.png) | [After](after-phone.png) |
| Phone empty canvas, 390 × 844 | Not included | [After](after-phone-canvas.png) |

The captures show the preserved palette and panel layout, revised text hierarchy, labeled phone navigation, server readiness, and empty-canvas guidance. Connection setup is available through Development rather than the Home header.

## Interaction recording

[Play the phone workflow](focus-flow.mp4).

The recording switches to Canvas, returns to the composer through **Ask for a view**, fills the temperature starter without sending it, and visits Development before returning Home. Playback is slowed 15× to make the captured transitions readable. No frame content was reconstructed or redacted.

## Additional verification

- Inspected layouts at 320, 390, 820, and 1440 pixels. The 320-pixel page fits without horizontal document overflow.
- Checked expanded-canvas Escape and focus return, starter focus, and the unsaved-changes guard.
- Created one simulator-only dashboard fixture through HTTP. The browser sent exactly one revision-checked dashboard update, advancing revision 1 to 2. History-query POSTs were read-only; no model or device action appeared in the browser HAR.
- Checked unavailable and not-ready status presentation with browser-intercepted readiness responses. Those presentations do not establish a real Core failure. Normal readiness used the real local Core.
- Axe reported zero violations on desktop Home, phone Conversation, and phone Canvas. Canvas gradient backgrounds left contrast checks incomplete; this is not a claim of complete accessibility conformance.
- Focused component tests cover pending readiness checks, failure/recovery, backend changes, starter focus, disabled loading actions, operator-control placement, and discarding an expanded unsaved view.

The browser was headless Chromium. Physical touch devices, Safari, and hosted-model generation were not tested. Simulator readings are not evidence of physical household behavior.
