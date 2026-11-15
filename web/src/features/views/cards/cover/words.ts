import type { CommandFailure } from "../../../../api/commands";

export type CoverMotion = "idle" | "opening" | "closing";

/** The State in words: Closed at 0 %, Open at 100 %, Partly open between, or the reported motion. */
export const coverWord = (position: number, motion: CoverMotion): string => {
  if (motion === "opening") {
    return "Opening";
  }
  if (motion === "closing") {
    return "Closing";
  }
  if (position <= 0) {
    return "Closed";
  }
  return position >= 100 ? "Open" : "Partly open";
};

/** Tiles are too narrow for Partly open at the reading scale; their position row carries the percentage. */
export const coverTileWord = (position: number, motion: CoverMotion): string =>
  motion === "idle" && position > 0 ? "Open" : coverWord(position, motion);

/** The request, never the reported motion: the reading keeps the cover's own words. */
export const pendingWords = (position: number): string => {
  if (position >= 100) {
    return "Opening…";
  }
  if (position <= 0) {
    return "Closing…";
  }
  return `Moving to ${position} %…`;
};

/** A failed position Command: full words for the card, short words for a tile's one-line row. */
export const coverFailures: Record<
  CommandFailure["kind"],
  { text: string; short: string }
> = {
  disabled: { text: "This Entity is disabled.", short: "Disabled." },
  unreachable: {
    text: "Couldn't reach this Device.",
    short: "Couldn't reach it.",
  },
  rejected: { text: "Change rejected. Try again.", short: "Rejected." },
  unconfirmed: {
    text: "No report of that position in time. It may still be moving.",
    short: "No report in time.",
  },
  network: {
    text: "Couldn't reach Hearth. The change may still apply.",
    short: "Couldn't reach Hearth.",
  },
  invalid: {
    text: "Couldn't change this cover.",
    short: "Couldn't change it.",
  },
  "not-found": {
    text: "Couldn't change this cover.",
    short: "Couldn't change it.",
  },
  internal: {
    text: "Couldn't change this cover.",
    short: "Couldn't change it.",
  },
};
