import type { Meta, StoryObj } from "@storybook/react-vite";

import { expectNoHorizontalOverflow } from "../../../../../.storybook/play";
import { ChartPrototypes } from "./chart-prototypes";

const meta = {
  title: "Prototypes/Charts/Review sheet",
  component: ChartPrototypes,
  tags: ["ai-generated"],
} satisfies Meta<typeof ChartPrototypes>;
export default meta;
type Story = StoryObj<typeof meta>;

export const AllTakes: Story = {};
export const Phone: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvasElement }) => {
    await expectNoHorizontalOverflow(canvasElement);
  },
};
