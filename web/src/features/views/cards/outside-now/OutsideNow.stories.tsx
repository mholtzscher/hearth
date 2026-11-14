import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { entityId } from "../../../../../.storybook/fixtures";
import { entityScenario } from "../../../../../.storybook/msw-handlers";
import { BoundOutsideNow } from "./OutsideNow";

const meta = {
  component: BoundOutsideNow,
  tags: ["ai-generated"],
  args: {
    label: "Outside",
    temperature: { kind: "entity-state", entity_id: entityId(101), entity_type: "hearth.temperature/v1" },
    humidity: { kind: "entity-state", entity_id: entityId(102), entity_type: "hearth.relativehumidity/v1" },
    wind: { kind: "entity-state", entity_id: entityId(103), entity_type: "hearth.speed/v1" },
    wind_direction: { kind: "entity-state", entity_id: entityId(104), entity_type: "hearth.numericsensor/v1" },
    rain_rate: { kind: "entity-state", entity_id: entityId(105), entity_type: "hearth.numericsensor/v1" },
    rain_today: { kind: "entity-state", entity_id: entityId(106), entity_type: "hearth.numericsensor/v1" },
  },
} satisfies Meta<typeof BoundOutsideNow>;
export default meta;
type Story = StoryObj<typeof meta>;

export const RainyMorning: Story = {
  play: async ({ canvas }) => { await expect(await canvas.findByText("9.4")).toBeVisible(); },
};
export const TemperatureOnly: Story = { args: { humidity: undefined, wind: undefined, wind_direction: undefined, rain_rate: undefined, rain_today: undefined } };
export const HumidityUnavailable: Story = { beforeEach: ({ msw }) => { msw.use(entityScenario(102, "unavailable")); } };
