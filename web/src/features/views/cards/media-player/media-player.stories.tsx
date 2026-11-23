import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fireEvent, userEvent, waitFor } from "storybook/test";

import { entityId } from "../../../../../.storybook/fixtures";
import {
  mediaPlayerCore,
  playingTrack,
} from "../../../../../.storybook/media-player-fixtures";
import type { MediaPlayerFixture } from "../../../../../.storybook/media-player-fixtures";
import {
  expectNoHorizontalOverflow,
  watchHeight,
} from "../../../../../.storybook/play";
import { BoundMediaPlayer } from "./media-player";

const id = entityId(601);
const speaker = (fixture: Omit<MediaPlayerFixture, "id">) => ({
  id,
  ...fixture,
});

const screenWidth = (width: number) => `${width}px`;

const meta = {
  title: "Components/Cards/MediaPlayer",
  component: BoundMediaPlayer,
  tags: ["ai-generated"],
  args: {
    label: "Living room",
    layout: "card",
    source: {
      kind: "entity-state",
      entity_id: entityId(601),
      entity_type: "hearth.mediaplayer/v1",
    },
  },
  decorators: [
    (Story, { parameters }) => (
      <div
        className="w-full"
        style={{ maxWidth: screenWidth(parameters.cardWidth ?? 340) }}
      >
        <Story />
      </div>
    ),
  ],
  beforeEach: ({ msw }) => {
    msw.use(...mediaPlayerCore(speaker({ state: playingTrack })));
  },
  parameters: {
    docs: {
      description: {
        component:
          "A speaker's Transport Deck: artwork, track, playhead, transport, and volume, or a whole-card play/pause tile. Fixtures are synthetic; Commands are simulated and prove no speaker output.",
      },
    },
  },
} satisfies Meta<typeof BoundMediaPlayer>;
export default meta;
type Story = StoryObj<typeof meta>;

const card = (canvasElement: HTMLElement) => {
  const article = canvasElement.querySelector("article");
  if (!article) {
    throw new Error("Expected a card article");
  }
  return article;
};

/** A playing track: pausing and muting never change the card's height. */
export const Playing: Story = {
  play: async ({ canvas, canvasElement }) => {
    await expect(
      await canvas.findByText("Nocturne in F-Sharp Minor")
    ).toBeVisible();
    await expect(canvas.getByText("Nils Frahm · All Melody")).toBeVisible();
    // MockDate holds the clock 30 s after the 1:58 anchor.
    await expect(canvas.getByText("2:28")).toBeVisible();
    await waitFor(async () => {
      const image = card(canvasElement).querySelector("img");
      await expect(image?.naturalWidth).toBeGreaterThan(0);
    });
  },
};

const exerciseTransport: NonNullable<Story["play"]> = async ({
  canvas,
  canvasElement,
}) => {
  await canvas.findByText("Nocturne in F-Sharp Minor");
  const frame = card(canvasElement);
  const body = frame.querySelector('[data-slot="card-body"]');
  if (!body) {
    throw new Error("Expected card body");
  }
  await expect(body.scrollWidth).toBeLessThanOrEqual(body.clientWidth);
  await watchHeight(frame, async () => {
    await userEvent.click(
      canvas.getByRole("button", { name: "Pause Living room" })
    );
    await canvas.findByText("Paused");
    const seek = canvas.getByRole("slider", {
      name: "Living room playback position",
    });
    await fireEvent.pointerDown(seek);
    await fireEvent.input(seek, { target: { value: "45" } });
    await fireEvent.pointerUp(seek);
    await waitFor(async () => {
      await expect(seek).toHaveAttribute("aria-valuetext", "0:45 of 4:44");
      await expect(canvas.getByText("0:45")).toBeVisible();
    });
    await userEvent.click(
      canvas.getByRole("button", { name: "Next track on Living room" })
    );
    await canvas.findByText("Solstice Drift");
    await userEvent.click(
      canvas.getByRole("button", { name: "Previous track on Living room" })
    );
    await canvas.findByText("Says");
    const volume = canvas.getByRole("slider", { name: "Living room volume" });
    await fireEvent.pointerDown(volume);
    await fireEvent.input(volume, { target: { value: "60" } });
    await fireEvent.pointerUp(volume);
    await canvas.findByText("60 %");
    await userEvent.click(
      canvas.getByRole("button", { name: "Mute Living room" })
    );
    await canvas.findByText("60 % · Muted");
    await userEvent.click(
      canvas.getByRole("button", { name: "Stop Living room" })
    );
    await canvas.findByText("Nothing playing");
  });
};

