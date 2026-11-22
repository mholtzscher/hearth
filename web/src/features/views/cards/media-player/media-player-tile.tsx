import { SharedControlStatus } from "../../controls/shared-control-status";
import { Reading } from "../reading";
import { TileFrame, tileActionClass } from "../tile-frame";
import { arrivalSignature, pressTarget, useMediaPlayer } from "./data";
import type { MediaPlayer } from "./data";
import type { MediaPlayerProps } from "./schema";
import { playbackWords } from "./words";
import type { PlaybackRequest } from "./words";

const pressWords: Record<PlaybackRequest, string> = {
  playing: "Press to play",
  paused: "Press to pause",
  idle: "Press to stop",
};

const trackLine = (player: MediaPlayer) => {
  const state = player.observed;
  if (!state || state.playback === "idle" || !state.title) {
    return "Nothing playing";
  }
  return state.artist ? `${state.title} · ${state.artist}` : state.title;
};

const detailLine = (player: MediaPlayer, target?: PlaybackRequest) => {
  const state = player.observed;
  if (state?.grouping === "grouped") {
    return "Grouped · ungroup to control";
  }
  const volume = player.shown.volumePercent;
  const parts = [
    volume === undefined ? undefined : `${volume} %`,
    player.shown.muted ? "Muted" : undefined,
    target ? pressWords[target] : undefined,
  ].filter(Boolean);
  return parts.length > 0 ? parts.join(" · ") : " ";
};

/**
 * A whole-card play/pause press for grids of quick switches. Each row is one
 * fixed line, so a press never changes the tile's height.
 */
export const MediaPlayerTile = ({ props }: { props: MediaPlayerProps }) => {
  const player = useMediaPlayer(props);
  const { observed, status, title, enabled } = player;
  const target = pressTarget(player);
  const word = observed
    ? playbackWords[player.shown.playback ?? observed.playback]
    : "—";
  const playing = observed?.playback === "playing" && status.value === "live";
  const rowClass = "m-0 h-[21px] truncate text-label";
  const reading = (
    <Reading as="span" value={status.value} className="block truncate">
      {word}
    </Reading>
  );
  return (
    <TileFrame
      title={title}
      classNames={{ body: "gap-0.5" }}
      on={playing}
      interactive={target !== undefined}
      status={status.entries}
      observedAt={player.observedAt}
      arrival={
        player.observedAt
          ? {
              updatedAt: player.observedAt,
              shown: arrivalSignature(player),
              eligible: status.glows,
            }
          : undefined
      }
      sources={player.sources}
    >
      {target ? (
        <button
          type="button"
          data-slot="tile-action"
          className={tileActionClass}
          aria-label={`${title}, ${word.toLowerCase()}, ${trackLine(player)}. ${pressWords[target]}`}
          disabled={!enabled}
          onClick={() => player.setPlayback(target)}
        >
          {reading}
        </button>
      ) : (
        <p>{reading}</p>
      )}
      <p
        data-slot="media-track"
        title={observed?.title}
        className={`${rowClass} text-foreground`}
      >
        {trackLine(player)}
      </p>
      <p data-slot="media-detail" className={`${rowClass} text-gray-8`}>
        {detailLine(player, target)}
      </p>
      <SharedControlStatus
        controls={player.statusControls.filter(
          (control) => control.target.operation === "set_playback"
        )}
      />
    </TileFrame>
  );
};
