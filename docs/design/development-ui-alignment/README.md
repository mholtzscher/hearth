# Development UI alignment verification

Captured on November 15, 2026, in system Chromium using the production components
and Storybook's synthetic household fixtures. Before images use
`a934319277b0f9cd0b6a7e98c4e198de31a96f93`. After images and the interaction clip
show this change. These checks do not establish physical-Device effects.

| Capture | Scenario and viewport |
| --- | --- |
| [Desktop before](development-desktop-before.png) / [after](development-desktop-after.png) | Development Devices, 1440×1000 |
| [Phone before](development-phone-before.png) / [after](development-phone-after.png) | Development Devices, 390×844 |
| [Detail before](detail-desktop-before.png) / [after](detail-desktop-after.png) | Entity metadata, Commands, availability, and State history, 1440px wide |
| [Product reference](product-unchanged.png) | Saved household dashboard, 1440×1000; its appearance is retained |
| [Phone interaction](phone-navigation.mp4) | Page switching, Entity jump, Connection settings, keyboard scrolling, and direct return from detail to its list |

The clip is trimmed to remove page loading. Brief pauses allow the viewer to read
each screen. It shows the actual browser interaction, not a reconstructed UI.
The State-history scroll area is 350px wide with 701px of content. ArrowRight
changes its horizontal scroll position while keyboard focus remains visible.

Desktop, rail/tablet, and phone layouts were inspected at widths of 1440, 981,
768, 390, and 320px. The 390px Development toolbar shrinks from 185px to 61px.
At 320px, navigation and diagnostic controls wrap into two rows rather than
truncating the page selector or widening the document.

## Styling reduction

Twelve debug page headers now use `PageHeader`, which shares its heading style
with the product canvas and dashboard library. Fifteen local table-header hover
overrides disappear. `Section` no longer repeats uppercase styling or a separator;
`Facts` uses a definition list instead of overriding table and row styling.
Compact controls, raw JSON, debugging actions, and history lifecycles remain.

A conservative utility matcher over production TS/TSX string literals and
template spans counted 4176 recognized utility references before and 4055 after:
**121 fewer references**, including the new shared definitions. Recognized unique
candidates fell from 1100 to 1097. Stories and tests are excluded. This measures
styling repetition, not generated CSS size or total source lines.

## Checks

- Original code: 1051 frontend unit tests and selected Storybook interaction
  checks passed before refactoring.
- Final `mise run validate`: passed, including 1052 frontend tests, 294 Chromium
  stories, six Storybook lifecycle tests, backend checks, both web builds, and
  Storybook API/snippet validation.
- Storybook MCP broad run and focused final navigation checks passed without
  accessibility violations.
- `mise run web-fix`, `mise run web-check`, and `git diff --check`: passed.
- Impeccable detector: no findings in the final changed targets.
- Independent review: the detail-to-list selector issue and a missing narrow
  headline token were fixed and reviewed again.
