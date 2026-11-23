import { useEffect, useState } from "react";

import { motionMilliseconds, prefersReducedMotion } from "../../../lib/motion";

/** How long a card's top edge glows after its displayed reading changes. */
export const ARRIVAL_GLOW_MS = 10_000;

const ageParts = (minutes: number) => {
  if (minutes < 60) {
    return [minutes, "minute", "m"] as const;
  }
  if (minutes < 24 * 60) {
    return [Math.floor(minutes / 60), "hour", "h"] as const;
  }
  return [Math.floor(minutes / (24 * 60)), "day", "d"] as const;
};

/** Compact ages stay on the card; full words name the same evidence in Source and accessible labels. */
export const observationAge = (
  observedAt: string,
  now: number,
  style: "short" | "long" = "short"
): string | undefined => {
  const minutes = Math.floor((now - Date.parse(observedAt)) / 60_000);
  if (!Number.isFinite(minutes)) {
    return undefined;
  }
  if (minutes < 1) {
    return "just now";
  }
  const [amount, unit, short] = ageParts(minutes);
  return style === "short"
    ? `${amount}${short} ago`
    : `${amount} ${unit}${amount === 1 ? "" : "s"} ago`;
};

/** Current time, refreshed once a minute so relative ages advance while a view stays open. */
export const useMinuteClock = (): number => {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 60_000);
    return () => clearInterval(timer);
  }, []);
  return now;
};

/** Displayed content and its evidence time, including ineligible readings so recovery cannot replay them. */
export interface Arrival {
  updatedAt: string;
  shown: string;
  eligible?: boolean;
}

interface ArrivalTrack {
  identity: string;
  seen: Map<string, { at: number; shown: string }>;
  sequence: number;
}

/** Compare each Entity independently: an older row can change while a newer row stays still. */
export const useArrivals = (
  arrivals: (Arrival & { key: string })[]
): number | undefined => {
  const [glowing, setGlowing] = useState<number>();
  const observed = arrivals.filter((item) =>
    Number.isFinite(Date.parse(item.updatedAt))
  );
  const eligible = observed.filter((item) => item.eligible !== false);
  const identity = JSON.stringify(observed);
  const [tracked, setTracked] = useState<ArrivalTrack>(() => ({
    identity,
    seen: new Map(
      observed.map((item) => [
        item.key,
        { at: Date.parse(item.updatedAt), shown: item.shown },
      ])
    ),
    sequence: 0,
  }));
  // Record each Entity's evidence while rendering the reading that carries it.
  // A displayed change starts one glow, and ineligible readings still advance
  // the record so recovery cannot replay a change that was already shown.
  if (tracked.identity !== identity) {
    const seen = new Map(tracked.seen);
    let changed = false;
    for (const item of observed) {
      const at = Date.parse(item.updatedAt);
      const previous = seen.get(item.key);
      if (
        item.eligible !== false &&
        previous !== undefined &&
        at > previous.at &&
        item.shown !== previous.shown
      ) {
        changed = true;
      }
      if (!previous || at >= previous.at) {
        seen.set(item.key, { at, shown: item.shown });
      }
    }
    const sequence = changed ? tracked.sequence + 1 : tracked.sequence;
    setTracked({ identity, seen, sequence });
    if (eligible.length === 0) {
      setGlowing(undefined);
    } else if (changed) {
      setGlowing(sequence);
    }
  }
  useEffect(() => {
    if (glowing === undefined) {
      return;
    }
    const ignition = prefersReducedMotion() ? 0 : motionMilliseconds("layout");
    const timer = setTimeout(
      () => setGlowing(undefined),
      ARRIVAL_GLOW_MS + ignition
    );
    return () => clearTimeout(timer);
  }, [glowing]);
  return eligible.length ? glowing : undefined;
};

/**
 * Returns a key while a changed reading is glowing, and undefined otherwise.
 * A reading glows when newer data changes what the card shows, so a
 * sensor re-reporting the same value stays dark. The first reading a card
 * shows is not an arrival: opening a view never lights every card at once.
 */
export const useArrival = (arrival: Arrival | undefined): number | undefined =>
  useArrivals(arrival ? [{ ...arrival, key: "reading" }] : []);
