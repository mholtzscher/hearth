import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, waitFor, within } from "storybook/test";
import { PlusIcon } from "lucide-react";
import { Button } from "./button.tsx";
import { expectNoHorizontalOverflow } from "../../../.storybook/play.ts";

const meta = {
  component: Button,
  tags: ["ai-generated"],
  args: { children: "Save dashboard" },
  parameters: {
    docs: { description: { component: "Use the primary variant for the main action; outline for secondary actions. Hearth is dark-only." } },
  },
} satisfies Meta<typeof Button>;
export default meta;
type Story = StoryObj<typeof meta>;

/** The primary action sends one explicit user intent. */
export const Primary: Story = {
  args: { onClick: fn() },
  play: async ({ canvas, userEvent, args }) => {
    await userEvent.click(canvas.getByRole("button", { name: "Save dashboard" }));
    await expect(args.onClick).toHaveBeenCalledTimes(1);
  },
};
export const Secondary: Story = { args: { variant: "outline", children: "Discard changes" } };
export const Disabled: Story = { args: { disabled: true } };

/** Proves Tailwind, the production theme, and the preview stylesheet loaded. */
export const CssCheck: Story = {
  play: async ({ canvas }) => {
    const button = canvas.getByRole("button", { name: "Save dashboard" });
    await expect(getComputedStyle(button).backgroundColor).toBe("rgb(255, 138, 61)");
    await expect(getComputedStyle(button).fontFamily).toContain("Figtree Variable");
  },
};

export const Variants: Story = {
  render: () => (
    <div className="flex flex-wrap gap-3">
      <Button>Save dashboard</Button>
      <Button variant="outline">Discard changes</Button>
      <Button variant="secondary">Refresh</Button>
      <Button variant="ghost">More details</Button>
      <Button variant="destructive">Delete dashboard</Button>
      <Button variant="link">View history</Button>
    </div>
  ),
  play: async ({ canvasElement }) => expectNoHorizontalOverflow(canvasElement),
};

export const Sizes: Story = {
  render: () => (
    <div className="flex flex-wrap items-center gap-3">
      <Button>Default</Button>
      <Button size="touch">Touch</Button>
      <Button size="icon" aria-label="Default icon"><PlusIcon /></Button>
      <Button size="icon-touch" aria-label="Touch icon"><PlusIcon /></Button>
    </div>
  ),
  play: async ({ canvas, canvasElement }) => {
    for (const [name, height] of [["Default", "32px"], ["Touch", "44px"], ["Default icon", "32px"], ["Touch icon", "44px"]]) {
      const button = canvas.getByRole("button", { name });
      await expect(getComputedStyle(button).height).toBe(height);
      await expect(getComputedStyle(button).borderRadius).toBe("8px");
      if (name.includes("icon")) await expect(getComputedStyle(button).width).toBe(height);
    }
    await expectNoHorizontalOverflow(canvasElement);
  },
};

export const DisabledVariants: Story = {
  render: () => (
    <div className="flex flex-wrap gap-3">
      <Button disabled>Save dashboard</Button>
      <Button variant="outline" disabled>Discard changes</Button>
      <Button variant="secondary" disabled>Refresh</Button>
      <Button variant="ghost" disabled>More details</Button>
      <Button variant="destructive" disabled>Delete dashboard</Button>
      <Button variant="link" disabled>View history</Button>
    </div>
  ),
  play: async ({ canvas }) => {
    for (const button of canvas.getAllByRole("button")) await expect(button).toBeDisabled();
  },
};

export const KeyboardActivation: Story = {
  args: { size: "touch", onClick: fn() },
  play: async ({ canvas, userEvent, args }) => {
    await userEvent.tab();
    const button = canvas.getByRole("button", { name: "Save dashboard" });
    await expect(button).toHaveFocus();
    const style = getComputedStyle(button);
    await expect(style.height).toBe("44px");
    await expect(style.borderRadius).toBe("8px");
    await expect(style.outlineWidth).toBe("2px");
    await expect(style.outlineStyle).toBe("solid");
    await expect(style.outlineOffset).toBe("3px");
    await waitFor(() => expect(getComputedStyle(button).outlineColor).toBe("rgb(167, 123, 255)"));
    await userEvent.keyboard("{Enter}");
    await userEvent.keyboard(" ");
    await expect(args.onClick).toHaveBeenCalledTimes(2);
  },
};

export const AsChild: Story = {
  args: { asChild: true, variant: "outline", children: <a href="#button-details">Open details</a> },
  play: async ({ canvas, userEvent }) => {
    const link = canvas.getByRole("link", { name: "Open details" });
    await expect(link).toHaveAttribute("href", "#button-details");
    await userEvent.tab();
    await expect(link).toHaveFocus();
  },
};

export const Invalid: Story = {
  args: { variant: "outline", "aria-invalid": true, children: "Choose a destination" },
};

/** Temporary migration reference. Remove after the household controls adopt Button. */
export const HouseholdComparison: Story = {
  render: () => (
    <div className="grid gap-6">
      {[false, true].map(disabled => (
        <section key={String(disabled)} aria-label={disabled ? "Disabled actions" : "Ready actions"} className="grid gap-3">
          <div className="workspace-actions">
            <button type="button" className="primary" disabled={disabled} aria-label="Legacy Save dashboard">Save dashboard</button>
            <button type="button" disabled={disabled} aria-label="Legacy Discard changes">Discard changes</button>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button size="touch" disabled={disabled}>Save dashboard</Button>
            <Button size="touch" variant="outline" disabled={disabled}>Discard changes</Button>
          </div>
        </section>
      ))}
    </div>
  ),
  play: async ({ canvas, canvasElement }) => {
    await canvasElement.ownerDocument.fonts.ready;
    const properties = [
      "height", "width", "backgroundColor", "color", "borderColor", "borderWidth",
      "borderStyle", "borderRadius", "fontFamily", "fontSize", "fontWeight", "lineHeight", "padding", "opacity",
    ] as const;
    for (const state of ["Ready actions", "Disabled actions"]) {
      const group = within(canvas.getByRole("region", { name: state }));
      for (const name of ["Save dashboard", "Discard changes"]) {
        const legacy = getComputedStyle(group.getByRole("button", { name: `Legacy ${name}` }));
        const shared = getComputedStyle(group.getByRole("button", { name }));
        for (const property of properties) {
          await expect(shared[property], `${state}: ${name} ${property}`).toBe(legacy[property]);
        }
      }
    }
    await expectNoHorizontalOverflow(canvasElement);
  },
};
