# Cover card verification

These captures show the production React dashboard and Core on November 13,
2026, from `feat/views-cover-card`, based on `a6cb875` with the feature changes
uncommitted. They remain applicable to the feature commit that adds this file.
The worktree-local scripted garage door supplied reported State and Command
outcomes. No physical Adapter, household Device, or model turn was used.

| Capture | Scenario |
| --- | --- |
| [Desktop](desktop.png) | 1440px; diagram card and two tiles bound to the same cover, reporting 40% open and idle |
| [Travel](travel.png) | 1440px; reported Opening at 40%, requested Open at 100%, shared pending feedback and afterglow |
| [Phone](phone.png) | 390px at 2x; card above two compact tiles |
| [Phone Source](phone-source.png) | The existing Source bottom sheet with position and Stop limitations |
| [Controls video](controls.mp4) | 45 seconds at 1440×1000: confirm Open, report travel, send Stop from the tile, then report idle |

The video retains real timing, including the warning while the cover still
reports motion after Stop. Simulator inputs supply the position and
motion reports; the scripted cover does not implement a physical motor.

## Observed results

- The first Open press sent nothing. Confirm sent exactly one
  `set {"position":100}`. The reading stayed Closed until an Observation reported
  `{"position":40,"motion":"opening"}`.
- The tile's Stop sent exactly one `stop {}` and Core returned `dispatched`.
  The UI said **Stop sent.** without changing the reading. A later simulated
  idle report changed the reading to Partly open.
- The earlier position Command eventually reached `outcome_timeout` in Core.
  The card hid that outcome after Stop, as intended.
- HAR capture for the video contains exactly two Command submissions: the set
  and the Stop. The Stop response's Command ID matches Core's retained record.
- Desktop tile heights remained 226px through confirmation, pending, travel,
  and Stop feedback. Both phone tiles remained equal height.
- At 320px and 390px there was no horizontal overflow. Open, Stop, Close, and
  Confirm fit after responsive layout settled. Reduced motion removed the
  panel transition.
- The editor discovered the cover and its Device name. Toggling **Ask before
  opening** sent no Command.

The new Cover component has no previous production control card to compare
against. The discarded prototypes are design references, not baseline captures.
Failure, read-only, unknown State, repeated Stop, source-change confirmation,
and queue ordering are covered by component and queue tests rather than
physical-device testing.

The run followed the `verify-hearth` launch and Command-audit guidance using
Browse instead of the unavailable `agent-browser` CLI. Raw receipts, HAR,
Observation IDs, and logs are retained locally under
`.data/verification/cover-20261114T013000Z/`.
