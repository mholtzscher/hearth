import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { http, HttpResponse } from "msw";
import { entityId } from "../../../../../.storybook/fixtures";
import { BoundTimeSeries } from "./TimeSeries";

const meta = {
  component: BoundTimeSeries,
  tags: ["ai-generated"],
  args: {
    title: "Temperatures",
    series: [
      { label: "Outdoor", source: { entity_id: entityId(101), entity_type: "hearth.temperature/v1", window: "24h" } },
      { label: "Bedroom", source: { entity_id: entityId(114), entity_type: "hearth.temperature/v1", window: "24h" } },
    ],
  },
  parameters: { docs: { description: { component: "Compare compatible quantities over the same window. Missing history stays a gap rather than becoming zero." } } },
} satisfies Meta<typeof BoundTimeSeries>;
export default meta;
type Story = StoryObj<typeof meta>;

export const WithGaps: Story = {
  play: async ({ canvas }) => {
    const chart = await canvas.findByRole("region", { name: "Temperatures, average line with minimum to maximum band" });
    await expect(chart).toHaveTextContent("Outdoor: latest");
  },
};
export const OneSeries: Story = { args: { series: [
  { label: "Outdoor", source: { entity_id: entityId(101), entity_type: "hearth.temperature/v1", window: "24h" } },
] } };
export const HistoryUnavailable: Story = {
  beforeEach: ({ msw }) => { msw.use(http.post("/v1/entities/:id/state/history", () => HttpResponse.json({ detail: "History unavailable" }, { status: 503 }))); },
};
