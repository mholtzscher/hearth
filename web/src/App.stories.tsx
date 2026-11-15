import type { Meta, StoryObj } from "@storybook/react-vite";
import { StrictMode, useEffect, useState, type ComponentProps } from "react";
import { createMemoryRouter } from "react-router-dom";
import { expect, waitFor, within } from "storybook/test";
import { createApplicationHandlers, conversationPath, conversationPrompt, conversationReply, dashboardId, dashboardPath, sentReply } from "../.storybook/application-fixtures";
import { getDashboard } from "./api/dashboards";
import { expectNoHorizontalOverflow } from "../.storybook/play";
import App, { appRoutes } from "./App";

const meta = {
  title: "application/Workspace",
  component: App,
  tags: ["ai-generated", "!autodocs"],
  decorators: [Story => <StrictMode><Story /></StrictMode>],
  parameters: {
    application: true,
    layout: "fullscreen",
    appPath: "/",
    docs: {
      // A live router contains circular state; use the extracted source instead of serializing its props.
      source: { type: "code" },
      description: { component: "The production application with an isolated memory router and synthetic Core HTTP/SSE. These fullscreen stories include real navigation, conversation, and canvas providers. They do not contact a model or physical Devices." },
    },
  },
  argTypes: { router: { control: false } },
  beforeEach({ msw, parameters }) {
    msw.use(...createApplicationHandlers(parameters.applicationScenario));
  },
  render: function RoutedApplication(_args, { parameters }) {
    const [router, setRouter] = useState<ReturnType<typeof createMemoryRouter>>();
    useEffect(() => {
      const instance = createMemoryRouter(appRoutes, { initialEntries: [parameters.appPath] });
      setRouter(instance);
      return () => instance.dispose();
    }, [parameters.appPath]);
    return router ? <App router={router} /> : <></>;
  },
} satisfies Meta<typeof App>;
export default meta;
// The render function supplies the required router; it is not a serializable Control.
type Story = StoryObj<Partial<ComponentProps<typeof App>>>;
type InteractionContext = Parameters<NonNullable<Story["play"]>>[0];

async function askTemperature({ canvas, userEvent }: InteractionContext) {
  const prompt = await canvas.findByRole("textbox", { name: "Ask Hearth" });
  await waitFor(() => expect(prompt).toBeEnabled());
  await userEvent.click(prompt);
  await userEvent.paste("What is the living room temperature?");
  await userEvent.click(canvas.getByRole("button", { name: "Send message" }));
}

async function editTitle({ canvas, userEvent }: InteractionContext, title: string) {
  const view = within(await canvas.findByRole("region", { name: "View canvas" }));
  await view.findByRole("heading", { name: "Morning overview" });
  await userEvent.click(view.getByRole("button", { name: "Edit" }));
  const input = view.getByRole("textbox", { name: "Dashboard title" });
  await userEvent.clear(input);
  if (title) await userEvent.paste(title);
  return view;
}

export const DesktopHome: Story = {
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Ready", { exact: true })).toBeVisible();
    await expect(canvas.getByRole("heading", { name: "What would make home feel better?" })).toBeVisible();
    await expect(canvas.getByRole("textbox", { name: "Ask Hearth" })).toBeEnabled();
    await expect(canvas.getByRole("navigation", { name: "Main navigation" })).toBeVisible();
  },
};

export const PhoneNavigation: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement, userEvent }) => {
    const menu = await canvas.findByRole("button", { name: "Open navigation" });
    await userEvent.click(menu);
    const close = canvas.getByRole("button", { name: "Close navigation" });
    await expect(close).toHaveFocus();
    await waitFor(() => expect(canvas.getByRole("complementary", { name: "Hearth navigation" })).toBeVisible());
    await userEvent.keyboard("{Escape}");
    await expect(menu).toHaveFocus();
    await expect(menu).toHaveAttribute("aria-expanded", "false");
    await expectNoHorizontalOverflow(canvasElement);
  },
};

export const LoadedConversation: Story = {
  parameters: { appPath: conversationPath },
  play: async ({ canvas, userEvent }) => {
    await expect(await canvas.findByText(conversationReply)).toBeVisible();
    await userEvent.click(canvas.getByText("Used 1 tool", { exact: true }));
    await expect(canvas.getByText("present_view · complete")).toBeVisible();
    await expect(canvas.getByRole("button", { name: "Open in canvas" })).toBeVisible();
    await expect(within(canvas.getByRole("log")).getByText(conversationPrompt)).toBeVisible();
    await expect(await canvas.findByRole("switch", { name: "Hallway lamp power" })).toHaveAttribute("aria-checked", "true");
  },
};

