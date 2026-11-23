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
  violet-soft: "#a77bff29"
  orange: "#ff8a3d"
  orange-hover: "#ffa366"
  on-orange: "#1f0d00"
  primary: "#ff8a3d"
  ok: "#5ad19a"
  warn: "#e2b931"
  error: "#ff5c7a"
  info: "#7fb4ff"
  chart-orchid: "#e79bce"
  chart-pale-gold: "#f2d07d"
  chart-ice: "#86e1f2"
  chart-sand: "#b59b63"
typography:
  reading:
    fontFamily: "Figtree Variable, sans-serif"
    fontSize: "40px"
    fontWeight: 350
    lineHeight: 1.15
    letterSpacing: "-0.03em"
    fontFeature: "tabular-nums"
  reading-narrow:
    fontFamily: "Figtree Variable, sans-serif"
    fontSize: "32px"
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
    fontSize: "20px"
    fontWeight: 600
    lineHeight: 1.3
    letterSpacing: "-0.02em"
  compact-reading:
    fontFamily: "Figtree Variable, sans-serif"
    fontSize: "20px"
    fontWeight: 400
    lineHeight: 1.3
    letterSpacing: "-0.02em"
    fontFeature: "tabular-nums"
  body:
    fontFamily: "Figtree Variable, sans-serif"
    fontSize: "16px"
    lineHeight: 1.6
    letterSpacing: "0"
  label:
    fontFamily: "Figtree Variable, sans-serif"
    fontSize: "14px"
    lineHeight: 1.5
    letterSpacing: "0"
  caption:
    fontFamily: "Figtree Variable, sans-serif"
    fontSize: "12px"
    lineHeight: 1.5
    letterSpacing: "0"
rounded:
  xs: "4px"
  md: "8px"
  card: "12px"
spacing:
  stack-sm: "8px"
  grid-gap: "12px"
  stack-md: "16px"
  card-padding: "18px"
  page-gutter-phone: "20px"
  stack-lg: "24px"
  page-gutter: "28px"
components:
  button-primary:
    backgroundColor: "{colors.orange}"
    textColor: "{colors.on-orange}"
    rounded: "{rounded.md}"
    height: "32px"
    padding: "0 12px"
  button-primary-touch:
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
    rounded: "{rounded.md}"
    height: "32px"
    padding: "0 12px"
  button-outline-hover:
    backgroundColor: "{colors.gray-3}"
  field:
    backgroundColor: "{colors.gray-1}"
    textColor: "{colors.gray-10}"
    typography: "{typography.label}"
    rounded: "{rounded.md}"
    height: "32px"
    padding: "4px 8px"
  field-touch:
    height: "44px"
    padding: "8px 8px"
  switch-track:
    backgroundColor: "{colors.gray-3}"
    rounded: "{rounded.card}"
    width: "44px"
    height: "24px"
  switch-track-on:
    backgroundColor: "{colors.violet}"
  segmented-control:
    backgroundColor: "{colors.gray-1}"
    rounded: "{rounded.md}"
    padding: "2px"
  segment-selected:
    backgroundColor: "{colors.violet-soft}"
    textColor: "{colors.violet-text}"
    rounded: "{rounded.xs}"
  card:
    backgroundColor: "{colors.gray-1}"
    textColor: "{colors.gray-10}"
    rounded: "{rounded.card}"
    padding: "18px"
  card-compact:
    typography: "{typography.label}"
    padding: "12px"
  popover:
    backgroundColor: "{colors.gray-2}"
    textColor: "{colors.gray-10}"
    rounded: "{rounded.card}"
    padding: "22px"
  dialog:
    backgroundColor: "{colors.gray-2}"
    textColor: "{colors.gray-10}"
    rounded: "{rounded.card}"
    padding: "24px"
  notice:
    typography: "{typography.label}"
    rounded: "{rounded.md}"
    padding: "12px"
---

# Design System: Hearth

## Overview

**Creative North Star: "Afterglow"**

Hearth is a quiet, dark-only household workspace: true black ground, untinted
gray structure, and small deliberate signals of voice, action, and freshness.
Readings and their evidence lead; ornament stays subordinate to the work.

The household workspace is spacious enough to read at a glance. Development
pages use the same tokens and controls at a higher information density. Follow
[GLOSSARY.md](GLOSSARY.md): State, Entity availability, and Command outcome are
distinct facts, not interchangeable status labels.

**Key Characteristics:**

- True black ground with a numbered, neutral gray ramp.
- Violet for Hearth's voice; orange for the primary action and arrival signals.
- A changed reading arrives as an afterglow: the card's top edge lights orange
  and fades over ten seconds. Text states each reading's age and availability.
- A shared reading scale with a narrow-card step; compact diagnostics share the
  same tokens.
- One card frame and one state table for every data card in a view.

## Colors

The palette is a neutral night ground with sparing, purposeful color.

### Primary

- **Hearth violet** (`violet`) marks the agent, focus, selection, links, and the
  selected part of a control, such as an On switch or a slider's level. The
  lighter **violet text** (`violet-text`) is for readable text, including the
  selected view and unsaved state. **Soft violet** (`violet-soft`, violet at 16%
  alpha) backs selected segments and small selected-view marks.
- **Action orange** (`orange`, and its alias `primary`) fills Save dashboard,
  Send, a Development page's primary action, and the afterglow on a changed
  reading. Hover uses the lighter `orange-hover`, and text on orange uses the
  dark `on-orange`.

### Secondary

- **Status green, amber, red-pink, and dusk blue** (`ok`, `warn`, `error`,
  `info`) communicate success, warning, error, and in-progress or informational
  states, such as dispatched Commands and running Runs. They never borrow either
  brand hue. Always pair a status color with a status label.

### Tertiary

- **Chart series** use `--chart-1` through `--chart-8`, in this order: ink
  (`gray-10`), violet, orchid (`chart-orchid`), dusk blue (`info`), pale gold
  (`chart-pale-gold`), gray (`gray-7`), ice (`chart-ice`), and sand
  (`chart-sand`). A view allows eight series, so each has its own color. Orange
  is not a data series.

