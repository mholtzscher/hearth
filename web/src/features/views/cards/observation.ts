import { useEffect, useRef, useState } from "react";

/** How long a card's top edge glows after its displayed reading changes. */
export const ARRIVAL_GLOW_MS = 60_000;

/**
 * Names an observation's age in words beside its time. Older than a day, the
 * absolute observation time says enough.
 */
export function observationAge(observedAt: string, now: number): string | undefined {
  const minutes = Math.floor((now - Date.parse(observedAt)) / 60_000);
  if (!Number.isFinite(minutes)) return undefined;
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes} min ago`;
  if (minutes < 24 * 60) return `${Math.floor(minutes / 60)} h ago`;
  return undefined;
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