export const SavedDashboard: Story = {
  parameters: { appPath: dashboardPath },
  play: async ({ canvas }) => {
    const view = within(await canvas.findByRole("region", { name: "View canvas" }));
    await expect(await view.findByRole("heading", { name: "Morning overview" })).toBeVisible();
    await expect(view.getByRole("button", { name: "Save changes" })).toBeDisabled();
    await expect(await view.findByRole("switch")).toHaveAttribute("aria-checked", "true");
  },
};

export const TabletRecentDrawer: Story = {
  globals: { viewport: { value: "tablet", isRotated: false } },
  play: async ({ canvas, userEvent }) => {
    // Recent is display:none while the rail drawer is closed; wait for its data before testing focus.
    await canvas.findByText(conversationPrompt);
    const toggle = canvas.getByRole("button", { name: "Recent conversation history" });
    await userEvent.click(toggle);
    const recent = within(canvas.getByRole("region", { name: "Recent" }));
    const conversation = await recent.findByRole("link", { name: conversationPrompt });
    await waitFor(() => expect(conversation).toHaveFocus());
    await userEvent.click(recent.getByRole("button", { name: "Close recent conversations" }));
    await expect(toggle).toHaveFocus();
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
    await userEvent.click(toggle);
    await waitFor(() => expect(conversation).toBeVisible());
  },
};

export const RecentConversation: Story = {
  play: async ({ canvas, userEvent }) => {
    const recent = within(canvas.getByRole("region", { name: "Recent" }));
    const conversation = await recent.findByRole("link", { name: conversationPrompt });
    await userEvent.click(conversation);
    await expect(await canvas.findByText(conversationReply)).toBeVisible();
    await expect(conversation).toHaveAttribute("aria-current", "page");
    await userEvent.click(canvas.getByRole("button", { name: "New conversation" }));
    await expect(await canvas.findByRole("heading", { name: "What would make home feel better?" })).toBeVisible();
    await expect(conversation).not.toHaveAttribute("aria-current");
  },
};

export const ServerNotReady: Story = {
  parameters: { applicationScenario: { server: "not-ready" } },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Not ready", { exact: true })).toBeVisible();
    await expect(canvas.getByRole("textbox", { name: "Ask Hearth" })).toBeEnabled();
  },
};

export const ServerUnavailable: Story = {
  parameters: { applicationScenario: { server: "unavailable" } },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Status unavailable", { exact: true })).toBeVisible();
  },
};

export const DevelopmentConnectionSettings: Story = {
  parameters: { appPath: "/devices" },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await expect(await canvas.findByRole("heading", { name: "Devices" })).toBeVisible();
    const trigger = canvas.getByRole("button", { name: "Connection settings" });
    await userEvent.click(trigger);
    const body = within(canvasElement.ownerDocument.body);
    await waitFor(() => expect(body.getByRole("textbox", { name: "hearthd base URL" })).toBeVisible());
    await expect(body.getByRole("link", { name: "Open OpenAPI JSON" })).toHaveAttribute("href", "/openapi.json");
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(trigger).toHaveFocus());
  },
};

export const PhoneConversation: Story = {
  parameters: { appPath: conversationPath },
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    await expect(await canvas.findByText(conversationReply)).toBeVisible();
    await expect(canvas.getByRole("button", { name: "Open in canvas" })).toBeVisible();
    await expect(await canvas.findByRole("switch", { name: "Hallway lamp power" })).toHaveAttribute("aria-checked", "true");
    await expectNoHorizontalOverflow(canvasElement);
  },
};

export const SendMessage: Story = {
  play: async context => {
    await askTemperature(context);
    await expect(await context.canvas.findByText(sentReply)).toBeVisible();
    await expect(context.canvas.getByRole("textbox", { name: "Ask Hearth" })).toHaveValue("");
    await waitFor(() => expect(context.canvas.queryByText("Looking through your home…")).not.toBeInTheDocument());
  },
};

export const PendingReply: Story = {
  parameters: { applicationScenario: { turn: "pending" } },
  play: async context => {
    await askTemperature(context);
    await expect(await context.canvas.findByText("Used 1 tool · 1 pending or unknown")).toBeVisible();
    await expect(context.canvas.getByText("Looking through your home…")).toBeVisible();
    await expect(context.canvas.getByRole("textbox", { name: "Ask Hearth" })).toBeDisabled();
  },
};

