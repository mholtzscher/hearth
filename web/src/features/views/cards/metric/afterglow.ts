import { useEffect, useState } from "react";

/** Readings this recent show a fully orange afterglow line. */
export const AFTERGLOW_FRESH_MS = 5 * 60_000;
/** Readings this old have cooled to violet. */
export const AFTERGLOW_COOLED_MS = 60 * 60_000;

/**
 * Returns how much orange remains in a reading's afterglow line: 1 within five
 * minutes of observation, falling linearly to 0 at one hour. It describes the
 * age of the observation only; availability decides whether a line shows at all.
 */
export function afterglow(observedAt: string, now: number): number {
  const age = now - Date.parse(observedAt);
  if (!Number.isFinite(age)) return 0;
  if (age <= AFTERGLOW_FRESH_MS) return 1;
  if (age >= AFTERGLOW_COOLED_MS) return 0;
  return 1 - (age - AFTERGLOW_FRESH_MS) / (AFTERGLOW_COOLED_MS - AFTERGLOW_FRESH_MS);
}

/**
 * Names an observation's age in words, so the afterglow line's meaning is also
 * stated in text. Older than a day, the absolute observation time says enough.
 */
export function observationAge(observedAt: string, now: number): string | undefined {
  const minutes = Math.floor((now - Date.parse(observedAt)) / 60_000);
  if (!Number.isFinite(minutes)) return undefined;
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes} min ago`;
  if (minutes < 24 * 60) return `${Math.floor(minutes / 60)} h ago`;
  return undefined;
}

/** Current time, refreshed once a minute so afterglow lines cool while a view stays open. */
export function useMinuteClock(): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 60_000);
    return () => clearInterval(timer);
  }, []);
  return now;
}
