import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { Input } from "./input.tsx";
import { expectNoHorizontalOverflow } from "../../../.storybook/play.ts";

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

export const Sizes: Story = {
  render: () => (
    <div className="grid gap-3">
      <Input aria-label="Default size" placeholder="Default" />
      <Input aria-label="Touch size" placeholder="Touch" />
      <Input aria-label="Automatic size" placeholder="Automatic" />
    </div>
  ),
  play: async ({ canvasElement }) => expectNoHorizontalOverflow(canvasElement),
};

export const KeyboardFocus: Story = {
  play: async ({ canvas, userEvent }) => {
    await userEvent.tab();
    await expect(canvas.getByRole("textbox", { name: "Dashboard name" })).toHaveFocus();
  },
};

export const ReadOnly: Story = {
  args: { readOnly: true, defaultValue: "Living room" },
  play: async ({ canvas, userEvent }) => {
    const input = canvas.getByRole("textbox", { name: "Dashboard name" });
    await userEvent.type(input, "Changed");
    await expect(input).toHaveValue("Living room");
  },
};

export const Search: Story = {
  args: { type: "search", "aria-label": "Search Device facts" },
  play: async ({ canvas, userEvent }) => {
    const input = canvas.getByRole("searchbox", { name: "Search Device facts" });
    await userEvent.type(input, "temperature");
    await expect(input).toHaveValue("temperature");
    await userEvent.clear(input);
    await expect(input).toHaveValue("");
  },
};

/** Temporary household reference; remove when the editor adopts Input. */
export const HouseholdComparison: Story = {
  render: () => (
    <div className="grid gap-4">
      <div className="view-editor">
        <input aria-label="Legacy dashboard name" defaultValue="Living room" />
      </div>
      <Input aria-label="Shared dashboard name" defaultValue="Living room" />
    </div>
  ),
  play: async ({ canvasElement }) => expectNoHorizontalOverflow(canvasElement),
};
