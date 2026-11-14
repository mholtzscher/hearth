import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn } from "storybook/test";
import { Button } from "./button";

const meta = {
  component: Button,
  tags: ["ai-generated"],
  args: { children: "Save dashboard", onClick: fn() },
  parameters: {
    docs: { description: { component: "Use the primary variant for the main action; outline for secondary actions. Hearth is dark-only." } },
  },
} satisfies Meta<typeof Button>;
export default meta;
type Story = StoryObj<typeof meta>;

/** The primary action sends one explicit user intent. */
export const Primary: Story = {
  play: async ({ canvas, userEvent, args }) => {
    await userEvent.click(canvas.getByRole("button", { name: "Save dashboard" }));
    await expect(args.onClick).toHaveBeenCalledTimes(1);
  },
};
export const Secondary: Story = { args: { variant: "outline", children: "Discard changes" } };
export const Disabled: Story = { args: { disabled: true } };

/** Proves Tailwind, the production theme, and the preview stylesheet loaded. */
export const CssCheck: Story = {
  play: async ({ canvas }) => {
    const button = canvas.getByRole("button", { name: "Save dashboard" });
    await expect(getComputedStyle(button).backgroundColor).toBe("rgb(255, 138, 61)");
    await expect(getComputedStyle(button).fontFamily).toContain("Figtree Variable");
  },
};