### Neutral

- **Untinted gray ramp** (`gray-0` through `gray-10`) covers background,
  surfaces, borders, secondary text, and ink. The black ground is `gray-0`.
  Cards use `gray-1`; raised fields, dialogs, and popovers use `gray-2`; borders
  use `gray-3` or `gray-4`; quiet text uses `gray-8`; primary text uses
  `gray-10`.

### Role aliases

Components name colors by role through aliases in `web/src/index.css`. Each
alias resolves to one token above:

| Alias | Token |
|---|---|
| `background` | `gray-0` |
| `card` | `gray-1` |
| `popover`, `muted` | `gray-2` |
| `border`, `secondary`, `accent` | `gray-3` |
| `input` | `gray-4` |
| `muted-foreground` | `gray-8` |
| `foreground` | `gray-10` |
| `primary` | `orange` |
| `primary-foreground` | `on-orange` |
| `ring` | `violet` |
| `link` | `violet-text` |
| `success` | `ok` |
| `warning` | `warn` |
| `destructive` | `error` |

### Named Rules

**The One Action Rule.** Reserve orange for the primary action and the afterglow
on a changed reading. Charts and trends draw no now marker: the time axis ends
at the window's end. Use outline controls for filters, pagination, and secondary
actions. Disabled primary actions become gray, not dimmed orange.

**The Evidence Rule.** The afterglow marks newer data that changed what a card
shows. It says nothing about a reading's age, Entity availability, or Command
outcome; text states those. Never rely on brand color alone to communicate
status.

**The Color Math Rule.** Measure a new or changed color in OKLCH, OKLab
distance, and WCAG 2 contrast before adding it. Gray ramp steps are untinted
and lighten at every step. Text tokens reach 4.5:1 on `gray-3`, and status text
reaches 4.5:1 on its own 10 to 20% tint, so a status control shows hover with
a border, not a stronger tint. Outside disabled controls, `gray-7` text sits
only on `gray-0` and `gray-1` and reaches 4.5:1 there. Status colors sit
between L 0.68 and 0.82 with C of at least 0.11, and at least 0.11 from
`orange` and `violet`.
`chart-orchid`, `chart-pale-gold`, `chart-ice`, and `chart-sand` keep C between
0.075 and 0.12. Chart series stay at least 0.12 from each other and 0.11 from
`orange`, and `gray-0` labels reach 4.5:1 on each series. `design-tokens.test.ts`
checks every rule, and `design-system.test.ts` keeps status tints behind
status text at 20% or less.

## Typography

**Font:** Figtree Variable, self-hosted through `@fontsource-variable/figtree`,
with a sans-serif fallback. Opaque identifiers, codes, timestamps, and raw
structured data use each device's own monospace at the size of the text around
them, usually Label. `index.css` sets `font-size-adjust: 0.5` on monospace, so
every device's monospace matches Figtree's x-height.

**Character:** a clear, restrained voice throughout. Readings are light and
large; everything around them is quieter.

Each typography token is a Tailwind utility (`text-reading`, `text-label`, and
so on) that sets its whole role: size, line height, and tracking, plus weight
for the display roles. Body, Label, and Caption leave weight to inherit, so a
bold parent stays bold. A class on one element may override one part, such as
`font-semibold` on a Body heading. `index.css` resets Tailwind's own type scale,
so `text-sm` and its siblings set no size.

The scale has seven sizes: 12, 14, 16, 20, 26, 32, and 40px. Line height
follows the size: 1.15 for readings, 1.3 from 20 to 26px, 1.5 for 12 and
14px, and 1.6 for prose at 16px. Tracking tightens only from 20px up: −0.03em
at 26px and above, −0.02em at 20px.

### Hierarchy

- **Reading** (350, 40px, 1.15, −0.03em, tabular figures): a card's single
  reading. Units stay smaller and quieter.
- **Reading narrow** (350, 32px, 1.15, −0.03em): a reading in a card narrower
  than 200px. `Reading` applies it; the Cover card switches at 250px because
  its icon shares the row.
- **Headline** (500, 26px, 1.3, −0.03em): canvas, library, and Development page
  titles at every width, and the empty conversation heading.
- **Title** (600, 20px, 1.3, −0.02em): view headings, Development section
  headings, dialog titles, and headings inside replies, without uppercase
  labels or extra separator rules.
- **Compact reading** (400, 20px, 1.3, −0.02em, tabular figures): row values in
  ReadingList, Device, ToggleList, AirPurifier, Thermostat, the timeline ledger,
  weather rows, and forecast temperatures.
- **Body** (16px, 1.6): conversation text. At weight 600, Body is the
  subsection heading below a Title: the automation editor's headings and the
  headings inside a Development section.
- **Label** (14px, 1.5): the workspace base size, used for card titles, reading
  metadata, chart legends, editor details, fields, buttons, and tables.
- **Caption** (12px, 1.5): only small labels: the Recent conversations date
  headings, badges, chart and forecast axes, compass letters, and the rail's
  navigation labels. Notes and explanations use Label.

Prose keeps a 32em measure, about 74 Figtree characters, and wraps with
`text-pretty`: reply paragraphs, lists, and quotes, and a view's Notes. Avoid
`ch` for a measure, because Figtree's zero is about 1.45 average characters
wide.

Digits are tabular everywhere by default: `index.css` sets
`font-variant-numeric: tabular-nums` on `html`, so readings, ages, levels,
tables, and chart axes hold still as they update and line up in columns. Only
prose opts out with `proportional-nums`: reply text, user messages, and view
Notes. Tables inside a reply opt back in.

Only the wordmark (24px, 20px in the phone bar) sits off this scale.
`design-system.test.ts` lists every pixel size off the scale, and every line
height or tracking value outside the roles.

## Layout

Hearth has two densities on one set of tokens: the spacious household workspace
and the compact Development pages.

### Workspace

