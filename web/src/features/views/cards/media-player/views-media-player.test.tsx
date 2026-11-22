import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { entityId, fixtureNow } from "../../../../../.storybook/fixtures";
import {
  mediaPlayerEntities,
  playingTrack,
} from "../../../../../.storybook/media-player-fixtures";
import type { MediaPlayerFixture } from "../../../../../.storybook/media-player-fixtures";
import { pairedEntityResponse } from "../../../../../.storybook/paired-fixtures";
import { setBaseUrl } from "../../../../api/client";
import { ViewDataProvider } from "../../data/provider";
import { BoundMediaPlayer } from "./media-player";
import type { MediaPlayerProps } from "./schema";

const id = entityId(601);
const slot = (name: string) => document.querySelector(`[data-slot="${name}"]`);
const tick = (ms = 0) => act(() => vi.advanceTimersByTimeAsync(ms));

const mount = async (
  fixture: Partial<MediaPlayerFixture> = {},
  layout?: MediaPlayerProps["layout"]
) => {
  const core = {
    state: playingTrack,
    observedAt: playingTrack.position_anchored_at,
    id,
    ...fixture,
  };
  const posts: unknown[] = [];
  vi.spyOn(globalThis, "fetch").mockImplementation((input, init) => {
    if (String(input).endsWith("/commands")) {
      posts.push(JSON.parse(String(init?.body)));
      return Promise.resolve(
        Response.json({ command_id: "cmd", status: "dispatched" })
      );
    }
    const { display, canonical } = mediaPlayerEntities(core);
    return Promise.resolve(
      Response.json(pairedEntityResponse(display, canonical))
    );
  });
  render(
    <MemoryRouter>
      <ViewDataProvider>
        <BoundMediaPlayer
          layout={layout}
          source={{
            kind: "entity-state",
            entity_id: id,
            entity_type: "hearth.mediaplayer/v1",
          }}
        />
      </ViewDataProvider>
    </MemoryRouter>
  );
  await tick();
  return { core, posts };
};

describe("MediaPlayer card", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date(fixtureNow));
  });
  afterEach(() => {
    cleanup();
    setBaseUrl("");
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it("ticks without afterglow", async () => {
    await mount();
    expect(slot("media-elapsed")?.textContent).toBe("2:28");
    await tick(1000);
    expect(slot("media-elapsed")?.textContent).toBe("2:29");
    expect(slot("card-arrival")).toBeNull();
  });

  it("glows for a newer track title but not for a corrected anchor", async () => {
    const { core } = await mount();
    core.state = {
      ...playingTrack,
      position_seconds: 40,
      position_anchored_at: new Date().toISOString(),
    };
    core.observedAt = core.state.position_anchored_at;
    await tick(10_000);
    expect(slot("media-elapsed")?.textContent).toBe("0:50");
    expect(slot("card-arrival")).toBeNull();

    core.state = { ...core.state, title: "New track" };
    core.observedAt = new Date().toISOString();
    await tick(10_000);
    expect(slot("media-title")?.textContent).toBe("New track");
    expect(slot("card-arrival")).not.toBeNull();
  });

  it("loads artwork from the configured Core and falls back when it fails", async () => {
    const base = "https://core.example";
    setBaseUrl(base);
    await mount();
    const artwork = slot("media-artwork");
    const image = artwork?.querySelector("img");
    expect(image?.getAttribute("src")).toContain(
      `${base}/v1/entities/${id}/artwork?v=`
    );
    if (!image) {
      throw new Error("Expected artwork");
    }
    fireEvent.error(image);
    expect(artwork?.querySelector("img")).toBeNull();
    expect(artwork?.querySelector("svg")).not.toBeNull();
  });

  it("retries failed artwork when a later track brings it back", async () => {
    const { core } = await mount();
    const artwork = slot("media-artwork");
    const first = artwork?.querySelector("img");
    if (!first) {
      throw new Error("Expected artwork");
    }
    fireEvent.error(first);
    expect(artwork?.querySelector("img")).toBeNull();
    core.state = {
      ...playingTrack,
      artwork_url: `${playingTrack.artwork_url}&size=256`,
    };
    await tick(10_000);
    expect(artwork?.querySelector("img")?.src).not.toBe(first.src);
    core.state = playingTrack;
    await tick(10_000);
    expect(artwork?.querySelector("img")?.src).toBe(first.src);
  });

  it("does not send unsupported playback requests", async () => {
    const { posts } = await mount({
      playbackStates: ["playing"],
      operations: ["set_playback"],
    });
    const button = screen.getByRole<HTMLButtonElement>("button", {
      name: "Play Living room",
    });
    expect(button.disabled).toBeTruthy();
    fireEvent.click(button);
    await tick();
    expect(posts).toHaveLength(0);
    expect(screen.queryByRole("button", { name: /next track/iu })).toBeNull();
    expect(screen.queryByRole("slider")).toBeNull();
  });

  it("commits the latest slider value after keyboard changes settle", async () => {
    const { posts } = await mount();
    const volume = screen.getByRole("slider", { name: "Living room volume" });
    fireEvent.change(volume, { target: { value: "45" } });
    await tick(250);
    fireEvent.change(volume, { target: { value: "48" } });
    await tick(499);
    expect(posts).toHaveLength(0);
    await tick(1);
    expect(posts).toStrictEqual([
      { operation: "set_volume", parameters: { percent: 48 } },
    ]);
  });

  it("lets the tile playhead control and Source act independently", async () => {
    const { posts } = await mount({}, "tile");
    fireEvent.click(screen.getByRole("button", { name: "Source" }));
    expect(posts).toHaveLength(0);
    fireEvent.click(
      screen.getByRole("button", { name: /Living room, playing/u })
    );
    await tick();
    expect(posts).toStrictEqual([
      { operation: "set_playback", parameters: { state: "paused" } },
    ]);
  });
});
