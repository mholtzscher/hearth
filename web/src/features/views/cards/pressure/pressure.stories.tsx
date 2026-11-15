import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";

import { entityId } from "../../../../../.storybook/fixtures";
import { expectNoHorizontalOverflow } from "../../../../../.storybook/play";
import { BoundPressure } from "./pressure";

const meta = {
  title: "Components/Cards/Pressure",
  component: BoundPressure,
  tags: ["ai-generated"],
  args: {
    label: "Pressure",
    trend: "24h",
    source: {
      kind: "entity-state",
      entity_id: entityId(113),
      entity_type: "hearth.pressure/v1",
    },
  },
} satisfies Meta<typeof BoundPressure>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Falling: Story = {
  play: async ({ canvas }) => {
    await expect(await canvas.findByText(/Falling/u)).toBeVisible();
  },
};
export const ReadingOnly: Story = {
  args: { trend: undefined },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText(/Falling/u)).toBeVisible();
    await expect(canvas.queryByText(/24 h range/u)).not.toBeInTheDocument();
  },
};
export const Phone: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    await canvas.findByText(/Falling/u);
    await expectNoHorizontalOverflow(canvasElement);
  },
};
