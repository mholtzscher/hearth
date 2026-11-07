# Replacement proposal regression

These captures show the real browser and Core using the retained, worktree-local
simulator database. They contain no household data. No physical Adapter or new
model turn was used.

## Capture provenance

- Date: 2026-11-07 UTC. Chromium 156 through agent-browser 0.41.2, using the
  already-installed 0.39.0 CLI guide. No shared tools were changed.
- Viewport: 1440 × 1000.
- Before source: `492a2c08bdcec1e0bd9f43032feb6d8075838852`.
- After source: `364912f21ebb2975f8ba4f33de7b0fcc431232dc`. Captured before
  committing the same source and test changes; the capture patch is retained locally.
- Entry point: the historical Evening pause replacement proposal, opened with no
  existing canvas draft. Edit Name to Temporary, then restore Evening pause revised.

| File | Observed result |
|---|---|
| `before-reverted.png` | Incorrect Saved label and disabled Save changes; closing also bypassed the warning |
| `after-reverted.png` | Unsaved changes label and enabled Save changes after edit/revert |
| `after-navigation-warning.png` | Navigation asks whether to keep the unsaved proposal |
| `edit-revert.webm` | Open the proposal, edit/revert, then exercise close and navigation warnings |

The video contains actual browser frames. The native recording encoded the
sequence into 0.9 seconds; playback is slowed eightfold to 7.166 seconds for
readability. No frames were reconstructed or redacted. The original recording
and contact sheet remain local. Both recordings were played through to the end.

## Verification

- Component regression failed before the fix. Afterward, all 18 focused tests
  passed, covering proposal edit/revert, Save/reload/discard resets, and ordinary
  saved-reference reversion.
- Full `mise run validate` passed: 448 frontend tests across 50 files, frontend
  typecheck/build, generation, formatting, module tidying, Go tests, lint, and vet.
  Go test results were cached. Existing bundle and crypto-externalization warnings
  remain unchanged.
- Browser edit/revert kept Save available and prompted on both close and navigation.
  Discard reloaded the saved definition; edit/revert then returned to Saved.
- Explicit Save as new created one disabled copy. Its authoritative Core GET
  matched the submitted definition at revision 1, and edit/revert returned to Saved.
  The retained proposal targets an older revision, so replacement Save itself is
  covered by the component test rather than repeated against that stale base.
- The complete browser HAR contains exactly one mutation: that successful
  Automation POST. No Run or model request was submitted. Raw HTTP evidence
  remains outside Git.

All owned browsers and the four simulator daemons were stopped. SQLite,
JetStream, and proof files remain in the worktree.
