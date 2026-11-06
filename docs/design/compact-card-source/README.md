# Compact card recency and Source

The household chose the compact footer after comparing three interactive
prototypes and inspecting the mobile Source sheet. Cards retain their reading,
status, and afterglow; the footer shows relative age and a Source action.

## Capture conditions

- Before: `d0dfafaf99d1a0cde2cead54afdd578cc2d29483`.
- After: the implementation in this change, captured in its worktree before
  committing. The final grid correction changes only narrow-grid behavior;
  the earlier full-width and phone captures remain representative.
- Real Core and worktree-local scripted simulator; no physical Devices or model
  turns. Dashboards were created through the public HTTP API using simulator
  Entity IDs. Reading values and timestamps differ naturally between captures.
- Desktop 1440 × 1000; phone 390 × 844; long Device source list 320 × 740;
  conversation/canvas split 1024 × 900. These are not pixel-parity comparisons.

## Screenshots

| Capture | What it establishes |
| --- | --- |
| [Before desktop](before-desktop.png) | The old exact-time-plus-age footer and Source disclosure. |
| [After desktop](after-desktop.png) | Compact age and Source share a footer row. |
| [Desktop Source](after-desktop-source.png) | Dated observation evidence, availability, enablement, and Entity link in an anchored popover. Opening it left all card bounds unchanged. |
| [Phone cards](after-phone.png) | Compact footer and 44px Source target. |
| [Phone Source](after-phone-source.png) | Modal bottom sheet with local date/time, age, and Entity facts. |
| [Phone chart Source](after-phone-chart-source.png) | History interval and aggregation method remain accessible. |
| [Phone Device Source](after-phone-device-source.png) | Eight links, wrapped IDs, and scrolling inside a viewport-bounded sheet. |
| [Phone changes guard](after-phone-source-guard.png) | Source closes before the unsaved-changes dialog; Stay retains the edit and Discard allows Entity navigation. |
| [Narrow split view](after-split-grid.png) | Requested three-column grid reduces to one readable column beside the conversation. |

[Watch or download the mobile Source recording](source-overlays.mp4): an
8.23-second native browser recording at 390 × 844, opening and closing reading
and chart details. Cursor movements and brief holds make the states readable.
No content was reconstructed or redacted. Playback reached the end without a
media error.

## Verification

- `mise run validate` passed: generation, formatting, module tidying, lint,
  every default Go test level, vet, frontend tests (39 files, 371 tests), and
  frontend build. Some Go results were cached; local tests were not race-enabled.
- Focused regression checks failed before implementation for the intended
  compact-age and missing-overlay behavior. Source tests cover exact dated
  evidence, oldest-reading labels, age advancement, invalid evidence,
  Source-only cards, phone sheet semantics, and guarded navigation in both modes.
- Browser checks: unchanged card bounds on desktop Source opening, phone focus
  containment and Escape/Close focus restoration, actual Source-to-Entity
  navigation, usable changes guard, long source-list scrolling, and no page
  horizontal overflow at the exercised sizes.
- The narrow split-view regression initially reproduced with 157px cards; after
  the fix, cards were 496px and their age text no longer overflowed its box.
- Axe WCAG 2 A/AA checks on the phone and desktop details: zero violations or
  incomplete checks. Impeccable detector: no findings.
- Independent code/comment review found the narrow-grid issue, which was fixed
  and re-reviewed. Calendar timestamps intentionally display through seconds
  with timezone; the full original precision remains in `time.datetime`.
- The owned browser and all four worktree simulator daemons were stopped.
  Unrelated simulator stacks and the disposable prototype playground were left
  untouched.

Detailed HTTP traces, worktree diffs, and failed diagnostic recordings remain
under `.data/verification/compact-source`, outside tracked source. The initial
recording with automatic contact-sheet analysis failed; the published recording
was captured again without that analyzer and verified by playback.
