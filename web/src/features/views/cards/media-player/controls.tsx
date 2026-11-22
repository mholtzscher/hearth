import {
  Pause,
  Play,
  SkipBack,
  SkipForward,
  Square,
  Volume1,
  Volume2,
  VolumeX,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import type { CSSProperties } from "react";

import { Button } from "../../../../components/button";
import { Input } from "../../../../components/input";
import { cn } from "../../../../lib/utils";
import { sliderClasses } from "../../controls/slider-styles";
import { pressTarget } from "./data";
import type { MediaPlayer } from "./data";
import { formatDuration } from "./playhead";
import type { PlaybackRequest } from "./words";

/** Keyboard steps send once they settle for this long, as the brightness slider does. */
const KEYBOARD_SETTLE_MS = 500;
/** The timeline is a 24px track on a fine pointer and a 44px target on touch. */
const timelineHeight = "h-6 pointer-coarse:h-11";

const clamp = (value: number, min: number, max: number) =>
  Math.min(max, Math.max(min, value));

/**
 * A native range on the shared Input with BrightnessSlider geometry. The
 * value follows the pointer locally and is sent once on release; keyboard
 * steps send after they settle.
 */
const RangeTrack = ({
  label,
  value,
  max,
  step,
  valueText,
  disabled,
  quiet,
  className,
  onPreview,
  onCommit,
}: {
  label: string;
  value: number;
  max: number;
  step: number;
  valueText: (value: number) => string;
  disabled: boolean;
  quiet?: boolean;
  className?: string;
  onPreview: (value: number | undefined) => void;
  onCommit: (value: number) => void;
}) => {
  const [draft, setDraft] = useState<number>();
  const dragging = useRef(false);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);
  const shown = clamp(draft ?? value, 0, max);
  const commit = (next: number | undefined) => {
    clearTimeout(timer.current);
    timer.current = undefined;
    setDraft(undefined);
    onPreview(undefined);
    if (next !== undefined && next !== value) {
      onCommit(next);
    }
  };
  return (
    <Input
      type="range"
      size="touch"
      min={0}
      max={max}
      step={step}
      value={shown}
      aria-label={label}
      aria-valuetext={valueText(shown)}
      disabled={disabled}
      style={
        { "--fill": `${(shown / Math.max(1, max)) * 100}%` } as CSSProperties
      }
      className={cn(
        "border-0 bg-transparent px-0 py-0 disabled:opacity-100",
        sliderClasses,
        quiet && "[--knob:var(--gray-8)] [--level:var(--gray-6)]",
        className
      )}
      onPointerDown={() => {
        dragging.current = true;
        clearTimeout(timer.current);
      }}
      onPointerUp={() => {
        dragging.current = false;
        commit(draft);
      }}
      onPointerCancel={() => {
        dragging.current = false;
        commit(undefined);
      }}
      onChange={(event) => {
        const next = Number(event.currentTarget.value);
        setDraft(next);
        onPreview(next);
        if (dragging.current) {
          return;
        }
        clearTimeout(timer.current);
        timer.current = setTimeout(() => commit(next), KEYBOARD_SETTLE_MS);
      }}
      onBlur={() => {
        if (timer.current !== undefined) {
          commit(draft);
        }
      }}
    />
  );
};

const VolumeIcon = ({
  percent,
  muted,
}: {
  percent?: number;
  muted?: boolean;
}) => {
  if (muted || percent === 0) {
    return <VolumeX aria-hidden="true" />;
  }
  return (percent ?? 0) < 40 ? (
    <Volume1 aria-hidden="true" />
  ) : (
    <Volume2 aria-hidden="true" />
  );
};

const volumeReadout = (percent: number | undefined, muted: boolean) => {
  if (percent === undefined) {
    return "—";
  }
  return muted ? `${percent} % · Muted` : `${percent} %`;
};

/** The volume slider with its Mute toggle; either appears only when supported. */
export const VolumeRow = ({ player }: { player: MediaPlayer }) => {
  const [preview, setPreview] = useState<number>();
  const { shown, can, title, enabled } = player;
  const muted = shown.muted ?? false;
  const percent = preview ?? shown.volumePercent;
  const handleVolume = (value: number) => player.setVolume(value);
  return (
    <div
      data-slot="media-volume"
      className="border-border grid gap-1 border-t pt-3"
    >
      <div className="text-label text-muted-foreground flex items-baseline justify-between gap-2">
        <span>Volume</span>
        <span
          data-slot="media-volume-value"
          data-muted={muted || undefined}
          className="text-gray-9 data-[muted]:text-gray-8 text-right"
        >
          {volumeReadout(percent, muted)}
        </span>
      </div>
      <div className="flex items-center gap-2.5">
        {can.mute && (
          <Button
            variant="outline"
            size="icon-touch"
            type="button"
            aria-pressed={muted}
            aria-label={muted ? `Unmute ${title}` : `Mute ${title}`}
            disabled={!enabled}
            onClick={() => player.setMuted(!muted)}
            className="aria-pressed:border-gray-5 aria-pressed:text-muted-foreground touch-manipulation"
          >
            <VolumeIcon percent={percent} muted={muted} />
          </Button>
        )}
        {can.volume && (
          <div className="min-w-0 flex-1">
            <RangeTrack
              label={`${title} volume`}
              value={shown.volumePercent ?? 0}
              max={100}
              step={1}
              valueText={(value) =>
                muted ? `${value} percent, muted` : `${value} percent`
              }
              disabled={!enabled || shown.volumePercent === undefined}
              quiet={muted}
              onPreview={setPreview}
              onCommit={handleVolume}
            />
          </div>
        )}
      </div>
    </div>
  );
};

