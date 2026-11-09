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
  compact-reading:
    fontFamily: "Figtree Variable, sans-serif"
    fontSize: "20px"
    fontWeight: 400
    lineHeight: 1.3
    letterSpacing: "-0.02em"
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
- Violet for Hearth's voice; orange for the primary action and arrival signals.
- A changed reading arrives as an afterglow: the card's top edge lights orange and fades over a minute. Text states each reading's age and availability.
- One fixed numeral scale for reading cards; compact diagnostics share the same tokens.
- One card frame and one state table for every data card in a view.

## Colors

The palette is a neutral night ground with sparing, purposeful color.

### Primary
- **Hearth violet** (`--violet`) marks the agent, focus, selection, links, and the selected part of a control (an On switch, a slider's level); the lighter `--violet-text` is used for readable text, including the selected view and unsaved state. `--violet-soft` backs small selected-view marks.
- **Action orange** (`--orange`, `--orange-hover`, `--on-orange`) highlights Save dashboard, Send, a Development page's main shadcn action, and the afterglow on a changed reading. Hover uses the lighter orange and action text uses the dark on-orange color.

### Secondary
- **Status green, amber, red-pink, and dusk blue** (`--ok`, `--warn`, `--error`, `--info`) communicate success, warning, error, and in-progress or informational states (dispatched Commands, running Runs) without borrowing either brand hue. Always accompany color with a meaningful status label.

### Tertiary
- **Chart series** use `--chart-1` through `--chart-8`: ink (`--gray-10`), violet, orchid, dusk blue (`--info`), pale gold, gray (`--gray-7`), ice, and sand, respectively. The view contract allows eight series, so each has its own color. The `chart-*` color entries above name the independent series colors; dusk blue is shared with the info status. Orange is not a data series.

### Neutral
- **Untinted gray ramp** (`--gray-0` through `--gray-10`) covers background, surfaces, borders, secondary text, and ink. The black ground is `--gray-0`; cards use `--gray-1`, raised fields and popovers use `--gray-2`, borders commonly use `--gray-3` or `--gray-4`, quiet text uses `--gray-8`, and primary text uses `--gray-10`.

**The One Action Rule.** Reserve orange for the primary action and the afterglow on a changed reading. Charts and trends draw no now marker: the time axis ends at the window's end. Use outline controls for filters, pagination, and secondary actions. Disabled primary actions become gray, not dimmed orange.

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

At desktop widths, a 218px navigation sidebar sits beside the workspace. When a canvas is present, the conversation column is `clamp(368px, 28vw, 480px)` with 20px gutters, which keeps the composer prompt on one line, and the canvas takes the remaining width. An independent conversation is centered at a maximum reading width of 720px. Canvas content has a maximum width of 1080px beside the conversation and 1440px when it has the screen to itself: expanded, or as a dashboard page. Reading grids use 12px gaps; vertical stacks use 8px, 16px, or 24px gaps.

The sidebar narrows to a 104px rail, with recent conversations in a drawer, at 721–980px, and also at 981–1279px while a canvas is open, so the view keeps room for its cards. At 720px and below, navigation becomes a top row, Conversation and Canvas become separate full-width tabbed views, and reading grids collapse to one column. Household controls commonly provide a 44px minimum target, with composer padding respecting the phone safe area. Development pages retain compact controls; their page list shares one row with the entity jump field and health badges where the width allows, and wraps otherwise.

The canvas header puts the title and window controls (Expand, Close) on its first row and the view actions on the next. When the canvas is at least 1000px wide, the actions join the title row. One rule separates the header from the view. On phones, Expand is hidden because the Canvas tab already fills the screen, and while there is something to save, Discard, Save as new, and Save move to a bar along the bottom edge, within thumb reach. A disabled Save is not shown on phones.

## Elevation & Depth

Surfaces are mostly flat: tonal separation and fine borders establish hierarchy against black. Overlay contexts are exceptions: the modal uses `0 18px 60px #0008` and the history drawer uses `12px 0 40px #0006` against a dark backdrop. The canvas enters with a short 240ms eased transition; drawer movement takes 180ms. Reduced-motion preference removes workspace animation and transitions.

## Shapes

Cards use a soft 12px radius and a 1px neutral border; household buttons and navigation commonly use 8px corners. The underlying shadcn radius is 0.625rem, while editor fields use 6px and the workspace modal uses 14px. Gradients appear only as one- or two-pixel horizon lines, not as large decorative fills.

## Components

### Buttons

Household primary buttons are orange with dark text, 8px corners, and a 44px minimum height; hover lightens the orange. Their disabled state uses gray background and text at full opacity. Secondary workspace actions are neutral bordered controls. Development uses the same semantic colors through shadcn's compact default, outline, secondary, ghost, destructive, and link variants; the default button is 32px high and uses its own translucent-orange hover treatment. Use outline for filters and paging. Focus is visibly violet: household controls use a 2px outline, while shadcn controls use a ring.

### View cards

State cards show boolean and text fields in words at the 40px reading scale.
Power reads On/Off, color modes use readable names, and adapter choices retain
their text. Boolean labels may come from the definition. Long words wrap rather
than shrinking the reading scale.

Metric may add a trend window (1 h, 6 h, 24 h, 7 d), a declared range, or both.
The 44px trend uses a neutral mean line, a faint min–max band, and preserved
gaps. Its range sentence is the text equivalent; the
drawing is hidden from assistive technology. Method belongs in Source. A range
uses a thin neutral track, end labels, and a marker, never a radial gauge. Values
outside it pin the marker and state Above/Below the range in words.

ReadingList and Device cards use 20px, weight 400, tabular row values with 14px
labels and units. This compact-reading step is distinct from the 40px single
reading. Each row carries its own statuses. A changed row lights its bottom rule
orange with the same afterglow as the card's top edge. Their footer states the
oldest displayed reading, never the latest. Source lists every contributing
Entity. Lists hold 1–12 rows; Device membership follows its readable Entities,
shows up to 12 additional rows, and names truncation. A light with one power and
one brightness Entity uses a composite On/Off reading, brightness, and a swatch
of its active color. Off uses a hollow swatch. Color temperature stays in mireds.
A light whose Entities support `set` also carries controls (see Controls).

ToggleTile is a single whole-card power switch, with no separate toggle. Its
observed On reading selects an 11 % violet mix on the card surface and a 38 %
violet border mix. Pending text names the request; neither the reading nor the
surface changes before observed State. Source stays an independent control
above the card's press target. A read-only power Entity has no press target.
Its optional metering reading shares one explicitly selected numeric source
with a standard Trend; an optional additional numeric row can show total energy.
All readings retain their own statuses and the footer states the oldest.

ToggleList uses 1–12 power rows at the 20px compact scale with a switch and
reserved since/pending/failure line per row. An optional outline Turn all off
action sits above the footer. Its feedback counts observed Off values and
per-Entity failures, not a transactional multi-Device result. Since lines use
recorded State changes, never the age of an observation.

Grids containing only ToggleTiles use a 160px minimum, including on phones;
other cards keep their existing 248px minimum and single-column phone layout.
Below a 200px content width, ToggleTiles use a 32px reading and omit the visual
Observed/Oldest prefix while retaining the full accessible time label. Grid
column counts remain a ceiling, now 1–4. These are scoped compact-control
exceptions, not changes to Metric's fixed 40px reading scale.

State timeline cards show when yes/no and text fields changed over one window,
in one of three styles. **Lanes** put every row on one time axis: an 18px track
per row in its chart series color, the name and current value on the left and
totals on the right (`Open 43 min · 7×`, or time per value for text fields).
True spans are solid; false and absent time is the thin baseline; text values
rank by duration as solid, 32 % and 16 % mixes with their words inside when they
fit. Time with no earlier record is a dashed box reading “No history”. Below a
600px card the name and totals move above each lane, and below 420px the totals
take their own line under the name. **Ledger** rows lead with
the current value at the 20px compact-reading scale, a “since” time and totals,
over a 6px neutral strip; one axis sits below the last row. **Feed** lists
changes newest first under part-of-day headings (“This morning”, “Overnight”),
each with a quiet time, a series-colored dot on a rail, and words such as
“Front door open for 6 min”. More than three changes of one row in one part of
the day fold into one line with a count and a pulse strip; text changes pulse as
ticks. It shows 12 entries and an outline **Show earlier changes** button. Axes
end with a quiet “Now” label and no marker. Each row carries its own statuses;
the feed lists them in the header. An unavailable or disabled row draws at 60 %
with a dashed baseline.

Week pattern cards shade one yes/no field over 7 local days by 24 hours. Cells
are 2px-rounded squares on the neutral ramp at five levels (0, 1–5, 6–15, 16–30
and over 30 minutes true); future hours are outlined and hours with no known
value are dashed. Each day states its changes into true and time true, hidden
below a 420px card. A key names the levels.

Every data card in a view uses one frame. Future card types follow this contract rather than copying an existing card.

The frame is the card surface (`--gray-1`, a 1px `--gray-3` border, 12px corners, 18px padding) with these slots, in order:

| Slot | Content | Style |
|---|---|---|
| Title | The card's label, as a heading one level below the view's headings. | 14px, weight 500, `--gray-10` |
| Unit | A chart's unit, beside its title. A reading's unit stays attached to its value. | 14px `--gray-8` |
| Status | Zero or more status entries opposite the title, wrapping beneath it in a narrow card. Each entry is a 6px tone dot and words, separated by `·`. A ready card from an available Entity has none. | 14px; warn and error words take their tone, neutral words are `--gray-9` |
| Body | A reading at the fixed 40px scale with its unit, or a chart's legend, notes, and plot. | See Typography and Charts |
| Time | Compact age: `Observed just now`, `Observed 2m ago`, `Observed 3h ago`, or `Observed 2d ago`. Multi-reading cards say `Oldest 2m ago`, with “Oldest reading” in the accessible label. | 14px `--gray-8`, tabular figures |
| Source | An action named **Source** opens reading details in a desktop popover or a phone bottom sheet, outside the grid flow. It links each Entity to its detail page and shows its ID; a chart adds its interval and method. | 14px `--gray-8`, 44px target on phones and coarse pointers |
| Afterglow | A 2px orange top edge when newer data changes what the card shows while it is on screen. | See Afterglow |

The footer, holding Time and Source, sits on the card's bottom edge. When a grid row stretches a card, the spare height falls between the body and the footer. Cards are named articles, never landmarks. A view's host sets its heading level: the canvas title is `h2`, so view headings are `h3` and card titles `h4`; in conversation the artifact title is `h3`, so they are `h4` and `h5`.

Time and Source share one non-wrapping footer row. Opening Source never changes a card or grid row's height. A Grid treats its requested column count as a ceiling and reduces columns when cards would be narrower than 248px, including beside a conversation. The details show the local observation date and time (with seconds and timezone), its age in full words, and Entity availability and enablement when known. The original full timestamp remains on the time element's `datetime` attribute. Multiple sources carry those Entity facts beside their own links; a card-level timestamp on a multi-reading card remains the oldest displayed reading. Charts do not invent a card-level observation time: their interval and method remain the timing evidence.

At 720px and below, Source uses a modal bottom sheet with a dim backdrop, a 44px close target, safe-area padding, and scrolling within 85% of the viewport height. At larger widths it uses an anchored, viewport-bounded nonmodal popover. Escape and Close return focus to Source; links close the details before route navigation, so the dashboard's unsaved-changes dialog remains usable.

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

#### Controls

A light composite adds controls when its Entities support `set`: a power switch
at the end of the reading row and a brightness slider below it. Cards derive
controls from Entity support; definitions never declare them.

- The switch is 44 × 24px with a 44px-tall target. Off is a `--gray-3` track
  with a `--gray-7` border and a `--gray-8` thumb; On is a violet track with a
  `--gray-10` thumb.
- The slider spans the body below a row naming **Brightness** and its level. Its
  4px track is `--gray-4`, its level violet, and its 18px thumb `--gray-10`,
  with a 44px-tall target. It runs from 1 to 100 %; only the switch turns a
  light off.
- Disabled controls use `--gray-3` to `--gray-6` at full opacity, never dimmed
  violet. The card's **Disabled** status says why.
- The reading keeps showing observed State. While a Command is pending, the
  control shows the requested value and a line beneath it, with a dusk-blue dot,
  says **Turning on…** or **Setting brightness to 40 %…**. Each control reserves
  that line's row, empty while idle, so a Command that settles in milliseconds
  doesn't move the card. A failure uses the same row.
- A failure returns the control to observed State. A line with a red-pink dot
  names the problem, such as **The light rejected the change.**, until the next
  attempt. After a timeout or a lost connection, it says the change may still
  apply.
- Focus uses the 2px violet outline, on the slider's thumb rather than its
  whole track.

### Afterglow

When newer data changes what a card shows while the card is on screen, a 2px orange line lights the card's top edge and fades out over 60 seconds with an exponential ease-out. Each later change restarts it. A Metric compares the formatted value and unit of successive observations. A chart compares the latest plotted value of each live series when its history interval refreshes; State refreshes alone never trigger it. A sensor re-reporting the same displayed value stays dark, and a unit-system change without newer data does not glow. Same-value reports do not extend an existing glow. Opening a view lights nothing: the first reading a card shows is not an arrival. Only ready data from an available, enabled Entity glows. Under reduced motion, the line stays lit for the minute without fading, then disappears.

The afterglow needs no key, because the card lights as its value changes. Age is stated in compact text that advances each minute, including day-old readings; the exact date and time live in Source. Neither age nor the absence of glow implies that an Entity is unavailable or that a sensor is stale: some report on change or at long intervals. The afterglow replaced an earlier line that cooled from orange to violet with age. Age alone misled for those sensors, and that line needed a key on every view.

### Charts

- Series use the eight chart tokens in order, with a 2px mean line, small dots, and a translucent min–max band. Preserve gaps instead of implying samples exist.
- Y-axis ticks fall on round steps of 1, 2, or 5 × 10ⁿ. Time ticks fall on round local times; when a window crosses midnight, the first tick of each day names the weekday.
- The tooltip uses the axis time format, with the weekday when the bucket isn't today, and plain words per series: `Living room avg 21.5 °C (21.4–21.6)`.
- When history starts more than 10% of the way into the window, a note says "History since 6:41 PM". The axis still spans the whole window.
- A chart is one tab stop: the plot region, with a text summary per series ("Living room: latest 21.5 °C, range 19.8 to 21.6 over the last 24 hours."). The drawn plot is hidden from assistive technology.
- The method (bucket length, mean, min–max band, what a gap means) lives in **Source**, not in a caption on the card.

### Conversation

Every reply shares one left edge. Reply text, view cards, tables, and the composer start on it; metadata rows (the Hearth label, tool activity, Copy) place a 16px icon on that edge and their text 24px in, with any hover padding hanging outside the edge. User messages are content-width bubbles aligned to the column's right edge. Text inside user bubbles, view cards, tables, and the composer starts at a 16px inset. Blocks within a reply are 12px apart, a reply starts 24px below its prompt, and a new turn starts 32px below the previous reply. Markdown tables use the card treatment: 12px radius, 1px border, and row rules only.

### Inputs and navigation

The conversation composer is a bordered, near-black 12px container with a violet focus border and caret; its send action is orange when enabled and sits in its own row below the text, so it never floats between lines. The selected view and any editor note share that row, to the left of Send, rather than adding rows of their own. Development inputs are compact, bordered shadcn fields with violet focus rings and error-colored invalid states. Navigation uses quiet text, neutral active backgrounds, and a violet active icon; on phones it becomes a compact top row with accessible labels. Status chips pair words with their tone.

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
