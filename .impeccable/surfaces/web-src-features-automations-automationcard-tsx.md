---
version: 1
slug: "web-src-features-automations-automationcard-tsx"
primary_target: "web/src/features/automations/AutomationCard.tsx"
related_targets: ["web/src/features/automations/AutomationEditor.tsx","web/src/features/workspace/AutomationCanvas.tsx","web/src/pages/AutomationsPage.tsx","web/src/pages/AutomationDetailPage.tsx"]
---

## Scope

Mode: Operate. Shared Automation cards, stack authoring, conversation proposals,
and their canvas/library/detail hosts. Inherit Hearth's established Afterglow
system and the user-approved Sentence/Step stack prototypes. No identity change.

## Direction contract

THESIS: Read one canonical Automation in Sentence or Step stack form, then edit
its owned paths in one builder. Avoid a panning node graph or separate definitions.

OWN-WORLD: OLED black, untinted numbered grays, Figtree, 12px card frame and 18px
inset. Violet marks selection/focus; orange is the scoped Save action. Outcomes
use words and semantic tones, never reading afterglow.

STORY: Understand when it starts, what admission allows, and each ordered path.
Inspect a referenced child without editing a copy. A proposal is not saved or run.

FIRST VIEWPORT: Card title and enablement above When/Only if, a Sentence/Steps
switch, then owned nested paths. The canvas header carries draft/revision state,
Edit, Ask Hearth, and explicit Save. Invalid fields block Save without losing text.

FORM: User-selected Sentence and Step stack from the completed three-design
prototype comparison; production is code-led. No new concept roll applies.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance

## Verification boundary

Read-only child previews follow the saved head. Real Run/Skip evidence remains
on Automation detail pages. Preserve keyboard focus through collapsed ancestors
and verify desktop, narrow canvas, and phone layouts. DESIGN.md is inherited,
not rewritten; no new shipping illustration assets are planned.
