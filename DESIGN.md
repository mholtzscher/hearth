---
name: Hearth
description: A dark, evidence-led workspace for a connected household.
colors:
  gray-0: "#000000"
  gray-1: "#0c0c0c"
  gray-2: "#151515"
  gray-3: "#1f1f1f"
  gray-4: "#2c2c2c"
  gray-5: "#3d3d3d"
  gray-6: "#575757"
  gray-7: "#7d7d7d"
  gray-8: "#a6a6a6"
  gray-9: "#d1d1d1"
  gray-10: "#f4f4f4"
  violet: "#a77bff"
  violet-text: "#c4a8ff"
  violet-soft: "color-mix(in oklch, #a77bff 16%, transparent)"
  orange: "#ff8a3d"
  orange-hover: "#ffa366"
  on-orange: "#1f0d00"
  ok: "#5ad19a"
  warn: "#f2c94c"
  error: "#ff5c7a"
  chart-orchid: "#ef8fd0"
  info: "#7fb4ff"
  chart-pale-gold: "#f2d38a"
typography:
  reading:
    fontFamily: "Figtree Variable, sans-serif"
    fontSize: "40px"
    fontWeight: 350
    lineHeight: 1.15
    letterSpacing: "-0.03em"
    fontFeature: "tabular-nums"
  headline:
    fontFamily: "Figtree Variable, sans-serif"
    fontSize: "26px"
    fontWeight: 500
    lineHeight: 1.3
    letterSpacing: "-0.03em"
  title:
    fontFamily: "Figtree Variable, sans-serif"
    fontSize: "18px"
    fontWeight: 600
  body:
    fontFamily: "Figtree Variable, sans-serif"
    fontSize: "16px"
    lineHeight: 1.7
  label:
    fontFamily: "Figtree Variable, sans-serif"
    fontSize: "14px"
    lineHeight: 1.5
rounded:
  editor: "6px"
  control: "8px"
  shadcn: "0.625rem"
  card: "12px"
  modal: "14px"
spacing:
  small: "8px"
  medium: "16px"
  large: "24px"
  card: "18px"
components:
  button-primary:
    backgroundColor: "{colors.orange}"
    textColor: "{colors.on-orange}"
    rounded: "{rounded.control}"
    height: "44px"
    padding: "10px 12px"
  button-primary-hover:
    backgroundColor: "{colors.orange-hover}"
  button-primary-disabled:
    backgroundColor: "{colors.gray-3}"
    textColor: "{colors.gray-7}"
  button-outline:
    backgroundColor: "{colors.gray-1}"
    textColor: "{colors.gray-10}"
    rounded: "{rounded.control}"
    height: "44px"
    padding: "10px 12px"
  reading-card:
    backgroundColor: "{colors.gray-1}"
    textColor: "{colors.gray-10}"
    rounded: "{rounded.card}"
    padding: "18px"
---

# Design System: Hearth

## Overview

**Creative North Star: "Afterglow"**

Hearth is a quiet, dark-only household workspace: true black ground, untinted gray structure, and small deliberate signals of voice, action, and freshness. Readings and their evidence lead; ornament stays subordinate to the work.

The household workspace is spacious enough to read at a glance. Development uses the same palette through shadcn variables at a higher information density, not a separate visual identity. Follow [GLOSSARY.md](GLOSSARY.md): State, Entity availability, and Command outcome are distinct facts, not interchangeable status labels.

**Key Characteristics:**
- True black ground with a numbered, neutral gray ramp.
- Violet for Hearth's voice; orange for the primary action and the chart's now edge.
- An afterglow horizon and text explain the age and availability of a reading.
- One fixed numeral scale for reading cards; compact diagnostics share the same tokens.

## Colors

The palette is a neutral night ground with sparing, purposeful color.

### Primary
- **Hearth violet** (`--violet`) marks the agent, focus, selection, and links; the lighter `--violet-text` is used for readable text, including the selected view and unsaved state. `--violet-soft` backs small selected-view marks.
- **Action orange** (`--orange`, `--orange-hover`, `--on-orange`) highlights Save dashboard, Send, a Development page's main shadcn action, and the chart's now edge. Hover uses the lighter orange and action text uses the dark on-orange color.

### Secondary
- **Status green, amber, red-pink, and dusk blue** (`--ok`, `--warn`, `--error`, `--info`) communicate success, warning, error, and in-progress or informational states (dispatched Commands, running Runs) without borrowing either brand hue. Always accompany color with a meaningful status label.

### Tertiary
- **Chart series** use `--chart-1` through `--chart-6`: ink (`--gray-10`), violet, orchid, dusk blue (`--info`), pale gold, and gray (`--gray-7`), respectively. The `chart-*` color entries above name the independent series colors; dusk blue is shared with the info status. Orange is not a data series.

### Neutral
- **Untinted gray ramp** (`--gray-0` through `--gray-10`) covers background, surfaces, borders, secondary text, and ink. The black ground is `--gray-0`; cards use `--gray-1`, raised fields and popovers use `--gray-2`, borders commonly use `--gray-3` or `--gray-4`, quiet text uses `--gray-8`, and primary text uses `--gray-10`.

