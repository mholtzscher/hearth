import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { entityId } from "../../../../../.storybook/fixtures";
import { BoundStateTimeline } from "./StateTimeline";

const meta = {
  title: "features/views/cards/StateTimeline",
  component: BoundStateTimeline,
  tags: ["ai-generated"],
  args: {
    title: "Around the house", style: "ledger", window: "24h",
    rows: [
      { label: "Front door", source: { kind: "entity-state", entity_id: entityId(1), entity_type: "hearth.binarysensor/v1" }, words: { true: "Open", false: "Closed" } },
      { label: "Porch light", source: { kind: "entity-state", entity_id: entityId(2), entity_type: "hearth.power/v1" } },
      { label: "Thermostat", source: { kind: "entity-state", entity_id: entityId(10), entity_type: "hearth.enumsetting/v1" } },
    ],
  },
} satisfies Meta<typeof BoundStateTimeline>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Ledger: Story = {
  play: async ({ canvas }) => { await expect(await canvas.findByText("Closed")).toBeVisible(); },
};
export const Lanes: Story = {
  args: { style: "lanes" },
  play: async ({ canvas }) => { await expect(await canvas.findByText("Open 23 h 20 min")).toBeVisible(); },
};
export const Feed: Story = {
  args: { style: "feed" },
  play: async ({ canvas }) => { await expect(await canvas.findByRole("heading", { name: "Overnight" })).toBeVisible(); },
};
