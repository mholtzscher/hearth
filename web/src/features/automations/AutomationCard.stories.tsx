import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { automationHandlers, childAutomationId, definition } from "../../../.storybook/automation-fixtures";
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

export const ReferencedAutomation: Story = {
  args: { document: readAutomationDocument(JSON.stringify({
    ...definition, steps: [{ id: "child", kind: "sub_automation", automation_id: childAutomationId }],
  })) },
  play: async ({ canvas, userEvent }) => {
    await userEvent.click(await canvas.findByRole("button", { name: "Inspect child Settle the hallway" }));
    await expect(await canvas.findByRole("heading", { name: "Settle the hallway" })).toBeVisible();
    await expect(canvas.getByText("Current definition · read-only")).toBeVisible();
    await expect(canvas.getByRole("link", { name: "Open Automation and history" })).toHaveAttribute("href", `/automations/${childAutomationId}`);
  },
};

export const Phone: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    await expect(await canvas.findByText("Household")).toBeVisible();
    await expectNoHorizontalOverflow(canvasElement);
  },
};
