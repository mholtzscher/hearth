# Light color picker evidence

Captured from the production dashboard with worktree-local simulated Devices.
No physical lights or model calls were used. These captures replace the rejected
below-slider trigger layout with the user-approved inline color wheel.

| Capture | Conditions |
| --- | --- |
| `before-desktop.png` | Base `e4c9bf1`, 1440 × 1000, three Device cards. |
| `after-desktop.png` | Inline wheel beside On, passive observed swatch, and brightness shown once. Same values and viewport as the baseline. |
| `before-phone.png` | Base at 390 × 844, dashboard scrolled to the color light. |
| `after-phone.png` | Revised closed cards at 390 × 844. |
| `feedback-phone.png` | One color failure and one brightness request share the original reserved row. These outcomes use held HTTP requests and a scripted 502 response, not a Core rejection. |
| `picker-desktop.png` | Anchored color picker, top of its scrollable content. |
| `picker-phone.png` | Modal color sheet at 390 × 844, scrolled to the complete pending and failure details. |
| `white-phone.png` | Temperature mode, mired value and Kelvin equivalent. |
| `color-only-failure.png` | Final color-only fallback at 320px, with a visible Failed label and retained read-only brightness. Scripted HTTP rejection. |
| `light-color-picker.mp4` | An 18-second browser recording: set kitchen white to 250 mired, observe the confirmed reading, then open and scroll the color picker. |

The baseline uses archived UI source from `e4c9bf1`. Its temporary Vite
filesystem allowlist was extended to read the archived automation schema and
the installed Figtree font. No baseline UI source was changed.

The phone captures use the same newer 250-mired kitchen and 205°/80% living-room
values. Desktop before/closed-after captures use 370 mired and 205°/80%.
The revised captures use `ea95fcb` plus the height-neutral implementation in the
commit containing this README. The main captures precede the final labeling
change for color-only writable lights; their depicted controls are unchanged.
`color-only-failure.png` captures that final fallback. Local source hashes
record both snapshots.

## Height checks

Eleven browser comparisons assert equal original/revised card heights and
widths. Each comparison measures the real archived baseline and revised UI,
after responsive layout settles. This verifies height, not pixel-exact parity.

| Viewport | States | Original and revised heights |
| --- | --- | --- |
| 1440 × 1000 | Idle, pending, concurrent, mixed failure/pending | All three cards: 321px |
| 390 × 844 | Idle, pending, concurrent, mixed, failures, power failure | Bedside: 292px; kitchen and living room: 321px |
| 320 × 844 | Concurrent failures, including power | Bedside: 292px; kitchen and living room: 321px |
| 320 × 844 | Color-only writable kitchen light: idle, pending, failure | 186px in every state |

Pending and failure layout checks hold requests or supply scripted HTTP errors.
They prove the rendered feedback contract, not simulator rejection behavior.
The raw measurements retain an initial failed comparison taken before the
baseline's responsive grid settled; the corrected run waits for both layouts.

## Outcome evidence

- The white form sent one `set` Command with `{"value":250}`. Core recorded
  `satisfied`, with a matching applied Observation and the displayed reading.
- Earlier verification of the unchanged hex form sent one `set` Command with `{"hue":205,"saturation":80}`.
  Core recorded `satisfied`, with an exact Command-linked refresh.
- The stock simulator does not apply coordinate parameters. Its HS State was
  explicitly preconfigured through the simulator control API before the
  Command-linked refresh. This proves the browser/Core contract, not a physical
  color change.
- Exact native RGB, rejection/recovery, same-value mode requests, disabled
  controls, and single live announcements have component coverage.
- Phone Tab/Shift+Tab containment, Escape focus restoration, and 320px overflow
  were also checked in the real browser.
- For lights with only writable color, the browser accessibility tree retains
  **White rejected** as the wheel button's description after the picker closes.
  A visible **Pending** or **Failed** label provides the same state without
  relying on color.

Full receipts, HARs, source hashes, and validation logs remain in the task's
local `.data/verification/light-color-picker` directory, with the revision checks
under `revision/`. HAR endpoint IDs are
redacted by Browse; receipt IDs were independently matched against Core.
