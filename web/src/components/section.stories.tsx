import type { Meta, StoryObj } from "@storybook/react-vite";

import { Section } from "./section";

const meta = {
  title: "Components/Section",
  component: Section,
  args: {
    title: "Command history",
    children: <p>No commands recorded.</p>,
  },
} satisfies Meta<typeof Section>;
export default meta;
type Story = StoryObj<typeof meta>;

export const History: Story = {};