export const RetryMessage: Story = {
  parameters: { applicationScenario: { turn: "fail-once" } },
  play: async context => {
    await askTemperature(context);
    const retry = await context.canvas.findByRole("button", { name: "Retry message" });
    await waitFor(() => expect(retry).toBeEnabled());
    await expect(context.canvas.getByRole("textbox", { name: "Ask Hearth" })).toHaveValue("What is the living room temperature?");
    await expect(context.canvas.getByRole("alert")).toHaveTextContent("503");
    await context.userEvent.click(retry);
    await expect(await context.canvas.findByText(sentReply)).toBeVisible();
    await expect(context.canvas.queryByRole("alert")).not.toBeInTheDocument();
  },
};

export const HistoryFailure: Story = {
  parameters: { appPath: conversationPath, applicationScenario: { historyFailure: true } },
  play: async ({ canvas }) => {
    await expect(await canvas.findByRole("button", { name: "Retry loading history" })).toBeVisible();
    await expect(canvas.getByRole("alert")).toHaveTextContent("Conversation history unavailable");
    await expect(canvas.getByRole("button", { name: "Send message" })).toBeDisabled();
  },
};

export const EditAndSave: Story = {
  parameters: { appPath: dashboardPath },
  play: async context => {
    const view = await editTitle(context, "Evening overview");
    await context.userEvent.click(view.getByRole("button", { name: "Save changes" }));
    await expect(await view.findByText(/Dashboard · revision 2/)).toBeVisible();
    await expect(view.getByRole("heading", { name: "Evening overview" })).toBeVisible();
    await expect(view.getByRole("button", { name: "Save changes" })).toBeDisabled();
  },
};

export const DiscardChanges: Story = {
  parameters: { appPath: dashboardPath },
  play: async context => {
    const view = await editTitle(context, "Temporary title");
    await context.userEvent.click(view.getByRole("button", { name: "Discard changes" }));
    await expect(await view.findByRole("heading", { name: "Morning overview" })).toBeVisible();
    await expect(view.getByRole("textbox", { name: "Dashboard title" })).toHaveValue("Morning overview");
    await expect(view.getByRole("button", { name: "Save changes" })).toBeDisabled();
  },
};

export const InvalidDashboard: Story = {
  parameters: { appPath: dashboardPath },
  play: async context => {
    const view = await editTitle(context, "");
    await expect(view.getByRole("textbox", { name: "Dashboard title" })).toHaveAttribute("aria-invalid", "true");
    await expect(view.getByText("Use 1–80 characters for the dashboard title.")).toBeVisible();
    await expect(view.getByRole("button", { name: "Save changes" })).toBeDisabled();
  },
};

export const SaveConflict: Story = {
  parameters: { appPath: dashboardPath, applicationScenario: { saveConflict: true } },
  play: async context => {
    const view = await editTitle(context, "Keep my changes");
    await context.userEvent.click(view.getByRole("button", { name: "Save changes" }));
    await expect(await view.findByRole("alert")).toHaveTextContent("This dashboard changed elsewhere.");
    await expect(view.getByRole("textbox", { name: "Dashboard title" })).toHaveValue("Keep my changes");
  },
};

export const RecoverSaveConflict: Story = {
  parameters: { appPath: dashboardPath, applicationScenario: { saveConflict: true } },
  play: async context => {
    const view = await editTitle(context, "Keep my changes");
    await context.userEvent.click(view.getByRole("button", { name: "Save changes" }));
    await view.findByRole("alert");
    await context.userEvent.click(view.getByRole("button", { name: "Reload saved version" }));
    await expect(await view.findByRole("heading", { name: "Updated elsewhere" })).toBeVisible();
    await waitFor(() => expect(view.queryByRole("alert")).not.toBeInTheDocument());
    const title = view.getByRole("textbox", { name: "Dashboard title" });
    await context.userEvent.clear(title);
    await context.userEvent.paste("Merged overview");
    await context.userEvent.click(view.getByRole("button", { name: "Save changes" }));
    await expect(await view.findByText(/Dashboard · revision 3/)).toBeVisible();
    await expect(view.getByRole("heading", { name: "Merged overview" })).toBeVisible();
  },
};

export const SaveAsNew: Story = {
  parameters: { appPath: dashboardPath },
  play: async context => {
    const view = await editTitle(context, "Evening copy");
    await context.userEvent.click(view.getByRole("button", { name: "Save as new" }));
    await waitFor(() => {
      expect(view.getByRole("button", { name: "Save changes" })).toBeDisabled();
      expect(view.queryByRole("button", { name: "Discard changes" })).not.toBeInTheDocument();
    });
    await expect(view.getByRole("heading", { name: "Evening copy" })).toBeVisible();
    // Copying must not turn the original dashboard's read endpoint into a 404.
    await expect(getDashboard(dashboardId)).resolves.toMatchObject({
      id: dashboardId, revision: 1, definition: { title: "Morning overview" },
    });
  },
};