**The One Action Rule.** Reserve orange for the primary action and freshness signals: the afterglow and the chart's now edge. Use outline controls for filters, pagination, and secondary actions. Disabled primary actions become gray, not dimmed orange.

**The Evidence Rule.** A freshness line describes observation age, not Entity availability or Command outcome. Never rely on brand color alone to communicate status.

## Typography

Figtree Variable is self-hosted through `@fontsource-variable/figtree`, with sans-serif fallback. It provides a clear, restrained voice throughout; opaque identifiers and raw structured data may use monospace.

### Hierarchy
- **Reading:** 40px, weight 350, line-height 1.15, tabular figures; units remain smaller and quieter. Every reading card uses this fixed scale.
- **Headline:** canvas and library titles use 26px, weight 500; canvas titles become 22px on phones. The empty conversation heading uses 28px.
- **Title:** view headings use 18px, weight 600.
- **Body:** conversation text uses 16px with 1.7 line-height; the workspace base is 14px.
- **Label:** reading metadata, chart legends, and editor details use 14px with 1.5 line-height. Supporting copy is quieter rather than competing with readings.

## Layout

At desktop widths, a 218px navigation sidebar sits beside the workspace; when a canvas is present, the conversation column is 320–400px and the canvas takes the remaining width. An independent conversation is centered at a maximum reading width of 720px; canvas content has a maximum width of 1080px. Reading grids use 12px gaps; vertical stacks use 8px, 16px, or 24px gaps.

At 980px and below, recent conversations move into a drawer; between 721px and 980px the sidebar narrows to 104px. At 720px and below, navigation becomes a top row, Conversation and Canvas become separate full-width tabbed views, and reading grids collapse to one column. Household controls commonly provide a 44px minimum target, with composer padding respecting the phone safe area. Development pages retain compact controls and wrapping navigation.

## Elevation & Depth

Surfaces are mostly flat: tonal separation and fine borders establish hierarchy against black. Overlay contexts are exceptions: the modal uses `0 18px 60px #0008` and the history drawer uses `12px 0 40px #0006` against a dark backdrop. The canvas enters with a short 240ms eased transition; drawer movement takes 180ms. Reduced-motion preference removes workspace animation and transitions.

## Shapes

Cards use a soft 12px radius and a 1px neutral border; household buttons and navigation commonly use 8px corners. The underlying shadcn radius is 0.625rem, while editor fields use 6px and the workspace modal uses 14px. Gradients appear only as one- or two-pixel horizon lines, not as large decorative fills.

## Components

### Buttons

Household primary buttons are orange with dark text, 8px corners, and a 44px minimum height; hover lightens the orange. Their disabled state uses gray background and text at full opacity. Secondary workspace actions are neutral bordered controls. Development uses the same semantic colors through shadcn's compact default, outline, secondary, ghost, destructive, and link variants; the default button is 32px high and uses its own translucent-orange hover treatment. Use outline for filters and paging. Focus is visibly violet: household controls use a 2px outline, while shadcn controls use a ring.

### Cards and readings

Reading and chart cards use the near-black card surface, 1px neutral border, 12px corners, and 18px padding. Reading labels, units, observation time, and source details stay secondary to the fixed-scale value. Unavailable data may retain a last reading, but its availability is explicitly stated.

### Afterglow

A 2px top-edge line is fully orange for observations up to five minutes old; its orange proportion decreases linearly until the line is violet at 60 minutes. It is absent when Entity availability is unavailable or unknown. The value updates each minute. Readings show an observation time and, for valid observations less than a day old, a relative age in words; older readings retain the time without a relative-age phrase. Each active rendered view containing readings carries a one-line key explaining the line and its absence.

### Charts

Series use the six chart tokens in order, with a 2px mean line, small dots, and a translucent min–max band. A 2px orange vertical reference marks the window's now edge. Legends name series and show resource status; captions explain aggregation and missing samples. Preserve gaps instead of implying samples exist.

### Inputs and navigation

The conversation composer is a bordered, near-black 12px container with a violet focus border and caret; its send action is orange when enabled. Development inputs are compact, bordered shadcn fields with violet focus rings and error-colored invalid states. Navigation uses quiet text, neutral active backgrounds, and a violet active icon; on phones it becomes a compact top row with accessible labels. Status chips pair words with their tone.

## Do's and Don'ts

### Do:
- **Do** use the neutral gray ramp for surfaces and preserve true black as the ground.
- **Do** pair freshness, status, and availability signals with explicit text.
- **Do** use violet for Hearth's voice and orange for the main action and freshness signals.
- **Do** preserve the fixed reading scale and use outline buttons for filters and paging.
- **Do** use glossary terms precisely when presenting State, availability, and Command outcomes.

### Don't:
- **Don't** turn orange into a series color or a disabled brown primary button.
- **Don't** tint the neutral gray ramp or replace status colors with brand hues.
- **Don't** use the afterglow to imply availability, successful Commands, or a new observation.
- **Don't** use broad decorative gradients or depend on color alone for meaning.
