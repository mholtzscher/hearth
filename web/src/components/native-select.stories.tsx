import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { expectNoHorizontalOverflow } from "../../.storybook/play.ts";
import { NativeSelect } from "./native-select.tsx";
import { expectFieldFocus, expectFieldSize } from "./field-story-assertions.ts";

const meta = {
  title: "Components/NativeSelect",
  component: NativeSelect,
  tags: ["ai-generated"],
  args: {
    "aria-label": "State field",
    defaultValue: "temperature",
    children: <>
      <option value="temperature">Temperature</option>
      <option value="humidity">Humidity</option>
      <option value="pressure" disabled>Pressure unavailable</option>
    </>,
  },
} satisfies Meta<typeof NativeSelect>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Selection: Story = {
  play: async ({ canvas, userEvent }) => {
    const select = canvas.getByRole("combobox", { name: "State field" });
    await expect(select).toHaveValue("temperature");
    await userEvent.selectOptions(select, "humidity");
    await expect(select).toHaveValue("humidity");
    await userEvent.selectOptions(select, "pressure");
    await expect(select).toHaveValue("humidity");
  },
};

export const Disabled: Story = {
  args: { disabled: true },
  play: async ({ canvas, userEvent }) => {
    const select = canvas.getByRole("combobox", { name: "State field" });
    await expect(select).toBeDisabled();
    await expect(getComputedStyle(select).opacity).toBe("0.5");
    await userEvent.selectOptions(select, "humidity");
    await expect(select).toHaveValue("temperature");
  },
};

export const Invalid: Story = {
  args: { "aria-invalid": true },
  play: async ({ canvas }) => {
    await expect(getComputedStyle(canvas.getByRole("combobox")).borderColor).toBe("rgb(255, 92, 122)");
  },
};

export const Sizes: Story = {
  render: (args) => (
    <div className="grid gap-3">
      <NativeSelect {...args} size="default" aria-label="Default size" />
      <NativeSelect {...args} size="touch" aria-label="Touch size" />
      <NativeSelect {...args} size="auto" aria-label="Automatic size" />
    </div>
  ),
  play: async ({ canvas, canvasElement }) => {
    await expectFieldSize(canvas.getByRole("combobox", { name: "Default size" }), "32px");
    await expectFieldSize(canvas.getByRole("combobox", { name: "Touch size" }), "44px");
    await expectFieldSize(canvas.getByRole("combobox", { name: "Automatic size" }), matchMedia("(pointer: coarse)").matches ? "44px" : "32px");
    await expectNoHorizontalOverflow(canvasElement);
  },
};

export const KeyboardFocus: Story = {
  args: { size: "touch" },
  play: async ({ canvas, userEvent }) => {
    await userEvent.tab();
    const select = canvas.getByRole("combobox", { name: "State field" });
    await expectFieldSize(select, "44px");
    await expectFieldFocus(select);
  },
};
