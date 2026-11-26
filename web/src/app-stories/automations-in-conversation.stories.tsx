import type { Meta } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, waitFor, within } from "storybook/test";

import { applicationMeta } from "../../.storybook/application";
import type {
  ApplicationContext,
  ApplicationStory,
} from "../../.storybook/application";
import { conversationPath } from "../../.storybook/application-fixtures";
import { automationId } from "../../.storybook/automation-fixtures";
import App from "../app";

const meta = {
  title: "Application/Automations/In conversation",
  component: App,
  tags: ["ai-generated", "!autodocs"],
  decorators: applicationMeta.decorators,
  parameters: applicationMeta.parameters,
  argTypes: applicationMeta.argTypes,
  loaders: applicationMeta.loaders,
  beforeEach: applicationMeta.beforeEach,
  render: (_args, { loaded }) => <App router={loaded.router} />,
} satisfies Meta<typeof App>;
export default meta;

const openSavedAutomationEditor = async ({
  canvas,
  userEvent,
}: ApplicationContext) => {
  await userEvent.click(
    await canvas.findByRole("link", { name: "Development" })
  );
  await userEvent.click(
    await canvas.findByRole("link", { name: /^Automations$/u })
  );
  await userEvent.click(
    await canvas.findByRole("link", { name: "Open Hallway light at dusk" })
  );
  await userEvent.click(
    await canvas.findByRole("button", { name: "Edit Automation" })
  );
  return within(
    await canvas.findByRole("region", { name: "Automation canvas" })
  );
};

export const AutomationShowingInCanvas: ApplicationStory = {
  parameters: {
    appPath: conversationPath,
    applicationScenario: { messages: "automation-create" },
  },
  play: async ({ canvas, userEvent }) => {
    const log = within(await canvas.findByRole("log"));
    await expect(
      await log.findByRole("heading", {
        name: "Hallway light at dusk",
        level: 4,
      })
    ).toBeVisible();
    await expect(await log.findByText("Household")).toBeVisible();
    const officeLinks = await log.findAllByRole("link", {
      name: "Office plug (Office plug)",
    });
    await expect(officeLinks.length).toBeGreaterThan(0);
    await userEvent.click(log.getByRole("button", { name: "Open in canvas" }));
    await expect(
      await log.findByRole("button", { name: "Showing in canvas" })
    ).toBeVisible();
    const automationCanvas = await canvas.findByRole("region", {
      name: "Automation canvas",
    });
    await waitFor(() => expect(automationCanvas).toBeVisible());
  },
};

export const CloseUnchangedAutomationFromDetail: ApplicationStory = {
  parameters: {
    appPath: conversationPath,
    applicationScenario: { messages: "automation-reference" },
  },
  beforeEach: ({ msw }) => {
    msw.use(
      http.get(`/v1/automations/${automationId}/history`, () =>
        HttpResponse.json({ items: [], next_cursor: null })
      )
    );
  },
  play: async (context) => {
    const editor = await openSavedAutomationEditor(context);
    await context.userEvent.click(
      editor.getByRole("button", { name: "Close Automation" })
    );
    await expect(
      await context.canvas.findByRole("button", { name: "Edit Automation" })
    ).toBeVisible();
    await expect(
      context.canvas.queryByRole("region", { name: "Automation canvas" })
    ).not.toBeInTheDocument();
  },
};

export const DiscardAutomationFromDetail: ApplicationStory = {
  parameters: {
    appPath: conversationPath,
    applicationScenario: { messages: "automation-reference" },
  },
  beforeEach: ({ msw }) => {
    msw.use(
      http.get(`/v1/automations/${automationId}/history`, () =>
        HttpResponse.json({ items: [], next_cursor: null })
      )
    );
  },
  play: async (context) => {
    const editor = await openSavedAutomationEditor(context);
    const name = editor.getByRole("textbox", { name: "Name" });
    await context.userEvent.clear(name);
    await context.userEvent.type(name, "Changed hallway");
    await context.userEvent.click(
      editor.getByRole("button", { name: "Close Automation" })
    );
    const body = within(context.canvasElement.ownerDocument.body);
    const dialog = within(
      await body.findByRole("dialog", { name: "Keep your changes?" })
    );
    await context.userEvent.click(
      dialog.getByRole("button", { name: "Discard" })
    );
    await expect(
      await context.canvas.findByRole("button", { name: "Edit Automation" })
    ).toBeVisible();
    await expect(
      body.queryByRole("dialog", { name: "Keep your changes?" })
    ).not.toBeInTheDocument();
    await context.userEvent.click(
      context.canvas.getByRole("button", { name: "Edit Automation" })
    );
    await expect(
      await context.canvas.findByRole("textbox", { name: "Name" })
    ).toHaveValue("Hallway light at dusk");
  },
};