At desktop widths, a navigation sidebar of `clamp(218px, 22vw, 340px)` sits
beside the workspace. When a canvas is present, the conversation column is
`clamp(368px, 28vw, 480px)` with 20px gutters, which keeps the composer prompt
on one line, and the canvas takes the remaining width. An independent
conversation is centered at a maximum width of 960px; replies keep a 720px
maximum on the shared left edge, and their paragraphs, lists, and quotes stop
at the 32em prose measure. Canvas content has a maximum width of 1080px
beside the conversation and 1440px when it has the screen to itself: expanded,
or as a dashboard page.

The sidebar narrows to a 104px rail, with recent conversations in a drawer, at
721–980px. It also narrows at 981–1279px while a canvas is open, so the view
keeps room for its cards.

The canvas slides in 16px from the right at layout duration, beside the
conversation only; drawers slide 12px from their edge at state duration (Slide,
see Motion).

At 720px and below:

- A 52px top bar holds a menu button, the page name, and New conversation. The
  whole sidebar opens from the menu as one drawer.
- With a canvas open on Home, the Conversation and Canvas switch takes the page
  name's place, so one bar is the only chrome above the content. Conversation
  and Canvas become separate full-width views.
- The app fills the area the on-screen keyboard leaves visible, so the top bar
  stays in view and the composer sits on the keyboard. Composer padding respects
  the phone safe area, except the home-indicator padding the keyboard covers.
- Household controls provide a 44px minimum target.

### Pages

Development pages and the dashboard library share the dashboard's 1440px maximum
width and 28px gutters (20px on phones). `PageHeader` pairs the headline with
wrapping actions and optional context, separated from content by one rule.

Metadata uses `Facts` definition lists. Each monospace value sits on its label's
baseline and wraps below the label when space is limited. Tables use the label
size and sentence-case column labels. Their named scroll areas enter the Tab
order only while content overflows.

Development pages keep compact diagnostic controls. Their page links use the
household navigation treatment; below 981px a shared native select replaces the
links. The Entity jump field and Connection settings stay alongside navigation,
and health and readiness diagnostics live inside Connection settings.

### Spacing

Reading grids use 12px gaps. Vertical stacks use 8px, 16px, or 24px gaps.

### Grids

A view grid treats its requested column count (1–4) as a ceiling. It drops
columns when cards would be narrower than its minimum, including beside a
conversation.

- Most grids use a 248px minimum and collapse to one column on phones.
- Grids made only of ToggleTiles and tile-layout AirPurifiers, Covers, and
  MediaPlayers, or made only of Metric and State cards, use a 160px minimum,
  including on phones.
- Metering ToggleTiles span the full row on phones, so the history range and
  additional reading stay readable instead of wrapping into tall, narrow tiles.
- Below a 200px content width, ToggleTile, Metric, and State use the narrow
  reading and omit the visual Observed/Oldest prefix. The accessible time label
  stays complete.

## Elevation & Depth

Surfaces are flat: tonal steps of the gray ramp and fine 1px borders establish
hierarchy against black.

### Shadow Vocabulary

- **Overlay** (`box-shadow: 0 18px 60px #0008`): the modal dialog and popovers.
- **Drawer** (`box-shadow: 12px 0 40px #0006`): the navigation and Recent
  drawers, over a dark backdrop.

### Motion

Motion explains a change: it acknowledges an input, shows where something went,
reports an outcome, or marks newer data. Every motion uses one of the patterns
below with the shared timing tokens. Component sections name the pattern each
part uses; a new motion picks the closest pattern instead of its own timing.

#### Timing

| Token | Duration | Use |
|---|---|---|
| `duration-feedback` | 140ms | Acknowledging an input |
| `duration-state` | 180ms | A routine change in place |
| `duration-layout` | 240ms | A change of size or position within one surface |
| `duration-travel` | 360ms | An element moving between surfaces or becoming another element |

Two fixed timings sit outside the scale: the afterglow fades over 10 seconds,
and the pending pulse breathes on a 1.6-second cycle.

- Every transition uses `ease-out-expo`. Only the pending pulse uses
  `ease-pulse`, and a turning Pending icon turns at a constant rate. Nothing
  bounces or overshoots.
- An exit takes the next shorter duration than its entrance.
- Motion uses opacity, transform, clip-path, color, and blur of at most 3px. The
  only height that animates is a disclosure opening, through grid rows or
  `::details-content`.
- Motion follows an input or a newer observation. Patterns that move an element
  between surfaces run at tablet and desktop widths only.

#### Patterns

| Pattern | Motion | Duration | Reduced motion |
|---|---|---|---|
| Arrive | An element entering a sequence fades in while rising 8px; a block inside an arriving group rises 4px | state | Fade |
| Resolve | Text that arrives in pieces fades in from a 3px blur, piece by piece | layout | Fade |
| Grow | A disclosure opens from zero height while fading in; closing reverses it | layout | Fade |
| Slide | A panel fades in while moving 12–16px from the edge it opens from | layout or state | None |
| Morph | A placeholder becomes the element it held a place for: its box moves and resizes to the element's, whose content reaches full opacity within 120ms | travel | The element fades in |
| Travel | An element moves between surfaces as one snapshot; the reverse action reverses it | travel | None |
| Pending | While an outcome is awaited, a dot or icon breathes between full and 40% opacity, or a dashed icon turns once every 8 seconds | 1.6s cycle | None |
| Confirm | An icon swap: the old icon shrinks to 50% and fades, and the new icon grows from 50% and a 2px blur, or a check draws its stroke | feedback | Fade |
| Roll | A single changed digit rolls inside a fixed line, up for a rise and down for a fall; a change to more than one digit, or to the value's length, replaces the value at once | layout | None |
| Ignite | A line spreads from its center to full width | layout | None |
| Spread | A selected surface spreads from the press point, or from the center for a keyboard press; deselection fades | travel, state to deselect | Fade |
| Give | A whole-card press target shrinks to 98.5% while pressed | feedback | None |
| Refuse | A control shakes 3px sideways, one and a half times | layout | None |

#### Reduced motion

Under a reduced-motion preference, `web/src/index.css` removes every animation
and transition, except on elements marked `data-reduced-motion="fade"`. A marked
element keeps only opacity and color transitions at feedback or state duration,
declared with `motion-reduce:` utilities. Movement, scaling, blur, breathing,
rolling, and shaking stop. The workspace is never marked: the canvas and drawers
change at once.

