import type { Meta, StoryObj } from "@storybook/react-vite";

import { RankedReadings } from "./ranked-readings";

const meta = {
  title: "Prototypes/Charts/RankedReadings",
  component: RankedReadings,
  tags: ["ai-generated"],
} satisfies Meta<typeof RankedReadings>;
export default meta;
type Story = StoryObj<typeof meta>;

export const BarsFromZero: Story = { args: { take: "bars" } };
export const RangePlot: Story = { args: { take: "range" } };
