import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { entityId } from "../../../../../.storybook/fixtures";
import { expectNoHorizontalOverflow } from "../../../../../.storybook/play";
import { BoundRain } from "./Rain";

const meta = {
  component: BoundRain,
  tags: ["ai-generated"],
  args: {
    label: "Rain", trend: "24h",
    rate: { kind: "entity-state", entity_id: entityId(105), entity_type: "hearth.numericsensor/v1" },
    storm: { kind: "entity-state", entity_id: entityId(107), entity_type: "hearth.numericsensor/v1" },
    totals: {
      day: { kind: "entity-state", entity_id: entityId(106), entity_type: "hearth.numericsensor/v1" },
      week: { kind: "entity-state", entity_id: entityId(108), entity_type: "hearth.numericsensor/v1" },
      month: { kind: "entity-state", entity_id: entityId(109), entity_type: "hearth.numericsensor/v1" },
      year: { kind: "entity-state", entity_id: entityId(110), entity_type: "hearth.numericsensor/v1" },
    },
  },
} satisfies Meta<typeof BoundRain>;
export default meta;
type Story = StoryObj<typeof meta>;

export const WithTotals: Story = {
  play: async ({ canvas }) => { await expect(await canvas.findByText("6.8")).toBeVisible(); },
};
export const RateOnly: Story = {
  args: { totals: undefined, storm: undefined, trend: undefined },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("6.8")).toBeVisible();
    await expect(canvas.queryByText(/This storm/)).not.toBeInTheDocument();
  },
};
export const Phone: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    await canvas.findByText("18.6");
    await expectNoHorizontalOverflow(canvasElement);
  },
};
