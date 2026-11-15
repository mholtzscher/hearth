import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { expect, fn, waitFor, within } from "storybook/test";
import { expectNoHorizontalOverflow } from "../../../.storybook/play.ts";
import { SegmentedControl } from "./segmented-control.tsx";

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
    await expect(getComputedStyle(group.getByRole("button", { name: "Auto" })).height).toBe("32px");
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
    const style = getComputedStyle(auto);
    await expect(style.height).toBe("44px");
    await expect(style.borderRadius).toBe("8px");
    await expect(style.outlineWidth).toBe("2px");
    await expect(style.outlineStyle).toBe("solid");
    await expect(style.outlineOffset).toBe("3px");
    await waitFor(() => expect(getComputedStyle(auto).outlineColor).toBe("rgb(167, 123, 255)"));
    await userEvent.keyboard("{Enter}");
    await expect(args.onValueChange).toHaveBeenCalledWith("auto");
    await userEvent.tab();
    await expect(canvas.getByRole("button", { name: "Sleep" })).toHaveFocus();
    await userEvent.keyboard(" ");
    await expect(args.onValueChange).toHaveBeenCalledWith("sleep");
    await expectNoHorizontalOverflow(canvasElement);
  },
};

export const Controlled: Story = {
  render: function Controlled(args) {
    const [value, setValue] = useState(args.value);
    return <form onSubmit={event => { event.preventDefault(); args.onValueChange("submitted"); }}>
      <SegmentedControl {...args} value={value} onValueChange={setValue} />
    </form>;
  },
  play: async ({ canvas, userEvent, args }) => {
    await userEvent.click(canvas.getByRole("button", { name: "Sleep" }));
    await expect(canvas.getByRole("button", { name: "Sleep" })).toHaveAttribute("aria-pressed", "true");
    await expect(canvas.getByRole("button", { name: "Auto" })).toHaveAttribute("aria-pressed", "false");
    await expect(args.onValueChange).not.toHaveBeenCalled();
  },
};

export const LongLabels: Story = {
  render: args => <div className="grid w-[180px] gap-6">
    {(["default", "touch"] as const).map(size => <SegmentedControl key={size} {...args}
      label={`${size} mode`} size={size} options={[
        { value: "auto", label: <span className="block">Automatic air quality monitoring</span> },
        { value: "sleep", label: <span className="block">Quiet night-time operation</span> },
        { value: "manual", label: <span className="block">Manual fan speed control</span> },
      ]} />)}
  </div>,
  play: async ({ canvas, canvasElement, userEvent, args }) => {
    await canvasElement.ownerDocument.fonts.ready;
    for (const size of ["default", "touch"]) {
      const group = within(canvas.getByRole("group", { name: `${size} mode` }));
      for (const name of ["Automatic air quality monitoring", "Quiet night-time operation", "Manual fan speed control"]) {
        const button = group.getByRole("button", { name });
        const label = within(button).getByText(name);
        const bounds = button.getBoundingClientRect();
        const text = label.getBoundingClientRect();
        await expect(text.top, `${size} ${name}: top`).toBeGreaterThanOrEqual(bounds.top);
        await expect(text.bottom, `${size} ${name}: bottom`).toBeLessThanOrEqual(bounds.bottom);
        await expect(text.left, `${size} ${name}: left`).toBeGreaterThanOrEqual(bounds.left);
        await expect(text.right, `${size} ${name}: right`).toBeLessThanOrEqual(bounds.right);
        await expect(button.scrollWidth).toBeLessThanOrEqual(button.clientWidth);
        await expect(button.scrollHeight).toBeLessThanOrEqual(button.clientHeight);
      }
    }
    const first = within(canvas.getByRole("group", { name: "default mode" })).getByRole("button", { name: "Automatic air quality monitoring" });
    const second = within(canvas.getByRole("group", { name: "touch mode" })).getByRole("button", { name: "Automatic air quality monitoring" });
    await userEvent.tab();
    await expect(first).toHaveFocus();
    await expect(getComputedStyle(first).outlineWidth).toBe("2px");
    await userEvent.keyboard("{Enter}");
    await expect(args.onValueChange).toHaveBeenCalledWith("auto");
    await userEvent.tab();
    await userEvent.tab();
    await userEvent.tab();
    await expect(second).toHaveFocus();
    await expect(getComputedStyle(second).outlineWidth).toBe("2px");
    await userEvent.keyboard(" ");
    await expect(args.onValueChange).toHaveBeenCalledTimes(2);
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
    <SegmentedControl {...args} label="Disabled shared mode" disabled />
    <div role="group" aria-label="Disabled legacy mode" className="view-air-purifier-segmented">
      {options.map(option => <button key={option.value} type="button" aria-pressed={args.value === option.value}
        disabled>{option.label}</button>)}
    </div>
  </div>,
  play: async ({ canvas, canvasElement }) => {
    await canvasElement.ownerDocument.fonts.ready;
    const shared = within(canvas.getByRole("group", { name: "Shared mode" }));
    const legacy = within(canvas.getByRole("group", { name: "Legacy mode" }));
    const sharedFrame = getComputedStyle(canvas.getByRole("group", { name: "Shared mode" }));
    const legacyFrame = getComputedStyle(canvas.getByRole("group", { name: "Legacy mode" }));
    for (const property of ["backgroundColor", "borderColor", "borderWidth", "borderStyle", "borderRadius", "padding", "gap"] as const) {
      await expect(sharedFrame[property], `frame: ${property}`).toBe(legacyFrame[property]);
    }
    for (const name of ["Auto", "Sleep"]) {
      const a = getComputedStyle(shared.getByRole("button", { name }));
      const b = getComputedStyle(legacy.getByRole("button", { name }));
      for (const property of ["backgroundColor", "color", "fontFamily", "fontSize", "fontWeight", "lineHeight"] as const) {
        await expect(a[property], `${name}: ${property}`).toBe(b[property]);
      }
      if (name === "Auto") await expect(a.boxShadow).toContain(b.boxShadow);
      await expect(a.height).toBe("32px");
      await expect(b.height).toBe("38px");
      await expect(a.borderRadius).toBe("8px");
      await expect(b.borderRadius).toBe("6px");
    }
    const enabledSelected = getComputedStyle(shared.getByRole("button", { name: "Auto" }));
    const disabledShared = getComputedStyle(within(canvas.getByRole("group", { name: "Disabled shared mode" })).getByRole("button", { name: "Auto" }));
    const disabledLegacy = getComputedStyle(within(canvas.getByRole("group", { name: "Disabled legacy mode" })).getByRole("button", { name: "Auto" }));
    await expect(disabledShared.backgroundColor).toBe(enabledSelected.backgroundColor);
    await expect(disabledShared.color).toBe(enabledSelected.color);
    await expect(disabledShared.boxShadow).toBe(enabledSelected.boxShadow);
    await expect(disabledShared.opacity).toBe("0.5");
    await expect(disabledLegacy.backgroundColor).toBe("rgb(31, 31, 31)");
    await expect(disabledLegacy.color).toBe("rgb(125, 125, 125)");
    await expect(disabledLegacy.boxShadow).toContain("rgb(61, 61, 61)");
    await expect(disabledLegacy.opacity).toBe("1");
  },
};
