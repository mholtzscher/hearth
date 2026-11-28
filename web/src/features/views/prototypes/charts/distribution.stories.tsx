import type { Meta, StoryObj } from "@storybook/react-vite";

import { Distribution } from "./distribution";

const meta = {
  title: "Prototypes/Charts/Distribution",
  component: Distribution,
  tags: ["ai-generated"],
} satisfies Meta<typeof Distribution>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Histogram: Story = { args: { take: "histogram" } };
export const Bands: Story = { args: { take: "bands" } };
