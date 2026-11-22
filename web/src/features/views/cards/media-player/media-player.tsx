import { Link2 } from "lucide-react";

import { SharedControlStatus } from "../../controls/shared-control-status";
import { DataCard } from "../data-card";
import { MediaArtwork, PlaybackBars } from "./artwork";
import { TimelineRow, TransportRow, VolumeRow } from "./controls";
import { arrivalSignature, artworkKey, useMediaPlayer } from "./data";
import type { MediaPlayer, MediaPlayerState } from "./data";
import { MediaPlayerTile } from "./media-player-tile";
import { usePlayhead } from "./playhead";
import type { MediaPlayerProps } from "./schema";
import { creditLine, playbackWords } from "./words";

/** What the header shows about the media; an idle speaker shows none. */
const mediaText = (state: MediaPlayerState | undefined) => {
  if (!state || state.playback === "idle") {
    return {};
  }
  return {
    title: state.title,
    credits: creditLine(state.artist, state.album),
    artwork: state.artwork_url ? artworkKey(state.artwork_url) : undefined,
  };
};

const TrackHeader = ({ player }: { player: MediaPlayer }) => {
  const { observed, status, shown } = player;
  const dimmed = status.value === "dimmed";
  const { title, credits, artwork } = mediaText(observed);
  const grouped = observed?.grouping === "grouped";
  return (
    <div className="flex items-start gap-3.5">
      <MediaArtwork
        entityId={player.sources[0].entityId}
        artworkKey={artwork}
        dimmed={dimmed}
      />
      <div className="min-w-0 flex-1">
        <p className="text-caption flex items-center gap-2">
          <PlaybackBars
            playback={observed?.playback}
            muted={shown.muted ?? false}
            dimmed={dimmed}
          />
          <span
            data-slot="media-playback"
            data-playback={dimmed ? "dimmed" : observed?.playback}
            className="text-gray-9 data-[playback=dimmed]:text-gray-8 data-[playback=playing]:text-violet-text font-medium"
          >
            {observed ? playbackWords[observed.playback] : "—"}
          </span>
          {grouped && (
            <span
              title="Ungroup to control"
              className="text-muted-foreground inline-flex items-center gap-1"
            >
              <Link2 className="size-3" aria-hidden="true" />
              Grouped
              <span className="sr-only"> · Ungroup to control</span>
            </span>
          )}
        </p>
        <p
          data-slot="media-title"
          data-value={title ? status.value : "empty"}
          title={title}
          className="text-label text-foreground data-[value=dimmed]:text-gray-8 data-[value=empty]:text-muted-foreground mt-1 truncate font-medium"
        >
          {title ?? "Nothing playing"}
        </p>
        <p
          data-slot="media-credits"
          title={credits}
          className="text-label text-muted-foreground h-[21px] truncate"
        >
          {credits}
        </p>
      </div>
    </div>
  );
};

/**
 * The Transport Deck: artwork and track, the playhead, transport, and volume.
 * Every row is reserved from support alone, so playing, skipping, and seeking
 * never change the card's height.
 */
export const MediaPlayerCard = ({ props }: { props: MediaPlayerProps }) => {
  const player = useMediaPlayer(props);
  const { observed, status, title } = player;
  const playhead = usePlayhead({
    anchor: player.anchor,
    playing: observed?.playback === "playing",
    durationSeconds: observed?.duration_seconds,
  });
  return (
    <DataCard
      title={title}
      className="@container"
      classNames={{ body: "grid grid-cols-1 gap-3" }}
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
      <TrackHeader player={player} />
      <TimelineRow player={player} playhead={playhead} />
      <TransportRow player={player} />
      {(player.can.volume || player.can.mute) && <VolumeRow player={player} />}
      <SharedControlStatus controls={player.statusControls} />
    </DataCard>
  );
};

export const BoundMediaPlayer = (props: MediaPlayerProps) =>
  props.layout === "tile" ? (
    <MediaPlayerTile props={props} />
  ) : (
    <MediaPlayerCard props={props} />
  );
