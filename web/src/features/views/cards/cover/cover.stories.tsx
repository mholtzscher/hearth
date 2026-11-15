import type { Meta, StoryObj } from "@storybook/react-vite";
import { delay, http, HttpResponse } from "msw";
import { expect } from "storybook/test";

import { entityId, fixtureEntity } from "../../../../../.storybook/fixtures";
import {
  createCoreHandlers,
  entityScenario,
} from "../../../../../.storybook/msw-handlers";
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

export const ConfirmOpening: Story = {
  args: { layout: "tile" },
  beforeEach: ({ msw }) => {
    msw.use(
      http.post(`/v1/entities/${entityId(401)}/commands`, () =>
        HttpResponse.json({ detail: "Rejected" }, { status: 502 })
      ),
      ...createCoreHandlers([writableCover()])
    );
  },
  play: async ({ canvas, userEvent }) => {
    await userEvent.click(
      await canvas.findByRole("button", { name: /Garage door/u })
    );
    await expect(
      await canvas.findByText("Press again within 4 s to open.")
    ).toBeInTheDocument();
    await userEvent.click(canvas.getByRole("button", { name: /Garage door/u }));
    await expect(await canvas.findByRole("alert")).toHaveTextContent(
      "Change rejected. Try again."
    );
    await expect(canvas.getByText("Rejected.")).toBeVisible();
  },
};
export const ConfirmOpeningCard: Story = {
  beforeEach: ({ msw }) => {
    msw.use(...createCoreHandlers([writableCover()]));
  },
  play: async ({ canvas, userEvent }) => {
    await userEvent.click(
      await canvas.findByRole("button", { name: "Open Garage door" })
    );
    const confirm = await canvas.findByRole("button", {
      name: "Confirm open Garage door",
    });
    await expect(confirm).toBeVisible();
    await expect(confirm).toHaveTextContent("Confirm");
    await expect(getComputedStyle(confirm).backgroundColor).toBe(
      "rgb(255, 138, 61)"
    );
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
    await userEvent.click(
      await canvas.findByRole("button", { name: "Open Garage door" })
    );
    await expect(await canvas.findByText(/Opening/u)).toBeVisible();
    await expect(canvas.getByText("0 % open")).toBeVisible();
  },
};
