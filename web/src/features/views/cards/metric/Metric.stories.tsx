import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { entityId } from "../../../../../.storybook/fixtures";
import { entityScenario } from "../../../../../.storybook/msw-handlers";
import { BoundMetric } from "./Metric";

const meta = {
  component: BoundMetric,
  tags: ["ai-generated"],
  args: {
    label: "Outdoor humidity", source: { entity_id: entityId(102), entity_type: "hearth.relativehumidity/v1" },
    trend: "24h", range: true,
  },
  parameters: { docs: { description: { component: "One quantity reading with optional trend and catalog-supported range. Never invent a reading for missing State." } } },
} satisfies Meta<typeof BoundMetric>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Observed: Story = {
  play: async ({ canvas }) => { await expect(await canvas.findByText("94.0")).toBeVisible(); },
};
export const Loading: Story = { beforeEach: ({ msw }) => { msw.use(entityScenario(102, "loading")); } };
export const NeverObserved: Story = {
  beforeEach: ({ msw }) => { msw.use(entityScenario(102, "unobserved")); },
  play: async ({ canvas }) => { await expect(await canvas.findByText("Never observed")).toBeVisible(); },
};
export const Unavailable: Story = { beforeEach: ({ msw }) => { msw.use(entityScenario(102, "unavailable")); } };
export const ReadFailed: Story = {
  beforeEach: ({ msw }) => { msw.use(entityScenario(102, "error")); },
  play: async ({ canvas }) => { await expect(await canvas.findByText(/Couldn't load this reading/)).toBeVisible(); },
};
