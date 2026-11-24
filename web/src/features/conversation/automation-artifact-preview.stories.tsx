import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn } from "storybook/test";

import {
  automationArtifacts,
  createApplicationHandlers,
} from "../../../.storybook/application-fixtures";
import { AutomationArtifactPreview } from "./automation-artifact-preview";

const meta = {
  title: "Components/AutomationArtifactPreview",
  component: AutomationArtifactPreview,
  tags: ["ai-generated"],
  args: {
    artifact: automationArtifacts.reference,
    open: false,
    active: true,
    onSelect: fn(),
  },
  parameters: {
    docs: {
      description: {
        component:
          "An Automation tool result in a conversation reply. A saved reference loads the current definition from Core; proposals render their own definition.",
      },
    },
  },
  beforeEach({ msw, parameters }) {
    msw.use(...createApplicationHandlers(parameters.applicationScenario));
  },
} satisfies Meta<typeof AutomationArtifactPreview>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Missing: Story = {
  parameters: { applicationScenario: { messages: "automation-missing" } },
  play: async ({ canvas }) => {
    await expect(await canvas.findByRole("alert")).toHaveTextContent(
      "This Automation no longer exists. Its earlier tool result is retained below."
    );
    await expect(
      canvas.getByRole("button", { name: "Open in canvas" })
    ).toBeEnabled();
  },
};

export const Loading: Story = {
  parameters: { applicationScenario: { messages: "automation-loading" } },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Loading Automation…")).toBeVisible();
    await expect(
      canvas.getByText("Saved reference · current definition")
    ).toBeVisible();
  },
};
