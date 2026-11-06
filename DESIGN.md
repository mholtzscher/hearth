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
  chart-ice: "#9fe3f0"
  chart-sand: "#c9b38f"
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
- A changed reading arrives as an afterglow: the card's top edge lights orange and fades over a minute. Text states each reading's age and availability.
- One fixed numeral scale for reading cards; compact diagnostics share the same tokens.
- One card frame and one state table for every data card in a view.

## Colors

The palette is a neutral night ground with sparing, purposeful color.

### Primary
- **Hearth violet** (`--violet`) marks the agent, focus, selection, and links; the lighter `--violet-text` is used for readable text, including the selected view and unsaved state. `--violet-soft` backs small selected-view marks.
- **Action orange** (`--orange`, `--orange-hover`, `--on-orange`) highlights Save dashboard, Send, a Development page's main shadcn action, and the chart's now edge. Hover uses the lighter orange and action text uses the dark on-orange color.

### Secondary
- **Status green, amber, red-pink, and dusk blue** (`--ok`, `--warn`, `--error`, `--info`) communicate success, warning, error, and in-progress or informational states (dispatched Commands, running Runs) without borrowing either brand hue. Always accompany color with a meaningful status label.

### Tertiary
- **Chart series** use `--chart-1` through `--chart-8`: ink (`--gray-10`), violet, orchid, dusk blue (`--info`), pale gold, gray (`--gray-7`), ice, and sand, respectively. The view contract allows eight series, so each has its own color. The `chart-*` color entries above name the independent series colors; dusk blue is shared with the info status. Orange is not a data series.

### Neutral
- **Untinted gray ramp** (`--gray-0` through `--gray-10`) covers background, surfaces, borders, secondary text, and ink. The black ground is `--gray-0`; cards use `--gray-1`, raised fields and popovers use `--gray-2`, borders commonly use `--gray-3` or `--gray-4`, quiet text uses `--gray-8`, and primary text uses `--gray-10`.

**The One Action Rule.** Reserve orange for the primary action and arrival signals: the afterglow on a changed reading and the chart's now edge. Use outline controls for filters, pagination, and secondary actions. Disabled primary actions become gray, not dimmed orange.

**The Evidence Rule.** The afterglow marks newer data that changed what a card shows. It says nothing about a reading's age, Entity availability, or Command outcome; text states those. Never rely on brand color alone to communicate status.

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

### View cards

Every data card in a view uses one frame. Future card types follow this contract rather than copying an existing card.

The frame is the card surface (`--gray-1`, a 1px `--gray-3` border, 12px corners, 18px padding) with these slots, in order:

| Slot | Content | Style |
|---|---|---|
| Title | The card's label, as a heading one level below the view's headings. | 14px, weight 500, `--gray-10` |
| Unit | A chart's unit, beside its title. A reading's unit stays attached to its value. | 14px `--gray-8` |
| Status | Zero or more status entries opposite the title, wrapping beneath it in a narrow card. Each entry is a 6px tone dot and words, separated by `·`. A ready card from an available Entity has none. | 14px; warn and error words take their tone, neutral words are `--gray-9` |
| Body | A reading at the fixed 40px scale with its unit, or a chart's legend, notes, and plot. | See Typography and Charts |
| Time | `Observed 6:42 PM · just now`. | 14px `--gray-8`, tabular figures |
| Source | A disclosure named **Source** on every card. It links each Entity to its detail page and shows its ID; a chart adds its interval and method. | 14px `--gray-8`, 44px target on coarse pointers |
| Afterglow | A 2px orange top edge when newer data changes what the card shows while it is on screen. | See Afterglow |

The footer, holding Time and Source, sits on the card's bottom edge. When a grid row stretches a card, the spare height falls between the body and the footer. Cards are named articles, never landmarks. A view's host sets its heading level: the canvas title is `h2`, so view headings are `h3` and card titles `h4`; in conversation the artifact title is `h3`, so they are `h4` and `h5`.

#### Card states

A card shows every state that applies, in this order of precedence. Status, State, and Entity availability stay separate facts (see GLOSSARY.md).

| State | Status entry | Tone | Value | Afterglow |
|---|---|---|---|---|
| Loading | Loading… | neutral | `—` in `--gray-6` | none |
| Missing | Entity not found. It may have been removed. | error | `—` | none |
| Request error | Couldn't load this reading, with the error | error | `—` | none |
| Unavailable | Unavailable | warn | last value in `--gray-8` | none |
| Disabled | Disabled | neutral | last value in `--gray-8` | none |
| Refresh failed | Couldn't refresh. Showing the last reading. | warn | last value | on arrival |
| Availability unknown | Availability unknown | neutral | last value | none |
| Never observed | Never observed | neutral | `—` | none |
| Absent | No value. The field isn't in the current State. | neutral | `—` | none |
| Ready and available | none | — | value in `--gray-10` | on arrival |

