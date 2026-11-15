import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, within } from "storybook/test";
import { Switch } from "./switch";

const meta = {
  component: Switch,
  tags: ["ai-generated"],
  args: { "aria-label": "Enable automation" },
} satisfies Meta<typeof Switch>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Off: Story = {
  play: async ({ canvas, userEvent }) => {
    const control = canvas.getByRole("switch", { name: "Enable automation" });
    await expect(control).toHaveAttribute("aria-checked", "false");
    await userEvent.click(control);
    await expect(control).toHaveAttribute("aria-checked", "true");
  },
};
export const On: Story = { args: { defaultChecked: true } };
export const Disabled: Story = {
  args: { disabled: true, defaultChecked: true, onCheckedChange: fn() },
  play: async ({ canvas, userEvent, args }) => {
    const control = canvas.getByRole("switch", { name: "Enable automation" });
    await expect(control).toBeDisabled();
    await userEvent.click(control);
    await expect(control).toHaveAttribute("aria-checked", "true");
    await expect(args.onCheckedChange).not.toHaveBeenCalled();
  },
};

export const Keyboard: Story = {
  args: { onCheckedChange: fn() },
  play: async ({ canvas, userEvent, args }) => {
    const control = canvas.getByRole("switch", { name: "Enable automation" });
    await userEvent.tab();
    await expect(control).toHaveFocus();
    await userEvent.keyboard(" ");
    await expect(control).toHaveAttribute("aria-checked", "true");
    await expect(args.onCheckedChange).toHaveBeenCalledWith(true);
    await userEvent.keyboard("{Enter}");
    await expect(control).toHaveAttribute("aria-checked", "false");
    await expect(args.onCheckedChange).toHaveBeenLastCalledWith(false);
  },
};

export const Invalid: Story = { args: { "aria-invalid": true } };

export const Sizes: Story = {
  render: () => (
    <div className="flex flex-wrap items-center gap-6">
      <Switch aria-label="Default switch" />
      <Switch aria-label="Touch switch" />
    </div>
  ),
};

/** Temporary migration reference. Remove when PowerSwitch adopts the shared switch. */
export const HouseholdComparison: Story = {
  render: () => (
    <div className="grid gap-6">
      {[false, true].map(checked => (
        <section key={String(checked)} aria-label={checked ? "On" : "Off"} className="flex items-center gap-6">
          <button type="button" role="switch" aria-label="Legacy power" aria-checked={checked} className="view-switch">
            <span className="view-switch-thumb" aria-hidden="true" />
          </button>
          <Switch aria-label="Shared power" defaultChecked={checked} />
        </section>
      ))}
    </div>
  ),
  play: async ({ canvas }) => {
    for (const state of ["Off", "On"]) {
      const group = within(canvas.getByRole("region", { name: state }));
      const checked = state === "On" ? "true" : "false";
      await expect(group.getByRole("switch", { name: "Legacy power" })).toHaveAttribute("aria-checked", checked);
      await expect(group.getByRole("switch", { name: "Shared power" })).toHaveAttribute("aria-checked", checked);
    }
  },
};
