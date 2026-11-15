import type { Meta, StoryObj } from "@storybook/react-vite";
import { useEffect, useState } from "react";
import { expect, fn } from "storybook/test";
import { automationHandlers, definition } from "../../../.storybook/automation-fixtures";
import { expectNoHorizontalOverflow } from "../../../.storybook/play";
import { AutomationEditor } from "./AutomationEditor";
import { readAutomationDocument } from "./document";

const meta = {
  component: AutomationEditor,
  tags: ["ai-generated"],
  args: { document: readAutomationDocument(JSON.stringify(definition)), onChange: fn() },
  render: function Editor(args) {
    const [document, setDocument] = useState(args.document);
    useEffect(() => setDocument(args.document), [args.document]);
    return <AutomationEditor {...args} document={document} onChange={next => { args.onChange(next); setDocument(next); }}/>;
  },
  beforeEach: ({ msw }) => { msw.use(...automationHandlers()); },
} satisfies Meta<typeof AutomationEditor>;
export default meta;
type Story = StoryObj<typeof meta>;

export const StackBuilder: Story = {
  play: async ({ canvas }) => {
    await expect((await canvas.findAllByRole("option", { name: /Office plug/ })).length).toBeGreaterThan(0);
    await expect(canvas.getByRole("heading", { name: "Admission Conditions" })).toBeVisible();
    await expect(canvas.getByText("Operation parameter schema · canonical units")).toBeVisible();
  },
};

export const InvalidWait: Story = {
  args: { document: readAutomationDocument(JSON.stringify({ ...definition, steps: [{ id: "wait", kind: "delay", duration_ms: 1000 }] })) },
  play: async ({ canvas, userEvent }) => {
    await canvas.findByRole("option", { name: /Office plug/ });
    const wait = canvas.getByLabelText("Wait · milliseconds");
    await userEvent.clear(wait);
    await userEvent.type(wait, "0");
    await expect(wait).toHaveAttribute("aria-invalid", "true");
    await expect(canvas.getByRole("alert")).toHaveTextContent("This value is invalid");
  },
};

export const EditName: Story = {
  play: async ({ canvas, userEvent, args }) => {
    await canvas.findAllByRole("option", { name: /Office plug/ });
    const name = canvas.getByRole("textbox", { name: "Name" });
    await userEvent.type(name, " routine");
    await expect(name).toHaveValue("Hallway light at dusk routine");
    await expect(args.onChange).toHaveBeenLastCalledWith(expect.objectContaining({
      definition: expect.objectContaining({ name: "Hallway light at dusk routine" }),
    }));
  },
};

export const Phone: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    await expect((await canvas.findAllByRole("option", { name: /Office plug/ })).length).toBeGreaterThan(0);
    await expectNoHorizontalOverflow(canvasElement);
  },
};
