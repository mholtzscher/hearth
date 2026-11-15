import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { Textarea } from "./textarea.tsx";
import { expectFieldFocus, expectFieldSize } from "./field-story-assertions.ts";

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
export const Invalid: Story = {
  args: { "aria-invalid": true },
  play: async ({ canvas }) => {
    await expect(getComputedStyle(canvas.getByRole("textbox")).borderColor).toBe("rgb(255, 92, 122)");
  },
};
export const Disabled: Story = {
  args: { disabled: true, defaultValue: "No changes allowed." },
  play: async ({ canvas }) => {
    const input = canvas.getByRole("textbox");
    await expect(input).toBeDisabled();
    await expect(getComputedStyle(input).opacity).toBe("0.5");
  },
};

export const KeyboardFocus: Story = {
  args: { rows: 1 },
  play: async ({ canvas, userEvent }) => {
    await userEvent.tab();
    const input = canvas.getByRole("textbox", { name: "Dashboard note" });
    await expectFieldSize(input, "44px");
    await expect(getComputedStyle(input).minHeight).toBe("44px");
    await expectFieldFocus(input);
  },
};

export const Multiline: Story = {
  args: { rows: 4, defaultValue: "Check the front door.\nTurn off the kitchen lights." },
  play: async ({ canvas, userEvent }) => {
    const input = canvas.getByRole("textbox", { name: "Dashboard note" });
    await userEvent.clear(input);
    await userEvent.type(input, "Check the front door.{Enter}Turn off the kitchen lights.");
    await expect(input).toHaveValue("Check the front door.\nTurn off the kitchen lights.");
    await expect(getComputedStyle(input).height).toBe("102px");
  },
};

export const ReadOnly: Story = {
  args: { readOnly: true, defaultValue: "Keep the hall light on." },
  play: async ({ canvas, userEvent }) => {
    const input = canvas.getByRole("textbox", { name: "Dashboard note" });
    await userEvent.type(input, "Changed");
    await expect(input).toHaveValue("Keep the hall light on.");
  },
};
