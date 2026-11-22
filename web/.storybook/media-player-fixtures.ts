import { bypass, delay, http, HttpResponse } from "msw";

import type { FieldEntity } from "../src/features/views/bindings/entity";
import type { Quantity } from "../src/features/views/bindings/quantity";
import type { MediaPlayerState } from "../src/features/views/cards/media-player/data";
import { playheadAt } from "../src/features/views/cards/media-player/playhead";
import { pairedEntityResponse } from "./paired-fixtures";

/** Synthetic hearth.mediaplayer/v1 fixtures; Commands prove no speaker output. */
export interface MediaPlayerFixture {
  id: string;
  name?: string;
  state: MediaPlayerState | null;
  operations?: string[];
  playbackStates?: string[];
  enabled?: boolean;
  availability?: string;
  observedAt?: string;
}

const percent: Quantity = { unit: "%", label: "%", fraction_digits: 0 };
const secondQuantity: Quantity = { unit: "s", label: "s", fraction_digits: 0 };

export const allMediaOperations = [
  "set_playback",
  "set_volume",
  "set_muted",
  "next_track",
  "previous_track",
  "seek",
];

/** A track a few minutes in, anchored at the fixture clock. */
export const playingTrack: MediaPlayerState = {
  playback: "playing",
  grouping: "standalone",
  title: "Nocturne in F-Sharp Minor",
  artist: "Nils Frahm",
  album: "All Melody",
  duration_seconds: 284,
  position_seconds: 118,
  position_anchored_at: "2026-11-09T10:00:00Z",
  artwork_url:
    "http://10.0.0.5:8095/imageproxy/0f3c1d2e4b5a69788796a5b4c3d2e1f00f3c1d2e4b5a69788796a5b4c3d2e1f0?fmt=png&size=512",
  volume_percent: 42,
  muted: false,
};

const setPlayback = (
  state: MediaPlayerState,
  playback: MediaPlayerState["playback"],
  now: string
): MediaPlayerState => {
  if (playback === "idle") {
    return {
      playback,
      grouping: state.grouping,
      volume_percent: state.volume_percent,
      muted: state.muted,
    };
  }
  const { position_seconds: seconds, position_anchored_at: anchoredAt } = state;
  if (seconds === undefined || anchoredAt === undefined) {
    return { ...state, playback };
  }
  return {
    ...state,
    playback,
    position_seconds: playheadAt({
      anchor: { seconds, anchoredAt },
      playing: state.playback === "playing",
      durationSeconds: state.duration_seconds,
      now: Date.parse(now),
    }),
    position_anchored_at: now,
  };
};

/** The paired projections Core returns; seconds and percent never convert. */
export const mediaPlayerEntities = ({
  id,
  name = "Living room",
  state,
  operations = allMediaOperations,
  playbackStates = ["idle", "playing", "paused"],
  enabled = true,
  availability = "available",
  observedAt = "2026-11-09T10:00:00Z",
}: MediaPlayerFixture): { display: FieldEntity; canonical: FieldEntity } => {
  const quantities: Record<string, Quantity> = {};
  if (state?.volume_percent !== undefined) {
    quantities["/volume_percent"] = percent;
  }
  if (state?.duration_seconds !== undefined) {
    quantities["/duration_seconds"] = secondQuantity;
  }
  if (state?.position_seconds !== undefined) {
    quantities["/position_seconds"] = secondQuantity;
  }
  const entity: FieldEntity = {
    id,
    name,
    type: "hearth.mediaplayer/v1",
    enabled,
    availability: { status: availability },
    support: {
      state: {},
      operations: Object.fromEntries(
        operations.map((operation) => [
          operation,
          operation === "set_playback" ? { states: playbackStates } : {},
        ])
      ),
    },
    quantities,
    state: state ? { value: state, observed_at: observedAt } : null,
  };
  return { display: entity, canonical: entity };
};

/**
 * The State a Command leaves and its outcome. Skip and seek are dispatched,
 * as Core reports them: the change arrives with the next Observation.
 */
export const applyMediaPlayerCommand = (
  state: MediaPlayerState,
  operation: string,
  parameters: Record<string, unknown>,
  now: string
):
  | { state: MediaPlayerState; status: "satisfied" | "dispatched" }
  | undefined => {
  switch (operation) {
    case "set_playback": {
      const playback = parameters.state as MediaPlayerState["playback"];
      return {
        status: "satisfied",
        state: setPlayback(state, playback, now),
      };
    }
    case "set_volume": {
      return {
        status: "satisfied",
        state: { ...state, volume_percent: Number(parameters.percent) },
      };
    }
    case "set_muted": {
      return {
        status: "satisfied",
        state: { ...state, muted: Boolean(parameters.muted) },
      };
    }
    case "seek": {
      return {
        status: "dispatched",
        state: {
          ...state,
          position_seconds: Number(parameters.position_seconds),
          position_anchored_at: now,
        },
      };
    }
    case "next_track":
    case "previous_track": {
      return {
        status: "dispatched",
        state: {
          ...state,
          title: operation === "next_track" ? "Solstice Drift" : "Says",
          artist:
            operation === "next_track" ? "Kaitlyn Aurelia Smith" : "Nils Frahm",
          album: operation === "next_track" ? "EARS" : "Spaces",
          duration_seconds: 236,
          position_seconds: 0,
          position_anchored_at: now,
        },
      };
    }
    default: {
      return undefined;
    }
  }
};

/** Serves the synthetic sleeve for every artwork request. */
export const artworkHandler = http.get("/v1/entities/:id/artwork", async () => {
  const image = await fetch(
    bypass(new URL("/media-artwork.png", location.href))
  );
  return new HttpResponse(await image.arrayBuffer(), {
    headers: { "Content-Type": "image/png", ETag: '"fixture"' },
  });
});

/**
 * Story-local Core for one speaker. Commands are applied, rejected, or held
 * open; synthetic State changes only in this story.
 */
export const mediaPlayerCore = (
  fixture: MediaPlayerFixture,
  outcome: "apply" | "reject" | "hold" = "apply"
) => {
  let current = fixture;
  let sequence = 0;
  return [
    artworkHandler,
    http.get(`/v1/entities/${fixture.id}`, () => {
      const { display, canonical } = mediaPlayerEntities(current);
      return HttpResponse.json(pairedEntityResponse(display, canonical));
    }),
    http.post(`/v1/entities/${fixture.id}/commands`, async ({ request }) => {
      const body = (await request.json()) as {
        operation: string;
        parameters: Record<string, unknown>;
      };
      if (outcome === "hold") {
        await delay("infinite");
      }
      if (outcome === "reject") {
        return HttpResponse.json({ detail: "Rejected" }, { status: 502 });
      }
      sequence += 1;
      const now = new Date().toISOString();
      const next =
        current.state &&
        applyMediaPlayerCommand(
          current.state,
          body.operation,
          body.parameters,
          now
        );
      if (!next) {
        return HttpResponse.json({ detail: "Unsupported" }, { status: 400 });
      }
      current = {
        ...current,
        state: next.state,
        observedAt: new Date(Date.parse(now) + sequence).toISOString(),
      };
      return HttpResponse.json(
        next.status === "dispatched"
          ? { command_id: `cmd_story_media_${sequence}`, status: "dispatched" }
          : {
              command_id: `cmd_story_media_${sequence}`,
              status: "satisfied",
              observation_id: `obs_story_media_${sequence}`,
              value: body.parameters,
            }
      );
    }),
  ];
};