export const ExpandedCanvas: Story = {
  parameters: { appPath: dashboardPath },
  play: async ({ canvas, userEvent }) => {
    await canvas.findByRole("heading", { name: "Morning overview" });
    const expand = canvas.getByRole("button", { name: "Expand" });
    await userEvent.click(expand);
    await expect(canvas.getByRole("dialog", { name: "View canvas" })).toBeVisible();
    await userEvent.keyboard("{Escape}");
    await expect(expand).toHaveFocus();
    await expect(canvas.getByRole("region", { name: "View canvas" })).toBeVisible();
    await userEvent.click(expand);
    await expect(canvas.getByRole("dialog", { name: "View canvas" })).toBeVisible();
  },
};

/** Invalid editor input lives locally until corrected; expanding must not remount it. */
export const ExpandedCanvasRetainsInvalidInput: Story = {
  parameters: { appPath: dashboardPath },
  play: async ({ canvas, userEvent }) => {
    await canvas.findByRole("heading", { name: "Morning overview" });
    await userEvent.click(canvas.getByRole("button", { name: "Edit" }));
    const title = canvas.getByRole("textbox", { name: "Dashboard title" });
    await userEvent.clear(title);
    await expect(title).toHaveAttribute("aria-invalid", "true");
    const expand = canvas.getByRole("button", { name: "Expand" });
    await userEvent.click(expand);
    const expanded = canvas.getByRole("dialog", { name: "View canvas" });
    await expect(expanded).toHaveFocus();
    await expect(within(expanded).getByRole("textbox", { name: "Dashboard title" })).toBe(title);
    await expect(title).toHaveValue("");
    await expect(title).toHaveAttribute("aria-invalid", "true");
    await userEvent.keyboard("{Escape}");
    await expect(expand).toHaveFocus();
    await expect(canvas.getByRole("textbox", { name: "Dashboard title" })).toBe(title);
    await expect(title).toHaveValue("");
    await expect(title).toHaveAttribute("aria-invalid", "true");
  },
};

/** Escape closes a nested Source before closing the expanded canvas. */
export const ExpandedCanvasNestedSource: Story = {
  parameters: { appPath: dashboardPath },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await canvas.findByRole("heading", { name: "Morning overview" });
    const expand = canvas.getByRole("button", { name: "Expand" });
    await userEvent.click(expand);
    const expanded = canvas.getByRole("dialog", { name: "View canvas" });
    await expect(expanded).toHaveFocus();
    const source = within(expanded).getAllByRole("button", { name: "Source" })[0];
    await userEvent.click(source);
    const body = within(canvasElement.ownerDocument.body);
    await expect(await body.findByRole("heading", { name: "Reading details" })).toBeVisible();
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(body.queryByRole("heading", { name: "Reading details" })).not.toBeInTheDocument());
    await expect(canvas.getByRole("dialog", { name: "View canvas" })).toBeVisible();
    await expect(source).toHaveFocus();
    await userEvent.keyboard("{Escape}");
    await expect(expand).toHaveFocus();
    await expect(canvas.getByRole("region", { name: "View canvas" })).toBeVisible();
  },
};

export const UnsavedNavigation: Story = {
  parameters: { appPath: dashboardPath },
  play: async context => {
    await editTitle(context, "Unfinished changes");
    await context.userEvent.click(context.canvas.getByRole("link", { name: "Home" }));
    const dialog = within(await within(context.canvasElement.ownerDocument.body).findByRole("dialog", { name: "Keep your changes?" }));
    await context.userEvent.click(dialog.getByRole("button", { name: "Stay" }));
    await expect(context.canvas.getByRole("textbox", { name: "Dashboard title" })).toHaveValue("Unfinished changes");
    await context.userEvent.click(context.canvas.getByRole("link", { name: "Home" }));
    const reopened = within(await within(context.canvasElement.ownerDocument.body).findByRole("dialog", { name: "Keep your changes?" }));
    await expect(reopened.getByRole("button", { name: "Discard" })).toBeVisible();
  },
};

