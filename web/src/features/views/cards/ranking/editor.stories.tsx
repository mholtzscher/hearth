import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import type { ComponentProps } from "react";
import { expect, fn } from "storybook/test";

import { createEntities, entityId } from "../../../../../.storybook/fixtures";
import { RankingEditor } from "./editor";

const Draft = (args: ComponentProps<typeof RankingEditor>) => {
  const [value, setValue] = useState(args.value);
  const [syncedValue, setSyncedValue] = useState(args.value);
  if (args.value !== syncedValue) {
    setSyncedValue(args.value);
    setValue(args.value);
  }
  return (
    <RankingEditor
      {...args}
      value={value}
      onChange={(next) => {
        args.onChange(next);
        setValue(next);
      }}
    />
  );
};

const row = (number: number, label?: string) => ({
  label,
  source: {
    kind: "entity-state" as const,
    entity_id: entityId(number),
    entity_type: "hearth.temperature/v1" as const,
  },
});

const meta = {
  title: "Components/Editors/RankingEditor",
  component: RankingEditor,
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
      title: "Room temperatures",
      rows: [row(101, "Outdoor"), row(114, "Bedroom")],
    },
    choices: createEntities(),
    onChange: fn(),
  },
  render: (args) => <Draft {...args} />,
  parameters: {
    docs: {
      description: {
        component:
          "The Ranking card's fields in the dashboard editor: its title, optional range window, and two to twelve readings that share one canonical unit.",
      },
    },
  },
} satisfies Meta<typeof RankingEditor>;
export default meta;
type Story = StoryObj<typeof meta>;

export const ChooseRangeWindow: Story = {
  play: async ({ canvas, userEvent, args }) => {
    await userEvent.selectOptions(
      canvas.getByLabelText("Show each row's range over"),
      "7d"
    );
    await expect(args.onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ range: "7d" })
    );
    await userEvent.selectOptions(
      canvas.getByLabelText("Show each row's range over"),
      ""
    );
    await expect(args.onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ range: undefined })
    );
  },
};
export const AddReading: Story = {
  play: async ({ canvas, userEvent, args }) => {
    await userEvent.selectOptions(
      canvas.getByLabelText("Add reading"),
      entityId(15)
    );
    await expect(args.onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({
        rows: expect.arrayContaining([
          {
            source: expect.objectContaining({ entity_id: entityId(15) }),
          },
        ]),
      })
    );
    await expect(
      await canvas.findByRole("textbox", { name: "Row 3 label" })
    ).toBeVisible();
  },
};
export const OnlyCompatibleReadings: Story = {
  play: async ({ canvas }) => {
    const add = canvas.getByLabelText("Add reading");
    await expect(
      add.querySelector(`option[value="${entityId(15)}"]`)
    ).not.toBeNull();
    // Rain is in millimetres, not milli-Celsius.
    await expect(
      add.querySelector(`option[value="${entityId(106)}"]`)
    ).toBeNull();
    // Already ranked.
    await expect(
      add.querySelector(`option[value="${entityId(101)}"]`)
    ).toBeNull();
  },
};
export const LabelRow: Story = {
  play: async ({ canvas, userEvent, args }) => {
    const label = canvas.getByRole("textbox", { name: "Row 2 label" });
    await userEvent.clear(label);
    await userEvent.type(label, "Guest room");
    await expect(args.onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({
        rows: [
          expect.objectContaining({ label: "Outdoor" }),
          expect.objectContaining({ label: "Guest room" }),
        ],
      })
    );
  },
};
/** A ranking needs two rows, so the last two cannot be removed. */
export const RemoveRow: Story = {
  args: {
    value: {
      title: "Room temperatures",
      rows: [row(101, "Outdoor"), row(114, "Bedroom"), row(15, "Living room")],
    },
  },
  play: async ({ canvas, userEvent, args }) => {
    await userEvent.click(canvas.getByRole("button", { name: "Remove row 2" }));
    await expect(args.onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({
        rows: [
          expect.objectContaining({ label: "Outdoor" }),
          expect.objectContaining({ label: "Living room" }),
        ],
      })
    );
    await expect(
      canvas.getByRole("button", { name: "Remove row 1" })
    ).toBeDisabled();
  },
};
export const TitleRequired: Story = {
  args: { value: { ...meta.args.value, title: "" } },
  play: async ({ canvas }) => {
    await expect(
      canvas.getByText("Use 1–120 characters for the ranking title.")
    ).toBeVisible();
  },
};
