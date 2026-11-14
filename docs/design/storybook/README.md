# Storybook browser evidence

These captures show the production React components inside the new Storybook,
using synthetic, story-local HTTP fixtures. They do not prove Core behavior or
physical-device compatibility. There is no baseline capture because the base
revision (`a6cb875`) has no Storybook.

The phone captures and recording come from the uncommitted feature snapshot on
2026-11-13. The stories, CSS, and fixtures they show are unchanged in the
delivered revision. Later changes repair generated prop documentation and
controls, add explicit card titles, and exclude the mock worker from the
production build. `desktop.png` was recaptured on 2026-11-14 to show the
current controls.

| File | Capture conditions and observed result |
| --- | --- |
| `desktop.png` | Morning Dashboard in the Storybook manager, 1440×1100, default args. Production theme, readings, and history render without Core. `definition` is a dropdown of valid examples. `active` is a boolean control. |
| `cover.png` | Cover Closed story, 1100×700, captured after synchronizing with main's Cover catalog addition. The read-only synthetic Entity reports Closed and 0 % open. No baseline Storybook story existed for this component. |
| `phone-dashboard.png` | Phone Dashboard, 390px wide, full-page capture. Document width and scroll width both measure 390px. |
| `phone-color.png` | Device card at 390×844 with the real light-color dialog open after setting red. The synthetic observed swatch and hex value agree. |
| `light-control.mp4` | 26-second browser recording at 390×844: open the picker, enter `#ff0000`, submit, and observe the red swatch. The recording ends during the close-button interaction. |

The original WebM was played through its end and converted to H.264 MP4 without
cuts, overlays, or redactions. Screenshots were inspected at their native size.
The phone story test separately verifies that closing the dialog restores focus
to **Adjust color**.

## Direct-component and page coverage

The `coverage-*` captures come from the follow-up's uncommitted snapshot based on
`277795c`. The demonstrated source is unchanged in the delivered revision.
These are new stories, so there is no Storybook baseline for them.

| File | Capture conditions and observed result |
| --- | --- |
| `coverage-entity-detail.png` | EntityDetailPage Loaded, 1280×900 viewport, full-page capture. The route supplies Office plug's Entity ID. Command, availability, and State histories render separate synthetic records. |
| `coverage-metric-editor.png` | MetricEditor RemoveTrend, 1280×900. After the play function removes the trend, a manual change to 7d persists in the local draft. The story uses the production `view-editor` wrapper. |
| `coverage-color-phone.png` | LightColorPicker Closed story, manually opened at 390×844. After submitting `#ff0000`, the dialog is scrolled to show the refreshed synthetic observation `#FF0000`. Document client and scroll widths both measure 390px. |
| `coverage-color.mp4` | 13-second recording of the same phone story: submit red, scroll to the observed value, and close the dialog. The first six seconds of blank recording startup were trimmed; there are no internal cuts or speed changes. The clip was played to its end and sampled frames were inspected. |

The recording and screenshots establish browser rendering and story-local State
refresh only, not a device effect. The page-read stories deliberately do not mock
Rename or enablement PATCH requests.

Run `mise run storybook`, then open:

- `http://localhost:6006/?path=/story/features-views-renderer-viewrenderer--morning-dashboard`
- `http://localhost:6006/?path=/story/features-views-renderer-viewrenderer--phone-dashboard`
- `http://localhost:6006/?path=/story/features-views-cards-device--phone-color-sheet`
