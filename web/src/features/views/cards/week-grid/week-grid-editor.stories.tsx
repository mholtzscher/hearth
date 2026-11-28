import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import type { ComponentProps } from "react";
import { expect, fn } from "storybook/test";

import { entityId } from "../../../../../.storybook/fixtures";
import { WeekGridEditor } from "./editor";

const Draft = (args: ComponentProps<typeof WeekGridEditor>) => {
  const [value, setValue] = useState(args.value);
  return (
    <WeekGridEditor
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
  title: "Components/Editors/WeekGridEditor",
  component: WeekGridEditor,
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
      label: "Living room temperature",
      source: {
        kind: "entity-state",
        entity_id: entityId(15),
        entity_type: "hearth.temperature/v1",
      },
    },
    onChange: fn(),
  },
  render: (args) => <Draft {...args} />,
} satisfies Meta<typeof WeekGridEditor>;
export default meta;
type Story = StoryObj<typeof meta>;

export const ChangeLabel: Story = {
  play: async ({ canvas, userEvent, args }) => {
    const input = canvas.getByLabelText("Label");
    await userEvent.clear(input);
    await userEvent.type(input, "Lounge");
    await expect(args.onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ label: "Lounge" })
    );
  },
};
export const ChooseField: Story = {
  args: {
    value: {
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
      "/target_temperature"
    );
    await expect(args.onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({
        source: expect.objectContaining({ field: "/target_temperature" }),
      })
    );
  },
};
