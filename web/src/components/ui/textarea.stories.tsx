import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { Textarea } from "./textarea";

const meta = {
  component: Textarea,
  tags: ["ai-generated"],
  args: { "aria-label": "Dashboard note", placeholder: "What matters at home?" },
} satisfies Meta<typeof Textarea>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Editable: Story = {
  play: async ({ canvas, userEvent }) => {
    const input = canvas.getByRole("textbox", { name: "Dashboard note" });
    await userEvent.type(input, "Check the front door before bed.");
    await expect(input).toHaveValue("Check the front door before bed.");
  },
};
export const Invalid: Story = { args: { "aria-invalid": true } };
export const Disabled: Story = { args: { disabled: true, defaultValue: "No changes allowed." } };
