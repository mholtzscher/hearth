/**
 * Token names shown on the Design system pages. Names only: every value the
 * pages display is read at runtime from the live CSS in `index.css`.
 */
import { TEXT_TOKENS } from "../lib/utils";

export const GRAY_RAMP = [
  "gray-0",
  "gray-1",
  "gray-2",
  "gray-3",
  "gray-4",
  "gray-5",
  "gray-6",
  "gray-7",
  "gray-8",
  "gray-9",
  "gray-10",
] as const;

/** Where each gray step is used, from DESIGN.md Colors. */
export const GRAY_USE: Partial<Record<(typeof GRAY_RAMP)[number], string>> = {
  "gray-0": "Ground",
  "gray-1": "Cards",
  "gray-2": "Dialogs, popovers",
  "gray-3": "Borders",
  "gray-4": "Field borders",
  "gray-7": "Disabled text",
  "gray-8": "Quiet text",
  "gray-10": "Ink",
};

export const BRAND = [
  { token: "violet", use: "Focus, selection, the agent" },
  { token: "violet-text", use: "Links, selected view" },
  { token: "violet-soft", use: "Selected segments" },
  { token: "orange", use: "Primary action, afterglow" },
  { token: "orange-hover", use: "Primary hover" },
  { token: "on-orange", use: "Text on orange" },
] as const;

export const STATUS = [
  { token: "ok", alias: "success", use: "Success" },
  { token: "warn", alias: "warning", use: "Warning" },
  { token: "error", alias: "destructive", use: "Error" },
  { token: "info", alias: undefined, use: "Pending, informational" },
] as const;

/** Role aliases in `index.css` and the token each resolves to. */
export const ROLE_ALIASES = [
  { alias: "background", token: "gray-0" },
  { alias: "card", token: "gray-1" },
  { alias: "popover", token: "gray-2" },
  { alias: "muted", token: "gray-2" },
  { alias: "border", token: "gray-3" },
  { alias: "secondary", token: "gray-3" },
  { alias: "accent", token: "gray-3" },
  { alias: "input", token: "gray-4" },
  { alias: "muted-foreground", token: "gray-8" },
  { alias: "foreground", token: "gray-10" },
  { alias: "primary", token: "orange" },
  { alias: "primary-foreground", token: "on-orange" },
  { alias: "ring", token: "violet" },
  { alias: "link", token: "violet-text" },
  { alias: "success", token: "ok" },
  { alias: "warning", token: "warn" },
  { alias: "destructive", token: "error" },
] as const;

/** Chart series in order; DESIGN.md names the hue and its source token. */
export const CHART_SERIES = [
  { variable: "chart-1", name: "Ink", token: "gray-10" },
  { variable: "chart-2", name: "Violet", token: "violet" },
  { variable: "chart-3", name: "Orchid", token: "chart-orchid" },
  { variable: "chart-4", name: "Dusk blue", token: "info" },
  { variable: "chart-5", name: "Pale gold", token: "chart-pale-gold" },
  { variable: "chart-6", name: "Gray", token: "gray-7" },
  { variable: "chart-7", name: "Ice", token: "chart-ice" },
  { variable: "chart-8", name: "Sand", token: "chart-sand" },
] as const;

export { TEXT_TOKENS };
export type TextToken = (typeof TEXT_TOKENS)[number];

/** Where each type size is used, from DESIGN.md Typography. */
export const TYPE_USE: Record<TextToken, string> = {
  reading: "A card's single reading",
  "reading-narrow": "Metric, State, ToggleTile below 200px",
  headline: "Canvas, library, and Development page titles",
  "headline-narrow": "Page titles on phones",
  title: "View and section headings",
  body: "Conversation text",
  label: "Workspace base: cards, fields, buttons, tables",
  "compact-reading": "Row values in lists and ledgers",
  caption: "Notices, Recent date headings",
};

export const RADII = ["md", "card"] as const;
export type Radius = (typeof RADII)[number];

export const RADIUS_USE: Record<Radius, string> = {
  md: "Controls: buttons, fields, notices, press targets",
  card: "Surfaces: cards, popovers, composer, modal",
};

export const BREAKPOINTS = [
  { variable: "breakpoint-tablet", name: "tablet", use: "Rail sidebar" },
  { variable: "breakpoint-desktop", name: "desktop", use: "Full sidebar" },
] as const;
