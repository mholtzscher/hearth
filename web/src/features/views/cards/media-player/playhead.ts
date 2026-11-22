import { useEffect, useState } from "react";

/** An adapter anchor: the playhead was `seconds` at `anchoredAt`. */
export interface Anchor {
  seconds: number;
  anchoredAt: string;
}

/**
 * The playhead `now`: it advances from the anchor only while playing and
 * never passes the duration. This is computed, never an observation.
 */
export const playheadAt = ({
  anchor,
  playing,
  durationSeconds,
  now,
}: {
  anchor: Anchor;
  playing: boolean;
  durationSeconds?: number;
  now: number;
}): number => {
  const elapsed = playing
    ? Math.max(0, (now - Date.parse(anchor.anchoredAt)) / 1000)
    : 0;
  const position = Math.floor(anchor.seconds + elapsed);
  return durationSeconds === undefined
    ? position
    : Math.min(position, durationSeconds);
};

/**
 * The playhead to show, ticking once a second while playing. A paused or
 * stopped speaker does not tick, so it causes no re-renders.
 */
export const usePlayhead = ({
  anchor,
  playing,
  durationSeconds,
}: {
  anchor?: Anchor;
  playing: boolean;
  durationSeconds?: number;
}): number | undefined => {
  const [now, setNow] = useState(() => Date.now());
  const ticking = playing && anchor !== undefined;
  useEffect(() => {
    if (!ticking) {
      return;
    }
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [ticking]);
  if (!anchor) {
    return undefined;
  }
  return playheadAt({
    anchor,
    playing,
    durationSeconds,
    now: ticking ? now : Date.parse(anchor.anchoredAt),
  });
};

/** m:ss, or h:mm:ss from an hour, in tabular-friendly form. */
export const formatDuration = (totalSeconds: number): string => {
  const seconds = Math.max(0, Math.floor(totalSeconds));
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const rest = String(seconds % 60).padStart(2, "0");
  return hours > 0
    ? `${hours}:${String(minutes).padStart(2, "0")}:${rest}`
    : `${minutes}:${rest}`;
};
