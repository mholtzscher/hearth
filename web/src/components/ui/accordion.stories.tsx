import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from "./accordion";

const meta = {
  component: Accordion,
  tags: ["ai-generated"],
  args: {
    type: "single", collapsible: true,
    children: <>
      <AccordionItem value="state">
        <AccordionTrigger>Observed State</AccordionTrigger>
        <AccordionContent>Living room · 21.5 °C</AccordionContent>
      </AccordionItem>
      <AccordionItem value="availability">
        <AccordionTrigger>Entity availability</AccordionTrigger>
        <AccordionContent>Available</AccordionContent>
      </AccordionItem>
    </>,
  },
} satisfies Meta<typeof Accordion>;
export default meta;
type Story = StoryObj<typeof meta>;

export const ExpandWithKeyboard: Story = {
  play: async ({ canvas, userEvent }) => {
    const trigger = canvas.getByRole("button", { name: "Observed State" });
    trigger.focus();
    await userEvent.keyboard("{Enter}");
    await expect(trigger).toHaveAttribute("aria-expanded", "true");
    await expect(canvas.getByText("Living room · 21.5 °C")).toBeVisible();
    await userEvent.keyboard("{Enter}");
    await expect(trigger).toHaveAttribute("aria-expanded", "false");
  },
};
export const Expanded: Story = { args: { defaultValue: "state" } };