### Named Rules

**The Flat Surface Rule.** Surfaces at rest cast no shadow. Only the overlays
above the workspace use one: the modal, popovers, and drawers.

**The Observed Motion Rule.** A reading or surface moves only when an
observation changes it. A press moves only the pressed control and its reserved
status line, and a requested value never animates into a reading.

**The Single Exposure Rule.** Text never crossfades over other text. The old
text leaves before the new text arrives, and a moving card travels as one
snapshot rather than as two overlapping sizes.

**The Quiet Load Rule.** Opening a page, view, or conversation animates nothing
except the canvas entering and any Pending indicator already waiting on an
outcome. Motion follows a change, never a load.

## Shapes

Corners have three sizes, one for each kind of element. The same radius looks
rounder on a small element than on a large one, so details, controls, and
surfaces need different values. Two sizes only 2px apart look like a mistake,
not a different role.

- **Detail** (`xs`, 4px): elements smaller than a control or nested inside one:
  segments, inline code, the sent-message corner toward the composer, the sheet
  handle, and chart marks such as slider tracks, timeline bars, and week cells.
- **Control** (`md`, 8px): buttons, fields, segmented controls, notices,
  household navigation, and compact press targets such as the Source action.
- **Card** (`card`, 12px): cards, popovers, the modal and phone sheets,
  Markdown tables, the Accordion, and the composer.

Every corner uses one of these tokens. A browser caps a radius at half the
element's height, so a 4px-high track with `xs` corners has fully round ends.

Switch tracks are pills. Borders are 1px and neutral. Gradients appear only as
one- or two-pixel horizon lines, never as large decorative fills.

## Components

Both surfaces use the shared controls in `web/src/components`. The Storybook
**Design system** section (`mise run storybook-start`) shows these tokens and
components rendered from the live CSS.

**The One Focus Ring Rule.** Every focusable control shows the same 2px violet
outline with a 3px offset, from the global `:focus-visible` rule. A component
may move the ring to the part that takes input, as the brightness slider does
with its thumb, but never restyles it.

**The Stable Height Rule.** At a given width, interacting with a card never
changes its height or its grid row's height, even briefly. This includes
hover, focus, control changes, tabs, Command feedback, and opening Source.
Reserve space for inline states before interaction, or put extra content
outside the grid flow in a popover or sheet. Command feedback stays on one
reserved line. Use short failure messages on the card and retain the full
sentence for screen readers and in a tooltip.

### Buttons

`Button` from `components/button.tsx` has 8px corners and 14px text. The
`default` size is 32px high and `touch` is 44px. Icon-only buttons use the
square `icon` (32px) or `icon-touch` (44px) size and have an accessible name.

- **Primary:** orange with dark, semibold text. Hover uses solid `orange-hover`.
  Disabled primary buttons use a `gray-3` background and border with `gray-7`
  text at full opacity.
- **Outline:** the card surface, a neutral border, and regular-weight text.
  Hover uses `gray-3`. Use outline for filters, paging, and secondary workspace
  actions.
- **Destructive:** `destructive` text on a 20% `destructive` tint. Hover adds a
  `destructive` border at 50% and keeps the tint, so the text stays at 4.5:1.
- **Secondary, ghost, and link** variants are also available. Disabled
  non-primary buttons use 50% opacity.

The shared Button supplies its own styling, including when `asChild` renders a
link.

### Fields

`Input`, `Textarea`, and `NativeSelect` share the card background, a 1px
`gray-4` border, 8px corners, 8px horizontal padding, and 14px text with a 21px
line height. Invalid fields use a `destructive` border, and disabled fields use
50% opacity.

- Input and NativeSelect default to 32px high with 4px vertical padding. The
  `touch` size is 44px with 8px vertical padding; `auto` uses 32px with a fine
  pointer and 44px with a coarse pointer. These size names replace the native
  numeric `size` attribute.
- NativeSelect keeps the browser's selection behavior and arrow.
- Textarea has a 44px minimum height and 8px vertical padding. Its `rows` prop
  sets the initial height; it does not grow with content. Callers may override
  typography for diagnostic JSON and identifiers.

The brightness slider and the light color picker's range and exact-value inputs
are specialized native controls, not shared fields.

### Switches

`Switch` from `components/switch.tsx` draws a 44×24px pill track with an 18px
thumb inside a target that is 32px high by default or 44px with `size="touch"`.
The target has 8px corners, so the focus ring is a rounded rectangle while the
track stays a pill.

- **Off:** a `gray-3` track, `gray-7` border, and `gray-8` thumb.
- **On:** a violet track and a `gray-10` thumb.
- **Disabled:** the same state colors at 50% opacity.

### Segmented controls

`SegmentedControl` from `components/segmented-control.tsx` renders native
`aria-pressed` buttons in an accessibly named group. The caller supplies the
current value; a value that matches no option leaves every segment unselected.
Each enabled segment stays in the Tab order.

The frame is the card surface with a 1px `gray-4` border and 2px padding.
Segments have 4px corners and a 32px minimum height by default, or 44px with
`size="touch"`; long labels wrap and increase the height. A selected segment
uses `violet-soft`, `violet-text`, and a 1px inset violet line. Disabled
segments use 50% opacity.

### Dialogs and popovers

`Dialog` from `components/dialog.tsx` uses the `gray-2` surface, a `gray-4`
border, 12px corners, a dim backdrop, and a centered decision layout. Short
desktop viewports let the dialog body scroll. With `placement="bottom"`, it
becomes a viewport-bounded sheet at 720px and below, with 12px top corners,
square bottom corners, and safe-area padding.

Modal dialogs contain focus, close on Escape, and return focus to their trigger.
Non-modal dialogs neither trap focus nor draw an overlay. The expanded canvas is
the one modal surface outside Dialog; decision dialogs always use Dialog.

`Popover` from `components/popover.tsx` uses the `gray-2` surface, 12px corners,
22px padding, and scrolls within the available height.

### Notices

