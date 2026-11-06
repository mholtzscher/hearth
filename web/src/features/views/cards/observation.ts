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

/** Eligible card content: its observation time (or chart interval end) and formatted values. */
export type Arrival = { updatedAt: string; shown: string };

/**
 * Returns a key while a changed reading is glowing, and undefined otherwise.
 * A reading glows when newer data changes what the card shows, so a
 * sensor re-reporting the same value stays dark. The first reading a card
 * shows is not an arrival: opening a view never lights every card at once.
 */
export function useArrival(arrival: Arrival | undefined): number | undefined {
  const seen = useRef<{ at: number; shown: string } | undefined>(undefined);
  const [count, setCount] = useState(0);
  const [glowing, setGlowing] = useState<number>();
  const at = arrival === undefined ? NaN : Date.parse(arrival.updatedAt);
  const shown = arrival?.shown;
  useEffect(() => {
    if (!Number.isFinite(at) || shown === undefined) return;
    const previous = seen.current;
    if (previous && at > previous.at && shown !== previous.shown) setCount(n => n + 1);
    // A unit change re-renders the same observation; it updates what was shown without glowing.
    if (!previous || at >= previous.at) seen.current = { at, shown };
  }, [at, shown]);
  useEffect(() => {
    if (count === 0) return;
    setGlowing(count);
    const timer = setTimeout(() => setGlowing(undefined), ARRIVAL_GLOW_MS);
    return () => clearTimeout(timer);
  }, [count]);
  return glowing;
}
