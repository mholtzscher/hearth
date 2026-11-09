# Mobile metering polish

These captures show the production dashboard at the mobile-polish revision of
PR #319, following `82aa12e`. The same worktree-local simulator and saved source
definitions were used before and after the fix. Readings advanced naturally;
no history or values were fabricated.

| Capture | Viewport | Result |
| --- | --- | --- |
| [Before](before.png) | 390×844 | Two metering cards squeeze into 169px cells and stretch to 396px high |
| [After](after.png) | 390×1000 | Metering cards span 350px; their content determines heights of 322px and 265px |
| [Narrow phone](phone-320.png) | 320×1000 | Readings, range text, total energy, and Source remain readable without horizontal overflow |
| [Error and long label](error.png) | 390×1000 | Long labels wrap naturally and the rejected Command remains visible |
| [Source sheet](source.png) | 390×1000 | Source stays independent from the tile's press target |
| [Control video](controls.mp4) | 390×1000 | Source, rejection, keyboard input, pending state, and timeout |

The fix gives only metering variants a full phone grid row. It reduces padding
and reserved status space, aligns the State and measurement on one row, and
keeps the measurement's unit smaller. Simple quick switches retain two columns
where space permits. Tablet and desktop styles remain unchanged.

Browser geometry checks passed at widths 320, 390, 430, 768, and 1440px. Charts
remain 44px high, phone Source targets remain 44px high, and no viewport has
horizontal overflow. Mixed quick-toggle and metering grids were also checked.

The control fixtures bind brightness as the numeric field of the same simulated
Device, solely to exercise rejection and timeout layouts. The browser submitted
exactly two intended Commands; Core retained `rejected` and `outcome_timeout`.
The timeout did not invent observed On State. A disabled-Entity check was restored
after its capture. The owned browser and simulator were stopped afterward.
