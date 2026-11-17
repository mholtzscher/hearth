---
name: Hearth
description: A dark, evidence-led workspace for a connected household.
---

# Design System: Hearth

## Overview

**Creative North Star: "Afterglow"**

Hearth is a quiet, dark-only household workspace: true black ground, untinted
gray structure, and small, deliberate signals of voice, action, and freshness.
Readings and their evidence lead; ornament stays subordinate to the work.
The household workspace has room to read at a glance. Development uses the same
visual language and controls at a higher information density.

Afterglow makes a change visible without pretending that old data is bad data.
Some sensors report only on change or at long intervals, so age alone does not
establish freshness or availability. A changed reading briefly lights an orange
edge; separate text explains its age and availability.

Follow [GLOSSARY.md](GLOSSARY.md): State, Entity availability, and Command
outcome are distinct facts, not interchangeable status labels. The
[dashboard coding standards](CODING_STANDARDS.md#dashboard-design-system)
govern implementation. The source of truth for current palette, type, radius,
and motion tokens is [the theme](web/src/index.css); shared controls and their
examples live in [components](web/src/components/).

## Colors

Use the untinted gray ramp for ground, surfaces, borders, and text. Cards sit
above true black; fields, popovers, and dialogs can use a raised gray. Keep
supporting text quieter than the reading without making it illegible.

Violet is Hearth's voice: agent identity, focus, links, selection, and the
selected part of a control. Use the lighter violet text color for readable
selected text. Orange identifies the primary action and the afterglow on a
changed reading. Primary action text is dark for contrast. Disabled primary
actions turn gray rather than dim orange. Use outline controls for filters,
pagination, and secondary actions.

Status colors communicate success, warning, error, and informational or
in-progress states without borrowing the brand hues. Pair every status color
with meaningful words. Chart series have their own ordered palette; orange is
not a series color. Charts and trends draw no now marker; the time axis ends at
the window's end.

**The Evidence Rule.** Afterglow marks newer data that changes what a card
shows. It does not assert age, availability, or Command success. Never rely on
brand color or a status dot alone to convey those facts.

## Typography

Figtree Variable is self-hosted, with a sans-serif fallback. Opaque identifiers
and raw structured data may use monospace. Use the theme's named type sizes
rather than copying a scale into individual components.

Readings lead the hierarchy, with tabular figures and smaller, quieter units.
Compact rows use a separate reading step, not a shrunken single-reading card.
Metric, State, and ToggleTile use the narrow reading step below 200px of content
width. Long words wrap rather than forcing the reading scale smaller.
Headlines identify canvases and libraries; labels and supporting copy remain
subordinate. The workspace base is denser than conversation prose.

## Layout

On desktop, navigation sits beside the workspace. A conversation column shares
the page with an open canvas; on its own, the conversation is centered while
reply prose keeps a shorter measure. Canvas and Development content have a
bounded width rather than spreading indefinitely.

At 721–980px, and at 981–1279px when a canvas is open, navigation becomes a
rail with recent conversations in a drawer. At 720px and below, one top bar
opens the full navigation drawer. On Home with a canvas open, the Conversation
and Canvas switch replaces the page name; the two views occupy the available
width separately. The phone canvas hides Expand and moves available save and
discard actions to a bottom bar; it does not show a disabled Save.

The phone layout respects safe areas and the space left by the on-screen
keyboard. Keep the top bar visible and the composer above the keyboard without
adding home-indicator padding under it. Household controls commonly provide a
44px minimum touch target. Development retains compact diagnostic controls;
below 981px a native select replaces its page links, while Entity jump and
Connection settings remain available.

Reading grids normally keep a 248px minimum card width and become one column
on phones. Grids consisting only of Metric and State cards, or only of
ToggleTiles and tile-layout AirPurifiers and Covers, may use a 160px minimum,
including on phones. Requested columns are a ceiling. Metering tiles span the
full phone row so their history and additional readings remain legible.

Development and the dashboard library share the dashboard's bounded content
width and page-header treatment. Diagnostic facts wrap values below labels when
space is limited; tables use sentence-case headings. A named scroll area enters
the Tab order only when it overflows. Library loading frames retain ready-card
height; a load failure shows its notice rather than an empty state.

Recent conversations group by local calendar date and update at midnight.
Long lists offer search; Escape clears it and an empty result says so. On
phones, search keeps Recent visible above the keyboard while focused or while
a query exists. Truncated conversation previews have full-text tooltips.

## Elevation & Depth

Surfaces are flat by default. Tonal separation and fine borders establish
hierarchy against black. Modal dialogs and navigation drawers are exceptions:
their shadows and dark backdrops separate them from the workspace.

Canvas and drawer transitions are short and eased. Honor reduced motion by
removing workspace transitions and animation. Afterglow has a specific
reduced-motion alternative described below.

## Shapes

Cards share a neutral border, rounded corners, and a near-black surface.
Development cards use tighter padding. Controls and household navigation use
smaller corners, and dialogs use a slightly larger radius. Gradients are
reserved for thin horizon lines, not broad decorative fills.

## Components

Use the shared [controls](web/src/components/) and their colocated stories for
current variants and dimensions. Use [Card](web/src/components/card.tsx) for
card-shaped surfaces and [DataCard](web/src/features/views/cards/data-card.tsx)
for the view-card frame. The composer, sent messages, and unsupported view
content are not cards. Feature-specific card implementations and examples live
with [view cards](web/src/features/views/cards/).

### Buttons, fields, and selection

Shared controls offer compact sizes for fine pointers and larger touch sizes.
Icon-only buttons need accessible names. Focus is a 2px violet outline with a
3px offset, including when a shared button renders a link. Invalid fields use a
destructive border; disabled non-primary controls use reduced opacity.

Segmented controls use individually focusable, native pressed buttons in a
named group. Long labels wrap. A value matching no option leaves every segment
unselected. Native selects keep browser selection behavior; textareas use their
native rows for initial height and do not automatically grow. Brightness and
color picker inputs remain specialized native controls.

### Dialogs and popovers

Decision dialogs use the shared [Dialog](web/src/components/dialog.tsx).
Modal dialogs contain focus, dismiss on Escape, and return focus to their
registered trigger. Programmatically opened dialogs must preserve their own
return target. Non-modal dialogs do not trap focus or use an overlay. A bottom
placement becomes a safe-area-aware, scrollable sheet on phones; short desktop
viewports can scroll the dialog body.

Use modal `forceMount` only during an intentional exit transition. A closed,
force-mounted modal still retains its overlay, scroll lock, and assistive
technology hiding; `hidden` on the content does not release them. Remove
`forceMount` or unmount when the transition ends.

The expanded canvas is an exception to the shared dialog: it stays in place
and retains `useModalFocus`. Moving its editor into a dialog portal remounts
the editor and loses invalid, unsaved input.

Notices grow with their contents. Use `role="alert"` for errors and
`role="status"` for non-urgent feedback; the caller owns recovery actions.

### View cards

Every data card in a view uses one frame, regardless of card type. A named
article contains a heading, status entries, body, and a footer with time and
Source where applicable. Its title is one heading level below the view
heading. The host sets the hierarchy; cards are not landmarks.

Status entries wrap beneath the title when needed. Each entry pairs a tone dot
with words. A ready card from an available Entity has no status entry unless
another condition, such as a failed refresh, applies.
The footer stays at the bottom of stretched cards. Time and Source share a
non-wrapping row; opening Source does not change the grid height.

A single reading states its observation age. A multi-reading card states the
oldest displayed reading, not the newest, and keeps individual row statuses.
Charts do not invent a card-level observation time: their interval and method
are the timing evidence. Narrow cards may omit the visible Observed or Oldest
prefix while retaining the full accessible time label.

Source gives the exact local observation time, age, Entity links and IDs, and
availability and enablement when known. For multiple sources, it identifies
each Entity's facts; for charts, it also explains interval and method. Source
opens in an anchored non-modal popover on larger screens and a modal bottom
sheet on phones. Escape and Close return focus to Source. Links close the
details before navigation so an unsaved-changes dialog can open. Its touch
target expands without increasing card height. See
[CardSource](web/src/features/views/cards/card-source.tsx) and its
[story](web/src/features/views/cards/card-source.stories.tsx).

#### Card states

Show all applicable entries in precedence order. Keep status, State, and
availability separate. The exact entries, tones, and ordering are owned by
[card status](web/src/features/views/cards/status.ts).

| Situation | Treatment |
| --- | --- |
| Loading, missing Entity, or request error | Show the relevant status and an empty value. Missing and error are errors; loading is neutral. |
| Unavailable or disabled | Name the condition and retain the last value in a dimmed treatment. Neither glows. |
| Refresh failed | Warn that the last reading is shown. An eligible changed arrival can still glow. |
| Availability unknown | State that availability is unknown; do not imply it is available. |
| Never observed or field absent | Explain the difference in words and show an empty value. |
| Ready and available | Show the value without an entry for readiness. Other applicable statuses remain; eligible changes can glow. |

In charts, statuses belong to their series' legend entries. Unavailable or
disabled series use a dimmed, dashed line without a band. A failed card keeps
its frame, title, error, and recovery guidance. Unsupported content instead uses
a dashed outline without a card surface; a loading placeholder keeps the
height of a ready card.

#### Controls

Offer controls based on Entity support, not on declarations in the card
definition. A read-only Entity has no press target. Display observed State as
the reading; a pending control may show the requested value, but must not
present it as observed. A whole-card ToggleTile keeps Source as an independent
control and changes its selected surface only after observed State changes.

Reserve feedback space so a fast Command does not move the card. Cards with
more than two controls use one shared line for the most recently changed
control. On failure, return the control to observed State and name the problem.
After a timeout or lost connection, say the change may still apply. Slider
focus appears on its thumb. Brightness ranges from 1 to 100%; only the power
switch turns a light off.

For an AirPurifier, name the PM2.5 category in words as a reading, not a
status; dim it with unavailable or disabled data. Warn when filter life is at
or below 10%. When a mode other than Manual drives the fan, retain the manual
level and say so rather than presenting it as active.
Only PM2.5 and Filter life light an AirPurifier's afterglow.

Cover opening can require a second press: its temporary Confirm is the primary
orange action. Stop reports **Sending Stop…**, then **Stop sent.** If the cover
still reports motion after three seconds, warn that Stop was sent but motion
continues. Cover tiles are an exception to growing status rows: they keep
fixed-height rows, with full status text in a tooltip and for screen readers.
ToggleList's bulk-off feedback counts observed Off values and individual
failures; it does not claim a transactional multi-Device result. Its since
lines describe recorded State changes, not observation age.

### Afterglow

When newer data changes what an on-screen card shows, an orange top edge fades
over ten seconds. Each later change restarts it. The first displayed reading,
a same-value report, or a unit-system change without newer data does not glow.
Same-value reports do not extend a glow. Only ready data from an available,
enabled Entity qualifies. A chart compares the latest plotted values on a
history refresh; State refreshes alone do not trigger its glow. A changed row
in a ReadingList or Device card lights its bottom rule by the same rule.

Under reduced motion, the edge remains visible for ten seconds without fading,
then disappears. Observation age advances in text; Source gives the exact time.
Neither an old age nor the absence of glow means an Entity is unavailable.
This replaced an age-based orange-to-violet line, which misled readers about
infrequently reporting sensors and required a key on every view.
The [arrival implementation](web/src/features/views/cards/observation.ts)
owns the comparison and timing details.

### Charts

Preserve gaps rather than implying samples exist. Use round value and local
time ticks; identify the first tick of a day when a window crosses midnight.
When history begins well into a window, say when it starts without shortening
the axis. Tooltips use plain words and units.

A chart is one Tab stop with a text summary per series; the drawing is hidden
from assistive technology. Explain aggregation, ranges, and gaps in Source,
not in a repeated card caption. Metric trends remain neutral; the Rain and
Pressure trend treatments are exceptions. See the
[time-series implementation and stories](web/src/features/views/cards/time-series/).

Metric ranges use a linear track, not a radial gauge. Pin an out-of-range
marker to the end and state Above or Below in words. The trend's range sentence
is its text equivalent, and its drawing is hidden from assistive technology.

Station weather cards describe measured conditions, not inferred sky conditions.
Forecast cards show modeled predictions and are the exception that depicts sky
conditions. Wind's arrow points in the direction the wind blows; calm uses a
ring, and unavailable direction retains its last arrow in a dashed treatment.
Derived words and values dim when their source reading dims. State timelines
and week patterns distinguish missing history from an observed false value.
Their compact layouts retain words and status evidence when space is limited.
Weather cards use available card width, not grid column count, to choose
between side-by-side details and a narrow stacked layout.

### Conversation

Replies, inline views, tables, and the composer share a left edge; user messages
are content-width bubbles aligned right. Inline views have no outer card frame.
Their title and Open in canvas action share a header; on narrow containers the
visible action may shorten to Open while its accessible name stays complete.
Pending and Showing in canvas previews retain their bordered treatment.

The composer places Send in a row below the text, alongside the selected view
and editor note rather than floating between lines. A selected view or Automation
chip opens the canvas; its close action follows the canvas's unsaved-change
confirmation. Both parts have touch targets. Sent messages retain a plain,
non-interactive record of the attached view.

### Inputs and navigation

Navigation uses quiet text, neutral active backgrounds, and a violet active
icon. On phones it moves into the drawer. When the server is not ready, the
menu's status dot is accompanied by a status in its accessible name and by
words inside the drawer. Status chips likewise pair words with tone.

## Do's and Don'ts

- **Do** preserve true black, neutral grays, and the shared reading hierarchy.
- **Do** use violet for Hearth's voice and orange for the main action and
  changed-reading arrival.
- **Do** state status, availability, age, and Command outcomes in words.
- **Don't** use orange for chart series or tint disabled primary actions orange.
- **Don't** treat age, absence of glow, and availability as the same fact.
- **Don't** rely on color alone or add broad decorative gradients.
