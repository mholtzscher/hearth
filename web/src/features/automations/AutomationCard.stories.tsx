import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { automationHandlers, definition } from "../../../.storybook/automation-fixtures";
import { expectNoHorizontalOverflow } from "../../../.storybook/play";
import { AutomationCard } from "./AutomationCard";
import { readAutomationDocument } from "./document";

const meta = {
  component: AutomationCard,
  tags: ["ai-generated"],
  args: { document: readAutomationDocument(JSON.stringify(definition)) },
  beforeEach: ({ msw }) => { msw.use(...automationHandlers()); },
} satisfies Meta<typeof AutomationCard>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Sentence: Story = {
  play: async ({ canvas }) => {
    await expect((await canvas.findAllByRole("link", { name: "Office plug (Office plug)" })).length).toBeGreaterThan(0);
    await expect(await canvas.findByText("Household")).toBeVisible();
    await expect(canvas.getByText("No Else; continue when false.")).toBeVisible();
  },
};

export const StepsAndExactDefinition: Story = {
  play: async ({ canvas, userEvent }) => {
    await canvas.findByText("Household");
    await userEvent.click(canvas.getByRole("button", { name: "Steps" }));
    await userEvent.click(canvas.getByText("Exact definition"));
    await expect(canvas.getAllByRole("list", { name: "Step stack sequence" })).toHaveLength(2);
    await expect(canvas.getByRole("button", { name: "Steps" })).toHaveAttribute("aria-pressed", "true");
  },
};

export const Phone: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    await expect(await canvas.findByText("Household")).toBeVisible();
    await expectNoHorizontalOverflow(canvasElement);
  },
};
