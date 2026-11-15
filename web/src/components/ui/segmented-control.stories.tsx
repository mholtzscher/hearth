import type { Meta, StoryObj } from "@storybook/react-vite";
import type { ReactNode } from "react";
import { expect, fn, within } from "storybook/test";
import { expectNoHorizontalOverflow } from "../../../.storybook/play.ts";

// Temporary baseline using the household air-purifier treatment.
function SegmentedControl({ label, value, onValueChange, options, disabled }: {
  label: string;
  value: string;
  onValueChange: (value: string) => void;
  options: Array<{ value: string; label: ReactNode; disabled?: boolean }>;
  size?: "default" | "touch";
  disabled?: boolean;
}) {
  return <div role="group" aria-label={label} className="view-air-purifier-segmented">
    {options.map(option => <button key={option.value} type="button" aria-pressed={value === option.value}
      disabled={disabled || option.disabled} onClick={() => onValueChange(option.value)}>{option.label}</button>)}
  </div>;
}

const options = [
  { value: "auto", label: "Auto" },
  { value: "sleep", label: "Sleep" },
  { value: "manual", label: "Manual", disabled: true },
];

const meta = {
  component: SegmentedControl,
  args: { label: "Purifier mode", value: "auto", options, onValueChange: fn() },
} satisfies Meta<typeof SegmentedControl>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
  play: async ({ canvas, userEvent, args }) => {
    const group = within(canvas.getByRole("group", { name: "Purifier mode" }));
    await expect(group.getByRole("button", { name: "Auto" })).toHaveAttribute("aria-pressed", "true");
    await expect(group.getByRole("button", { name: "Sleep" })).toHaveAttribute("aria-pressed", "false");
    await expect(group.getByRole("button", { name: "Manual" })).toBeDisabled();
    await userEvent.click(group.getByRole("button", { name: "Sleep" }));
    await expect(args.onValueChange).toHaveBeenCalledWith("sleep");
    await expect(group.getByRole("button", { name: "Auto" })).toHaveAttribute("aria-pressed", "true");
  },
};

export const Touch: Story = {
  args: { size: "touch" },
  play: async ({ canvas, userEvent, args, canvasElement }) => {
    await userEvent.tab();
    const auto = canvas.getByRole("button", { name: "Auto" });
    await expect(auto).toHaveFocus();
    await userEvent.keyboard("{Enter}");
    await expect(args.onValueChange).toHaveBeenCalledWith("auto");
    await userEvent.tab();
    await expect(canvas.getByRole("button", { name: "Sleep" })).toHaveFocus();
    await userEvent.keyboard(" ");
    await expect(args.onValueChange).toHaveBeenCalledWith("sleep");
    await expectNoHorizontalOverflow(canvasElement);
  },
};

export const Disabled: Story = {
  args: { disabled: true },
  play: async ({ canvas, userEvent, args }) => {
    const buttons = canvas.getAllByRole("button");
    for (const button of buttons) await expect(button).toBeDisabled();
    await userEvent.click(buttons[1]);
    await expect(args.onValueChange).not.toHaveBeenCalled();
  },
};

/** Temporary migration reference. Remove after the air-purifier control adopts the shared component. */
export const HouseholdComparison: Story = {
  render: args => <div className="grid gap-4">
    <SegmentedControl {...args} label="Shared mode" />
    <div role="group" aria-label="Legacy mode" className="view-air-purifier-segmented">
      {options.map(option => <button key={option.value} type="button" aria-pressed={args.value === option.value}
        disabled={option.disabled}>{option.label}</button>)}
    </div>
  </div>,
  play: async ({ canvas, canvasElement }) => {
    await canvasElement.ownerDocument.fonts.ready;
    const shared = within(canvas.getByRole("group", { name: "Shared mode" }));
    const legacy = within(canvas.getByRole("group", { name: "Legacy mode" }));
    for (const name of ["Auto", "Sleep"]) {
      const a = getComputedStyle(shared.getByRole("button", { name }));
      const b = getComputedStyle(legacy.getByRole("button", { name }));
      for (const property of ["backgroundColor", "color", "boxShadow", "fontFamily", "fontSize", "fontWeight", "lineHeight"] as const) {
        await expect(a[property], `${name}: ${property}`).toBe(b[property]);
      }
    }
  },
};
