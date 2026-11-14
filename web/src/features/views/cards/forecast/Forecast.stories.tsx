import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { entityId } from "../../../../../.storybook/fixtures";
import { entityScenario } from "../../../../../.storybook/msw-handlers";
import { BoundForecast } from "./Forecast";

const meta = {
  component: BoundForecast,
  tags: ["ai-generated"],
  args: {
    label: "Next 12 hours", periods: 12,
    source: { kind: "entity-state", entity_id: entityId(201), entity_type: "hearth.hourlyforecast/v1" },
  },
} satisfies Meta<typeof BoundForecast>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Hourly: Story = {
  play: async ({ canvas }) => { await expect(await canvas.findByRole("img", { name: /Next 12 hours/ })).toBeVisible(); },
};
export const Daily: Story = {
  args: { label: "Next 7 days", periods: 7, source: { kind: "entity-state", entity_id: entityId(202), entity_type: "hearth.dailyforecast/v1" } },
  play: async ({ canvas }) => {
    await canvas.findByRole("listitem", { name: /^Today:/ });
    await expect(canvas.getAllByRole("listitem")).toHaveLength(7);
  },
};
export const NeverObserved: Story = {
  beforeEach: ({ msw }) => { msw.use(entityScenario(201, "unobserved")); },
  play: async ({ canvas }) => { await expect(await canvas.findByText("Never observed")).toBeVisible(); },
};
