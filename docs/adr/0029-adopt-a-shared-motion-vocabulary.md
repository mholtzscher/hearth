---
status: accepted
---

# Adopt a shared motion vocabulary for the dashboard

The dashboard's conversation and view cards gain motion only through the patterns and timing tokens in DESIGN.md's Motion section: four durations, one transition curve, and twelve named patterns. Without this, each component would choose its own timing, and motion would drift as the dashboard grows. We chose the patterns after comparing them side by side in a disposable prototype that replicated Hearth's components, before writing any production motion.

**Considered options:**

- **Hover response on data cards:** rejected. A border change on a card that is not itself pressable suggests that it is.
- **Rolling every changed digit:** rejected. When several digits roll at once, two values overlap mid-roll, and a staggered roll briefly shows a value that was never observed, such as 21.8 between 21.6 and 19.8. Only a single changed digit rolls.
- **The afterglow's immediate full-width line:** replaced by Ignite, a 240ms spread from the center. An independent motion review preferred the immediate line as the clearer freshness cue; the owner chose the spread after comparing both in the prototype.
- **Changing a tile's surface when pressed:** rejected. The surface and reading follow observed State (ADR 0005), so a press changes only the status line and Give; the surface spreads when the observation arrives.

**Consequences:** reduced motion no longer removes all motion. Elements marked `data-reduced-motion="fade"` keep opacity and color fades. Movement, scaling, blur, breathing, rolling, and shaking still stop, and the workspace stays instant. Components adopt the patterns as they change; until then, existing transitions such as the 160ms switch keep their current timings.
