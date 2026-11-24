import type { Meta } from "@storybook/react-vite";
import { expect, waitFor, within } from "storybook/test";

import { applicationMeta, editTitle } from "../../.storybook/application";
import type { ApplicationStory } from "../../.storybook/application";
import {
  dashboardId,
  dashboardPath,
} from "../../.storybook/application-fixtures";
import { expectNoHorizontalOverflow } from "../../.storybook/play";
import { getDashboard } from "../api/dashboards";
import App from "../app";

const meta = {
  title: "Application/Dashboards",
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

export const SavedDashboard: ApplicationStory = {
  parameters: { appPath: dashboardPath },
  play: async ({ canvas }) => {
    const view = within(
      await canvas.findByRole("region", { name: "View canvas" })
    );
    await expect(
      await view.findByRole("heading", { name: "Morning overview" })
    ).toBeVisible();
    await expect(
      view.getByText(/Hearth can change the title, forecast periods/u)
    ).toBeVisible();
    await expect(
      view.getByRole("button", { name: "Save changes" })
    ).toBeDisabled();
    await expect(await view.findByRole("switch")).toHaveAttribute(
      "aria-checked",
      "true"
    );
  },
};

export const SelectedViewRevision: ApplicationStory = {
  parameters: {
    appPath: dashboardPath,
    applicationScenario: { turn: "view-edit" },
  },
  play: async (context) => {
    const { canvas, userEvent } = context;
    await canvas.findByRole("heading", { name: "Morning overview" });
    await userEvent.click(
      canvas.getByRole("button", { name: "Ask Hearth to change" })
    );
    await expect(
      canvas.getByRole("group", { name: "Selected view" })
    ).toBeVisible();
    const prompt = canvas.getByRole("textbox", { name: "Ask Hearth" });
    await userEvent.click(prompt);
    await userEvent.paste("Rename this view to Morning at home.");
    await userEvent.click(canvas.getByRole("button", { name: "Send message" }));
    const view = within(canvas.getByRole("region", { name: "View canvas" }));
    await expect(
      await view.findByRole("heading", { name: "Morning at home" })
    ).toBeVisible();
    await expect(
      view.getByRole("button", { name: "Save changes" })
    ).toBeEnabled();
    await expect(await view.findByRole("switch")).toHaveAttribute(
      "aria-checked",
      "true"
    );
  },
};

export const PhoneSavedCanvas: ApplicationStory = {
  parameters: { appPath: dashboardPath },
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    const view = within(
      await canvas.findByRole("region", { name: "View canvas" })
    );
    await expect(
      await view.findByRole("heading", { name: "Morning overview" })
    ).toBeVisible();
    await expect(
      view.queryByRole("button", { name: "Expand" })
    ).not.toBeInTheDocument();
    await expect(
      view.queryByRole("button", { name: "Save changes" })
    ).not.toBeInTheDocument();
    await expectNoHorizontalOverflow(canvasElement);
  },
};

export const EditAndSave: ApplicationStory = {
  parameters: { appPath: dashboardPath },
  play: async (context) => {
    const view = await editTitle(context, "Evening overview");
    await context.userEvent.click(
      view.getByRole("button", { name: "Save changes" })
    );
    await expect(
      await view.findByText(/Dashboard · revision 2/u)
    ).toBeVisible();
    await expect(
      view.getByRole("heading", { name: "Evening overview" })
    ).toBeVisible();
    await expect(
      view.getByRole("button", { name: "Save changes" })
    ).toBeDisabled();
  },
};

export const DiscardChanges: ApplicationStory = {
  parameters: { appPath: dashboardPath },
  play: async (context) => {
    const view = await editTitle(context, "Temporary title");
    await context.userEvent.click(
      view.getByRole("button", { name: "Discard changes" })
    );
    await expect(
      await view.findByRole("heading", { name: "Morning overview" })
    ).toBeVisible();
    await expect(
      view.getByRole("textbox", { name: "Dashboard title" })
    ).toHaveValue("Morning overview");
    await expect(
      view.getByRole("button", { name: "Save changes" })
    ).toBeDisabled();
  },
};

export const InvalidDashboard: ApplicationStory = {
  parameters: { appPath: dashboardPath },
  play: async (context) => {
    const view = await editTitle(context, "");
    await expect(
      view.getByRole("textbox", { name: "Dashboard title" })
    ).toHaveAttribute("aria-invalid", "true");
    await expect(
      view.getByText("Use 1–80 characters for the dashboard title.")
    ).toBeVisible();
    await expect(
      view.getByRole("button", { name: "Save changes" })
    ).toBeDisabled();
  },
};

