# Power card verification

These captures show the production React dashboard and Core with the default
worktree-local simulator on November 9, 2026. Sources are the power-card changes
on `feat/views-power-cards`, based on `1f72857`. No household Devices or model
turns were used.

| Capture | What it shows |
| --- | --- |
| [Desktop](desktop.png) | 1440×1300; individual tiles, room lists, metering tiles, observed On surfaces, and shared afterglow |
| [Phone](phone.png) | 390×844; two-column quick switches without horizontal overflow |
| [Phone metering](phone-metering.png) | Compact numeric readings, independently bound total energy, and partial group feedback |
| [Source editor](editor-source.png) | Explicit change from watts to voltage; the displayed value and history use the same selected Entity |
| [Partial group](partial-group.png) | One observed Off and one rejected Command, with a per-row failure message |
| [Interaction video](power-controls.mp4) | Source disclosure, keyboard toggles, shared tile/list State, and partial group failure |

The saved definition uses discovered simulator IDs. Its numeric history is sparse
because this is a fresh database; the chart gaps are real, not fabricated data.
The voltage selection was saved as revision 2 and verified after reload. Total
energy remains an independent reading rather than an integral of the chart.

The browser HAR contains exactly five intended Commands: hallway On and Off by
keyboard, two group Off requests, and a metering-tile On gesture on the phone.
Four successful receipts match retained Core Commands and their Observation IDs.
The remaining request has a durable rejected outcome. Source disclosure, editing,
rendering, refreshing, and saving produced no additional Commands. The HAR also
contains exactly one dashboard update.

The video predates the final source-label disambiguation, which does not change
the captured controls. The phone overview was refreshed after that correction.
Earlier versions cannot render these new card types, so there is no comparable
before screenshot. Hosted-model selection and physical-device behavior were not
tested. The owned browser and simulator daemons were stopped after verification.