export const DiscardUnsavedNavigation: Story = {
  parameters: { appPath: dashboardPath },
  play: async context => {
    await editTitle(context, "Unfinished changes");
    await context.userEvent.click(context.canvas.getByRole("link", { name: "Home" }));
    const dialog = within(await within(context.canvasElement.ownerDocument.body).findByRole("dialog", { name: "Keep your changes?" }));
    await context.userEvent.click(dialog.getByRole("button", { name: "Discard" }));
    await expect(await context.canvas.findByRole("heading", { name: "What would make home feel better?" })).toBeVisible();
  },
};

export const PhoneCanvas: Story = {
  parameters: { appPath: conversationPath },
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await canvas.findByText(conversationReply);
    await userEvent.click(canvas.getByRole("button", { name: "Open in canvas" }));
    const view = within(await canvas.findByRole("region", { name: "View canvas" }));
    await waitFor(() => expect(view.getByRole("heading", { name: "Morning overview" })).toBeVisible());
    await expect(view.queryByRole("button", { name: "Expand" })).not.toBeInTheDocument();
    await expect(await view.findByRole("switch", { name: "Hallway lamp power" })).toHaveAttribute("aria-checked", "true");
    const panels = within(canvas.getByRole("group", { name: "Workspace panels" }));
    await expect(panels.getByRole("button", { name: "Canvas" })).toHaveAttribute("aria-pressed", "true");
    await expectNoHorizontalOverflow(canvasElement);
    await userEvent.click(panels.getByRole("button", { name: "Conversation" }));
    await waitFor(() => expect(canvas.getByText(conversationReply)).toBeVisible());
    await userEvent.click(panels.getByRole("button", { name: "Canvas" }));
    await expectNoHorizontalOverflow(canvasElement);
  },
};

export const PhoneSavedCanvas: Story = {
  parameters: { appPath: dashboardPath },
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    const view = within(await canvas.findByRole("region", { name: "View canvas" }));
    await expect(await view.findByRole("heading", { name: "Morning overview" })).toBeVisible();
    await expect(view.queryByRole("button", { name: "Expand" })).not.toBeInTheDocument();
    await expect(view.queryByRole("button", { name: "Save changes" })).not.toBeInTheDocument();
    await expectNoHorizontalOverflow(canvasElement);
  },
};

export const DashboardLibrary: Story = {
  parameters: { appPath: "/dashboards" },
  play: async ({ canvas }) => {
    await expect(await canvas.findByRole("heading", { name: "Morning overview" })).toBeVisible();
    await expect(canvas.getByRole("link", { name: "Open dashboard →" })).toHaveAttribute("href", dashboardPath);
  },
};

export const EmptyDashboardLibrary: Story = {
  parameters: { appPath: "/dashboards", applicationScenario: { dashboards: "empty" } },
  play: async ({ canvas }) => {
    await expect(await canvas.findByRole("heading", { name: "Your first dashboard starts with a question." })).toBeVisible();
    await expect(canvas.getByRole("link", { name: "Start a conversation →" })).toHaveAttribute("href", "/");
  },
};

export const PhoneDashboardLibrary: Story = {
  parameters: { appPath: "/dashboards" },
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async context => {
    await DashboardLibrary.play!(context);
    await expectNoHorizontalOverflow(context.canvasElement);
  },
};

export const DeleteDashboard: Story = {
  parameters: { appPath: "/dashboards" },
  play: async ({ canvas, canvasElement, userEvent }) => {
    const remove = await canvas.findByRole("button", { name: "Delete Morning overview" });
    await userEvent.click(remove);
    const body = within(canvasElement.ownerDocument.body);
    const dialog = within(body.getByRole("dialog", { name: "Remove this dashboard?" }));
    await expect(dialog.getByText(/Your home data and conversation remain unchanged/)).toBeVisible();
    await userEvent.click(dialog.getByRole("button", { name: "Cancel" }));
    await expect(remove).toHaveFocus();
    await userEvent.click(remove);
    await userEvent.click(body.getByRole("button", { name: "Delete dashboard" }));
    await expect(await canvas.findByRole("heading", { name: "Your first dashboard starts with a question." })).toBeVisible();
  },
};

export const PhoneDeleteConfirmation: Story = {
  parameters: { appPath: "/dashboards" },
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await userEvent.click(await canvas.findByRole("button", { name: "Delete Morning overview" }));
    const dialog = within(within(canvasElement.ownerDocument.body).getByRole("dialog", { name: "Remove this dashboard?" }));
    await expect(dialog.getByRole("button", { name: "Cancel" })).toHaveFocus();
    await expect(dialog.getByText(/Your home data and conversation remain unchanged/)).toBeVisible();
    await expectNoHorizontalOverflow(canvasElement);
  },
};

