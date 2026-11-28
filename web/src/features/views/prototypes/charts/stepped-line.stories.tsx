import type { Meta, StoryObj } from "@storybook/react-vite";

import { SteppedLine } from "./stepped-line";

const meta = {
  title: "Prototypes/Charts/SteppedLine",
  component: SteppedLine,
  tags: ["ai-generated"],
} satisfies Meta<typeof SteppedLine>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Risers: Story = { args: { take: "risers" } };
export const Holds: Story = { args: { take: "holds" } };