`Notice` from `components/notice.tsx` uses the error tint: a border of
`destructive` at 45%, a background of `destructive` at 8%, foreground text, 8px
corners, and 12px padding. Its text uses the 14px label size with a 21px line
height. Notices grow with their content rather than taking a control height.

Callers choose `role="alert"` for errors or `role="status"` for non-urgent
feedback. Recovery actions are caller-owned shared Buttons.

### Navigation

Navigation uses quiet text, neutral active backgrounds, and a violet active
icon. On phones it moves into the drawer. While the server is not ready, the
menu button carries a status dot in its tone, with the status in its accessible
name and in words inside the drawer. Status chips pair words with their tone.

Recent conversations group under local date headings: Today, Yesterday, Previous
7 days, Previous 30 days, then month names, with the year for an earlier year.

- Each heading (12px, weight 500, `gray-7`) stays pinned while its conversations
  scroll. Headings follow the local calendar day and update at midnight.
- Rows are 36px with a fine pointer and 44px with touch. A truncated preview
  repeats in full as a tooltip.
- Once the list holds more than 12 conversations, a search field above it
  filters previews. Escape clears it, and an empty result says so in words.
- In the phone drawer, while the search has focus or a query, only Recent
  remains, so results fit above the keyboard and stay in place when the field
  loses focus.

### Canvas header

The canvas header puts the title and window controls (Expand, Close) on its
first row and the view actions on the next. When the canvas is at least 1000px
wide, the actions join the title row. One rule separates the header from the
view.

On phones, Expand is hidden because the Canvas tab already fills the screen.
While there is something to save, Discard, Save as new, and Save move to a bar
along the bottom edge, within thumb reach. A disabled Save is not shown on
phones.

### Dashboard library

Each saved dashboard is a card: a 20px `gray-8` dashboard icon, the 14px
weight-500 title, and "Updated" with an age under seven days or a short local
date after that.

- The title link covers the card, which shows the focus ring while that link has
  keyboard focus.
- A ghost 44px Delete icon button sits level with the icon and follows the title
  in reading order. Its confirmation uses the destructive button.
- The first load shows empty card frames at a ready card's height. A failed load
  shows only its notice, never the empty state.

### Conversation

Every reply shares one left edge. Reply text, view cards, tables, and the
composer start on it. Metadata rows (the Hearth label, tool activity, Copy)
place a 16px icon on that edge and their text 24px in, with any hover padding
hanging outside the edge.

- User messages are content-width bubbles aligned to the column's right edge.
- Text inside user bubbles, view cards, tables, and the composer starts at a
  16px inset.
- Blocks within a reply are 12px apart. A reply starts 24px below its prompt,
  and a new turn starts 32px below the previous reply.
- Markdown tables use the card treatment: 12px corners, a 1px border, and row
  rules only.

An inline view has no outer card frame or padding. Its icon, title and summary,
and Open in canvas action share one header row above the view's own cards. Below
a 440px container the visible action reads Open; its accessible name stays Open
in canvas. A view has 12px extra space above it to separate successive views.
The compact Showing in canvas preview and the pending placeholder keep their
bordered treatment.

The composer is a bordered, near-black container with 12px corners, a violet
focus border, and a violet caret.

- Send is orange when enabled and sits in its own row below the text, so it
  never floats between lines. The selected view and any editor note share that
  row, to the left of Send.
- The selected view or Automation is a chip on the composer's 8px inset,
  mirroring Send: a 1px violet border, its icon on the text's edge, and a name
  truncated to one line. The name shows the canvas, on phones by switching to
  the Canvas tab. Its close button closes the view like the canvas's own Close,
  asking first when there are unsaved changes. Both parts have 44px touch
  targets.
- The revision and saved state stay in the canvas header rather than repeating
  in the chip.
- Sent messages keep a plain, non-interactive record of the view they carried.

Each part of a conversation moves with a pattern from Motion. Opening a
conversation shows its history without motion.

- A sent message arrives (Arrive), and so does a reply with its Hearth label.
  Each block of the reply, such as tool activity or a view, arrives as it
  appears, rising 4px.
- A reply's text arrives whole when its turn finishes and resolves as one block
  (Resolve).
- While a reply runs, the Hearth icon breathes (Pending), and "Looking through
  your home…" appears below it after 600ms, so a reply that finishes sooner never
  shows it.
- Tool activity opens and closes with Grow. A pending tool's dashed icon turns
  (Pending), and a finished tool's check draws in (Confirm).
- A pending view's placeholders become its cards (Morph).
- Open in canvas moves the view's cards together into the canvas, and the inline
  header becomes the compact Showing in canvas preview (Travel). Closing
  reverses it. Phones switch screens without motion.
- Copy changes to Copied with Confirm.

### View cards

**The One Frame Rule.** Every data card in a view uses one frame. Future card
types follow this contract rather than copying an existing card. New cards
and their prototypes follow the Stable Height Rule.

Cards on Development pages use the compact card: 12px padding and 14px label
text.

#### Card frame

The frame is the card surface (`gray-1`, a 1px `gray-3` border, 12px corners,
18px padding) with these slots, in order:

| Slot | Content | Style |
|---|---|---|
| Title | The card's label, as a heading one level below the view's headings. | 14px, weight 500, `gray-10` |
| Unit | A chart's unit, beside its title. A reading's unit stays attached to its value. | 14px `gray-8` |
| Status | Zero or more status entries opposite the title, wrapping beneath it in a narrow card. Each entry is a 6px tone dot and words, separated by `·`. A ready card from an available Entity has none. | 14px; warn and error words take their tone, neutral words are `gray-9` |
| Body | A reading with its unit, or a chart's legend, notes, and plot. | See Typography and Charts |
| Time | Compact age: `Observed just now`, `Observed 2m ago`, `Observed 3h ago`, or `Observed 2d ago`. Multi-reading cards say `Oldest 2m ago`, with "Oldest reading" in the accessible label. | 14px `gray-8`, tabular figures |
| Source | An action named **Source** opens reading details in a desktop popover or a phone bottom sheet, outside the grid flow. It links each Entity to its detail page and shows its ID; a chart adds its interval and method. | 14px `gray-8`, 44px target on phones and coarse pointers |
| Afterglow | A 2px orange top edge when newer data changes what the card shows while it is on screen. | See Afterglow |

