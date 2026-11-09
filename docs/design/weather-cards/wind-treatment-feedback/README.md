# Wind description treatment regression

Production Wind cards, shared cache and styles, rendered with deterministic
HTTP-shaped fetch fixtures in an isolated headless browser. No live Core or
physical Devices. Captured November 9, 2026 at 1280×800 in metric units.

- [Before](before.png): Wind component from published `1a11a37`. Beaufort and
  direction words retain their live appearance despite unavailable/disabled
  inputs, while the speed reading or compass arrow dims.
- [After](after.png): this fix on the same source revision, viewport and fixture
  matrix. Beaufort words follow speed; direction words dim if either speed or
  direction is dimmed. A fault in direction alone leaves Beaufort words live.

Both captures show Ready, unavailable speed, unavailable direction, disabled
speed, and disabled direction. Computed colors after the fix confirm that the
existing styles apply: Beaufort words change from `rgb(209, 209, 209)` to
`rgb(166, 166, 166)` when speed is dimmed; direction words change from
`rgb(166, 166, 166)` to `rgb(125, 125, 125)` when either input is dimmed.

All four new regression cases failed before the fix (no treatment attribute).
They now pass, including retained values, the correct status words and recovery
to live treatments on the next State refresh. No Commands are sent. No new
styles, animations or controls were added; screenshots cover the changed output.
