import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { Textarea } from "./textarea.tsx";
import { expectNoHorizontalOverflow } from "../../../.storybook/play.ts";

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

export const KeyboardFocus: Story = {
  args: { rows: 1 },
  play: async ({ canvas, userEvent }) => {
    await userEvent.tab();
    await expect(canvas.getByRole("textbox", { name: "Dashboard note" })).toHaveFocus();
  },
};

export const Multiline: Story = {
  args: { rows: 4, defaultValue: "Check the front door.\nTurn off the kitchen lights." },
  play: async ({ canvas, userEvent }) => {
    const input = canvas.getByRole("textbox", { name: "Dashboard note" });
    await userEvent.clear(input);
    await userEvent.type(input, "Check the front door.{Enter}Turn off the kitchen lights.");
    await expect(input).toHaveValue("Check the front door.\nTurn off the kitchen lights.");
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

/** Temporary household reference; remove when the editor adopts Textarea. */
export const HouseholdComparison: Story = {
  render: () => (
    <div className="grid gap-4">
      <div className="view-editor">
        <textarea aria-label="Legacy dashboard note" rows={2} defaultValue={"Check the front door.\nTurn off the kitchen lights."} />
      </div>
      <Textarea aria-label="Shared dashboard note" rows={2} defaultValue={"Check the front door.\nTurn off the kitchen lights."} />
    </div>
  ),
  play: async ({ canvasElement }) => expectNoHorizontalOverflow(canvasElement),
};
