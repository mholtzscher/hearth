import type { Meta, StoryObj } from "@storybook/react-vite";
import type { ComponentProps } from "react";
import { useState } from "react";
import { expect, fn } from "storybook/test";

import {
  automationHandlers,
  definition,
} from "../../../.storybook/automation-fixtures";
import { expectNoHorizontalOverflow } from "../../../.storybook/play";
import { AutomationEditor } from "./automation-editor";
import { readAutomationDocument } from "./document";

const Editor = (args: ComponentProps<typeof AutomationEditor>) => {
  const [document, setDocument] = useState(args.document);
  const [syncedDocument, setSyncedDocument] = useState(args.document);
  // Controls changes replace the story's local draft; the guard converges.
  if (args.document !== syncedDocument) {
    setSyncedDocument(args.document);
    setDocument(args.document);
  }
  return (
    <AutomationEditor
      {...args}
      document={document}
      onChange={(next) => {
        args.onChange(next);
        setDocument(next);
      }}
    />
  );
};

const meta = {
  title: "Components/Editors/AutomationEditor",
  component: AutomationEditor,
  tags: ["ai-generated"],
  args: {
    document: readAutomationDocument(JSON.stringify(definition)),
    onChange: fn(),
  },
  render: Editor,
  beforeEach: ({ msw }) => {
    msw.use(...automationHandlers());
  },
} satisfies Meta<typeof AutomationEditor>;
export default meta;
type Story = StoryObj<typeof meta>;

export const StackBuilder: Story = {
  play: async ({ canvas }) => {
    const options = await canvas.findAllByRole("option", {
      name: /Office plug/u,
    });
    await expect(options.length).toBeGreaterThan(0);
    await expect(
      canvas.getByRole("heading", { name: "Admission Conditions" })
    ).toBeVisible();
    await expect(
      canvas.getByText("Operation parameter schema · canonical units")
    ).toBeVisible();
  },
};

export const InvalidWait: Story = {
  args: {
    document: readAutomationDocument(
      JSON.stringify({
        ...definition,
        steps: [{ id: "wait", kind: "delay", duration_ms: 1000 }],
      })
    ),
  },
  play: async ({ canvas, userEvent }) => {
    await canvas.findByRole("option", { name: /Office plug/u });
    const wait = canvas.getByLabelText("Wait · milliseconds");
    await userEvent.clear(wait);
    await userEvent.type(wait, "0");
    await expect(wait).toHaveAttribute("aria-invalid", "true");
    await expect(canvas.getByRole("alert")).toHaveTextContent(
      "This value is invalid"
    );
  },
};

export const EditName: Story = {
  play: async ({ canvas, userEvent, args }) => {
    await canvas.findAllByRole("option", { name: /Office plug/u });
    const name = canvas.getByRole("textbox", { name: "Name" });
    await userEvent.type(name, " routine");
    await expect(name).toHaveValue("Hallway light at dusk routine");
    await expect(args.onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({
        definition: expect.objectContaining({
          name: "Hallway light at dusk routine",
        }),
      })
    );
  },
};

export const EnableAutomaticRuns: Story = {
  play: async ({ canvas, userEvent, args }) => {
    const enabled = canvas.getByRole("switch", {
      name: "Enable automatic Runs",
    });
    await expect(enabled).toHaveAttribute("aria-checked", "true");
    enabled.focus();
    await userEvent.keyboard(" ");
    await expect(enabled).toHaveAttribute("aria-checked", "false");
    await expect(args.onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({
        definition: expect.objectContaining({ enabled: false }),
      })
    );
  },
};

export const Phone: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    const options = await canvas.findAllByRole("option", {
      name: /Office plug/u,
    });
    await expect(options.length).toBeGreaterThan(0);
    await expectNoHorizontalOverflow(canvasElement);
  },
};