/**
 * Elapsed and total time over a track. It is reserved even with nothing to
 * show, so a track without a duration never changes the card's height. It
 * seeks only for supported media with a known duration.
 */
export const TimelineRow = ({
  player,
  playhead,
}: {
  player: MediaPlayer;
  playhead?: number;
}) => {
  const [preview, setPreview] = useState<number>();
  const { observed, shown, can, title, enabled } = player;
  const duration =
    observed?.playback === "idle" ? undefined : observed?.duration_seconds;
  const position = preview ?? shown.seekTarget ?? playhead;
  const handleSeek = (value: number) => player.seek(value);
  const known = duration !== undefined && position !== undefined;
  const fill = known ? (clamp(position, 0, duration) / duration) * 100 : 0;
  return (
    <div data-slot="media-timeline" className="grid gap-0.5">
      {can.seek && known ? (
        <RangeTrack
          label={`${title} playback position`}
          value={shown.seekTarget ?? playhead ?? 0}
          max={duration}
          step={1}
          valueText={(value) =>
            `${formatDuration(value)} of ${formatDuration(duration)}`
          }
          disabled={!enabled}
          className={timelineHeight}
          onPreview={setPreview}
          onCommit={handleSeek}
        />
      ) : (
        <div
          className={cn("flex items-center", timelineHeight)}
          aria-hidden="true"
        >
          <div className="bg-gray-3 relative h-1 w-full overflow-hidden rounded-xs">
            <div
              className="bg-gray-9 absolute inset-y-0 left-0 rounded-xs"
              style={{ width: `${fill}%` }}
            />
          </div>
        </div>
      )}
      <div className="text-caption text-muted-foreground flex justify-between gap-2">
        <span data-slot="media-elapsed">
          {known ? formatDuration(position) : "—:—"}
        </span>
        <span data-slot="media-duration">
          {duration === undefined ? "—:—" : formatDuration(duration)}
        </span>
      </div>
    </div>
  );
};

const actions = {
  playing: { label: "Play", Icon: Play },
  paused: { label: "Pause", Icon: Pause },
  idle: { label: "Stop", Icon: Square },
};

const PrimaryTransportButton = ({
  player,
  target,
  compact,
}: {
  player: MediaPlayer;
  target?: PlaybackRequest;
  compact: boolean;
}) => {
  const { label, Icon } =
    actions[target ?? (player.can.play ? "playing" : "paused")];
  return (
    <Button
      variant="outline"
      size="touch"
      type="button"
      data-playing={player.shown.playback === "playing" || undefined}
      aria-label={`${label} ${player.title}`}
      disabled={!player.enabled || !target}
      onClick={() => {
        if (target) {
          player.setPlayback(target);
        }
      }}
      className="data-[playing]:border-violet data-[playing]:bg-violet-soft data-[playing]:text-violet-text data-[playing]:hover:bg-violet-soft min-w-0 flex-1 touch-manipulation px-3 @max-[245px]:px-0"
    >
      <Icon aria-hidden="true" />
      <span className={cn("truncate", compact && "@max-[245px]:hidden")}>
        {label}
      </span>
    </Button>
  );
};

/**
 * Previous, Play or Pause, Next, and Stop at 44px. Without pause support the
 * main button stops a playing speaker instead.
 */
export const TransportRow = ({ player }: { player: MediaPlayer }) => {
  const { can, title, enabled, observed } = player;
  const idle = observed?.playback === "idle";
  const hasPrimary = can.play || can.pause;
  const stopButton = can.stop && (can.pause || !can.play);
  if (!hasPrimary && !stopButton && !can.skip) {
    return null;
  }
  const target = pressTarget(player);
  const primaryTarget = stopButton && target === "idle" ? undefined : target;
  return (
    <fieldset
      aria-label={`${title} playback controls`}
      className="m-0 flex min-w-0 gap-2 border-0 p-0"
    >
      {can.skip && (
        <Button
          variant="outline"
          size="icon-touch"
          type="button"
          aria-label={`Previous track on ${title}`}
          disabled={!enabled || idle}
          onClick={() => player.skip("previous")}
          className="touch-manipulation"
        >
          <SkipBack aria-hidden="true" />
        </Button>
      )}
      {hasPrimary && (
        <PrimaryTransportButton
          player={player}
          target={primaryTarget}
          compact={can.skip && stopButton}
        />
      )}
      {can.skip && (
        <Button
          variant="outline"
          size="icon-touch"
          type="button"
          aria-label={`Next track on ${title}`}
          disabled={!enabled || idle}
          onClick={() => player.skip("next")}
          className="touch-manipulation"
        >
          <SkipForward aria-hidden="true" />
        </Button>
      )}
      {stopButton && (
        <Button
          variant="outline"
          size="icon-touch"
          type="button"
          aria-label={`Stop ${title}`}
          disabled={!enabled || idle}
          onClick={() => player.setPlayback("idle")}
          className="touch-manipulation"
        >
          <Square aria-hidden="true" />
        </Button>
      )}
    </fieldset>
  );
};
