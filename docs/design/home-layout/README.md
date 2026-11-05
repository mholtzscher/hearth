# Home layout evidence

These captures were refreshed on 2026-11-04 (local time) in headless Chromium,
against the worktree-local Core and scripted simulator. They show implementation
`aebfcac`, including the removed top bar and composer notice, sidebar readiness,
compact view cards and collapsed tool activity. The tablet and phone captures
were refreshed again after moving the Recent toggle below main navigation;
desktop captures and the desktop recording remain applicable.

The temperature view came from a retained conversation. Refreshing the evidence
made no model requests, saved no dashboards and sent no device Commands. Its
browser HAR contains no requests other than GET and OPTIONS.

The previous layout appears in [the Home workspace captures](../home-workspace/after-desktop.png).

| State | Capture |
| --- | --- |
| Desktop Home with no view, 1440 × 1000 | [Empty](desktop-empty.png) |
| Desktop with a generated view open | [View open](desktop-view-open.png) |
| Desktop conversation, tools collapsed and no view open | [View closed](desktop-view-closed.png) |
| Desktop conversation, tools expanded | [Tool list](desktop-tools-expanded.png) |
| Closing a manually edited view | [Confirmation](desktop-close-edited.png) |
| Tablet rail with recent conversations, 820 × 1000 | [History drawer](tablet-history-drawer.png) |
| Phone conversation with a view open, 390 × 844 | [Conversation](phone-conversation.png) |
| Phone canvas | [Canvas](phone-canvas.png) |
| Phone recent conversations | [History drawer](phone-history-drawer.png) |
| Phone Home at 320 × 700 | [Empty](phone-320-empty.png) |

## Interaction recording

[Play the tool-list and close workflow](close-and-history.mp4) (1280 × 800,
about 15 seconds, slowed to half speed for legibility). It opens a retained
conversation, expands and collapses its tool list, and opens its view. It then
edits the title and closes the view, so the confirmation appears, and discards
the edit. Next it reopens the unedited view and closes it without a confirmation.
Finally, **New conversation** returns to the empty Home. No frames were
reconstructed or redacted.

## Additional checks

- At 320 pixels, `document.documentElement.scrollWidth` equals the viewport width, so there is no horizontal page overflow.
- Closing an unedited generated view, and discarding an edited one through the confirmation, both remove the canvas and return focus to the composer.
- Opening the history drawer focuses its current or first conversation. Choosing a conversation, pressing Escape or tapping the backdrop closes the drawer, and Escape returns focus to its toggle.
- The saved-dashboard page uses the same canvas header and actions, without the Close button.
- Mouse input: one collapsed tool summary is 32px high; expanded rows are 28px
  high without inter-row gaps. Chromium touch emulation reports
  `(pointer: coarse)` and gives both summaries and tool rows 44px targets.
- A component test streams new calls and results into an expanded disclosure;
  it stays open while its pending count updates and retains individual results.
- Recent remains after Home, Dashboards and Development when the sidebar
  collapses. At 820px, Recent previously started at y=124 and Home at y=206;
  after the fix Home starts at y=124 and Recent at y=346. At 390px, the Recent
  button follows the navigation horizontally. A DOM-order regression assertion
  failed before moving the toggle and passes afterward.

Physical touch devices and Safari were not tested. Simulator readings are not
evidence of physical household behavior. The installed browser CLI is 0.41.1;
its bundled guide was missing, so the installed 0.39.0 guide supplied the workflow
and current CLI help confirmed the commands. The verification skill's feature map
does not yet include Home conversations, so these are scoped browser checks,
not a claim that its mapped device-control recipes passed.

Raw final verification evidence is retained at
`/tmp/opencode/hearth-home-final/` (HAR, runtime inventory and source provenance).
The initial final validation log is `/tmp/opencode/hearth-validate-final.log`;
validation after the Recent-order fix is `/tmp/opencode/hearth-recent-validate.log`.

## Delivery checkpoint

- Playbook: mstack Feature → Bug fix (Recent order) → Finish delivery;
  PR #286, not approved for merge.
- Approved scope: adaptive Home workspace, sidebar history, compact view cards,
  shared canvas actions, immediate starters. Follow-up approvals remove redundant
  copy, the composer notice and top bar, move readiness to the sidebar, and
  collapse each reply's tools into one disclosure with compact expanded rows.
  The user also approved keeping Recent after main navigation at every width.
- Worktree: `/home/michael/code/worktrees/hearth/feat-web-home-layout`.
  Branch: `feat/web-home-layout`; reviewed implementation head: `aebfcac`.
- Implemented: all approved behaviors; visual evidence refreshed and inspected.
  Final `mise run validate` passed: 273 web tests, frontend build, lint, vet and
  all Go test packages (including cached results). No generated or module changes.
- Current step: publish the Recent-order fix and refreshed narrow captures,
  then verify CI against that exact head. Both backend and web CI passed on
  `aebfcac`; that result does not establish the latest head's checks.
- Review limitation: the initial independent Opus review completed; the attempted
  follow-up review could not run because its provider reached its usage limit.
  The implementation owner completed the follow-up review directly, correcting
  one comment's chronological-order claim. No further code findings were found.
- Remaining completion checks: current visual evidence published; latest PR
  head's backend and web CI pass.
- Leave the requested simulator and tailnet dashboard running at
  `https://nixos-desktop.tailea9b59.ts.net:7923/`.
