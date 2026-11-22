import { ApiError } from "../../../../api/client";
import {
  entityMetadata,
  entityStateRequest,
  readEntity,
} from "../../bindings/entity";
import type { FieldEntity } from "../../bindings/entity";
import { readErrorMessage } from "../../bindings/history";
import type { Resolved } from "../../bindings/types";
import type { ControlState, ControlTarget } from "../../controls/queue";
import type { StatusControl } from "../../controls/shared-control-status";
import { supportsOperation } from "../../controls/support";
import { useControl, useFastRefresh, useRead } from "../../data/provider";
import type { SourceItem } from "../data-card";
import { cardStatus } from "../status";
import type { CardStatus } from "../status";
import type { Anchor } from "./playhead";
import type { MediaPlayerProps } from "./schema";
import {
  mutePending,
  playbackPending,
  seekPending,
  skipPending,
  volumePending,
} from "./words";
import type { Playback, PlaybackRequest } from "./words";

/** The hearth.mediaplayer/v1 State the card reads. */
export interface MediaPlayerState {
  playback: Playback;
  grouping: "standalone" | "grouped" | "unknown";
  title?: string;
  artist?: string;
  album?: string;
  duration_seconds?: number;
  position_seconds?: number;
  position_anchored_at?: string;
  artwork_url?: string;
  volume_percent?: number;
  muted?: boolean;
}

export interface MediaPlayerData {
  state: MediaPlayerState;
  observed_at: string;
}

const working = (state: ControlState) =>
  state.phase === "pending" || state.phase === "confirming";

const requested = <T>(state: ControlState, key: string): T | undefined => {
  if (
    !working(state) ||
    typeof state.requested !== "object" ||
    state.requested === null
  ) {
    return undefined;
  }
  return (state.requested as Record<string, T>)[key];
};

// The read validates the one always-present field; the whole State comes back with it.
const playbackSource = (props: MediaPlayerProps) => ({
  ...props.source,
  field: "/playback",
});

const resolve = (
  props: MediaPlayerProps,
  snapshot: ReturnType<typeof useRead>
): { entity?: FieldEntity; resource: Resolved<MediaPlayerData> } => {
  const missing =
    snapshot.error instanceof ApiError && snapshot.error.status === 404;
  let status: Resolved<MediaPlayerData>["status"] = "loading";
  if (missing) {
    status = "missing";
  } else if (snapshot.error) {
    status = "error";
  }
  if (snapshot.data === undefined || missing) {
    return { resource: { status, error: readErrorMessage(snapshot.error) } };
  }
  try {
    // Canonical values keep seconds and percent unconverted.
    const entity = readEntity(
      snapshot.data,
      playbackSource(props),
      "canonical"
    );
    const state = entity.state?.value as MediaPlayerState | undefined;
    return {
      entity,
      resource: {
        status: entity.state && state ? "ready" : "unobserved",
        data:
          entity.state && state
            ? { state, observed_at: entity.state.observed_at }
            : undefined,
        metadata: entityMetadata(entity, props.source),
        error: readErrorMessage(snapshot.error),
        refreshFailed: !!snapshot.error,
      },
    };
  } catch {
    return {
      resource: {
        status: "error",
        error: "The resource returned incompatible data.",
      },
    };
  }
};

/** The playback States set_playback accepts on this speaker. */
const playbackStates = (entity: FieldEntity | undefined): PlaybackRequest[] => {
  const support = entity?.support as
    | { operations?: { set_playback?: { states?: unknown } } }
    | undefined;
  const states = support?.operations?.set_playback?.states;
  return Array.isArray(states)
    ? states.filter(
        (state): state is PlaybackRequest =>
          state === "idle" || state === "playing" || state === "paused"
      )
    : [];
};

const useOperation = (
  entityId: string,
  operation: ControlTarget["operation"],
  entity: FieldEntity | undefined,
  policy: "queue" | "replace" = "queue"
) => {
  const [state, request] = useControl({ entityId, operation }, policy);
  return {
    supported: !!entity && supportsOperation(entity, operation),
    state,
    request,
  };
};

/** A cache key for the artwork route that changes when the artwork does. */
export const artworkKey = (url: string): string => {
  // djb2 modulo 2^32: short and stable; not a security boundary.
  let hash = 5381;
  for (let index = 0; index < url.length; index += 1) {
    hash = (hash * 33 + (url.codePointAt(index) ?? 0)) % 2 ** 32;
  }
  return hash.toString(16);
};