export const SaveConflict: ApplicationStory = {
  parameters: {
    appPath: dashboardPath,
    applicationScenario: { saveConflict: true },
  },
  play: async (context) => {
    const view = await editTitle(context, "Keep my changes");
    await context.userEvent.click(
      view.getByRole("button", { name: "Save changes" })
    );
    await expect(await view.findByRole("alert")).toHaveTextContent(
      "This dashboard changed elsewhere."
    );
    await expect(
      view.getByRole("textbox", { name: "Dashboard title" })
    ).toHaveValue("Keep my changes");
  },
};

export const RecoverSaveConflict: ApplicationStory = {
  parameters: {
    appPath: dashboardPath,
    applicationScenario: { saveConflict: true },
  },
  play: async (context) => {
    const view = await editTitle(context, "Keep my changes");
    await context.userEvent.click(
      view.getByRole("button", { name: "Save changes" })
    );
    await view.findByRole("alert");
    await context.userEvent.click(
      view.getByRole("button", { name: "Reload saved version" })
    );
    await expect(
      await view.findByRole("heading", { name: "Updated elsewhere" })
    ).toBeVisible();
    await waitFor(() =>
      expect(view.queryByRole("alert")).not.toBeInTheDocument()
    );
    const title = view.getByRole("textbox", { name: "Dashboard title" });
    await context.userEvent.clear(title);
    await context.userEvent.paste("Merged overview");
    await context.userEvent.click(
      view.getByRole("button", { name: "Save changes" })
    );
    await expect(
      await view.findByText(/Dashboard · revision 3/u)
    ).toBeVisible();
    await expect(
      view.getByRole("heading", { name: "Merged overview" })
    ).toBeVisible();
  },
};

export const SaveAsNew: ApplicationStory = {
  parameters: { appPath: dashboardPath },
  play: async (context) => {
    const view = await editTitle(context, "Evening copy");
    await context.userEvent.click(
      view.getByRole("button", { name: "Save as new" })
    );
    await waitFor(() => {
      expect(view.getByRole("button", { name: "Save changes" })).toBeDisabled();
      expect(
        view.queryByRole("button", { name: "Discard changes" })
      ).not.toBeInTheDocument();
    });
    await expect(
      view.getByRole("heading", { name: "Evening copy" })
    ).toBeVisible();
    // Copying must not turn the original dashboard's read endpoint into a 404.
    await expect(getDashboard(dashboardId)).resolves.toMatchObject({
      id: dashboardId,
      revision: 1,
      definition: { title: "Morning overview" },
    });
  },
};

export const UnsavedNavigation: ApplicationStory = {
  parameters: { appPath: dashboardPath },
  play: async (context) => {
    await editTitle(context, "Unfinished changes");
    await context.userEvent.click(
      context.canvas.getByRole("link", { name: "Home" })
    );
    const dialog = within(
      await within(context.canvasElement.ownerDocument.body).findByRole(
        "dialog",
        { name: "Keep your changes?" }
      )
    );
    await context.userEvent.click(dialog.getByRole("button", { name: "Stay" }));
    await expect(
      context.canvas.getByRole("textbox", { name: "Dashboard title" })
    ).toHaveValue("Unfinished changes");
    await context.userEvent.click(
      context.canvas.getByRole("link", { name: "Home" })
    );
    const reopened = within(
      await within(context.canvasElement.ownerDocument.body).findByRole(
        "dialog",
        { name: "Keep your changes?" }
      )
    );
    await expect(
      reopened.getByRole("button", { name: "Discard" })
    ).toBeVisible();
  },
};

export const DiscardUnsavedNavigation: ApplicationStory = {
  parameters: { appPath: dashboardPath },
  play: async (context) => {
    await editTitle(context, "Unfinished changes");
    await context.userEvent.click(
      context.canvas.getByRole("link", { name: "Home" })
    );
    const dialog = within(
      await within(context.canvasElement.ownerDocument.body).findByRole(
        "dialog",
        { name: "Keep your changes?" }
      )
    );
    await context.userEvent.click(
      dialog.getByRole("button", { name: "Discard" })
    );
    await expect(
      await context.canvas.findByRole("heading", {
        name: "What would make home feel better?",
      })
    ).toBeVisible();
  },
};
