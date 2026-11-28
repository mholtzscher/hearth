import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import type { ComponentProps } from "react";
import { expect, fn } from "storybook/test";

import { entityId } from "../../../../../.storybook/fixtures";
import { TimeInRangeEditor } from "./editor";

const Draft = (args: ComponentProps<typeof TimeInRangeEditor>) => {
  const [value, setValue] = useState(args.value);
  const [syncedValue, setSyncedValue] = useState(args.value);
  if (args.value !== syncedValue) {
    setSyncedValue(args.value);
    setValue(args.value);
  }
  return (
    <TimeInRangeEditor
      {...args}
      value={value}
      onChange={(next) => {
        args.onChange(next);
        setValue(next);
      }}
    />
  );
};

const meta = {
  title: "Components/Editors/TimeInRangeEditor",
  component: TimeInRangeEditor,
  tags: ["ai-generated"],
  decorators: [
    (Story) => (
      <div className="text-label grid gap-3">
        <Story />
      </div>
    ),
  ],
  args: {
    value: {
      label: "Living room",
      source: {
        kind: "entity-state",
        entity_id: entityId(15),
        entity_type: "hearth.temperature/v1",
      },
      window: "7d",
      thresholds: [20_000, 22_000],
    },
    onChange: fn(),
  },
  render: (args) => <Draft {...args} />,
  parameters: {
    docs: {
      description: {
        component:
          "The TimeInRange card's fields in the dashboard editor. Thresholds are entered in the field's canonical unit, which the legend names.",
      },
    },
  },
} satisfies Meta<typeof TimeInRangeEditor>;
export default meta;
type Story = StoryObj<typeof meta>;

export const ChangeWindow: Story = {
  play: async ({ canvas, userEvent, args }) => {
    await userEvent.selectOptions(canvas.getByLabelText("Time window"), "24h");
    await expect(args.onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ window: "24h" })
    );
  },
};
export const EditThreshold: Story = {
  play: async ({ canvas, userEvent, args }) => {
    await expect(canvas.getByText("Thresholds, in mCel")).toBeVisible();
    const lower = canvas.getByLabelText("Lower threshold (mCel)");
    await userEvent.clear(lower);
    await userEvent.type(lower, "19500");
    await expect(args.onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ thresholds: [19_500, 22_000] })
    );
  },
};
export const DropUpperThreshold: Story = {
  play: async ({ canvas, userEvent, args }) => {
    await userEvent.click(
      canvas.getByRole("button", { name: "Remove the upper threshold" })
    );
    await expect(args.onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ thresholds: [20_000] })
    );
    await expect(canvas.getByLabelText("Threshold (mCel)")).toBeVisible();
    await userEvent.click(
      canvas.getByRole("button", { name: "Add an upper threshold" })
    );
    await expect(args.onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ thresholds: [20_000, 22_000] })
    );
  },
};
export const ThresholdsOutOfOrder: Story = {
  args: { value: { ...meta.args.value, thresholds: [22_000, 20_000] } },
  play: async ({ canvas }) => {
    await expect(
      canvas.getByText("The upper threshold must be above the lower one.")
    ).toBeVisible();
  },
};
export const SeveralFields: Story = {
  args: {
    value: {
      ...meta.args.value,
      source: {
        kind: "entity-state",
        entity_id: entityId(501),
        entity_type: "hearth.thermostat/v1",
        field: "/current_temperature",
      },
    },
  },
  play: async ({ canvas, userEvent, args }) => {
    await userEvent.selectOptions(
      canvas.getByLabelText("State field"),
      "/current_humidity"
    );
    await expect(args.onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({
        source: expect.objectContaining({ field: "/current_humidity" }),
      })
    );
    await expect(canvas.getByText("Thresholds, in %")).toBeVisible();
  },
};