export const SearchConversations: Story = {
  parameters: { applicationScenario: { conversations: "searchable" } },
  play: async ({ canvas, userEvent }) => {
    const search = await canvas.findByRole("searchbox", { name: "Search conversations" });
    await userEvent.type(search, "garden");
    await expect(await canvas.findByText("No conversations match “garden”.")).toBeVisible();
    await userEvent.click(canvas.getByRole("button", { name: "Clear search" }));
    await expect(search).toHaveFocus();
    await expect(canvas.getByRole("link", { name: conversationPrompt })).toBeVisible();
    await userEvent.type(search, "garden");
    await expect(within(canvas.getByRole("region", { name: "Recent" })).getByRole("status")).toHaveTextContent("No conversations match “garden”.");
  },
};

export const PendingView: Story = {
  parameters: { applicationScenario: { turn: "pending-view" } },
  play: async context => {
    await askTemperature(context);
    await expect(await context.canvas.findByRole("heading", { name: "Preparing a view" })).toBeVisible();
    await expect(context.canvas.getByRole("textbox", { name: "Ask Hearth" })).toBeDisabled();
  },
};

export const SelectedViewMessage: Story = {
  parameters: { appPath: conversationPath },
  play: async context => {
    await context.canvas.findByText(conversationReply);
    await context.userEvent.click(context.canvas.getByRole("button", { name: "Open in canvas" }));
    await askTemperature(context);
    // History reconciliation replaces the streamed message after the reply arrives.
    await waitFor(() => {
      expect(context.canvas.getByRole("textbox", { name: "Ask Hearth" })).toBeEnabled();
      expect(context.canvas.getByText(sentReply)).toBeVisible();
      expect(context.canvas.getByText("Selected view: Morning overview")).toBeVisible();
    });
  },
};

export const InvalidConversationDraft: Story = {
  parameters: { appPath: conversationPath },
  play: async context => {
    await context.canvas.findByText(conversationReply);
    await context.userEvent.click(context.canvas.getByRole("button", { name: "Open in canvas" }));
    await editTitle(context, "");
    await expect(context.canvas.getByText("Fix or discard invalid draft edits before sending")).toBeVisible();
    await expect(context.canvas.getByRole("button", { name: "Send message" })).toBeDisabled();
  },
};

export const ReadingOlderMessages: Story = {
  parameters: { appPath: conversationPath, applicationScenario: { messages: "long" } },
  play: async ({ canvas }) => {
    await canvas.findByText("Room 15 is comfortable. The temperature is 21.5 °C and the lamp is on.");
    const log = canvas.getByRole("log");
    await waitFor(() => expect(log.scrollTop).toBeGreaterThan(0));
    log.scrollTo({ top: 0, behavior: "instant" });
    await expect(await canvas.findByRole("button", { name: "Jump to latest" })).toBeVisible();
  },
};

export const EmptyRecent: Story = {
  parameters: { applicationScenario: { conversations: "empty" } },
  play: async ({ canvas }) => {
    const recent = within(await canvas.findByRole("region", { name: "Recent" }));
    await expect(await recent.findByText("Conversations you start appear here.")).toBeVisible();
    await expect(canvas.getByRole("textbox", { name: "Ask Hearth" })).toBeEnabled();
  },
};

export const RecentListFailure: Story = {
  parameters: { applicationScenario: { conversations: "error" } },
  play: async ({ canvas }) => {
    const recent = within(await canvas.findByRole("region", { name: "Recent" }));
    await expect(await recent.findByRole("alert")).toHaveTextContent("Could not load conversations.");
    await expect(recent.getByRole("button", { name: "Retry" })).toBeEnabled();
  },
};

export const PhoneHistorySearch: Story = {
  parameters: { applicationScenario: { conversations: "searchable" } },
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await userEvent.click(await canvas.findByRole("button", { name: "Open navigation" }));
    const recent = within(canvas.getByRole("region", { name: "Recent" }));
    const search = await recent.findByRole("searchbox", { name: "Search conversations" });
    const navigation = canvas.getByRole("navigation", { name: "Main navigation" });
    await waitFor(() => expect(navigation).toBeVisible());
    await userEvent.click(search);
    await expect(navigation).not.toBeVisible();
    await userEvent.type(search, "garden");
    await expect(await recent.findByRole("status")).toHaveTextContent("No conversations match “garden”.");
    await userEvent.click(recent.getByRole("heading", { name: "Recent" }));
    await expect(search).not.toHaveFocus();
    await expect(search).toHaveValue("garden");
    await expect(navigation).not.toBeVisible();
    await expect(recent.getByText("No conversations match “garden”.")).toBeVisible();
    await expectNoHorizontalOverflow(canvasElement);
  },
};

