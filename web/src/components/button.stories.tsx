import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, waitFor } from "storybook/test";
import { PlusIcon } from "lucide-react";
import { Button } from "./button.tsx";
import { expectNoHorizontalOverflow } from "../../.storybook/play.ts";

const meta = {
  title: "Components/Button",
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

/** Proves Tailwind and the production theme loaded. */
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
      <Button size="touch" aria-label="Save dashboard ready">Save dashboard</Button>
      <Button size="touch" variant="outline" aria-label="Discard changes ready">Discard changes</Button>
      <Button size="touch" disabled aria-label="Save dashboard disabled">Save dashboard</Button>
      <Button size="touch" variant="outline" disabled aria-label="Discard changes disabled">Discard changes</Button>
    </div>
  ),
  play: async ({ canvas, canvasElement }) => {
    for (const [name, height] of [["Default", "32px"], ["Touch", "44px"], ["Default icon", "32px"], ["Touch icon", "44px"]]) {
      const button = canvas.getByRole("button", { name });
      await expect(getComputedStyle(button).height).toBe(height);
      await expect(getComputedStyle(button).borderRadius).toBe("8px");
      if (name.includes("icon")) await expect(getComputedStyle(button).width).toBe(height);
    }
    await canvasElement.ownerDocument.fonts.ready;
    for (const [name, background, color, border, weight, opacity] of [
      ["Save dashboard ready", "rgb(255, 138, 61)", "rgb(31, 13, 0)", "rgb(255, 138, 61)", "600", "1"],
      ["Discard changes ready", "rgb(12, 12, 12)", "rgb(244, 244, 244)", "rgb(31, 31, 31)", "400", "1"],
      ["Save dashboard disabled", "rgb(31, 31, 31)", "rgb(125, 125, 125)", "rgb(31, 31, 31)", "600", "1"],
      ["Discard changes disabled", "rgb(12, 12, 12)", "rgb(244, 244, 244)", "rgb(31, 31, 31)", "400", "0.5"],
    ] as const) {
      const button = canvas.getByRole("button", { name });
      const style = getComputedStyle(button);
      // Glyph advances differ across Chromium builds; the contract is content + 12px padding + 1px border on each side.
      const text = canvasElement.ownerDocument.createRange();
      text.selectNodeContents(button);
      await expect(button.getBoundingClientRect().width).toBeCloseTo(text.getBoundingClientRect().width + 26, 1);
      for (const [property, value] of Object.entries({
        height: "44px", backgroundColor: background, color, borderColor: border,
        borderWidth: "1px", borderStyle: "solid", borderRadius: "8px", fontSize: "14px",
        fontWeight: weight, lineHeight: "21px", padding: "10px 12px", opacity,
      })) await expect(style.getPropertyValue(property.replace(/[A-Z]/g, letter => `-${letter.toLowerCase()}`)), `${name}: ${property}`).toBe(value);
      await expect(style.fontFamily).toContain("Figtree Variable");
      if (name.endsWith("disabled")) await expect(button).toBeDisabled();
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
