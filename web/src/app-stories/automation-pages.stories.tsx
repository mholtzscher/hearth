import type { Meta } from "@storybook/react-vite";
import { expect, waitFor, within } from "storybook/test";

import { applicationMeta } from "../../.storybook/application";
import type { ApplicationStory } from "../../.storybook/application";
import { createApplicationHandlers } from "../../.storybook/application-fixtures";
import {
  automationHandlers,
  automationId,
  childAutomationId,
} from "../../.storybook/automation-fixtures";
import { expectNoHorizontalOverflow } from "../../.storybook/play";
import App from "../app";

const meta = {
  title: "Application/Automations/Pages",
  component: App,
  tags: ["ai-generated", "!autodocs"],
  decorators: applicationMeta.decorators,
  parameters: { ...applicationMeta.parameters, appPath: "/automations" },
  argTypes: applicationMeta.argTypes,
  loaders: applicationMeta.loaders,
  beforeEach: ({ msw, loaded }) => {
    msw.use(...createApplicationHandlers(), ...automationHandlers());
    return () => loaded.router.dispose();
  },
  render: (_args, { loaded }) => <App router={loaded.router} />,
} satisfies Meta<typeof App>;
export default meta;

export const Library: ApplicationStory = {
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByRole("link", { name: "Open Hallway light at dusk" })
    ).toHaveAttribute("href", `/automations/${automationId}`);
    await expect(
      canvas.getByRole("link", { name: "Open Settle the hallway" })
    ).toBeVisible();
    await expect(await canvas.findByText("Household")).toBeVisible();
  },
};

export const NewDraft: ApplicationStory = {
  play: async ({ canvas, userEvent }) => {
    await canvas.findByRole("link", { name: "Open Hallway light at dusk" });
    await userEvent.click(
      canvas.getByRole("button", { name: "New Automation" })
    );
    const editor = within(
      await canvas.findByRole("region", { name: "Automation editor" })
    );
    await waitFor(() =>
      expect(
        editor.getByRole("button", { name: "Add admission Condition" })
      ).toBeEnabled()
    );
    await userEvent.click(
      editor.getByRole("button", { name: "Add admission Condition" })
    );
    await expect(
      await editor.findByRole("option", { name: /Office plug/u })
    ).toBeInTheDocument();
    await userEvent.selectOptions(
      editor.getByLabelText("Add Step"),
      "sub_automation"
    );
    await userEvent.click(editor.getByRole("button", { name: "Add to Steps" }));
    await expect(
      editor.getByRole("option", { name: /Settle the hallway/u })
    ).toBeInTheDocument();
    await userEvent.selectOptions(
      editor.getByLabelText("Automation"),
      childAutomationId
    );
    await userEvent.selectOptions(
      editor.getByLabelText("Add Step"),
      "notification"
    );
    await userEvent.click(editor.getByRole("button", { name: "Add to Steps" }));
    await expect(
      editor.getByRole("option", { name: /Household/u })
    ).toBeInTheDocument();
    await expect(
      await canvas.findByRole("button", {
        name: "Inspect child Settle the hallway",
      })
    ).toBeVisible();
    await expect(
      canvas.getByText(
        "Draft changes do not execute. Run saved Automations from their detail page."
      )
    ).toBeVisible();
  },
};

export const Phone: ApplicationStory = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    await expect(
      await canvas.findByRole("link", { name: "Open Hallway light at dusk" })
    ).toBeVisible();
    await canvas.findByText("Household");
    await expectNoHorizontalOverflow(canvasElement);
  },
};
