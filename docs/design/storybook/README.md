# Storybook browser evidence

These captures show the production React components inside the new Storybook,
using synthetic, story-local HTTP fixtures. They do not prove Core behavior or
physical-device compatibility. There is no baseline capture because the base
revision (`a6cb875`) has no Storybook.

Captured from the uncommitted feature snapshot on 2026-11-13. The depicted
stories, CSS, and fixtures are unchanged in the delivered revision. Later
changes only repair generated prop documentation and exclude the mock worker
from the production build.

| File | Capture conditions and observed result |
| --- | --- |
| `desktop.png` | Morning Dashboard in the Storybook manager, 1440×1100. Production theme, controls, readings, and history render without Core. |
| `phone-dashboard.png` | Phone Dashboard, 390px wide, full-page capture. Document width and scroll width both measure 390px. |
| `phone-color.png` | Device card at 390×844 with the real light-color dialog open after setting red. The synthetic observed swatch and hex value agree. |
| `light-control.mp4` | 26-second browser recording at 390×844: open the picker, enter `#ff0000`, submit, and observe the red swatch. The recording ends during the close-button interaction. |

The original WebM was played through its end and converted to H.264 MP4 without
cuts, overlays, or redactions. Screenshots were inspected at their native size.
The phone story test separately verifies that closing the dialog restores focus
to **Adjust color**.

Run `mise run storybook`, then open:

- `http://localhost:6006/?path=/story/features-views-renderer-viewrenderer--morning-dashboard`
- `http://localhost:6006/?path=/story/features-views-renderer-viewrenderer--phone-dashboard`
- `http://localhost:6006/?path=/story/features-views-cards-device--phone-color-sheet`
