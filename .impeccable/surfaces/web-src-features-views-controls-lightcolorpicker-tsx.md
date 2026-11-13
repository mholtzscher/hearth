---
version: 1
slug: "web-src-features-views-controls-lightcolorpicker-tsx"
primary_target: "web/src/features/views/controls/LightColorPicker.tsx"
related_targets: ["web/src/features/views/controls/ColorWheel.tsx", "web/src/features/views/cards/device/Device.tsx", "web/src/index.css"]
---

## Scope

Mode: Operate. Existing Device light cards, used on household dashboards,
in conversation, or in the canvas. The user chose the compact middle prototype
and requested implementation. Product authority remains `docs/product.md`;
this is a local addition, not a new visual world.

## Direction contract

THESIS: Keep routine power and brightness immediately available; open precise
color controls only when needed. Avoid permanently expanding every light card.

OWN-WORLD: Inherit Hearth's black/neutral frames, Figtree, violet focus and
selection, and orange arrival signal. Color spectra represent light settings,
not decorative backgrounds. Use the existing Radix popover/sheet conventions.

STORY: Choose white or color, preview an adjustment, and send on release or
explicit numeric submission. Observed State stays distinct from a request.
Pending/failure feedback remains accessible after closing.

FIRST VIEWPORT: A color-wheel edit button beside On/Off replaces the redundant
brightness percentage when a slider exists. The observed swatch stays passive.
Use existing control-status reservations for concise feedback; no added rows.
Desktop opens a bounded anchored popover; phone opens a focus-contained,
scrollable bottom sheet. The wheel, sliders, and exact forms stay inside it.

FORM: Local extension of Afterglow, inheriting seed:d43f0bf5d342cc25 from the app
brief. No direction roll. Capabilities determine available modes; native RGB is
exact and HS-only RGB conversion is stated.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance

## Documentation boundary

Preserve the incumbent global DESIGN.md and sidecar; no new identity or global
token changes. The component follows existing controls, card frames, typography,
and overlay rules. Its functional behavior is documented in `docs/dashboards.md`.
There are no shipping raster assets.

## Finish

Initial finish review: **superseded by user rejection of added card height**.
User approved the revised inline color-wheel mockup and production implementation.
Revised production finish review: **ship**, no material fixes. The reviewer
accepted the inline wheel, original-height cards, shared feedback reservations,
and visible Pending/Failed words when only color is writable. Browser checks
cover responsive overlays, complete request details, focus, and persistent
accessible failure descriptions. Pending/failure layout screenshots use
scripted HTTP outcomes, not Core failures.
Documentation and media: `docs/dashboards.md` and `docs/design/light-color-picker/`.
Global DESIGN.md and sidecar remain unchanged.
