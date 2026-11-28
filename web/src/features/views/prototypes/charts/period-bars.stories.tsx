import type { Meta, StoryObj } from "@storybook/react-vite";

import { PeriodBars } from "./period-bars";

const meta = {
  title: "Prototypes/Charts/PeriodBars",
  component: PeriodBars,
  tags: ["ai-generated"],
} satisfies Meta<typeof PeriodBars>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Columns: Story = { args: { take: "columns" } };
export const Ledger: Story = { args: { take: "ledger" } };
