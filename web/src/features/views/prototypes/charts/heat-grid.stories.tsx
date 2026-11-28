import type { Meta, StoryObj } from "@storybook/react-vite";

import { HeatGrid } from "./heat-grid";

const meta = {
  title: "Prototypes/Charts/HeatGrid",
  component: HeatGrid,
  tags: ["ai-generated"],
} satisfies Meta<typeof HeatGrid>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Hours: Story = { args: { take: "hours" } };
export const Blocks: Story = { args: { take: "blocks" } };
