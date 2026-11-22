import { useState } from "react";

import { getBaseUrl } from "../../../../api/client";
import { cn } from "../../../../lib/utils";
import type { Playback } from "./words";

/** A neutral speaker plate, drawn when there is no artwork to show. */
const SpeakerPlate = () => (
  <svg viewBox="0 0 64 64" className="block size-full" aria-hidden="true">
    <circle
      cx="32"
      cy="32"
      r="16"
      fill="none"
      stroke="var(--gray-5)"
      strokeWidth="1.5"
    />
    <circle
      cx="32"
      cy="32"
      r="5"
      fill="none"
      stroke="var(--gray-6)"
      strokeWidth="1.5"
    />
    <circle cx="32" cy="32" r="1.5" fill="var(--gray-7)" />
  </svg>
);

const ArtworkImage = ({ src }: { src: string }) => {
  const [failed, setFailed] = useState(false);
  return failed ? (
    <SpeakerPlate />
  ) : (
    <img
      src={src}
      alt=""
      width={64}
      height={64}
      className="block size-full object-cover"
      loading="lazy"
      decoding="async"
      onError={() => setFailed(true)}
    />
  );
};

/**
 * The current media's artwork, loaded from Core rather than the speaker's
 * server. It is decorative: the title and credits carry its meaning. A failed
 * load falls back to the speaker plate for that artwork key.
 */
export const MediaArtwork = ({
  entityId,
  artworkKey,
  dimmed,
}: {
  entityId: string;
  artworkKey?: string;
  dimmed: boolean;
}) => {
  const src = artworkKey
    ? `${getBaseUrl()}/v1/entities/${encodeURIComponent(entityId)}/artwork?v=${artworkKey}`
    : undefined;
  return (
    <div
      data-slot="media-artwork"
      data-dimmed={dimmed || undefined}
      className="border-gray-4 bg-gray-2 data-[dimmed]:border-gray-5 size-16 shrink-0 overflow-hidden rounded-md border data-[dimmed]:border-dashed data-[dimmed]:opacity-55"
    >
      {src ? <ArtworkImage key={src} src={src} /> : <SpeakerPlate />}
    </div>
  );
};

const barHeights = (playback: Playback | undefined, muted: boolean) => {
  if (playback === "playing" && !muted) {
    return ["h-2.5", "h-3.5", "h-2"];
  }
  if (playback === "paused") {
    return ["h-1.5", "h-1.5", "h-1.5"];
  }
  return ["h-1", "h-1", "h-1"];
};

/** A still three-bar mark beside the playback word; violet only while sound plays. */
export const PlaybackBars = ({
  playback,
  muted,
  dimmed,
}: {
  playback?: Playback;
  muted: boolean;
  dimmed: boolean;
}) => {
  const sounding = playback === "playing" && !muted && !dimmed;
  return (
    <span
      data-slot="playback-bars"
      data-sounding={sounding || undefined}
      aria-hidden="true"
      className="group/bars inline-flex h-3.5 shrink-0 items-end gap-0.5"
    >
      {barHeights(playback, muted).map((height, index) => (
        <span
          // The three bars are fixed and never reorder.
          key={index}
          className={cn(
            "bg-gray-6 group-data-[sounding]/bars:bg-violet w-0.5 rounded-xs",
            height
          )}
        />
      ))}
    </span>
  );
};
