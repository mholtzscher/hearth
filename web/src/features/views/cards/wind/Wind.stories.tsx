import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { entityId, fixtureEntity } from "../../../../../.storybook/fixtures";
import { expectNoHorizontalOverflow } from "../../../../../.storybook/play";
import { createCoreHandlers } from "../../../../../.storybook/msw-handlers";
import { BoundWind } from "./Wind";

const meta = {
  title: "Components/Cards/Wind",
  component: BoundWind,
  tags: ["ai-generated"],
  args: {
    label: "Wind", trend: "24h",
    speed: { kind: "entity-state", entity_id: entityId(103), entity_type: "hearth.speed/v1" },
    gust: { kind: "entity-state", entity_id: entityId(111), entity_type: "hearth.speed/v1" },
    direction: { kind: "entity-state", entity_id: entityId(104), entity_type: "hearth.numericsensor/v1" },
    max_gust: { kind: "entity-state", entity_id: entityId(112), entity_type: "hearth.speed/v1" },
  },
} satisfies Meta<typeof BoundWind>;
export default meta;
type Story = StoryObj<typeof meta>;

export const WithGusts: Story = {
  play: async ({ canvas }) => { await expect(await canvas.findByText("6.3")).toBeVisible(); },
};
export const SpeedOnly: Story = {
  args: { gust: undefined, direction: undefined, max_gust: undefined, trend: undefined },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("6.3")).toBeVisible();
    await expect(canvas.queryByText(/Gusts/)).not.toBeInTheDocument();
  },
};
export const Phone: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    await canvas.findByText("6.3");
    await expectNoHorizontalOverflow(canvasElement);
  },
};
export const Calm: Story = {
  beforeEach: ({ msw }) => {
    const speed = fixtureEntity(103);
    msw.use(...createCoreHandlers([{ ...speed, state: { ...speed.state!, value: 0 } }]));
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Calm")).toBeVisible();
    await expect(canvas.getByText("0.0")).toBeVisible();
  },
};
