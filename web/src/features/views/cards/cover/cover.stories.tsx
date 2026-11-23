import type { Meta, StoryObj } from "@storybook/react-vite";
import { delay, http, HttpResponse } from "msw";
import { expect, waitFor } from "storybook/test";

import { entityId, fixtureEntity } from "../../../../../.storybook/fixtures";
import {
  createCoreHandlers,
  entityScenario,
} from "../../../../../.storybook/msw-handlers";
import { watchHeight } from "../../../../../.storybook/play";
import { BoundCover } from "./cover";

const meta = {
  title: "Components/Cards/Cover",
  component: BoundCover,
  tags: ["ai-generated"],
  args: {
    layout: "card",
    label: "Garage door",
    source: {
      kind: "entity-state",
      entity_id: entityId(401),
      entity_type: "hearth.cover/v1",
    },
  },
  parameters: {
    docs: {
      description: {
        component:
          "Reported cover position and motion. Interactive fixtures simulate Command rejection or a pending request, not physical motion.",
      },
    },
  },
} satisfies Meta<typeof BoundCover>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Closed: Story = {
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Closed")).toBeVisible();
    await expect(canvas.getByText("0 % open")).toBeVisible();
  },
};
export const Tile: Story = {
  args: { layout: "tile" },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Closed")).toBeVisible();
  },
};
export const NeverObserved: Story = {
  beforeEach: ({ msw }) => {
    msw.use(entityScenario(401, "unobserved"));
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Never observed")).toBeVisible();
  },
};
const writableCover = () => {
  const cover = fixtureEntity(401);
  return {
    ...cover,
    support: { ...cover.support, operations: { set: {}, stop: {} } },
  };
};

export const MotionCoverTile: Story = {
  name: "Motion · Tile opens",
  tags: ["motion"],
  args: { layout: "tile", confirm_open: false },
  beforeEach: ({ msw }) => {
    msw.use(...createCoreHandlers([writableCover()], { commandDelayMs: 400 }));
  },
  play: async ({ canvas, userEvent }) => {
    const tile = canvas.getByRole("article", { name: "Garage door" });
    const control = await canvas.findByRole("button", {
      name: /Garage door, Closed/u,
    });
    await watchHeight(tile, async () => {
      await userEvent.click(control);
      await expect(tile).not.toHaveAttribute("data-on");
      await expect(await canvas.findByText("100 % open")).toBeVisible();
      await expect(tile).toHaveAttribute("data-on", "true");
    });
  },
};

export const ConfirmOpening: Story = {
  name: "Motion · Rejected tile opening",
  tags: ["motion"],
  args: { layout: "tile" },
  decorators: [
    (Story) => (
      <div className="w-40">
        <Story />
      </div>
    ),
  ],
  beforeEach: ({ msw }) => {
    msw.use(
      http.post(`/v1/entities/${entityId(401)}/commands`, () =>
        HttpResponse.json({ detail: "Rejected" }, { status: 502 })
      ),
      ...createCoreHandlers([writableCover()])
    );
  },
  play: async ({ canvas, userEvent }) => {
    const control = await canvas.findByRole("button", { name: /Garage door/u });
    await watchHeight(canvas.getByRole("article"), async () => {
      await userEvent.click(control);
      await expect(
        await canvas.findByText("Press again within 4 s to open.")
      ).toBeInTheDocument();
      await userEvent.click(
        canvas.getByRole("button", { name: /Garage door/u })
      );
      await expect(await canvas.findByRole("alert")).toHaveTextContent(
        "Change rejected. Try again."
      );
      await waitFor(() => expect(canvas.getByText("Rejected.")).toBeVisible());
    });
  },
};
export const ConfirmOpeningCard: Story = {
  name: "Motion · Rejected Open control",
  tags: ["motion"],
  decorators: [
    (Story) => (
      <div className="w-[248px]">
        <Story />
      </div>
    ),
  ],
  beforeEach: ({ msw }) => {
    msw.use(
      http.post(`/v1/entities/${entityId(401)}/commands`, () =>
        HttpResponse.json({ detail: "Rejected" }, { status: 502 })
      ),
      ...createCoreHandlers([writableCover()])
    );
  },
  play: async ({ canvas, userEvent }) => {
    const open = await canvas.findByRole("button", {
      name: "Open Garage door",
    });
    await watchHeight(canvas.getByRole("article"), async () => {
      await userEvent.click(open);
      const confirm = await canvas.findByRole("button", {
        name: "Confirm open Garage door",
      });
      await expect(confirm).toBeVisible();
      await expect(confirm).toHaveTextContent("Confirm");
      await expect(getComputedStyle(confirm).backgroundColor).toBe(
        "rgb(255, 138, 61)"
      );
      await userEvent.click(confirm);
      await expect(await canvas.findByRole("alert")).toHaveTextContent(
        "Change rejected. Try again."
      );
    });
  },
};
export const RequestedPosition: Story = {
  args: { confirm_open: false },
  beforeEach: ({ msw }) => {
    msw.use(
      http.post(`/v1/entities/${entityId(401)}/commands`, async () => {
        await delay("infinite");
        return HttpResponse.json({});
      }),
      ...createCoreHandlers([writableCover()])
    );
  },
  play: async ({ canvas, userEvent }) => {
    const open = await canvas.findByRole("button", {
      name: "Open Garage door",
    });
    await watchHeight(canvas.getByRole("article"), async () => {
      await userEvent.click(open);
      await expect(await canvas.findByText(/Opening/u)).toBeVisible();
      await expect(canvas.getByText("0 % open")).toBeVisible();
    });
  },
};
