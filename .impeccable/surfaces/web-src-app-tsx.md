---
version: 1
slug: "web-src-app-tsx"
primary_target: "web/src/App.tsx"
related_targets: ["web/src/index.css"]
---

## Scope

The whole web app: Home (conversation and canvas), Dashboards, and the Development pages. Mode: Operate. Dark-only, used in the evening at a desk or on a phone.

## Audience and job

Household members and the technical self-hoster read live house state, generate views in conversation, and save dashboards. The Development pages inspect entities, devices, automations and commands. Pinned by the user: true black (OLED) backgrounds, purple and orange.

## Direction contract

THESIS: Hearth's surfaces are night sky. A changed reading arrives as afterglow: the card's top edge lights orange and fades over a minute; age and availability are stated in text. This refuses the gray dashboard with a single blue accent, and the neon synthwave glow.

OWN-WORLD: True #000 background and one numbered, untinted gray ramp for surfaces, borders and text. Violet is Hearth's voice: the agent, the selected view, focus, links. Orange is the one action to take: Save, Send, the primary button. Status uses its own green, amber and red-pink, never the brand hues. Gradients exist only as one- or two-pixel horizon lines.

STORY: The household sees immediately what is current, what is aging and what is gone, and always knows the one thing to press.

FIRST VIEWPORT: Black sidebar (brand, New conversation, Home/Dashboards/Development, Recent, server status) | conversation column with a violet agent mark | canvas with title, an unsaved state in violet, toolbar with orange Save, three reading cards at one fixed numeral scale whose top edge glows when a reading changes, and a 24 h chart whose "now" edge glows orange.

FORM: Afterglow, #4 on the grounded list. seed:d43f0bf5d342cc25

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance

## Kept discipline (raises)

- One numbered gray ramp replaces the ad hoc hex grays (from the exposure-record world).
- No tinted grays; violet and orange are the only brand hues (from the bitmap-specimen world).
- One fixed numeral scale across reading cards (from the botanical-folio world).
- On phones one view owns the screen, with actions within thumb reach (from the vertical-feed world).

## Resolved decisions

- Typeface: Figtree (replacing Geist).
- Freshness: an arrival glow that fades over 60 s, only when a newer observation changes the shown value, replaced the age line (fully orange under 5 minutes, violet by 60). Chosen 2026-11-06 from a prototype comparing the age line, no line, and the arrival glow.
