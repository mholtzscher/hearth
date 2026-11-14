---
version: 1
slug: "cover"
primary_target: "web/src/features/views/cards/cover/Cover.tsx"
related_targets:
  - "web/src/features/views/cards/cover/CoverTile.tsx"
  - "web/src/index.css"
---

## Approved scope

Operate-mode extension of Hearth's Afterglow world. The user chose the diagram card and quick tile, then approved implementation and one PR (not merge). Confirm opening by a second press within four seconds; `confirm_open` defaults true for every cover because garage-door classification is unavailable. Open and Close stay enabled at the ends. Stop sends immediately and hides earlier position outcomes. Readings remain observed, with afterglow on changes. Tile body rows stay fixed height.

## Direction contract

THESIS: Keep reported position, a requested target, and dispatched Stop visibly distinct.

OWN-WORLD: Inherit DESIGN.md: neutral black/gray cards, Figtree reading scales, violet selection/focus, orange primary confirmation and afterglow.

STORY: Read position and motion, open/close/stop, confirm opening deliberately, and recover from failure without treating a request as a physical result.

FIRST VIEWPORT: In a real dashboard, the diagram sits beside the State word and percentage, above equal Open/Stop/Close controls. Compact tile grids retain reading, position, action and status rows.

FORM: Local extension of the established card frame and ToggleTile, inheriting Afterglow seed:d43f0bf5d342cc25. The signature interaction is observed travel toward a dashed requested target.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance

## Implementation checkpoint

- Playbook: `/home/michael/.agents/skills/mstack/playbooks/feature/PLAYBOOK.md`.
- Worktree `/home/michael/code/worktrees/hearth/feat-views-cover-card`, branch `feat/views-cover-card`, base `a6cb875`.
- Prototype and user selections remain in `/home/michael/code/hearth/docs/design/cover-card/`; unrelated main-checkout edits remain untouched.
- [x] Cover schema, generated contract, Core binding validation, shared examples and agent/present_view guidance.
- [x] Diagram card, fixed-row tile, editor, queue replacement/forget and boosted refresh.
- [x] DESIGN.md and dashboard documentation.
- [x] Focused Core tests; frontend 740 tests passed and TypeScript build passed.
- [x] Independent code/comment review and cleanup findings resolved.
- [x] Full `mise run validate` (747 frontend tests; all default checks passed).
- [x] Live simulator/browser verification and screenshots; video and exact Command audit.
- [x] Independent visual finish review: ship for the two scored fixes (endpoint target visibility and instant orange Confirm).
- [ ] Publish one PR and confirm required CI green.
- Evidence: `.data/verification/cover-20261114T013000Z/`; captures and video in `docs/design/cover-controls/`; review set in `.impeccable/review/cover-production/`. Detector returned zero findings.
- Code review `ses_e2853ae94ffeemf2jKkMsrNmuy` resolved six control issues; unchanged-reading afterglow finding withdrawn as contrary to DESIGN.md.
- Visual reviewer `ses_e2846b64effep3p4y5MEgaOiEy` scored both visual fixes resolved. Full review preceded that verdict.
- Current step: final validation passed again after visual fixes (747 frontend tests); scoped code, prose and comment cleanup complete. Owned stack and temporary video server stopped, browser reset, evidence retained.
- Principles: [Model the domain](/home/michael/.agents/skills/mstack/principles/model-the-domain.md) led to an explicit dispatched queue state, independent of State refresh. [Experience first](/home/michael/.agents/skills/mstack/principles/experience-first.md) kept tile feedback rows fixed and confirmation immediate.
- Next action: commit/push and open one PR; verify required CI.