Time and Source share one non-wrapping footer row on the card's bottom edge.
When a grid row stretches a card, the spare height falls between the body and
the footer.

Cards are named articles, never landmarks. A view's host sets its heading level:
the canvas title is `h2`, so view headings are `h3` and card titles `h4`. In a
conversation the artifact title is `h3`, so they are `h4` and `h5`.

#### Source details

Source uses a 28px layout row at every width. On phones and coarse pointers, its
press target extends 8px above and below that row to stay 44px tall without
increasing the card height.

- At 720px and below, the details open in a modal bottom sheet with a dim
  backdrop, a 44px close target, safe-area padding, and scrolling within 85% of
  the viewport height.
- At larger widths they open in an anchored, viewport-bounded nonmodal popover.
- Escape and Close return focus to Source.

The details show the local observation date and time (with seconds and
timezone), its age in full words, and Entity availability and enablement when
known. The time element's `datetime` attribute keeps the full timestamp.
Multiple sources carry those Entity facts beside their own links. Charts do not
invent a card-level observation time: their interval and method are the timing
evidence.

#### Card states

A card shows every state that applies, in this order of precedence. Status,
State, and Entity availability are separate facts (see GLOSSARY.md).

| State | Status entry | Tone | Value | Afterglow |
|---|---|---|---|---|
| Loading | Loading… | neutral | `—` in `gray-6` | none |
| Missing | Entity not found. It may have been removed. | error | `—` | none |
| Request error | Couldn't load this reading, with the error | error | `—` | none |
| Unavailable | Unavailable | warn | last value in `gray-8` | none |
| Disabled | Disabled | neutral | last value in `gray-8` | none |
| Refresh failed | Couldn't refresh. Showing the last reading. | warn | last value | on arrival |
| Availability unknown | Availability unknown | neutral | last value | none |
| Never observed | Never observed | neutral | `—` | none |
| Absent | No value. The field isn't in the current State. | neutral | `—` | none |
| Ready and available | none | — | value in `gray-10` | on arrival |

Tones map to tokens: error to `error`, warn to `warn`, neutral to `gray-7`.
Every entry carries words; color is never the only signal. In a chart, entries
attach to the series' legend item, and an unavailable or disabled series draws a
dashed mean line at 60% opacity without its band.

#### Blocks that are not cards

- A **Heading** sits one level below the host title. A **Note** that follows a
  Heading sits 4px below it, whatever the stack gap.
- A **failed card** keeps the frame and its title, with the error entry "This
  card couldn't be shown" and the recovery "Edit the view or ask Hearth to
  change it."
- **Unsupported content** is not a card: a 1px dashed `gray-4` outline, no
  surface, and `gray-8` text.
- A **loading placeholder** has the card surface, border, and the height of a
  ready reading card.

#### Afterglow

When newer data changes what a card shows while the card is on screen, a 2px
orange line spreads from the center of the card's top edge to its full width
(Ignite, see Motion), then fades out over 10 seconds with an exponential
ease-out. Each later change restarts it. Under reduced motion, the line lights at
full width, stays lit for those 10 seconds without fading, then disappears.

- A Metric compares the formatted value and unit of successive observations.
- A chart compares the latest plotted value of each live series when its history
  interval refreshes; State refreshes alone never trigger it.
- A sensor re-reporting the same displayed value stays dark, and same-value
  reports do not extend an existing glow. A unit-system change without newer
  data does not glow.
- Opening a view lights nothing: the first reading a card shows is not an
  arrival.
- Only ready data from an available, enabled Entity glows.

The afterglow needs no key, because the card lights as its value changes. Age is
stated in compact text that advances each minute, including day-old readings;
the exact date and time live in Source. Neither age nor the absence of glow
implies that an Entity is unavailable or that a sensor is stale: some sensors
report on change or at long intervals.

#### Controls

Controls appear on a card when its Entities support them. A light composite adds
a power switch at the end of the reading row and a brightness slider below it.

- The switch is the shared Switch at its 44px touch size.
- The slider spans the body below a row naming **Brightness** and its level. Its
  4px track is `gray-4`, its level violet, and its 18px thumb `gray-10`, with a
  44px-tall target. It runs from 1 to 100 %; only the switch turns a light off.
- A disabled control uses the shared 50% opacity, and the card's **Disabled**
  status says why.
- The reading keeps showing observed State. While a Command is pending, the
  control shows the requested value, and its reserved line shows a dusk-blue dot
  and words such as **Turning on…** or **Setting brightness to 40 %…**. The dot
  breathes until the outcome arrives (Pending, see Motion).
- A failure returns the control to observed State. A newly rejected Command
  gives the control a short shake (Refuse); the same line shows a red-pink dot
  and a short message, such as
  **Rejected.**, until the next attempt. The full sentence, such as "The light
  rejected the change.", is available to screen readers and in a tooltip.
- A card with more than two controls, such as AirPurifier, reserves one shared
  line at the end of its body instead. It shows the most recently changed
  control's pending words, or its failure after the control's name, such as
  **Fan level: Rejected.**

#### Charts

- Series use the eight chart colors in order, with a 2px mean line, small dots,
  and a translucent min–max band. Preserve gaps instead of implying samples
  exist.
- Y-axis ticks fall on round steps of 1, 2, or 5 × 10ⁿ. Time ticks fall on round
  local times; when a window crosses midnight, the first tick of each day names
  the weekday.
- The tooltip uses the axis time format, with the weekday when the bucket isn't
  today, and plain words per series: `Living room avg 21.5 °C (21.4–21.6)`.
- When history starts more than 10% of the way into the window, a note says
  "History since 6:41 PM". The axis still spans the whole window.
- A chart is one tab stop: the plot region, with a text summary per series
  ("Living room: latest 21.5 °C, range 19.8 to 21.6 over the last 24 hours.").
  The drawn plot is hidden from assistive technology.
- The method (bucket length, mean, min–max band, what a gap means) lives in
  **Source**, not in a caption on the card.

