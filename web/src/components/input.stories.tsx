import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";

import { expectNoHorizontalOverflow } from "../../.storybook/play.ts";
import { expectFieldFocus, expectFieldSize } from "./field-story-assertions.ts";
import { Input } from "./input.tsx";

const meta = {
  title: "Components/Input",
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
export const Invalid: Story = {
  args: { defaultValue: "", "aria-invalid": true },
  play: async ({ canvas }) => {
    await expect(
      getComputedStyle(canvas.getByRole("textbox")).borderColor
    ).toBe("rgb(255, 92, 122)");
  },
};
export const Disabled: Story = {
  args: { defaultValue: "Living room", disabled: true },
  play: async ({ canvas }) => {
    const input = canvas.getByRole("textbox");
    await expect(input).toBeDisabled();
    await expect(getComputedStyle(input).opacity).toBe("0.5");
  },
};

export const Sizes: Story = {
  render: () => (
    <div className="grid gap-3">
      <Input aria-label="Default size" placeholder="Default" />
      <Input size="touch" aria-label="Touch size" placeholder="Touch" />
      <Input size="auto" aria-label="Automatic size" placeholder="Automatic" />
    </div>
  ),
  play: async ({ canvas, canvasElement }) => {
    await expectFieldSize(
      canvas.getByRole("textbox", { name: "Default size" }),
      "32px"
    );
    await expectFieldSize(
      canvas.getByRole("textbox", { name: "Touch size" }),
      "44px"
    );
    await expectFieldSize(
      canvas.getByRole("textbox", { name: "Automatic size" }),
      matchMedia("(pointer: coarse)").matches ? "44px" : "32px"
    );
    await expectNoHorizontalOverflow(canvasElement);
  },
};

export const KeyboardFocus: Story = {
  args: { size: "touch" },
  play: async ({ canvas, userEvent }) => {
    await userEvent.tab();
    const input = canvas.getByRole("textbox", { name: "Dashboard name" });
    await expectFieldSize(input, "44px");
    await expectFieldFocus(input);
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
    const input = canvas.getByRole("searchbox", {
      name: "Search Device facts",
    });
    await userEvent.type(input, "temperature");
    await expect(input).toHaveValue("temperature");
    await userEvent.clear(input);
    await expect(input).toHaveValue("");
  },
};
