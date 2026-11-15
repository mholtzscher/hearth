import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { Separator } from "./separator";

const meta = {
  title: "Components/Separator",
  component: Separator,
  tags: ["ai-generated"],
  args: { orientation: "horizontal", decorative: false },
} satisfies Meta<typeof Separator>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Horizontal: Story = {
  render: args => <div><div className="grid max-w-[320px] gap-3">
    <p>Current State</p><Separator {...args} /><p>Source details</p>
  </div></div>,
  play: async ({ canvas }) => {
    const separator = canvas.getByRole("separator");
    await expect(separator).toHaveAttribute("data-orientation", "horizontal");
    await expect(separator.getBoundingClientRect().height).toBe(1);
    await expect(separator.getBoundingClientRect().width).toBeGreaterThan(0);
  },
};

export const Vertical: Story = {
  args: { orientation: "vertical" },
  render: args => <div className="flex h-11 items-center gap-3">
    <span>Observed</span><Separator {...args} /><span>Source</span>
  </div>,
  play: async ({ canvas }) => {
    const separator = canvas.getByRole("separator");
    await expect(separator).toHaveAttribute("aria-orientation", "vertical");
    await expect(separator.getBoundingClientRect().width).toBe(1);
    await expect(separator.getBoundingClientRect().height).toBeGreaterThan(0);
  },
};

export const Decorative: Story = {
  args: { decorative: true },
  render: args => <div><div className="grid max-w-[320px] gap-3">
    <p>Temperature</p><Separator {...args} /><p>21.3 °C</p>
  </div></div>,
  play: async ({ canvas, canvasElement }) => {
    await expect(canvas.queryByRole("separator")).not.toBeInTheDocument();
    const separator = canvasElement.querySelector('[data-slot="separator"]');
    await expect(separator).toHaveAttribute("role", "none");
    await expect(separator?.getBoundingClientRect().height).toBe(1);
  },
};
