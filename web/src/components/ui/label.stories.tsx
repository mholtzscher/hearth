import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { Input } from "./input";
import { Label } from "./label";

const meta = {
  component: Label,
  tags: ["ai-generated"],
  args: { htmlFor: "dashboard-name", children: "Dashboard name" },
  render: args => <div>
    <Label {...args} />
    <Input id="dashboard-name" defaultValue="Morning overview" />
  </div>,
} satisfies Meta<typeof Label>;
export default meta;
type Story = StoryObj<typeof meta>;

export const NamesItsInput: Story = {
  play: async ({ canvas, userEvent }) => {
    await userEvent.click(canvas.getByText("Dashboard name"));
    await expect(canvas.getByRole("textbox", { name: "Dashboard name" })).toHaveFocus();
  },
};
