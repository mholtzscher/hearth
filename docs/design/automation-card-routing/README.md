# Saved Automation card routing

## What changed

The agent previously answered “Use card to show gentle hallway” with a
`present_view` Heading + Note and claimed Automation cards were unavailable.
The tool existed; the model chose the dashboard-view tool instead.

The system prompt now routes saved Automation cards to `present_automation`
reference mode. The tool description includes the invocation shape, and the
Automation skill exposes a separate show-saved recipe before authoring.

## Capture provenance

- Date: 2026-11-07 UTC. Viewport: 1440 × 1000.
- Browser: Chromium 156 through agent-browser 0.41.2, using the installed 0.39.0
  guide. No shared tooling was upgraded.
- Before: `63ea9ae`, the reported conversation in the retained local simulator.
- After: `f88e5a5f7c3cfe0e00cf8386146a60046915b25d`. Captured from the matching
  uncommitted source before committing it, in a separate fresh simulator worktree.
- The after fixture has the same name and definition: disabled Gentle hallway,
  no Triggers, one 10 ms Delay. IDs differ between the databases.
- No physical Adapter or household data was used. Screenshots were not redacted.

| File | Observed state |
|---|---|
| `before.png` | Static dashboard-view Note and the false “no card type” explanation |
| `after.png` | Saved-reference Automation card in the conversation |
| `canvas.png` | The same reference opened in the canvas, clean at revision 1 |
| `open-card.webm` | Actual browser frames showing the card and opened canvas |

The generation recording failed because its encoder fell behind capture; no
second model turn was sent. The published video records later read-only card
inspection. Its two captured frames were retimed from 2 to 4 seconds for readability;
it does not establish generation latency or every intermediate interaction.
The original recording and contact sheet remain local. Published screenshots
and the video were inspected, and the video played through to the end.

## Verification and limits

- Before-fix regression checks failed for missing system-prompt routing and the
  missing reference invocation in the actual model-facing tool export.
- Afterward, focused Agent and Automation MCP tests passed uncached.
  Full `mise run validate` passed, including 448 frontend tests, Go tests at every
  default level, lint, vet, generation, formatting, module tidying, and build.
- Exactly one fresh live conversation used the original wording, with no tool
  names or IDs in the prompt. The configured `deepseek-v4.1-flash` model discovered
  the saved Automation and called `present_automation` with `mode: "reference"`.
  One `hearth.automation` artifact matched the discovered ID and rendered in the UI.
- A disabled test Automation was created as setup. The model turn made no
  Automation write or Run: the saved definition, revision, and history matched
  their before-turn values. Browser HAR confirmed one conversation and one
  message submission, with no automatic retry.
- This single live turn does not guarantee every future model choice. It used a
  fresh conversation rather than replaying the earlier misleading assistant text.
  Deterministic tests protect published guidance, not probabilistic selection.
- The isolated verification stack was stopped and its storage retained. The
  existing user simulator stayed available throughout verification.