/** Controls keep the same height at the full Transport Deck width. */
export const StableHeight: Story = { play: exerciseTransport };

/** The same controls fit the 248px minimum grid width. */
export const NarrowStableHeight: Story = {
  parameters: { cardWidth: 248 },
  play: exerciseTransport,
};

/** Paused playback keeps the anchor and offers Play to resume. */
export const Paused: Story = {
  beforeEach: ({ msw }) => {
    msw.use(
      ...mediaPlayerCore(
        speaker({ state: { ...playingTrack, playback: "paused" } })
      )
    );
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Paused")).toBeVisible();
    await expect(canvas.getByText("1:58")).toBeVisible();
    await expect(
      canvas.getByRole("button", { name: "Play Living room" })
    ).toBeEnabled();
  },
};

/** Rejected Commands return to the observed State without moving the card. */
export const Rejected: Story = {
  parameters: { cardWidth: 248 },
  beforeEach: ({ msw }) => {
    msw.use(...mediaPlayerCore(speaker({ state: playingTrack }), "reject"));
  },
  play: async ({ canvas, canvasElement }) => {
    await canvas.findByText("Playing");
    await watchHeight(card(canvasElement), async () => {
      await userEvent.click(
        canvas.getByRole("button", { name: "Pause Living room" })
      );
      await expect(await canvas.findByRole("alert")).toHaveTextContent(
        "Playback: Rejected."
      );
      await expect(
        canvas.getByRole("button", { name: "Pause Living room" })
      ).toBeEnabled();
    });
  },
};

/** Skip is dispatched; the next track arrives with the following read. */
export const SkipTrack: Story = {
  play: async ({ canvas, canvasElement }) => {
    await canvas.findByText("Nocturne in F-Sharp Minor");
    await watchHeight(card(canvasElement), async () => {
      await userEvent.click(
        canvas.getByRole("button", { name: "Next track on Living room" })
      );
      await expect(await canvas.findByText("Solstice Drift")).toBeVisible();
    });
  },
};

/** A speaker reporting only the original fields keeps its layout with placeholders. */
export const TitleOnly: Story = {
  beforeEach: ({ msw }) => {
    msw.use(
      ...mediaPlayerCore(
        speaker({
          state: {
            playback: "playing",
            grouping: "standalone",
            title: "Ocean Sleep Sounds",
            volume_percent: 30,
            muted: false,
          },
          operations: ["set_playback", "set_volume", "set_muted"],
        })
      )
    );
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Ocean Sleep Sounds")).toBeVisible();
    await expect(
      canvas.queryByRole("button", { name: "Next track on Living room" })
    ).toBeNull();
    await expect(canvas.getAllByText("—:—")).toHaveLength(2);
  },
};

/** Without pause support the main button stops a playing speaker. */
export const WithoutPause: Story = {
  beforeEach: ({ msw }) => {
    msw.use(
      ...mediaPlayerCore(
        speaker({ state: playingTrack, playbackStates: ["idle", "playing"] })
      )
    );
  },
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByRole("button", { name: "Stop Living room" })
    ).toBeVisible();
  },
};

