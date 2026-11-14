import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, waitFor, within } from "storybook/test";
import { Popover, PopoverContent, PopoverDescription, PopoverTitle, PopoverTrigger } from "./popover";

const meta = {
  component: Popover,
  tags: ["ai-generated"],
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
    // The portal exists before its fade-in makes it visible.
    await waitFor(() => expect(body.getByRole("dialog", { name: "Living room reading" })).toBeVisible());
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(trigger).toHaveFocus());
  },
};
