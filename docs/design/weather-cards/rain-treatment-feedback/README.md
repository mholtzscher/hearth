# Rain condition treatment regression

Production Rain cards, shared cache and styles, rendered with deterministic
HTTP-shaped fetch fixtures in an isolated headless browser. No live Core or
physical Devices. Captured November 9, 2026 at 1280×1050 in metric units.

- [Before](before.png): Rain component from published `7801cf9`. Storm and
  rate-only conditions retain their live appearance despite unavailable or
  disabled readings.
- [After](after.png): this fix with the same fixtures and viewport. Storm
  conditions dim if either the storm total or rain rate is dimmed. Rate-only
  rain words follow the rate's treatment. Ready conditions remain unchanged.

The matrix shows a ready control, unavailable/disabled rain rate while raining,
unavailable/disabled rain rate after rain stops, unavailable/disabled rate-only
cards, and unavailable/disabled storm totals. The retained rate and words are
still shown with explicit status words; this change only corrects their
treatment. Computed condition text changes from `rgb(209, 209, 209)` to the
existing dimmed `rgb(166, 166, 166)` in all eight fault cases.

All eight new regression cases failed before the fix because condition lines
had no treatment. They now pass, including recovery to live treatments on the
next State refresh and no Commands. No new styles or interactions were added;
screenshots cover the changed output.
