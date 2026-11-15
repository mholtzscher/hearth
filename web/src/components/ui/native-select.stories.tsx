import type { ComponentProps } from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { expectNoHorizontalOverflow } from "../../../.storybook/play.ts";

/** Pre-migration baseline: exercise the native household control before adding its shared wrapper. */
function HouseholdSelect(props: ComponentProps<"select">) {
  return <div className="view-editor"><select {...props} /></div>;
}

const meta = {
  title: "components/ui/native-select",
  component: HouseholdSelect,
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
} satisfies Meta<typeof HouseholdSelect>;
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
    await userEvent.selectOptions(select, "humidity");
    await expect(select).toHaveValue("temperature");
  },
};

export const Invalid: Story = {
  args: { "aria-invalid": true },
};

export const Sizes: Story = {
  render: (args) => (
    <div className="grid gap-3">
      <HouseholdSelect {...args} aria-label="Default size" />
      <HouseholdSelect {...args} aria-label="Touch size" />
      <HouseholdSelect {...args} aria-label="Automatic size" />
    </div>
  ),
  play: async ({ canvasElement }) => expectNoHorizontalOverflow(canvasElement),
};

export const KeyboardFocus: Story = {
  play: async ({ canvas, userEvent }) => {
    await userEvent.tab();
    await expect(canvas.getByRole("combobox", { name: "State field" })).toHaveFocus();
  },
};

/** Temporary household reference; remove when the editor adopts NativeSelect. */
export const HouseholdComparison: Story = {
  render: (args) => (
    <div className="grid gap-4">
      <div className="view-editor">
        <select {...args} aria-label="Legacy State field" />
      </div>
      <HouseholdSelect {...args} aria-label="Shared State field" />
    </div>
  ),
  play: async ({ canvasElement }) => expectNoHorizontalOverflow(canvasElement),
};
