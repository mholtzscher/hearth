import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { entityId } from "../../../../../.storybook/fixtures";
import { entityScenario } from "../../../../../.storybook/msw-handlers";
import { BoundCover } from "./Cover";

const meta = {
  component: BoundCover,
  tags: ["ai-generated"],
  args: {
    layout: "card", label: "Garage door",
    source: { kind: "entity-state", entity_id: entityId(401), entity_type: "hearth.cover/v1" },
  },
  parameters: { docs: { description: { component: "Reported cover position and motion. These read-only fixtures do not model cover Commands or physical motion." } } },
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
  play: async ({ canvas }) => { await expect(await canvas.findByText("Closed")).toBeVisible(); },
};
export const NeverObserved: Story = {
  beforeEach: ({ msw }) => { msw.use(entityScenario(401, "unobserved")); },
  play: async ({ canvas }) => { await expect(await canvas.findByText("Never observed")).toBeVisible(); },
};
