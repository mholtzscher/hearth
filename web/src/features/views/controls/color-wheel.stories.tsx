import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import type { ComponentProps } from "react";
import { expect, fn } from "storybook/test";

import { ColorWheel } from "./color-wheel";

const ControlledWheel = (args: ComponentProps<typeof ColorWheel>) => {
  const [value, setValue] = useState(args.value);
  const [committed, setCommitted] = useState("");
  const handleChange = (next: typeof value) => {
    args.onChange(next);
    setValue(next);
  };
  return (
    <>
      <ColorWheel
        {...args}
        value={value}
        onChange={handleChange}
        onEnd={(next) => {
          args.onEnd(next);
          setValue(next);
          setCommitted(
            `Committed hue ${next.hue}, saturation ${next.saturation}`
          );
        }}
      />
      <output>{committed}</output>
    </>
  );
};

const meta = {
  title: "Components/Controls/ColorWheel",
  component: ColorWheel,
  render: ControlledWheel,
  args: {
    value: { hue: 0, saturation: 50 },
    disabled: false,
    pending: false,
    onBegin: fn(),
    onChange: fn(),
    onEnd: fn(),
    onCancel: fn(),
    onBlur: fn(),
  },
} satisfies Meta<typeof ColorWheel>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Keyboard: Story = {
  play: async ({ canvas, userEvent, args }) => {
    const wheel = canvas.getByRole("slider", { name: "Color wheel" });
    await userEvent.tab();
    await expect(wheel).toHaveFocus();
    await userEvent.keyboard("{ArrowRight}{ArrowUp}");
    await expect(wheel).toHaveAttribute(
      "aria-valuetext",
      "Hue 1 degrees, saturation 51 percent"
    );
    await userEvent.keyboard("{Shift>}{ArrowRight}{/Shift}{Home}{End}");
    await expect(wheel).toHaveValue("11");
    await expect(wheel).toHaveAttribute(
      "aria-valuetext",
      "Hue 11 degrees, saturation 51 percent"
    );
    await expect(args.onChange).toHaveBeenCalled();
  },
};

// Pointer capture needs trusted browser input, not synthetic userEvent.pointer.
// The committed value makes the native release path observable in the preview.
export const Pointer: Story = {};

export const Disabled: Story = {
  args: { disabled: true },
  play: async ({ canvas, userEvent, args }) => {
    const wheel = canvas.getByRole("slider", { name: "Color wheel" });
    await expect(wheel).toBeDisabled();
    await userEvent.tab();
    await expect(wheel).not.toHaveFocus();
    await userEvent.click(wheel);
    await expect(args.onChange).not.toHaveBeenCalled();
    await expect(args.onBegin).not.toHaveBeenCalled();
  },
};
