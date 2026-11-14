import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { Input } from "./input";

const meta = {
  component: Input,
  tags: ["ai-generated"],
  args: { "aria-label": "Dashboard name", placeholder: "Morning overview" },
} satisfies Meta<typeof Input>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Editable: Story = {
  play: async ({ canvas, userEvent }) => {
    const input = canvas.getByRole("textbox", { name: "Dashboard name" });
    await userEvent.type(input, "Living room");
    await expect(input).toHaveValue("Living room");
  },
};
export const Invalid: Story = { args: { defaultValue: "", "aria-invalid": true } };
export const Disabled: Story = { args: { defaultValue: "Living room", disabled: true } };
