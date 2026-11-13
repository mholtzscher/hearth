# Light color picker evidence

Captured from the real dashboard and Core with worktree-local simulated Devices.
No physical lights or model calls were used.

| Capture | Conditions |
| --- | --- |
| `before-desktop.png` | Base `e4c9bf1`, 1440 × 1000, three Device cards. |
| `after-desktop.png` | The implemented compact triggers at 1440 × 1000, with the same initial light values. |
| `before-phone.png` | Base at 390 × 844, internal dashboard scrolled to the color light. |
| `picker-desktop.png` | Anchored color picker, top of its scrollable content. |
| `picker-phone.png` | Modal color sheet at 390 × 844. |
| `white-phone.png` | Temperature mode, mired value and Kelvin equivalent. |
| `light-color-picker.mp4` | Set kitchen white to 250 mired, observe the confirmed reading, then open and scroll the color picker. |

The baseline uses archived UI source from `e4c9bf1`. Its temporary Vite
filesystem allowlist was extended to read the archived automation schema and
the installed Figtree font. No baseline UI source was changed.

The phone captures use the same newer 250-mired kitchen and 205°/80% living-room
values. Desktop before/closed-after captures use 370 mired and 359°/100%.

## Outcome evidence

- The white form sent one `set` Command with `{"value":250}`. Core recorded
  `satisfied`, with a matching applied Observation and the displayed reading.
- The hex form sent one `set` Command with `{"hue":205,"saturation":80}`.
  Core recorded `satisfied`, with an exact Command-linked refresh.
- The stock simulator does not apply coordinate parameters. Its HS State was
  explicitly preconfigured through the simulator control API before the
  Command-linked refresh. This proves the browser/Core contract, not a physical
  color change.
- Exact native RGB, rejection/recovery, same-value mode requests, disabled
  controls, and single live announcements have component coverage.
- Phone Tab/Shift+Tab containment, Escape focus restoration, and 320px overflow
  were also checked in the real browser.

Full receipts, HARs, source hashes, and validation logs remain in the task's
local `.data/verification/light-color-picker` directory. HAR endpoint IDs are
redacted by Browse; receipt IDs were independently matched against Core.