#### Metric and State

State cards show boolean and text fields in words at the reading scale. Long
words wrap rather than shrinking the reading.

A Metric's changed reading rolls (Roll, see Motion).

A Metric trend is 44px tall, with a neutral mean line, a faint min–max band, and
preserved gaps. Its range sentence is the text equivalent, and the drawing is
hidden from assistive technology. A declared range uses a thin neutral track,
end labels, and a marker, never a radial gauge. A value outside it pins the
marker and states Above or Below the range in words.

#### ReadingList and Device

Rows use the compact reading with 14px labels and units. Each row carries its
own statuses, and a changed row lights its bottom rule orange with the card's
afterglow. The footer states the oldest displayed reading. A light composite
shows the On/Off reading, brightness, and a swatch of its active color; Off
draws a hollow swatch.

#### ToggleTile and ToggleList

ToggleTile is one whole-card power press with no separate toggle. An observed On
reading selects an 11% violet mix on the card surface and a 38% violet border
mix. A press gives (Give) and states the request on the status line; the
surface spreads from the press point only when the observed On arrives (Spread,
see Motion). Source stays
an independent control above the press target, and a read-only power Entity has
no press target. Each tile reserves one 21px status line at
every width.

A metering ToggleTile uses 16px padding and 8px frame gaps. The power State and
the selected measurement share a reading row, with the measurement at the
compact reading size and its unit at 14px. An optional additional row can show
total energy. Ordinary quick switches keep two compact columns where space
permits.

ToggleList rows use the compact reading with a switch and a reserved since,
pending, or failure line per row. An optional outline **Turn all off** action
sits above the footer.

#### AirPurifier

The card leads with Power at the reading size, its switch at the end of the row,
and the mode (and the manual fan level) beneath in 14px `gray-9`. PM2.5 and
Filter life follow as compact rows.

- PM2.5 names its air category in words beside the value, after a 6px dot: Good
  takes `ok`; Moderate and Unhealthy for sensitive groups take `warn`; Unhealthy
  and worse take `error`. Good's words stay `gray-9`; warn and error words take
  their tone. The category is part of the reading, never a Status entry, and
  dims with an unavailable or disabled PM2.5.
- Filter life at or below 10 % adds **Replace the filter soon** in `warn`.
- Mode is a touch-size segmented control.
- Fan level is a stepper: 44px outline minus and plus buttons around the
  compact-reading level, and one 14×6px pip per level, violet up to the level.
  When another mode drives the fan, the pips draw hollow in `gray-6` and the row
  says **Level N kept for Manual**.
- Display and Child lock share a two-column row of switches.

The tile layout is a ToggleTile with the mode word after On at the compact
reading size and a line naming PM2.5 and its category.

#### Cover

The card leads with a drawing of the cover beside the State in words. The 88px
drawing (64px below a 250px card) is a slatted panel in its opening, drawn on
the neutral ramp: a `gray-4` face, `gray-6` slats and frame, a `gray-8` bottom
seal, and a `gray-5` roller.

- The panel rolls up to the reported position. While a position Command is
  pending, a dashed 1.5px violet line marks the requested position.
- Unavailable and disabled covers draw a dashed frame and a `gray-3` face.
- The reading is Closed, Partly open, Open, Opening, or Closing at the reading
  size, with **N % open** beneath in 14px `gray-9`.
- Open, Stop, and Close are equal 44px outline buttons with 16px icons. A
  pending target's button takes the violet selected treatment. When opening asks
  for a second press, Open becomes an orange **Confirm**, the primary action
  while the confirmation lasts.
- A reserved status line follows. It turns warn when the cover still reports
  motion after Stop.

The Cover tile is a ToggleTile whose press opens, closes, or stops. It reads
**Open** for a partly open cover and has three fixed 21px rows: the position,
what a press does (**Press to stop**), and the status line. Status words are
short (**Rejected.**, **Press again.**) and keep the full sentence in a tooltip
and for screen readers. An open cover takes the On surface.

#### Thermostat

The card leads with what the thermostat is set to. A target block of two
columns and three fixed rows holds the targets the current mode uses: a 14px
`gray-9` label (**Heat to**, **Cool to**, or **Target**), the target at the
reading size with a `muted-foreground` degree sign, and a pair of 44px outline
− and + icon buttons. One target takes the left column. A mode with no target
spans both columns with **Mode**, the mode word at the reading size, and **No
target in this mode.** in 14px `muted-foreground`.

- The block has the same height in every mode and at every card width from
  248px, and the line beneath it is always reserved, so switching the mode or
  stepping a target never moves the card.
- Targets are neutral: `gray-10`, or `gray-8` when dimmed. While a target
  Command is pending, the requested value takes `violet-text` with a 1.5px dashed
  violet underline, as Cover marks a requested position.
- The line under the block names the mode and activity in 14px `gray-9`:
  **Heat & cool · Heating**. Where the mode leads the block, it names the
  activity alone.
- Inside temperature and humidity follow as compact reading rows. Only they
  light the afterglow.
- Mode is a touch segmented control for up to four modes and a touch select for
  more; Fan is a touch select. One shared reserved line follows.

#### Media player

The Transport Deck leads with a 64px artwork sleeve or neutral speaker plate.
Playback appears in words with a three-bar mark; the bars take violet only
while playing and not muted. The title and credits each truncate to one line.
Grouped speakers carry a Grouped label beside playback.

- Elapsed and total time keep their place above the transport buttons, even
  when unavailable. The playhead ticks locally while Playing without lighting
  the afterglow. A native range allows seeking when supported.
- Transport uses 44px controls. Play or Pause sits between the icon buttons.
  When all four controls are present at the narrow width, the main button
  shows just its icon. Stop can appear separately alongside Pause. Volume and
  Mute share a row, followed by a reserved Command status line.
- The Compact Tile uses the shared whole-card press and takes the On surface
  while Playing. Track and artist, volume or grouping, and Command feedback
  stay on fixed-height lines. Source is independently actionable.

#### State timeline and Week pattern

State timeline cards show when yes/no and text fields changed over one window,
in one of three styles.

