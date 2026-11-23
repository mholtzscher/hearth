import { formatDuration } from "./playhead";

export type Playback = "idle" | "playing" | "paused" | "unknown";
export type PlaybackRequest = Exclude<Playback, "unknown">;

export const playbackWords: Record<Playback, string> = {
  idle: "Idle",
  playing: "Playing",
  paused: "Paused",
  unknown: "Unknown",
};

export const playbackPending: Record<PlaybackRequest, string> = {
  idle: "Stopping…",
  playing: "Starting playback…",
  paused: "Pausing…",
};

export const volumePending = (percent: number) =>
  `Setting volume to ${percent} %…`;

export const mutePending = (muted: boolean) =>
  muted ? "Muting…" : "Unmuting…";

export const skipPending = (direction: "next" | "previous") =>
  direction === "next" ? "Skipping to the next track…" : "Going back…";

export const seekPending = (seconds: number) =>
  `Seeking to ${formatDuration(seconds)}…`;

/** Artist and album as one line, or undefined when neither is known. */
export const creditLine = (artist?: string, album?: string) => {
  const parts = [artist, album].filter(Boolean);
  return parts.length > 0 ? parts.join(" · ") : undefined;
};
