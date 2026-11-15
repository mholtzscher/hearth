import type { Meta, StoryObj } from "@storybook/react-vite";
import { useEffect, useState } from "react";
import { expect, fn } from "storybook/test";
import { entityId } from "../../../../../.storybook/fixtures";
import { MetricEditor } from "./Editor";

const meta = {
  title: "features/views/cards/MetricEditor",
  component: MetricEditor,
  tags: ["ai-generated"],
  decorators: [Story => <div className="grid gap-3 text-label leading-normal"><Story /></div>],
  args: {
    value: {
      label: "Outdoor humidity", trend: "24h", range: true,
      source: { kind: "entity-state", entity_id: entityId(102), entity_type: "hearth.relativehumidity/v1" },
    },
    onChange: fn(),
  },
  render: function Draft(args) {
    const [value, setValue] = useState(args.value);
    useEffect(() => setValue(args.value), [args.value]);
    return <MetricEditor {...args} value={value} onChange={next => { args.onChange(next); setValue(next); }} />;
  },
  parameters: { docs: { description: { component: "The Metric card's fields in the dashboard editor. It reports each edit to the owning draft and keeps no state." } } },
} satisfies Meta<typeof MetricEditor>;
export default meta;
type Story = StoryObj<typeof meta>;

export const RemoveTrend: Story = {
  play: async ({ canvas, userEvent, args }) => {
    await userEvent.selectOptions(canvas.getByLabelText("Trend"), "");
    await expect(args.onChange).toHaveBeenLastCalledWith(expect.objectContaining({ trend: undefined }));
    await expect(canvas.getByLabelText("Trend")).toHaveValue("");
  },
};
export const HideRange: Story = {
  play: async ({ canvas, userEvent, args }) => {
    await userEvent.click(canvas.getByRole("checkbox", { name: "Show range" }));
    await expect(args.onChange).toHaveBeenLastCalledWith(expect.objectContaining({ range: undefined }));
  },
};
export const LabelTooLong: Story = {
  args: { value: { ...meta.args.value, label: "x".repeat(121) } },
  play: async ({ canvas }) => {
    await expect(canvas.getByRole("textbox", { name: "Label" })).toHaveAttribute("aria-invalid", "true");
    await expect(canvas.getByText("Use at most 120 characters for the label.")).toBeVisible();
  },
};
