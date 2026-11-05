# Native assistant-ui interaction evidence

Captured on 2026-11-04 (local time) using Chromium against the worktree-local
Core and scripted simulator. The working tree includes the six approved
assistant-ui improvements on top of `74443b5`. Earlier layout captures remain
in the parent directory; these captures show the new native interactions.

## Features exercised

| Feature | Evidence |
| --- | --- |
| Native composer and starter | [Empty Home](empty.png), then [generated view](generated-view.png) after one starter click |
| Native grouped tool calls | [Expanded tool list](tools-expanded.png) |
| Markdown and copy feedback | [Rendered retained reply](markdown-copy.png), [generated reply copied](generated-copy.png) |
| Jump to latest | [Button while scrolled up](jump-to-latest.png); clicking returned to the bottom and removed the button |
| Autosizing composer | [Six-line phone draft](phone-composer.png), [320px view](phone-320.png) |
| Existing invalid-edit guard | [Send and save blocked](invalid-edits.png); discarding kept the prompt |

[Play the interaction recording](native-interactions.mp4) (1280 × 800, about
12 seconds, slowed to half speed for legibility). It scrolls up, jumps to the
latest reply, copies it, expands a multiline draft, starts a new conversation,
opens a retained view and expands its native tool group. No frames were
reconstructed or redacted.

The empty Home capture was refreshed after removing the subtitle that repeated
the composer placeholder; the recording predates that change.

## Observed outcomes

- The live starter produced conversation
  `aconv_01a1a9fe4b7978c58fbf45fd902a0662` and a temperature view. Its retained
  calls were `list_entities`, `list_devices` and `present_view`.
- The browser HAR records exactly one conversation-creation POST and one
  message-stream POST, both HTTP 200. Other requests were GET/OPTIONS.
  This incurred one hosted-model turn. No device Commands or dashboard saves
  were sent; edits used only the browser draft.
- Expanded native tool rows measured 28px with a mouse. The pending-count and
  open-state behavior during streaming is covered by the component tests.
- Copy showed **Copied**. The clipboard test asserts the original Markdown
  text is copied, excluding the tool result in the same reply.
- A six-line composer grew to 144px. At 320px, document `scrollWidth` and
  viewport width both measured 320px.
- Clearing the view title with real keyboard input made native Send and the
  existing save/customization actions disabled. Discarding retained the prompt.
- Component tests cover Markdown lists, tables, fenced code and links, skipped
  raw HTML, IME/Shift+Enter versus ordinary Enter, new-conversation draft reset,
  prompt recovery, context attachment and retained unknown tool outcomes.

Physical touch hardware and Safari were not tested. Device-preset viewport
emulation alone is not evidence of touch input. Simulator values are not
evidence of physical household behavior.

Raw evidence is retained in `/tmp/opencode/hearth-assistant-final/` (HAR and
retained history), and validation is in
`/tmp/opencode/hearth-assistant-final-validate.log`.
Final `mise run validate` passed with 277 web tests, frontend typecheck/build,
lint, vet and all Go test packages, including cached results. The mechanical
design detector returned no findings. The follow-up independent Opus review
was unavailable because its provider reached its usage limit; the implementation
owner completed direct code, prose and comment review instead.

## Implementation ownership

- `pages/AgentPage.tsx`: Hearth's HTTP/SSE lifecycle and external-store adapter;
  native composer, suggestions, viewport and scroll-to-bottom.
- `features/conversation/Messages.tsx`: native grouped parts, Markdown and copy,
  with Hearth-specific view/context cards and compact tool-row styling.
- `features/conversation/runtime.ts`: retained batch/result matching and
  display-only merging into one reply per user turn. Core remains the sole
  tool-execution owner; browser-side tool execution is not enabled.

`@assistant-ui/react-markdown` 0.14.24 and `remark-gfm` 4.0.1 are the only new
direct dependencies. Installed assistant-ui remains 0.15.32; deprecated
ToolGroup/ChainOfThought slots are not used.
