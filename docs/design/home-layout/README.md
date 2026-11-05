# Home layout evidence

For the subsequent migration to native composer, suggestions, Markdown, copy,
scroll-to-bottom and grouped tool parts, see [assistant-ui evidence](assistant-ui/README.md).

These captures were refreshed on 2026-11-04 (local time) in headless Chromium,
against the worktree-local Core and scripted simulator. They show the removed
top bar and composer notice, sidebar readiness, compact view cards and collapsed
tool activity. The narrow captures include Recent below main navigation.
Desktop and empty-state captures, the phone conversation and the recording were
refreshed again for the final icon change: no decorative mark above the empty
heading, and the sidebar's house icon beside assistant replies.

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
Validation after the house-icon change is
`/tmp/opencode/hearth-house-icon-validate.log`.

## Delivery checkpoint

### Approved inline views (canvas on demand)

The user chose to render generated views live in their replies. Nothing opens
the canvas automatically; **Open in canvas** opens it for editing, asking Hearth
to change the view, and saving. A view already open in the canvas stays compact
in the chat. Asking Hearth to change an open view still updates the canvas.
Implemented and browser-checked; evidence is in `assistant-ui/README.md`.
Current step: full validation, publish to PR #286, then verify CI.

### Approved assistant-ui migration

The user selected the six near-term improvements: native composer and starter
primitives, jump-to-latest, copy response, Markdown, and native tool grouping.
Appearance, context attachment, save/revision guards, invalid-editor gating,
prompt recovery and retained unknown tool outcomes must remain intact. No
backend changes, cancellation, regeneration, branching, voice, attachments or
conversation-list migration are included.

Playbook: mstack Feature → PR workflows (Publish and Inspect checks).
Implementation owner: main session, reusing `feat/web-home-layout` and PR #286.
Worktree: `/home/michael/code/worktrees/hearth/feat-web-home-layout`.
The preceding head `74443b5` passed backend and web CI. Implementation is complete:
composer and starters use native primitives; assistant replies render Markdown
and provide Copy; native GroupedParts wraps tool calls; Jump to latest shares
the viewport's scroll state. Focused tests pass, including prompt recovery,
context, invalid-edit gating, IME, clipboard/Markdown and unknown tool outcomes.
Browser checks confirm copy feedback, jump visibility and return to bottom,
six-line textarea autosizing to 144px, and no 390px horizontal overflow.
The live starter completed one temperature turn with read-only discovery and
presentation tools, and fresh evidence is in `assistant-ui/`. Full validation
passed with 277 web tests, frontend typecheck/build, lint, vet and all Go test
packages (including cached results). The log is
`/tmp/opencode/hearth-assistant-final-validate.log`. Direct code, prose and
comment review is complete; no unresolved findings or unrelated generated
changes remain. The lockfile additions accompany the two new dependencies.

- [x] All six features work through their browser entry points.
- [x] Streaming/recovery and clipboard/Markdown tests pass.
- [x] Refreshed evidence and final `mise run validate` pass.
- [ ] Publish implementation and media to PR #286.
- [ ] Verify backend and web CI for the new published head.

Current step: commit and push, update the PR evidence, then verify final-head CI.
Stop before merging. Leave the simulator and tailnet dashboard running.

The installed `@assistant-ui/react` is 0.15.32. Its deprecated ToolGroup slot
will not be introduced; the migration uses `MessagePrimitive.GroupedParts`.
Markdown 0.14.24 declares compatibility with React 19 and assistant-ui 0.15.

### Previous layout delivery

- Playbook: mstack Feature → Bug fix (Recent order) → Finish delivery;
  PR #286, not approved for merge.
- Approved scope: adaptive Home workspace, sidebar history, compact view cards,
  shared canvas actions, immediate starters. Follow-up approvals remove redundant
  copy, the composer notice and top bar, move readiness to the sidebar, and
  collapse each reply's tools into one disclosure with compact expanded rows.
  The user also approved keeping Recent after main navigation at every width.
  The latest approval removes the empty-state asterisk and uses the existing
  sidebar house icon for assistant replies.
- Worktree: `/home/michael/code/worktrees/hearth/feat-web-home-layout`.
  Branch: `feat/web-home-layout`; reviewed implementation head: `aebfcac`.
- Implemented: all approved behaviors; visual evidence refreshed and inspected.
  Final `mise run validate` passed: 273 web tests, frontend build, lint, vet and
  all Go test packages (including cached results). No generated or module changes.
- Current step: publish the house-icon change and refreshed affected captures,
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
