import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, waitFor } from "storybook/test";
import { Switch } from "./switch";

const meta = {
  component: Switch,
  tags: ["ai-generated"],
  args: { "aria-label": "Enable automation" },
} satisfies Meta<typeof Switch>;
export default meta;
type Story = StoryObj<typeof meta>;

async function expectTreatment(control: HTMLElement, checked: boolean) {
  const track = control.querySelector('[data-slot="switch-track"]')!;
  const thumb = control.querySelector('[data-slot="switch-thumb"]')!;
  const trackStyle = getComputedStyle(track), thumbStyle = getComputedStyle(thumb);
  await expect(trackStyle.width).toBe("44px");
  await expect(trackStyle.height).toBe("24px");
  await expect(trackStyle.borderWidth).toBe("1px");
  await expect(trackStyle.borderStyle).toBe("solid");
  await expect(trackStyle.borderRadius).toBe("12px");
  await expect(thumbStyle.width).toBe("18px");
  await expect(thumbStyle.height).toBe("18px");
  await expect(parseFloat(thumbStyle.borderRadius)).toBeGreaterThanOrEqual(9);
  await waitFor(() => {
    expect(getComputedStyle(track).backgroundColor).toBe(checked ? "rgb(167, 123, 255)" : "rgb(31, 31, 31)");
    expect(getComputedStyle(track).borderColor).toBe(checked ? "rgb(167, 123, 255)" : "rgb(125, 125, 125)");
    expect(getComputedStyle(thumb).backgroundColor).toBe(checked ? "rgb(244, 244, 244)" : "rgb(166, 166, 166)");
    expect(thumb.getBoundingClientRect().left - track.getBoundingClientRect().left).toBe(checked ? 23 : 3);
  });
}

export const Off: Story = {
  play: async ({ canvas, userEvent }) => {
    const control = canvas.getByRole("switch", { name: "Enable automation" });
    await expect(control).toHaveAttribute("aria-checked", "false");
    await expectTreatment(control, false);
    await userEvent.click(control);
    await expect(control).toHaveAttribute("aria-checked", "true");
    await expectTreatment(control, true);
  },
};
export const On: Story = {
  args: { defaultChecked: true },
  play: async ({ canvas }) => {
    const control = canvas.getByRole("switch", { name: "Enable automation" });
    await expect(control).toHaveAttribute("aria-checked", "true");
    await expectTreatment(control, true);
  },
};
export const Disabled: Story = {
  args: { disabled: true, defaultChecked: true, onCheckedChange: fn() },
  render: args => <div className="flex gap-6"><Switch {...args}/><Switch aria-label="Disabled off switch" disabled onCheckedChange={args.onCheckedChange}/></div>,
  play: async ({ canvas, userEvent, args }) => {
    const control = canvas.getByRole("switch", { name: "Enable automation" });
    const off = canvas.getByRole("switch", { name: "Disabled off switch" });
    await expect(control).toBeDisabled();
    await expect(off).toBeDisabled();
    await expect(getComputedStyle(control).opacity).toBe("0.5");
    await expect(getComputedStyle(off).opacity).toBe("0.5");
    await expectTreatment(control, true);
    await expectTreatment(off, false);
    await userEvent.click(control);
    await userEvent.click(off);
    await expect(control).toHaveAttribute("aria-checked", "true");
    await expect(off).toHaveAttribute("aria-checked", "false");
    await expect(args.onCheckedChange).not.toHaveBeenCalled();
  },
};

export const Keyboard: Story = {
  args: { size: "touch", onCheckedChange: fn() },
  play: async ({ canvas, userEvent, args }) => {
    const control = canvas.getByRole("switch", { name: "Enable automation" });
    await userEvent.tab();
    await expect(control).toHaveFocus();
    const style = getComputedStyle(control);
    await expect(style.height).toBe("44px");
    await expect(style.borderRadius).toBe("8px");
    await expect(style.outlineWidth).toBe("2px");
    await expect(style.outlineStyle).toBe("solid");
    await expect(style.outlineOffset).toBe("3px");
    await waitFor(() => expect(getComputedStyle(control).outlineColor).toBe("rgb(167, 123, 255)"));
    await userEvent.keyboard(" ");
    await expect(control).toHaveAttribute("aria-checked", "true");
    await expect(args.onCheckedChange).toHaveBeenCalledWith(true);
    await expectTreatment(control, true);
    await userEvent.keyboard("{Enter}");
    await expect(control).toHaveAttribute("aria-checked", "false");
    await expect(args.onCheckedChange).toHaveBeenLastCalledWith(false);
    await expectTreatment(control, false);
  },
};

export const Invalid: Story = {
  args: { "aria-invalid": true },
  play: async ({ canvas }) => {
    const control = canvas.getByRole("switch", { name: "Enable automation" });
    const track = control.querySelector('[data-slot="switch-track"]')!;
    await expect(control).toHaveAttribute("aria-invalid", "true");
    await expect(getComputedStyle(track).borderColor).toBe("rgb(255, 92, 122)");
  },
};

export const Sizes: Story = {
  render: () => (
    <div className="flex flex-wrap items-center gap-6">
      <Switch aria-label="Default switch" />
      <Switch size="touch" aria-label="Touch switch" />
    </div>
  ),
  play: async ({ canvas }) => {
    for (const [name, height] of [["Default switch", "32px"], ["Touch switch", "44px"]]) {
      const control = canvas.getByRole("switch", { name });
      await expect(getComputedStyle(control).width).toBe("44px");
      await expect(getComputedStyle(control).height).toBe(height);
      await expect(getComputedStyle(control).borderRadius).toBe("8px");
    }
  },
};
