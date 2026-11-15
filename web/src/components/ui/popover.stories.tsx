import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, waitFor, within } from "storybook/test";
import { CardSource } from "../../features/views/cards/CardSource.tsx";
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

/** The open state remains visible for visual comparison during migration. */
export const Open: Story = {
  args: { defaultOpen: true },
  play: async ({ canvasElement }) => {
    const body = within(canvasElement.ownerDocument.body);
    await waitFor(() => expect(body.getByRole("dialog", { name: "Living room reading" })).toBeVisible());
  },
};

/** Temporary reference: the actual Source popover and its legacy CSS, until D7. */
export const HouseholdComparison: Story = {
  render: () => <>
    <CardSource title="Living room" observedLabel="Observed" now={0} sources={[]} />
    <Popover>
      <PopoverTrigger>Shared details</PopoverTrigger>
      <PopoverContent aria-label="Shared reading">Living room</PopoverContent>
    </Popover>
  </>,
  play: async ({ canvas, canvasElement, userEvent }) => {
    const body = within(canvasElement.ownerDocument.body);
    await userEvent.click(canvas.getByRole("button", { name: "Source" }));
    const legacy = await body.findByRole("dialog", { name: "Source details" });
    const properties = ["width", "maxHeight", "backgroundColor", "color", "borderRadius", "padding", "fontFamily", "fontSize", "lineHeight"] as const;
    const expected = Object.fromEntries(properties.map(property => [property, getComputedStyle(legacy)[property]]));
    const shadow = getComputedStyle(legacy).boxShadow;
    await userEvent.click(within(legacy).getByRole("button", { name: "Close details" }));
    await userEvent.click(canvas.getByRole("button", { name: "Shared details" }));
    const shared = await body.findByRole("dialog", { name: "Shared reading" });
    for (const property of properties) await expect(getComputedStyle(shared)[property], property).toBe(expected[property]);
    await expect(getComputedStyle(shared).boxShadow).toContain(shadow);
  },
};