Tones map to tokens: error to `--error`, warn to `--warn`, neutral to `--gray-7`. Every entry carries words; color is never the only signal. In a chart, entries attach to the series' legend item, and an unavailable or disabled series draws a dashed mean line at 60% opacity without its band.

#### Blocks that are not cards

- A **Heading** sits one level below the host title. A **Note** that follows a Heading sits 4px below it, whatever the stack gap.
- A **failed card** keeps the frame and its title, with the error entry "This card couldn't be shown" and the recovery "Edit the view or ask Hearth to change it."
- **Unsupported content** is not a card: a 1px dashed `--gray-4` outline, no surface, and `--gray-8` text.
- A **loading placeholder** has the card surface, border, and the height of a ready reading card.

### Afterglow

When newer data changes what a card shows while the card is on screen, a 2px orange line lights the card's top edge and fades out over 60 seconds with an exponential ease-out. Each later change restarts it. A Metric compares the formatted value and unit of successive observations. A chart compares the latest plotted value of each live series when its history interval refreshes; State refreshes alone never trigger it. A sensor re-reporting the same displayed value stays dark, and a unit-system change without newer data does not glow. Same-value reports do not extend an existing glow. Opening a view lights nothing: the first reading a card shows is not an arrival. Only ready data from an available, enabled Entity glows. Under reduced motion, the line stays lit for the minute without fading, then disappears.

The afterglow needs no key, because the card lights as its value changes. Age is stated in text: readings show an observation time and, for valid observations less than a day old, a relative age in words that advances each minute; older readings retain the time without a relative-age phrase. The afterglow replaced an earlier line that cooled from orange to violet with age. Age alone misled for sensors that report on change or on long intervals, and that line needed a key on every view.

### Charts

- Series use the eight chart tokens in order, with a 2px mean line, small dots, and a translucent min–max band. A 2px orange vertical reference marks the window's now edge. Preserve gaps instead of implying samples exist.
- Y-axis ticks fall on round steps of 1, 2, or 5 × 10ⁿ. Time ticks fall on round local times; when a window crosses midnight, the first tick of each day names the weekday.
- The tooltip uses the axis time format, with the weekday when the bucket isn't today, and plain words per series: `Living room avg 21.5 °C (21.4–21.6)`.
- When history starts more than 10% of the way into the window, a note says "History since 6:41 PM". The axis still spans the whole window.
- A chart is one tab stop: the plot region, with a text summary per series ("Living room: latest 21.5 °C, range 19.8 to 21.6 over the last 24 hours."). The drawn plot is hidden from assistive technology.
- The method (bucket length, mean, min–max band, what a gap means) lives in **Source**, not in a caption on the card.

### Conversation

Every reply shares one left edge. Reply text, view cards, tables, and the composer start on it; metadata rows (the Hearth label, tool activity, Copy) place a 16px icon on that edge and their text 24px in, with any hover padding hanging outside the edge. User messages are content-width bubbles aligned to the column's right edge. Text inside user bubbles, view cards, tables, and the composer starts at a 16px inset. Blocks within a reply are 12px apart, a reply starts 24px below its prompt, and a new turn starts 32px below the previous reply. Markdown tables use the card treatment: 12px radius, 1px border, and row rules only.

### Inputs and navigation

The conversation composer is a bordered, near-black 12px container with a violet focus border and caret; its send action is orange when enabled and sits in its own row below the text, so it never floats between lines. Development inputs are compact, bordered shadcn fields with violet focus rings and error-colored invalid states. Navigation uses quiet text, neutral active backgrounds, and a violet active icon; on phones it becomes a compact top row with accessible labels. Status chips pair words with their tone.

## Do's and Don'ts

### Do:
- **Do** use the neutral gray ramp for surfaces and preserve true black as the ground.
- **Do** pair arrival, status, and availability signals with explicit text.
- **Do** use violet for Hearth's voice and orange for the main action and arrival signals.
- **Do** preserve the fixed reading scale and use outline buttons for filters and paging.
- **Do** use glossary terms precisely when presenting State, availability, and Command outcomes.

### Don't:
- **Don't** turn orange into a series color or a disabled brown primary button.
- **Don't** tint the neutral gray ramp or replace status colors with brand hues.
- **Don't** use the afterglow for anything but a changed reading: not age, availability, or successful Commands.
- **Don't** use broad decorative gradients or depend on color alone for meaning.
