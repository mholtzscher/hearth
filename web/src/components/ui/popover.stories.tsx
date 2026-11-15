import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, waitFor, within } from "storybook/test";
import { Popover, PopoverContent, PopoverDescription, PopoverTitle, PopoverTrigger } from "./popover";

const meta = {
  component: Popover,
  args: {
    children: <>
      <PopoverTrigger>Reading details</PopoverTrigger>
      <PopoverContent aria-label="Living room reading">
        <PopoverTitle>Living room</PopoverTitle>
        <PopoverDescription>21.5 °C · Observed just now</PopoverDescription>
      </PopoverContent>
    </>,
  },
} satisfies Meta<typeof Popover>;
export default meta;
type Story = StoryObj<typeof meta>;

export const OpenAndDismiss: Story = {
  play: async ({ canvas, canvasElement, userEvent }) => {
    const trigger = canvas.getByRole("button", { name: "Reading details" });
    await userEvent.click(trigger);
    const body = within(canvasElement.ownerDocument.body);
    await waitFor(() => expect(body.getByRole("dialog", { name: "Living room reading" })).toBeVisible());
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(trigger).toHaveFocus());
  },
};

/** The open state exposes the shared surface treatment. */
export const Open: Story = {
  args: { defaultOpen: true },
  play: async ({ canvasElement }) => {
    const body = within(canvasElement.ownerDocument.body);
    const dialog = await body.findByRole("dialog", { name: "Living room reading" });
    await expect(dialog).toBeVisible();
    const style = getComputedStyle(dialog);
    await expect(dialog.getBoundingClientRect().width).toBeCloseTo(Math.min(440, window.innerWidth - 24), 1);
    await expect(parseFloat(style.maxHeight)).toBeLessThanOrEqual(720);
    for (const [property, value] of Object.entries({
      backgroundColor: "rgb(21, 21, 21)", color: "rgb(244, 244, 244)", borderRadius: "12px",
      padding: "22px", fontSize: "14px", lineHeight: "21px",
    })) await expect(style.getPropertyValue(property.replace(/[A-Z]/g, letter => `-${letter.toLowerCase()}`)), property).toBe(value);
    await expect(style.fontFamily).toContain("Figtree Variable");
    await expect(style.boxShadow).toContain("rgba(0, 0, 0, 0.533) 0px 18px 60px 0px");
  },
};
