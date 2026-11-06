# PR #300 review fixes

Captured from source revision `66d9aaf` on 2026-11-06 at 1440×1000 using this worktree's real application,
Core, and scripted simulator. No physical devices or hosted model calls were
used. The recordings accompany the Source-link and active-glow review fixes.

- [Source navigation](source-navigation-final.mp4): invalid dashboard text
  opens the existing Stay/Discard/Save guard when a Source link is clicked.
  Stay keeps the dashboard and invalid input; Discard then opens the Entity
  page. Playback is slowed to one-quarter speed for readability.
- [Glow eligibility](disabled-glow.mp4): a 21.6°C observation starts the
  orange edge. Disabling that Entity removes the edge when the browser
  receives the update. Re-enabling it with the same reading does not replay
  the glow. The clip runs at its captured speed; polling can take up to ten
  seconds to show each Core update.
- [Guard screenshot](source-guard.png) and
  [disabled reading without a glow](disabled-no-glow.png) show the two states.

The MP4s are H.264 transcodes of browser WebM captures. Neither recording is
cut, cropped, overlaid, or redacted; only the Source recording's playback
speed changes. Decoded frame sequences and final frames were inspected.

The new regression tests fail on the pre-fix code. The complete validation
passes with 37 frontend test files / 335 tests, and the markup detector is
clean. Tests also cover an unavailable transition, preserving the last-seen
reading during recovery, and preventing a cancelled timer from ending a
later glow early.

Raw receipts and measurements remain under
`.data/verification/pr300-feedback/`. An initial browser attempt did not
put the controlled field into an invalid state; keyboard clearing did. A
second measurement initially read the dialog before its dismissal committed;
an explicit wait verified the settled state. Those attempts are retained
locally and are not the published recordings. The simulated Entity was
re-enabled, and the owned browser session and simulator stack were stopped.
