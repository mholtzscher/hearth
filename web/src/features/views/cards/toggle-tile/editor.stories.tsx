import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import type { ComponentProps } from "react";
import { expect, fn, waitFor } from "storybook/test";

import { createEntities, entityId } from "../../../../../.storybook/fixtures";
import { ToggleTileEditor } from "./editor";

const Draft = (args: ComponentProps<typeof ToggleTileEditor>) => {
  const [value, setValue] = useState(args.value);
  const [syncedValue, setSyncedValue] = useState(args.value);
  if (args.value !== syncedValue) {
    setSyncedValue(args.value);
    setValue(args.value);
  }
  return (
    <ToggleTileEditor
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
  title: "Components/Editors/ToggleTileEditor",
  component: ToggleTileEditor,
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
      label: "Office plug",
      source: {
        kind: "entity-state",
        entity_id: entityId(4),
        entity_type: "hearth.power/v1",
      },
      metric: {
        source: {
          kind: "entity-state",
          entity_id: entityId(6),
          entity_type: "hearth.numericsensor/v1",
        },
        window: "24h",
        detail: {
          source: {
            kind: "entity-state",
            entity_id: entityId(7),
            entity_type: "hearth.numericsensor/v1",
          },
          label: "Total energy",
        },
      },
    },
    choices: createEntities(),
    onChange: fn(),
  },
  render: (args) => <Draft {...args} />,
  parameters: {
    docs: {
      description: {
        component:
          "The ToggleTile card's fields in the dashboard editor. Sparkline choices come from the power Entity's own Device.",
      },
    },
  },
} satisfies Meta<typeof ToggleTileEditor>;
export default meta;
type Story = StoryObj<typeof meta>;

export const ChangeWindow: Story = {
  play: async ({ canvas, userEvent, args }) => {
    await userEvent.selectOptions(
      canvas.getByLabelText("Sparkline window"),
      "7d"
    );
    await expect(args.onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({
        metric: expect.objectContaining({ window: "7d" }),
      })
    );
  },
};
/** Removing the sparkline source removes its window and additional reading too. */
export const RemoveSparkline: Story = {
  play: async ({ canvas, userEvent, args }) => {
    await userEvent.selectOptions(
      canvas.getByLabelText("Sparkline source"),
      ""
    );
    await expect(args.onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ metric: undefined })
    );
    await waitFor(() =>
      expect(
        canvas.queryByLabelText("Sparkline window")
      ).not.toBeInTheDocument()
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