export const RichConversation: Story = {
  parameters: { appPath: conversationPath, applicationScenario: { messages: "rich" } },
  play: async ({ canvas, userEvent }) => {
    const log = within(await canvas.findByRole("log"));
    await expect(await log.findByText("Room readings")).toBeVisible();
    await expect(log.getByRole("table")).toBeVisible();
    await expect(log.getByText('{"temperature":21}')).toBeVisible();
    await expect(log.getByText("Kitchen: 21 °C")).toBeVisible();
    await expect(log.getByText("This artifact cannot be shown in this Hearth version. The original tool result is retained.")).toBeVisible();
    await userEvent.click(log.getByText("Original artifact"));
    await expect(log.getByText(/hearth.future/)).toBeVisible();
  },
};

export const StarterQuestion: Story = {
  play: async context => {
    await expect(await context.canvas.findByRole("textbox", { name: "Ask Hearth" })).toBeEnabled();
    await context.userEvent.click(context.canvas.getByRole("button", { name: "Show me the temperatures" }));
    await expect(await context.canvas.findByText(sentReply)).toBeVisible();
    await expect(within(context.canvas.getByRole("log")).getByText("Show me the temperatures throughout the house.")).toBeVisible();
  },
};

export const TabletConversationCanvas: Story = {
  parameters: { appPath: conversationPath },
  globals: { viewport: { value: "tablet", isRotated: false } },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await expect(await canvas.findByText(conversationReply)).toBeVisible();
    await userEvent.click(canvas.getByRole("button", { name: "Open in canvas" }));
    const view = within(await canvas.findByRole("region", { name: "View canvas" }));
    await expect(await view.findByRole("heading", { name: "Morning overview" })).toBeVisible();
    await expect(await view.findByRole("switch", { name: "Hallway lamp power" })).toHaveAttribute("aria-checked", "true");
    await expectNoHorizontalOverflow(canvasElement);
  },
};

async function queueDuringReply({ canvas, userEvent }: InteractionContext) {
  await expect(await canvas.findByRole("button", { name: "Show me the temperatures" })).toBeEnabled();
  await userEvent.click(canvas.getByRole("button", { name: "Show me the temperatures" }));
  await userEvent.click(await canvas.findByRole("link", { name: "Empty conversation" }));
  const prompt = await canvas.findByRole("textbox", { name: "Ask Hearth" });
  await waitFor(() => expect(prompt).toBeEnabled());
  await userEvent.type(prompt, "Check the hallway.");
  await userEvent.click(canvas.getByRole("button", { name: "Send message" }));
  await expect(await canvas.findByText("Message queued until the current reply finishes.")).toBeVisible();
  await expect(canvas.getByRole("button", { name: "Cancel queued message" })).toBeEnabled();
}

export const QueuedDuringReply: Story = {
  parameters: { applicationScenario: { turn: "queue-collision" } },
  play: queueDuringReply,
};

export const CancelQueuedMessage: Story = {
  parameters: { applicationScenario: { turn: "queue-collision" } },
  play: async context => {
    await queueDuringReply(context);
    await context.userEvent.click(context.canvas.getByRole("button", { name: "Cancel queued message" }));
    await waitFor(() => expect(context.canvas.queryByText("Message queued until the current reply finishes.")).not.toBeInTheDocument());
    await expect(context.canvas.getByText("Looking through your home…")).toBeVisible();
  },
};

export const QueuedAfterFailedReply: Story = {
  beforeEach({ msw, parameters }) {
    let release!: () => void;
    const queuedTurnFailure = new Promise<void>(resolve => { release = resolve; });
    parameters.releaseQueuedTurn = release;
    msw.use(...createApplicationHandlers({ turn: "queue-collision", queuedTurnFailure }));
    return () => { delete parameters.releaseQueuedTurn; };
  },
  play: async context => {
    await queueDuringReply(context);
    context.parameters.releaseQueuedTurn();
    await expect(await context.canvas.findByRole("alert")).toHaveTextContent("agent turn failed");
    await expect(await context.canvas.findByText("Queued message has not been sent.")).toBeVisible();
    await expect(await context.canvas.findByText("Used 1 tool · 1 pending or unknown")).toBeVisible();
    await expect(context.canvas.getByRole("button", { name: "Send queued message" })).toBeDisabled();
    await expect(context.canvas.getByRole("button", { name: "Discard failed prompt" })).toBeEnabled();
    await expect(context.canvas.getByRole("button", { name: "Cancel queued message" })).toBeEnabled();
    await expect(context.canvas.getByRole("textbox", { name: "Ask Hearth" })).toHaveValue("Check the hallway.");
  },
};

