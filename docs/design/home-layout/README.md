# Home layout evidence

These captures were taken on 2026-11-04 in headless Chromium, against this branch's worktree-local Hearth Core and scripted simulator. One hosted-model request generated the temperature view. The dashboard save was simulator-only. No device Commands were sent.

The previous layout appears in [the Home workspace captures](../home-workspace/after-desktop.png).

| State | Capture |
| --- | --- |
| Desktop Home with no view, 1440 × 900 | [Empty](desktop-empty.png) |
| Desktop with a generated view open | [View open](desktop-view-open.png) |
| Desktop after closing the view | [View closed](desktop-view-closed.png) |
| Closing a manually edited view | [Confirmation](desktop-close-edited.png) |
| Tablet rail with recent conversations, 820 × 1000 | [History drawer](tablet-history-drawer.png) |
| Phone conversation with a view open, 390 × 844 | [Conversation](phone-conversation.png) |
| Phone recent conversations | [History drawer](phone-history-drawer.png) |
| Phone Home at 320 × 700 | [Empty](phone-320-empty.png) |

## Interaction recording

[Play the close and history workflow](close-and-history.mp4) (1280 × 800, about 16 seconds, real time). It opens a conversation from **Recent** and opens its view. It then edits the title and closes the view, so the confirmation appears, and discards the edit. Next it reopens the unedited view and closes it without a confirmation. Finally, **New conversation** returns to the empty Home. The recording makes no model requests and sends no device Commands.

## Additional checks

- At 320 pixels, `document.documentElement.scrollWidth` equals the viewport width, so there is no horizontal page overflow.
- Closing an unedited generated view, and discarding an edited one through the confirmation, both remove the canvas and return focus to the composer.
- Opening the history drawer focuses its current or first conversation. Choosing a conversation, pressing Escape or tapping the backdrop closes the drawer, and Escape returns focus to its toggle.
- The saved-dashboard page uses the same canvas header and actions, without the Close button.

Physical touch devices and Safari were not tested. Simulator readings are not evidence of physical household behavior.
