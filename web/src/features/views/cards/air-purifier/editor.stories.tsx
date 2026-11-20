import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { expect, fn } from "storybook/test";

import { createEntities, entityId } from "../../../../../.storybook/fixtures";
import { AirPurifierEditor } from "./editor";

const Draft = (args: Parameters<typeof AirPurifierEditor>[0]) => {
  const [value, setValue] = useState(args.value);
  const [previous, setPrevious] = useState(args.value);
  if (args.value !== previous) {
    setPrevious(args.value);
    setValue(args.value);
  }
  return (
    <AirPurifierEditor
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
  title: "Components/Editors/AirPurifierEditor",
  component: AirPurifierEditor,
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
      layout: "card",
      label: "Living room purifier",
      power: {
        kind: "entity-state",
        entity_id: entityId(301),
        entity_type: "hearth.power/v1",
      },
      pm25: {
        kind: "entity-state",
        entity_id: entityId(302),
        entity_type: "hearth.numericsensor/v1",
      },
      filter_life: {
        kind: "entity-state",
        entity_id: entityId(303),
        entity_type: "hearth.numericsensor/v1",
      },
      mode: {
        kind: "entity-state",
        entity_id: entityId(304),
        entity_type: "hearth.enumsetting/v1",
      },
      fan_level: {
        kind: "entity-state",
        entity_id: entityId(305),
        entity_type: "hearth.enumsetting/v1",
      },
      display: {
        kind: "entity-state",
        entity_id: entityId(306),
        entity_type: "hearth.power/v1",
      },
      child_lock: {
        kind: "entity-state",
        entity_id: entityId(307),
        entity_type: "hearth.power/v1",
      },
    },
    choices: createEntities(),
    onChange: fn(),
  },
  render: Draft,
  parameters: {
    docs: {
      description: {
        component:
          "The AirPurifier card's fields in the dashboard editor. Each role offers only compatible Entities from the power Entity's Device.",
      },
    },
  },
} satisfies Meta<typeof AirPurifierEditor>;
export default meta;
type Story = StoryObj<typeof meta>;

export const SwitchToTile: Story = {
  play: async ({ canvas, userEvent, args }) => {
    await userEvent.selectOptions(canvas.getByLabelText("Layout"), "tile");
    await expect(args.onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ layout: "tile" })
    );
    await expect(canvas.getByLabelText("Layout")).toHaveValue("tile");
  },
};
export const ClearOptionalRole: Story = {
  play: async ({ canvas, userEvent, args }) => {
    await userEvent.selectOptions(canvas.getByLabelText("Mode"), "");
    await expect(args.onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ mode: undefined })
    );
  },
};
export const DeviceUnknown: Story = {
  args: { choices: [] },
  play: async ({ canvas }) => {
    await expect(
      canvas.getByText(
        "Device information is unavailable. Existing source selections are preserved."
      )
    ).toBeVisible();
  },
};