export const AutomationCreatePreview: Story = {
  parameters: { appPath: conversationPath, applicationScenario: { messages: "automation-create" } },
  play: async ({ canvas }) => {
    const log = within(await canvas.findByRole("log"));
    await expect(await log.findByText("Unsaved proposal · not created")).toBeVisible();
    await expect(await log.findByRole("heading", { name: "Hallway light at dusk", level: 4 })).toBeVisible();
    await expect(await log.findByText("Household")).toBeVisible();
    await expect((await log.findAllByRole("link", { name: "Office plug (Office plug)" })).length).toBeGreaterThan(0);
    await expect(log.getByRole("button", { name: "Open in canvas" })).toBeEnabled();
  },
};

export const AutomationShowingInCanvas: Story = {
  parameters: { appPath: conversationPath, applicationScenario: { messages: "automation-create" } },
  play: async ({ canvas, userEvent }) => {
    const log = within(await canvas.findByRole("log"));
    await expect(await log.findByRole("heading", { name: "Hallway light at dusk", level: 4 })).toBeVisible();
    await expect(await log.findByText("Household")).toBeVisible();
    await expect((await log.findAllByRole("link", { name: "Office plug (Office plug)" })).length).toBeGreaterThan(0);
    await userEvent.click(log.getByRole("button", { name: "Open in canvas" }));
    await expect(await log.findByRole("button", { name: "Showing in canvas" })).toBeVisible();
    await expect(await canvas.findByRole("region", { name: "Automation canvas" })).toBeVisible();
  },
};

export const AutomationReplacePreview: Story = {
  parameters: { appPath: conversationPath, applicationScenario: { messages: "automation-replace" } },
  play: async ({ canvas }) => {
    const log = within(await canvas.findByRole("log"));
    await expect(await log.findByText("Replacement proposal · based on revision 3")).toBeVisible();
    await expect(await log.findByRole("heading", { name: "Hallway light at dusk", level: 4 })).toBeVisible();
    await expect(await log.findByText("Household")).toBeVisible();
    await expect((await log.findAllByRole("link", { name: "Office plug (Office plug)" })).length).toBeGreaterThan(0);
  },
};

export const AutomationReferencePreview: Story = {
  parameters: { appPath: conversationPath, applicationScenario: { messages: "automation-reference" } },
  play: async ({ canvas }) => {
    const log = within(await canvas.findByRole("log"));
    await expect(await log.findByText("Saved reference · current definition")).toBeVisible();
    await expect(await log.findByRole("heading", { name: "Hallway light at dusk", level: 4 })).toBeVisible();
    await expect(await log.findByText("Household")).toBeVisible();
    await expect((await log.findAllByRole("link", { name: "Office plug (Office plug)" })).length).toBeGreaterThan(0);
  },
};

export const AutomationMissingPreview: Story = {
  parameters: { appPath: conversationPath, applicationScenario: { messages: "automation-missing" } },
  play: async ({ canvas }) => {
    const log = within(await canvas.findByRole("log"));
    await expect(await log.findByRole("alert")).toHaveTextContent("This Automation no longer exists. Its earlier tool result is retained below.");
    await expect(log.getByRole("button", { name: "Open in canvas" })).toBeEnabled();
  },
};

export const AutomationLoadingPreview: Story = {
  parameters: { appPath: conversationPath, applicationScenario: { messages: "automation-loading" } },
  play: async ({ canvas }) => {
    const log = within(await canvas.findByRole("log"));
    await expect(await log.findByText("Loading Automation…")).toBeVisible();
    await expect(log.getByText("Saved reference · current definition")).toBeVisible();
  },
};

export const ShortPhoneConversation: Story = {
  parameters: {
    appPath: conversationPath,
    viewport: { options: { shortPhone: { name: "Short phone", styles: { width: "390px", height: "540px" } } } },
  },
  globals: { viewport: { value: "shortPhone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    await expect(await canvas.findByText(conversationReply)).toBeVisible();
    await expect(canvas.getByRole("textbox", { name: "Ask Hearth" })).toBeEnabled();
    await expect(window.innerHeight).toBe(540);
    await expectNoHorizontalOverflow(canvasElement);
  },
};
