import type { Meta, StoryObj } from "@storybook/react-vite";

import { PeriodComparison } from "./period-comparison";

const meta = {
  title: "Prototypes/Charts/PeriodComparison",
  component: PeriodComparison,
  tags: ["ai-generated"],
} satisfies Meta<typeof PeriodComparison>;
export default meta;
type Story = StoryObj<typeof meta>;

export const OverYesterday: Story = { args: { take: "yesterday" } };
export const AgainstTypical: Story = { args: { take: "typical" } };