- **Lanes** put every row on one time axis: an 18px track per row in its chart
  series color, the name and current value on the left, and totals on the right
  (`Open 43 min · 7×`, or time per value for text fields). True spans are solid;
  false and absent time is the thin baseline; text values rank by duration as
  solid, 32% and 16% mixes, with their words inside when they fit. Time with no
  earlier record is a dashed box reading "No history". Below a 600px card the
  name and totals move above each lane, and below 420px the totals take their
  own line under the name.
- **Ledger** rows lead with the current value at the compact reading size, a
  "since" time, and totals, over a 6px neutral strip. One axis sits below the
  last row.
- **Feed** lists changes newest first under part-of-day headings ("This
  morning", "Overnight"), each with a quiet time, a series-colored dot on a
  rail, and words such as "Front door open for 6 min". More than three changes
  of one row in one part of the day fold into one line with a count and a pulse
  strip; text changes pulse as ticks. It shows 12 entries and an outline **Show
  earlier changes** button.

Axes use 12px Caption labels, like every chart axis, because 14px labels
overlap at a 440px width; labels may still overlap at narrower widths. The axis
ends with a quiet "Now" label and no marker. Each row carries its own statuses;
the feed lists them in the header. An unavailable or disabled row draws hollow
segments outlined in the series color, with `muted-foreground` labels, and a
dashed baseline.

Week pattern cards shade one yes/no field over 7 local days by 24 hours. Cells
are 4px-rounded squares on the neutral ramp at five levels (0, 1–5, 6–15, 16–30,
and over 30 minutes true). Future hours are outlined, and hours with no known
value are dashed. Each day states its changes into true and its time true; that
summary hides below a 420px card. A key names the levels.

#### Weather

Weather cards (Outside now, Rain, Wind, and Pressure) lead with one reading at
the reading size. Conditions follow in words at 14px `gray-9`, such as `Rain ·
Breezy` or `Moderate breeze`, and derived values, such as the 24 h high and low,
follow in quiet 14px text. Weather cards describe only what the station
measures, never sky conditions. Method and thresholds belong in Source.

- Further readings use compact rows or one inline sentence. A reading with no
  row of its own states its statuses on a named line, and rain totals that share
  one status share one line.
- Derived values and words dim when any reading they use is dimmed.
- Wind draws a 104px compass rose (76px below a 290px card): a neutral ring and
  ticks, N E S W letters, and a `gray-10` arrow pointing where the wind blows.
  Calm draws a small ring instead. An unavailable or disabled direction keeps
  its last arrow, dashed in `gray-7`; without a value there is no arrow.
- Pressure states its three-hour tendency with a 16px arrow icon and the signed
  change, or says why there is no tendency.

A weather card with at least 460px of content width uses its width rather than
its height. The lead reading and its words fill the left half; rows, the trend,
or Wind's gusts and direction take the right half, 24px away, so the right
halves line up down a grid column. The trend there grows to a 72px plot. Wind
keeps its compass to the left of the reading and reserves enough width for the
displayed value before allocating space to details. Rain's totals sentence spans
both halves below. Narrower cards keep the single column; the card's width, not
its grid's column count, chooses the layout.

With a trend on a wide card, Wind groups its current readings beside an 88px
compass on the left and a neutral 72px trend with its range on the right. The
reading group reserves its width before the trend gets space. Narrow cards stack
the trend below the current readings and keep their usual compass size.

Live Rain trends use dusk blue (`--chart-4`), and Pressure uses pale gold
(`--chart-5`). Their lines mix equal parts of that color and `gray-9` in OKLCH;
the min–max band uses the chart color at 8% opacity. Readings, labels, and card
backgrounds stay neutral. Unavailable or disabled trends keep the shared
dashed-gray treatment. Other Metric trends keep neutral lines.

#### Forecast

Forecast cards show modeled predictions, not station readings, and are the only
cards that draw sky conditions.

- **Daily** rows are 44px tall: the day at 14px (**Today** first), a 28px
  condition icon, the precipitation chance at 14px `info`, the low at the
  compact reading size in `gray-8`, a 4px `gray-3` track with a `gray-9` span
  from low to high on the shown days' range, and the high at the compact reading
  size in `gray-10`.
- **Hourly** keeps a 216px plot height and draws at its container's measured
  width, so text, icons, and strokes do not scale on desktop. Each hour needs at
  least 22px within a 264px minimum plot; narrower containers scroll
  horizontally. It draws a 2px `gray-10` temperature line, precipitation-chance
  bars in `info` at 55% opacity, icons and 14px temperature labels above the
  line, hour labels below the axis, and a dashed `gray-5` midnight rule with the
  weekday beneath it. A chance below 10% is hidden, and its column keeps its
  space.
- Condition icons are 1.6px line drawings in `gray-9`. Only here, the sun and
  lightning take pale gold (`--chart-5`), and drops and flakes take dusk blue
  (`info`).
- An unavailable or disabled forecast keeps its last periods with values, icons,
  and bars in `gray-8`.

## Do's and Don'ts

### Do:

- **Do** use the neutral gray ramp for surfaces and preserve true black as the
  ground.
- **Do** pair arrival, status, and availability signals with explicit text.
- **Do** use violet for Hearth's voice and orange for the main action and
  arrival signals.
- **Do** use the defined reading scales and outline buttons for filters and
  paging.
- **Do** use glossary terms precisely when presenting State, availability, and
  Command outcomes.
- **Do** reserve a control's status line while idle, so feedback never moves the
  card.
- **Do** give household controls a 44px target on touch.
- **Do** build a new data card on the shared card frame and card states.

### Don't:

- **Don't** turn orange into a series color or a disabled brown primary button.
- **Don't** tint the neutral gray ramp or replace status colors with brand hues.
- **Don't** use the afterglow for anything but a changed reading: not age,
  availability, or successful Commands.
- **Don't** use broad decorative gradients or depend on color alone for meaning.
- **Don't** draw a radial gauge or a now marker.
- **Don't** restyle the focus ring.
- **Don't** animate a data card on hover or when a view opens. Only its controls
  respond to the pointer.
