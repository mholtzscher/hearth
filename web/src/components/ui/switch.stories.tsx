import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { Switch } from "./switch";

const meta = {
  component: Switch,
  tags: ["ai-generated"],
  args: { "aria-label": "Enable automation" },
} satisfies Meta<typeof Switch>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Off: Story = {
  play: async ({ canvas, userEvent }) => {
    const control = canvas.getByRole("switch", { name: "Enable automation" });
    await expect(control).toHaveAttribute("aria-checked", "false");
    await userEvent.click(control);
    await expect(control).toHaveAttribute("aria-checked", "true");
  },
};
export const On: Story = { args: { defaultChecked: true } };
export const Disabled: Story = { args: { disabled: true, defaultChecked: true } };
