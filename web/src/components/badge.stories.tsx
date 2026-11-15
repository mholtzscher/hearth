import type { Meta, StoryObj } from "@storybook/react-vite";
import { Badge } from "./badge";

const meta = {
  title: "Components/Badge",
  component: Badge,
  tags: ["ai-generated"],
  args: { children: "Available" },
} satisfies Meta<typeof Badge>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {};
export const Secondary: Story = { args: { variant: "secondary", children: "Disabled" } };
export const Destructive: Story = { args: { variant: "destructive", children: "Read failed" } };
export const Outline: Story = { args: { variant: "outline", children: "Read-only" } };
