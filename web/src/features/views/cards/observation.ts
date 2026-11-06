import { useEffect, useRef, useState } from "react";

/** How long a card's top edge glows after a new observation arrives. */
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

/**
 * Returns a key while a newer observation than the last one this card showed
 * is glowing, and undefined otherwise. The first observation a card shows is
 * not an arrival: opening a view never lights every card at once.
 */
export function useArrival(observedAt: string | undefined): number | undefined {
  const seen = useRef<number | undefined>(undefined);
  const [count, setCount] = useState(0);
  const [glowing, setGlowing] = useState<number>();
  useEffect(() => {
    const at = observedAt === undefined ? NaN : Date.parse(observedAt);
    if (!Number.isFinite(at)) return;
    if (seen.current !== undefined && at > seen.current) setCount(n => n + 1);
    if (seen.current === undefined || at > seen.current) seen.current = at;
  }, [observedAt]);
  useEffect(() => {
    if (count === 0) return;
    setGlowing(count);
    const timer = setTimeout(() => setGlowing(undefined), ARRIVAL_GLOW_MS);
    return () => clearTimeout(timer);
  }, [count]);
  return glowing;
}
