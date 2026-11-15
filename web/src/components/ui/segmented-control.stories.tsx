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

async function expectSelected(button: HTMLElement) {
  const reference = button.ownerDocument.createElement("span");
  reference.style.backgroundColor = "color-mix(in oklch, rgb(167, 123, 255) 16%, transparent)";
  button.ownerDocument.body.append(reference);
  const expectedBackground = getComputedStyle(reference).backgroundColor;
  reference.remove();
  const style = getComputedStyle(button);
  await expect(style.backgroundColor).toBe(expectedBackground);
  await expect(style.color).toBe("rgb(196, 168, 255)");
  await expect(style.boxShadow).toContain("rgb(167, 123, 255) 0px 0px 0px 1px inset");
}

export const Default: Story = {
  play: async ({ canvas, userEvent, args }) => {
    const group = within(canvas.getByRole("group", { name: "Purifier mode" }));
    await expect(group.getByRole("button", { name: "Auto" })).toHaveAttribute("aria-pressed", "true");
    await expect(group.getByRole("button", { name: "Sleep" })).toHaveAttribute("aria-pressed", "false");
    await expect(group.getByRole("button", { name: "Manual" })).toBeDisabled();
    await expect(getComputedStyle(group.getByRole("button", { name: "Auto" })).height).toBe("32px");
    await expectSelected(group.getByRole("button", { name: "Auto" }));
    await expect(getComputedStyle(group.getByRole("button", { name: "Sleep" })).backgroundColor).toBe("rgba(0, 0, 0, 0)");
    await expect(getComputedStyle(group.getByRole("button", { name: "Manual" })).opacity).toBe("0.5");
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
    await expectSelected(canvas.getByRole("button", { name: "Sleep" }));
    await expect(getComputedStyle(canvas.getByRole("button", { name: "Auto" })).backgroundColor).toBe("rgba(0, 0, 0, 0)");
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
    const selected = canvas.getByRole("button", { name: "Auto" });
    await expect(selected).toHaveAttribute("aria-pressed", "true");
    await expectSelected(selected);
    await expect(getComputedStyle(selected).opacity).toBe("0.5");
    await expect(getComputedStyle(canvas.getByRole("button", { name: "Sleep" })).opacity).toBe("0.5");
    await userEvent.click(buttons[1]);
    await expect(args.onValueChange).not.toHaveBeenCalled();
  },
};
