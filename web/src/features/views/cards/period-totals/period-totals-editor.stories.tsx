import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import type { ComponentProps } from "react";
import { expect, fn } from "storybook/test";

import { entityId } from "../../../../../.storybook/fixtures";
import { PeriodTotalsEditor } from "./editor";

const Draft = (args: ComponentProps<typeof PeriodTotalsEditor>) => {
  const [value, setValue] = useState(args.value);
  return (
    <PeriodTotalsEditor
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
  title: "Components/Editors/PeriodTotalsEditor",
  component: PeriodTotalsEditor,
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
      label: "Front door",
      source: {
        kind: "entity-state",
        entity_id: entityId(1),
        entity_type: "hearth.binarysensor/v1",
      },
      period: "day",
      style: "columns",
    },
    onChange: fn(),
  },
  render: (args) => <Draft {...args} />,
} satisfies Meta<typeof PeriodTotalsEditor>;
export default meta;
type Story = StoryObj<typeof meta>;

export const ChangePeriod: Story = {
  play: async ({ canvas, userEvent, args }) => {
    await userEvent.selectOptions(canvas.getByLabelText("Period"), "hour");
    await expect(args.onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ period: "hour" })
    );
  },
};
export const ChangeStyle: Story = {
  play: async ({ canvas, userEvent, args }) => {
    await userEvent.click(canvas.getByRole("button", { name: "Ledger" }));
    await expect(args.onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ style: "ledger" })
    );
  },
};
export const ChangeLabel: Story = {
  play: async ({ canvas, userEvent, args }) => {
    const input = canvas.getByLabelText("Label");
    await userEvent.clear(input);
    await userEvent.type(input, "Back door");
    await expect(args.onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ label: "Back door" })
    );
  },
};
export const CustomWords: Story = {
  play: async ({ canvas, userEvent, args }) => {
    await userEvent.click(canvas.getByLabelText("Use custom words"));
    await expect(args.onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ words: { true: "On", false: "Off" } })
    );
  },
};