/** The adapter's playhead anchor, when the speaker reports one. */
const anchorOf = (state: MediaPlayerState | undefined): Anchor | undefined =>
  state?.position_seconds !== undefined &&
  state.position_anchored_at !== undefined
    ? {
        seconds: state.position_seconds,
        anchoredAt: state.position_anchored_at,
      }
    : undefined;

/** The shared status line's controls, with words for each pending request. */
const mediaStatusControls = (entityId: string): StatusControl[] => [
  {
    target: { entityId, operation: "set_playback" },
    name: "Playback",
    pending: (value) =>
      playbackPending[(value as { state: PlaybackRequest }).state],
  },
  {
    target: { entityId, operation: "set_volume" },
    name: "Volume",
    pending: (value) => volumePending((value as { percent: number }).percent),
  },
  {
    target: { entityId, operation: "set_muted" },
    name: "Mute",
    pending: (value) => mutePending((value as { muted: boolean }).muted),
  },
  {
    target: { entityId, operation: "next_track" },
    name: "Next",
    pending: () => skipPending("next"),
  },
  {
    target: { entityId, operation: "previous_track" },
    name: "Previous",
    pending: () => skipPending("previous"),
  },
  {
    target: { entityId, operation: "seek" },
    name: "Seek",
    pending: (value) =>
      seekPending((value as { position_seconds: number }).position_seconds),
  },
];

/**
 * Reads one speaker and drives its controls. Controls show what was requested
 * while a Command is in flight; readings keep observed State.
 */
export const useMediaPlayer = (props: MediaPlayerProps) => {
  const request = entityStateRequest(playbackSource(props));
  const { entity, resource } = resolve(props, useRead(request));
  const status: CardStatus = cardStatus(resource);
  const entityId = props.source.entity_id;
  const playback = useOperation(entityId, "set_playback", entity);
  const volume = useOperation(entityId, "set_volume", entity, "replace");
  const mute = useOperation(entityId, "set_muted", entity);
  const next = useOperation(entityId, "next_track", entity);
  const previous = useOperation(entityId, "previous_track", entity);
  const seek = useOperation(entityId, "seek", entity, "replace");
  const controls = [playback, volume, mute, next, previous, seek];
  useFastRefresh(
    request,
    controls.some((control) => working(control.state))
  );

  const observed = status.value === "empty" ? undefined : resource.data?.state;
  const states = playbackStates(entity);
  const shownPlayback =
    requested<PlaybackRequest>(playback.state, "state") ?? observed?.playback;
  const { metadata } = resource;

  const title = props.label ?? metadata?.label ?? "Media player";
  const sources: SourceItem[] = [
    {
      entityId,
      label: metadata?.label ?? title,
      availability: metadata?.availability,
      enabled: metadata?.enabled,
    },
  ];
  const statusControls = mediaStatusControls(entityId);

  return {
    status,
    title,
    sources,
    enabled: entity?.enabled ?? false,
    observedAt: resource.data?.observed_at,
    observed,
    anchor: anchorOf(observed),
    shown: {
      playback: shownPlayback,
      volumePercent:
        requested<number>(volume.state, "percent") ?? observed?.volume_percent,
      muted: requested<boolean>(mute.state, "muted") ?? observed?.muted,
      seekTarget: requested<number>(seek.state, "position_seconds"),
    },
    can: {
      play: states.includes("playing"),
      pause: states.includes("paused"),
      stop: states.includes("idle"),
      volume: volume.supported,
      mute: mute.supported,
      skip: next.supported && previous.supported,
      seek: seek.supported,
    },
    setPlayback: (state: PlaybackRequest) => playback.request({ state }),
    setVolume: (percent: number) => volume.request({ percent }),
    setMuted: (muted: boolean) => mute.request({ muted }),
    skip: (direction: "next" | "previous") =>
      (direction === "next" ? next : previous).request({}),
    seek: (seconds: number) => seek.request({ position_seconds: seconds }),
    statusControls,
  };
};

export type MediaPlayer = ReturnType<typeof useMediaPlayer>;

/** The primary playback action, if the speaker supports it in this state. */
export const pressTarget = (
  player: MediaPlayer
): PlaybackRequest | undefined => {
  const { shown, can } = player;
  if (shown.playback === "playing") {
    if (can.pause) {
      return "paused";
    }
    return can.stop ? "idle" : undefined;
  }
  return can.play ? "playing" : undefined;
};

/** What the afterglow compares: the playhead and artwork move without new data. */
export const arrivalSignature = (player: MediaPlayer) => {
  const state = player.observed;
  return state
    ? [
        state.playback,
        state.title,
        state.artist,
        state.volume_percent,
        state.muted,
      ].join("|")
    : "";
};
