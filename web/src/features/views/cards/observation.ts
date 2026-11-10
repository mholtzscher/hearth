import { useEffect, useRef, useState } from "react";

/** How long a card's top edge glows after its displayed reading changes. */
export const ARRIVAL_GLOW_MS = 10_000;

/** Compact ages stay on the card; full words name the same evidence in Source and accessible labels. */
export function observationAge(observedAt: string, now: number, style: "short" | "long" = "short"): string | undefined {
  const minutes = Math.floor((now - Date.parse(observedAt)) / 60_000);
  if (!Number.isFinite(minutes)) return undefined;
  if (minutes < 1) return "just now";
  const [amount, unit, short] = minutes < 60 ? [minutes, "minute", "m"] as const
    : minutes < 24 * 60 ? [Math.floor(minutes / 60), "hour", "h"] as const
    : [Math.floor(minutes / (24 * 60)), "day", "d"] as const;
  return style === "short" ? `${amount}${short} ago` : `${amount} ${unit}${amount === 1 ? "" : "s"} ago`;
}

/** Current time, refreshed once a minute so relative ages advance while a view stays open. */
export function useMinuteClock(): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 60_000);
    return () => clearInterval(timer);
  }, []);
  return now;
}

/** Displayed content and its evidence time, including ineligible readings so recovery cannot replay them. */
export type Arrival = { updatedAt: string; shown: string; eligible?: boolean };

/**
 * Returns a key while a changed reading is glowing, and undefined otherwise.
 * A reading glows when newer data changes what the card shows, so a
 * sensor re-reporting the same value stays dark. The first reading a card
 * shows is not an arrival: opening a view never lights every card at once.
 */
export function useArrival(arrival: Arrival | undefined): number | undefined {
  return useArrivals(arrival ? [{ ...arrival, key: "reading" }] : []);
}

/** Compare each Entity independently: an older row can change while a newer row stays still. */
export function useArrivals(arrivals: Array<Arrival & { key: string }>): number | undefined {
  const seen = useRef(new Map<string, { at: number; shown: string }>());
  const sequence = useRef(0);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const [glowing, setGlowing] = useState<number>();
  const observed = arrivals.filter(item => Number.isFinite(Date.parse(item.updatedAt)));
  const eligible = observed.filter(item => item.eligible !== false);
  const identity = JSON.stringify(observed);
  useEffect(() => {
    let changed = false;
    for (const item of observed) {
      const at = Date.parse(item.updatedAt), previous = seen.current.get(item.key);
      changed ||= item.eligible !== false && !!previous && at > previous.at && item.shown !== previous.shown;
      if (!previous || at >= previous.at) seen.current.set(item.key, { at, shown: item.shown });
    }
    if (eligible.length === 0) {
      clearTimeout(timer.current);
      setGlowing(undefined);
      return;
    }
    if (changed) {
      clearTimeout(timer.current);
      setGlowing(++sequence.current);
      timer.current = setTimeout(() => setGlowing(undefined), ARRIVAL_GLOW_MS);
    }
  }, [identity]);
  useEffect(() => () => clearTimeout(timer.current), []);
  return eligible.length ? glowing : undefined;
}
