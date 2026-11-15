import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import type { ComponentProps } from "react";
import { expect, fn } from "storybook/test";

import { createEntities, entityId } from "../../../../../.storybook/fixtures";
import { TimeSeriesEditor } from "./editor";

const Draft = (args: ComponentProps<typeof TimeSeriesEditor>) => {
  const [value, setValue] = useState(args.value);
  const [syncedValue, setSyncedValue] = useState(args.value);
  if (args.value !== syncedValue) {
    setSyncedValue(args.value);
    setValue(args.value);
  }
  return (
    <TimeSeriesEditor
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
  title: "Components/Editors/TimeSeriesEditor",
  component: TimeSeriesEditor,
  tags: ["ai-generated"],
  decorators: [
    (Story) => (
      <div className="text-label grid gap-3 leading-normal">
        <Story />
      </div>
    ),
  ],
  args: {
    value: {
      title: "Temperatures",
      series: [
        {
          label: "Outdoor",
          source: {
            kind: "entity-series",
            entity_id: entityId(101),
            entity_type: "hearth.temperature/v1",
            window: "24h",
          },
        },
        {
          label: "Bedroom",
          source: {
            kind: "entity-series",
            entity_id: entityId(114),
            entity_type: "hearth.temperature/v1",
            window: "24h",
          },
        },
      ],
    },
    choices: createEntities(),
    onChange: fn(),
  },
  render: (args) => <Draft {...args} />,
  parameters: {
    docs: {
      description: {
        component:
          "The TimeSeries card's fields in the dashboard editor. Every series shares one window, and only sensors with the chart's unit can be added.",
      },
    },
  },
} satisfies Meta<typeof TimeSeriesEditor>;
export default meta;
type Story = StoryObj<typeof meta>;

export const ChangeWindow: Story = {
  play: async ({ canvas, userEvent, args }) => {
    await userEvent.selectOptions(canvas.getByLabelText("Time window"), "7d");
    await expect(args.onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({
        series: [
          expect.objectContaining({
            source: expect.objectContaining({ window: "7d" }),
          }),
          expect.objectContaining({
            source: expect.objectContaining({ window: "7d" }),
          }),
        ],
      })
    );
  },
};
export const AddSensor: Story = {
  play: async ({ canvas, userEvent, args }) => {
    await userEvent.selectOptions(
      canvas.getByLabelText("Add sensor"),
      entityId(15)
    );
    await expect(args.onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({
        series: expect.arrayContaining([
          {
            source: expect.objectContaining({
              entity_id: entityId(15),
              window: "24h",
            }),
          },
        ]),
      })
    );
    await expect(
      await canvas.findByRole("textbox", { name: "Series 3 label" })
    ).toBeVisible();
  },
};
export const RemoveSeries: Story = {
  play: async ({ canvas, userEvent, args }) => {
    await userEvent.click(
      canvas.getByRole("button", { name: "Remove series 2" })
    );
    await expect(args.onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({
        series: [expect.objectContaining({ label: "Outdoor" })],
      })
    );
  },
};
export const TitleRequired: Story = {
  args: { value: { ...meta.args.value, title: "" } },
  play: async ({ canvas }) => {
    await expect(
      canvas.getByText("Use 1–120 characters for the chart title.")
    ).toBeVisible();
  },
};
