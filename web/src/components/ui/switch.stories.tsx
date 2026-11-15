import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, waitFor, within } from "storybook/test";
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
    await expect(getComputedStyle(control).opacity).toBe("0.5");
    await userEvent.click(control);
    await expect(control).toHaveAttribute("aria-checked", "true");
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
    await userEvent.keyboard("{Enter}");
    await expect(control).toHaveAttribute("aria-checked", "false");
    await expect(args.onCheckedChange).toHaveBeenLastCalledWith(false);
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

/** Temporary migration reference. Remove when PowerSwitch adopts the shared switch. */
export const HouseholdComparison: Story = {
  render: () => (
    <div className="grid gap-6">
      {[false, true].map(checked => (
        <section key={String(checked)} aria-label={checked ? "On" : "Off"} className="flex items-center gap-6">
          <button type="button" role="switch" aria-label="Legacy power" aria-checked={checked} className="view-switch">
            <span data-slot="legacy-switch-thumb" className="view-switch-thumb" aria-hidden="true" />
          </button>
          <Switch size="touch" aria-label="Shared power" defaultChecked={checked} />
        </section>
      ))}
    </div>
  ),
  play: async ({ canvas }) => {
    for (const state of ["Off", "On"]) {
      const group = within(canvas.getByRole("region", { name: state }));
      const checked = state === "On" ? "true" : "false";
      const legacy = group.getByRole("switch", { name: "Legacy power" });
      const shared = group.getByRole("switch", { name: "Shared power" });
      await expect(legacy).toHaveAttribute("aria-checked", checked);
      await expect(shared).toHaveAttribute("aria-checked", checked);
      const track = shared.querySelector('[data-slot="switch-track"]')!;
      const legacyThumb = legacy.querySelector('[data-slot="legacy-switch-thumb"]')!;
      const sharedThumb = shared.querySelector('[data-slot="switch-thumb"]')!;
      for (const property of ["width", "height", "backgroundColor", "borderColor", "borderWidth", "borderStyle", "borderRadius", "fontFamily", "fontSize", "fontWeight", "lineHeight"] as const) {
        await expect(getComputedStyle(track)[property], `${state} track ${property}`).toBe(getComputedStyle(legacy)[property]);
      }
      for (const property of ["width", "height", "backgroundColor"] as const) {
        await expect(getComputedStyle(sharedThumb)[property], `${state} thumb ${property}`).toBe(getComputedStyle(legacyThumb)[property]);
      }
      await waitFor(() => {
        const legacyOffset = legacyThumb.getBoundingClientRect().left - legacy.getBoundingClientRect().left;
        const sharedOffset = sharedThumb.getBoundingClientRect().left - track.getBoundingClientRect().left;
        expect(sharedOffset, `${state} thumb position`).toBe(legacyOffset);
      });
      const radius = (element: Element) => {
        const style = getComputedStyle(element);
        const height = parseFloat(style.height);
        const value = style.borderRadius.endsWith("%") ? height * parseFloat(style.borderRadius) / 100 : parseFloat(style.borderRadius);
        return Math.min(value, height / 2);
      };
      await expect(radius(sharedThumb), `${state} painted thumb radius`).toBe(radius(legacyThumb));
    }
  },
};