/** An idle speaker says nothing is playing and disables skip and stop. */
export const Idle: Story = {
  beforeEach: ({ msw }) => {
    msw.use(
      ...mediaPlayerCore(
        speaker({
          state: {
            playback: "idle",
            grouping: "standalone",
            volume_percent: 25,
            muted: false,
          },
        })
      )
    );
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Idle")).toBeVisible();
    await expect(canvas.getByText("Nothing playing")).toBeVisible();
    await expect(
      canvas.getByRole("button", { name: "Next track on Living room" })
    ).toBeDisabled();
  },
};

/** A grouped speaker says why it can't be controlled; a press shows the refusal. */
export const Grouped: Story = {
  beforeEach: ({ msw }) => {
    msw.use(
      ...mediaPlayerCore(
        speaker({ state: { ...playingTrack, grouping: "grouped" } }),
        "reject"
      )
    );
  },
  play: async ({ canvas, canvasElement }) => {
    await expect(await canvas.findByText("Grouped")).toBeVisible();
    await watchHeight(card(canvasElement), async () => {
      await userEvent.click(
        canvas.getByRole("button", { name: "Pause Living room" })
      );
      await expect(await canvas.findByRole("alert")).toHaveTextContent(
        "Playback: Rejected."
      );
    });
  },
};

/** While a Command is pending, the reserved line says what was asked for. */
export const Pending: Story = {
  beforeEach: ({ msw }) => {
    msw.use(...mediaPlayerCore(speaker({ state: playingTrack }), "hold"));
  },
  play: async ({ canvas, canvasElement }) => {
    const pause = await canvas.findByRole("button", {
      name: "Pause Living room",
    });
    await watchHeight(card(canvasElement), async () => {
      await userEvent.click(pause);
      await expect(await canvas.findByText("Pausing…")).toBeVisible();
    });
  },
};

/** A muted speaker keeps its volume and says it's muted. */
export const Muted: Story = {
  beforeEach: ({ msw }) => {
    msw.use(
      ...mediaPlayerCore(speaker({ state: { ...playingTrack, muted: true } }))
    );
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("42 % · Muted")).toBeVisible();
  },
};

/** An overlong title and credits truncate on one line each. */
export const OverlongTitle: Story = {
  beforeEach: ({ msw }) => {
    msw.use(
      ...mediaPlayerCore(
        speaker({
          state: {
            ...playingTrack,
            title:
              "Symphony No. 3 in E-Flat Major, Op. 55 (Eroica): II. Marcia funebre — Adagio assai (Remastered 2024)",
            artist: "Berliner Philharmoniker & Kirill Petrenko",
            album: "Beethoven: Complete Symphonies (Live Edition)",
          },
        })
      )
    );
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByTitle(/^Symphony No\. 3/u)).toBeVisible();
  },
};

/** An unavailable speaker dims its last values. */
export const Unavailable: Story = {
  beforeEach: ({ msw }) => {
    msw.use(
      ...mediaPlayerCore(
        speaker({
          state: { ...playingTrack, playback: "paused" },
          availability: "unavailable",
        })
      )
    );
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Unavailable")).toBeVisible();
  },
};

/** A disabled speaker keeps its State and disables every control. */
export const Disabled: Story = {
  beforeEach: ({ msw }) => {
    msw.use(
      ...mediaPlayerCore(speaker({ state: playingTrack, enabled: false }))
    );
  },
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByRole("button", { name: "Pause Living room" })
    ).toBeDisabled();
  },
};

/** The tile is one whole-card press that pauses or plays. */
export const Tile: Story = {
  args: { layout: "tile" },
  parameters: { cardWidth: 160 },
  play: async ({ canvas, canvasElement }) => {
    await canvas.findByText("Playing");
    await watchHeight(card(canvasElement), async () => {
      await userEvent.click(
        canvas.getByRole("button", { name: /^Living room, playing/u })
      );
      await waitFor(async () => {
        await expect(canvas.getByText("Paused")).toBeVisible();
      });
    });
  },
};

/** One card at phone width. */
export const Phone: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    await canvas.findByText("Nocturne in F-Sharp Minor");
    await expectNoHorizontalOverflow(canvasElement);
  },
};
