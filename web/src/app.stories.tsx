import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { StrictMode } from "react";
import type { ComponentProps } from "react";
import { createMemoryRouter } from "react-router-dom";
import { expect, waitFor, within } from "storybook/test";

import {
  createApplicationHandlers,
  conversationPath,
  conversationPrompt,
  conversationReply,
  dashboardId,
  dashboardPath,
  sentReply,
} from "../.storybook/application-fixtures";
import { entityId, fixtureNow } from "../.storybook/fixtures";
import { expectNoHorizontalOverflow } from "../.storybook/play";
import { getDashboard } from "./api/dashboards";
import type { CommandRecord } from "./api/types";
import App, { appRoutes } from "./app";

const meta = {
  title: "Application/Workspace",
  component: App,
  tags: ["ai-generated", "!autodocs"],
  decorators: [
    (Story) => (
      <StrictMode>
        <Story />
      </StrictMode>
    ),
  ],
  parameters: {
    application: true,
    layout: "fullscreen",
    appPath: "/",
    docs: {
      // A live router contains circular state; use the extracted source instead of serializing its props.
      source: { type: "code" },
      description: {
        component:
          "The production application with an isolated memory router and synthetic Core HTTP/SSE. These fullscreen stories include real navigation, conversation, and canvas providers. They do not contact a model or physical Devices.",
      },
    },
  },
  argTypes: { router: { control: false } },
  loaders: [
    ({ parameters }) => ({
      router: createMemoryRouter(appRoutes, {
        initialEntries: [parameters.appPath],
      }),
    }),
  ],
  beforeEach({ msw, parameters, loaded }) {
    msw.use(...createApplicationHandlers(parameters.applicationScenario));
    return () => loaded.router.dispose();
  },
  render: (_args, { loaded }) => <App router={loaded.router} />,
} satisfies Meta<typeof App>;
export default meta;
// The loader supplies the required router; it is not a serializable Control.
type Story = StoryObj<Partial<ComponentProps<typeof App>>>;
type InteractionContext = Parameters<NonNullable<Story["play"]>>[0];

const longSelectedTitle =
  "Detailed whole-home climate, lighting and comfort overview for every room";

const askTemperature = async ({ canvas, userEvent }: InteractionContext) => {
  const prompt = await canvas.findByRole("textbox", { name: "Ask Hearth" });
  await waitFor(() => expect(prompt).toBeEnabled());
  await userEvent.click(prompt);
  await userEvent.paste("What is the living room temperature?");
  await userEvent.click(canvas.getByRole("button", { name: "Send message" }));
};

const editTitle = async (
  { canvas, userEvent }: InteractionContext,
  title: string
) => {
  const view = within(
    await canvas.findByRole("region", { name: "View canvas" })
  );
  await view.findByRole("heading", { name: "Morning overview" });
  await userEvent.click(view.getByRole("button", { name: "Edit" }));
  const input = view.getByRole("textbox", { name: "Dashboard title" });
  await userEvent.clear(input);
  if (title) {
    await userEvent.paste(title);
  }
  return view;
};

const expectSelectedContextFits = async ({ canvas }: InteractionContext) => {
  const selected = within(canvas.getByRole("group", { name: "Selected view" }));
  const show = selected.getByRole("button", {
    name: `Show selected view: ${longSelectedTitle}`,
  });
  const close = selected.getByRole("button", { name: "Close selected view" });
  const send = canvas.getByRole("button", { name: "Send message" });
  const group = canvas
    .getByRole("group", { name: "Selected view" })
    .getBoundingClientRect();
  const showRect = show.getBoundingClientRect();
  const closeRect = close.getBoundingClientRect();
  const sendRect = send.getBoundingClientRect();
  await expect(closeRect.right).toBeLessThanOrEqual(group.right + 0.5);
  await expect(group.right).toBeLessThanOrEqual(sendRect.left + 0.5);
  await expect(showRect.right).toBeLessThanOrEqual(closeRect.left + 0.5);
  await expect(closeRect.width).toBe(44);
  await expect(closeRect.height).toBe(44);
  await expect(showRect.width).toBeGreaterThanOrEqual(44);
  await expect(showRect.height).toBe(44);
  await expect(sendRect.width).toBeGreaterThanOrEqual(44);
  await expect(sendRect.height).toBe(44);
  const title = within(show).getByText(longSelectedTitle);
  await expect(show).toHaveAttribute("title", longSelectedTitle);
  await expect(title.clientWidth).toBeGreaterThan(0);
  await expect(title.scrollWidth).toBeGreaterThan(title.clientWidth);
  await expect(getComputedStyle(title).textOverflow).toBe("ellipsis");
};

const expectSidebarNewTreatment = async (button: HTMLElement) => {
  const style = getComputedStyle(button);
  await expect(style.height).toBe("44px");
  await expect(style.borderRadius).toBe("8px");
  await expect(style.backgroundColor).toBe("rgb(12, 12, 12)");
  await expect(style.borderColor).toBe("rgb(31, 31, 31)");
};

export const DesktopHome: Story = {
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByText("Ready", { exact: true })
    ).toBeVisible();
    await expect(
      canvas.getByRole("heading", { name: "What would make home feel better?" })
    ).toBeVisible();
    await expect(
      canvas.getByRole("textbox", { name: "Ask Hearth" })
    ).toBeEnabled();
    await expect(
      canvas.getByRole("navigation", { name: "Main navigation" })
    ).toBeVisible();
    const sidebar = within(
      canvas.getByRole("complementary", { name: "Hearth navigation" })
    );
    await expectSidebarNewTreatment(
      sidebar.getByRole("button", { name: "New conversation" })
    );
  },
};

/** The Commands filter keeps native selection and sends the selected status to Core. */
export const CommandStatusFilter: Story = {
  parameters: { appPath: "/commands" },
  beforeEach({ msw }) {
    const commands: CommandRecord[] = [
      {
        id: "cmd_story_requested",
        entity_id: entityId(4),
        operation: "turn_on",
        parameters: {},
        status: "requested",
        requested_at: fixtureNow,
        deadline_at: fixtureNow,
      },
      {
        id: "cmd_story_rejected",
        entity_id: entityId(4),
        operation: "turn_off",
        parameters: {},
        status: "rejected",
        requested_at: fixtureNow,
        deadline_at: fixtureNow,
      },
    ];
    msw.use(
      http.get("/v1/commands", ({ request }) => {
        const status = new URL(request.url).searchParams.get("status");
        return HttpResponse.json({
          items: commands.filter(
            (command) => !status || command.status === status
          ),
        });
      })
    );
  },
  play: async ({ canvas, userEvent }) => {
    await expect(
      await canvas.findByRole("cell", { name: "turn_on" })
    ).toBeVisible();
    await expect(canvas.getByRole("cell", { name: "turn_off" })).toBeVisible();
    const filter = canvas.getByRole("combobox", { name: "Status filter" });
    await userEvent.selectOptions(filter, "rejected");
    await expect(filter).toHaveValue("rejected");
    // Changing the filter clears the list until the filtered response arrives.
    await waitFor(() => {
      expect(
        canvas.queryByRole("cell", { name: "turn_on" })
      ).not.toBeInTheDocument();
      expect(canvas.getByRole("cell", { name: "turn_off" })).toBeVisible();
    });
    await userEvent.click(canvas.getByRole("button", { name: "Clear" }));
    await expect(filter).toHaveValue("");
    await expect(
      await canvas.findByRole("cell", { name: "turn_on" })
    ).toBeVisible();
  },
};

export const PhoneNavigation: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement, userEvent }) => {
    const menu = await canvas.findByRole("button", { name: "Open navigation" });
    const title = canvasElement.querySelector('[data-slot="phone-bar-title"]');
    await expect(title).toBeVisible();
    await expect(title).toHaveTextContent("Home");
    await userEvent.click(menu);
    const close = canvas.getByRole("button", { name: "Close navigation" });
    await expect(close).toHaveFocus();
    await waitFor(() =>
      expect(
        canvas.getByRole("complementary", { name: "Hearth navigation" })
      ).toBeVisible()
    );
    const sidebar = within(
      canvas.getByRole("complementary", { name: "Hearth navigation" })
    );
    await expectSidebarNewTreatment(
      sidebar.getByRole("button", { name: "New conversation" })
    );
    await userEvent.keyboard("{Escape}");
    await expect(menu).toHaveFocus();
    await expect(menu).toHaveAttribute("aria-expanded", "false");
    await expectNoHorizontalOverflow(canvasElement);
  },
};

/** The phone toolbar keeps page switching, Entity jump, and diagnostics reachable. */
export const PhoneDevelopmentNavigation: Story = {
  parameters: { appPath: "/devices" },
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await expect(
      await canvas.findByRole("cell", { name: "Weather station" })
    ).toBeVisible();
    const pages = canvas.getByRole("combobox", { name: "Development page" });
    await expect(pages).toHaveValue("/devices");
    await userEvent.selectOptions(pages, "/entities");
    await expect(
      await canvas.findByRole("heading", { name: "Entities", level: 1 })
    ).toBeVisible();
    await expect(pages).toHaveValue("/entities");
    const jump = canvas.getByRole("textbox", { name: "Jump to entity id" });
    await userEvent.type(jump, `${entityId(4)}{Enter}`);
    await expect(
      await canvas.findByRole("heading", { name: "Office plug", level: 1 })
    ).toBeVisible();
    await expect(pages).toHaveValue(`/entities/${entityId(4)}`);
    const settings = canvas.getByRole("button", {
      name: "Connection settings",
    });
    await userEvent.click(settings);
    const body = within(canvasElement.ownerDocument.body);
    await expect(await body.findByText("healthz: ok")).toBeVisible();
    await expect(await body.findByText("readyz: ready")).toBeVisible();
    await userEvent.keyboard("{Escape}");
    await expect(settings).toHaveFocus();
    await expectNoHorizontalOverflow(canvasElement);
  },
};

export const PhoneDevelopmentReturnToList: Story = {
  parameters: { appPath: `/entities/${entityId(4)}` },
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await expect(
      await canvas.findByRole("heading", { name: "Office plug", level: 1 })
    ).toBeVisible();
    const pages = canvas.getByRole("combobox", { name: "Development page" });
    await expect(pages).toHaveValue(`/entities/${entityId(4)}`);
    await userEvent.selectOptions(pages, "/entities");
    await expect(
      await canvas.findByRole("heading", { name: "Entities", level: 1 })
    ).toBeVisible();
    await expect(
      await canvas.findByRole("cell", { name: "Office plug" })
    ).toBeVisible();
    await expect(pages).toHaveValue("/entities");
    await expectNoHorizontalOverflow(canvasElement);
  },
};

export const LoadedConversation: Story = {
  parameters: { appPath: conversationPath },
  play: async ({ canvas, userEvent }) => {
    await expect(await canvas.findByText(conversationReply)).toBeVisible();
    await userEvent.click(canvas.getByText("Used 1 tool", { exact: true }));
    await expect(canvas.getByText("present_view · complete")).toBeVisible();
    await expect(
      canvas.getByRole("button", { name: "Open in canvas" })
    ).toBeVisible();
    await expect(
      within(canvas.getByRole("log")).getByText(conversationPrompt)
    ).toBeVisible();
    await expect(
      await canvas.findByRole("switch", { name: "Hallway lamp power" })
    ).toHaveAttribute("aria-checked", "true");
  },
};

export const SavedDashboard: Story = {
  parameters: { appPath: dashboardPath },
  play: async ({ canvas }) => {
    const view = within(
      await canvas.findByRole("region", { name: "View canvas" })
    );
    await expect(
      await view.findByRole("heading", { name: "Morning overview" })
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

export const TabletRecentDrawer: Story = {
  globals: { viewport: { value: "tablet", isRotated: false } },
  play: async ({ canvas, userEvent }) => {
    // Recent is display:none while the rail drawer is closed; wait for its data before testing focus.
    await canvas.findByText(conversationPrompt);
    const toggle = canvas.getByRole("button", {
      name: "Recent conversation history",
    });
    await userEvent.click(toggle);
    const recent = within(canvas.getByRole("region", { name: "Recent" }));
    const conversation = await recent.findByRole("link", {
      name: conversationPrompt,
    });
    await waitFor(() => expect(conversation).toHaveFocus());
    await userEvent.click(
      recent.getByRole("button", { name: "Close recent conversations" })
    );
    await expect(toggle).toHaveFocus();
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
    await userEvent.click(toggle);
    await waitFor(() => expect(conversation).toBeVisible());
  },
};

export const RecentConversation: Story = {
  play: async ({ canvas, userEvent }) => {
    const recent = within(canvas.getByRole("region", { name: "Recent" }));
    const conversation = await recent.findByRole("link", {
      name: conversationPrompt,
    });
    await userEvent.click(conversation);
    await expect(await canvas.findByText(conversationReply)).toBeVisible();
    await expect(conversation).toHaveAttribute("aria-current", "page");
    await userEvent.click(
      canvas.getByRole("button", { name: "New conversation" })
    );
    await expect(
      await canvas.findByRole("heading", {
        name: "What would make home feel better?",
      })
    ).toBeVisible();
    await expect(conversation).not.toHaveAttribute("aria-current");
  },
};

export const ServerNotReady: Story = {
  parameters: { applicationScenario: { server: "not-ready" } },
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByText("Not ready", { exact: true })
    ).toBeVisible();
    await expect(
      canvas.getByRole("textbox", { name: "Ask Hearth" })
    ).toBeEnabled();
  },
};

export const ServerUnavailable: Story = {
  parameters: { applicationScenario: { server: "unavailable" } },
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByText("Status unavailable", { exact: true })
    ).toBeVisible();
  },
};

export const DevelopmentConnectionSettings: Story = {
  parameters: { appPath: "/devices" },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await expect(
      await canvas.findByRole("heading", { name: "Devices" })
    ).toBeVisible();
    const trigger = canvas.getByRole("button", { name: "Connection settings" });
    await userEvent.click(trigger);
    const body = within(canvasElement.ownerDocument.body);
    await waitFor(() =>
      expect(
        body.getByRole("textbox", { name: "hearthd base URL" })
      ).toBeVisible()
    );
    await expect(
      body.getByRole("link", { name: "Open OpenAPI JSON" })
    ).toHaveAttribute("href", "/openapi.json");
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(trigger).toHaveFocus());
  },
};

export const PhoneConversation: Story = {
  parameters: { appPath: conversationPath },
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    await expect(await canvas.findByText(conversationReply)).toBeVisible();
    await expect(
      canvas.getByRole("button", { name: "Open in canvas" })
    ).toBeVisible();
    await expect(
      await canvas.findByRole("switch", { name: "Hallway lamp power" })
    ).toHaveAttribute("aria-checked", "true");
    await expectNoHorizontalOverflow(canvasElement);
  },
};

export const SendMessage: Story = {
  play: async (context) => {
    await askTemperature(context);
    await expect(await context.canvas.findByText(sentReply)).toBeVisible();
    await expect(
      context.canvas.getByRole("textbox", { name: "Ask Hearth" })
    ).toHaveValue("");
    await waitFor(() =>
      expect(
        context.canvas.queryByText("Looking through your home…")
      ).not.toBeInTheDocument()
    );
  },
};

export const PendingReply: Story = {
  parameters: { applicationScenario: { turn: "pending" } },
  play: async (context) => {
    await askTemperature(context);
    await expect(
      await context.canvas.findByText("Used 1 tool · 1 pending or unknown")
    ).toBeVisible();
    await expect(
      context.canvas.getByText("Looking through your home…")
    ).toBeVisible();
    await expect(
      context.canvas.getByRole("textbox", { name: "Ask Hearth" })
    ).toBeDisabled();
  },
};

export const RetryMessage: Story = {
  parameters: { applicationScenario: { turn: "fail-once" } },
  play: async (context) => {
    await askTemperature(context);
    const retry = await context.canvas.findByRole("button", {
      name: "Retry message",
    });
    await waitFor(() => expect(retry).toBeEnabled());
    await expect(
      context.canvas.getByRole("textbox", { name: "Ask Hearth" })
    ).toHaveValue("What is the living room temperature?");
    await expect(context.canvas.getByRole("alert")).toHaveTextContent("503");
    await context.userEvent.click(retry);
    await expect(await context.canvas.findByText(sentReply)).toBeVisible();
    await expect(context.canvas.queryByRole("alert")).not.toBeInTheDocument();
  },
};

export const HistoryFailure: Story = {
  parameters: {
    appPath: conversationPath,
    applicationScenario: { historyFailure: true },
  },
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByRole("button", { name: "Retry loading history" })
    ).toBeVisible();
    await expect(canvas.getByRole("alert")).toHaveTextContent(
      "Conversation history unavailable"
    );
    await expect(
      canvas.getByRole("button", { name: "Send message" })
    ).toBeDisabled();
  },
};

export const EditAndSave: Story = {
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

export const DiscardChanges: Story = {
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

export const InvalidDashboard: Story = {
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

export const SaveConflict: Story = {
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

export const RecoverSaveConflict: Story = {
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

export const SaveAsNew: Story = {
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

export const ExpandedCanvas: Story = {
  parameters: { appPath: dashboardPath },
  play: async ({ canvas, userEvent }) => {
    await canvas.findByRole("heading", { name: "Morning overview" });
    const expand = canvas.getByRole("button", { name: "Expand" });
    await userEvent.click(expand);
    await expect(
      canvas.getByRole("dialog", { name: "View canvas" })
    ).toBeVisible();
    await userEvent.keyboard("{Escape}");
    await expect(expand).toHaveFocus();
    await expect(
      canvas.getByRole("region", { name: "View canvas" })
    ).toBeVisible();
    await userEvent.click(expand);
    await expect(
      canvas.getByRole("dialog", { name: "View canvas" })
    ).toBeVisible();
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
    await expect(
      within(expanded).getByRole("textbox", { name: "Dashboard title" })
    ).toBe(title);
    await expect(title).toHaveValue("");
    await expect(title).toHaveAttribute("aria-invalid", "true");
    await userEvent.keyboard("{Escape}");
    await expect(expand).toHaveFocus();
    await expect(canvas.getByRole("textbox", { name: "Dashboard title" })).toBe(
      title
    );
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
    const [source] = within(expanded).getAllByRole("button", {
      name: "Source",
    });
    if (!source) {
      throw new Error("Expected a Source button");
    }
    await userEvent.click(source);
    const body = within(canvasElement.ownerDocument.body);
    await expect(
      await body.findByRole("heading", { name: "Reading details" })
    ).toBeVisible();
    await userEvent.keyboard("{Escape}");
    await waitFor(() =>
      expect(
        body.queryByRole("heading", { name: "Reading details" })
      ).not.toBeInTheDocument()
    );
    await expect(
      canvas.getByRole("dialog", { name: "View canvas" })
    ).toBeVisible();
    await expect(source).toHaveFocus();
    await userEvent.keyboard("{Escape}");
    await expect(expand).toHaveFocus();
    await expect(
      canvas.getByRole("region", { name: "View canvas" })
    ).toBeVisible();
  },
};

export const UnsavedNavigation: Story = {
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

export const DiscardUnsavedNavigation: Story = {
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

export const PhoneCanvas: Story = {
  parameters: { appPath: conversationPath },
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async (context) => {
    const { canvas, canvasElement, userEvent } = context;
    await canvas.findByText(conversationReply);
    await userEvent.click(
      canvas.getByRole("button", { name: "Open in canvas" })
    );
    const view = within(
      await canvas.findByRole("region", { name: "View canvas" })
    );
    await waitFor(() =>
      expect(
        view.getByRole("heading", { name: "Morning overview" })
      ).toBeVisible()
    );
    await expect(
      view.queryByRole("button", { name: "Expand" })
    ).not.toBeInTheDocument();
    await expect(
      await view.findByRole("switch", { name: "Hallway lamp power" })
    ).toHaveAttribute("aria-checked", "true");
    const panels = within(
      canvas.getByRole("group", { name: "Workspace panels" })
    );
    await expect(
      panels.getByRole("button", { name: "Canvas" })
    ).toHaveAttribute("aria-pressed", "true");
    await expect(
      canvasElement.querySelector('[data-slot="phone-bar-title"]')
    ).not.toBeVisible();
    await expectNoHorizontalOverflow(canvasElement);
    await editTitle(context, longSelectedTitle);
    await userEvent.click(panels.getByRole("button", { name: "Conversation" }));
    await waitFor(() =>
      expect(canvas.getByText(conversationReply)).toBeVisible()
    );
    await expectSelectedContextFits(context);
    await userEvent.click(panels.getByRole("button", { name: "Canvas" }));
    await expectNoHorizontalOverflow(canvasElement);
  },
};

export const PhoneSavedCanvas: Story = {
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

export const DashboardLibrary: Story = {
  parameters: { appPath: "/dashboards" },
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByRole("heading", { name: "Morning overview" })
    ).toBeVisible();
    await expect(
      canvas.getByRole("link", { name: "Morning overview" })
    ).toHaveAttribute("href", dashboardPath);
    const remove = canvas.getByRole("button", {
      name: "Delete Morning overview",
    });
    await expect(remove.getBoundingClientRect().height).toBe(44);
  },
};

export const DashboardLibraryLoading: Story = {
  parameters: { appPath: "/dashboards" },
  beforeEach({ msw }) {
    msw.use(
      http.get("/v1/dashboards", async ({ request }) => {
        const aborted = Promise.withResolvers<undefined>();
        request.signal.addEventListener(
          "abort",
          () => aborted.resolve(undefined),
          { once: true }
        );
        await aborted.promise;
        return HttpResponse.error();
      })
    );
  },
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByText("Loading dashboards…")
    ).toBeInTheDocument();
    await expect(
      canvas.queryByRole("heading", {
        name: "Your first dashboard starts with a question.",
      })
    ).not.toBeInTheDocument();
  },
};

export const DashboardLibraryLoadFailure: Story = {
  parameters: { appPath: "/dashboards" },
  beforeEach({ msw }) {
    msw.use(
      http.get("/v1/dashboards", () =>
        HttpResponse.json({ detail: "Core is unavailable" }, { status: 503 })
      )
    );
  },
  play: async ({ canvas }) => {
    const alert = within(await canvas.findByRole("alert"));
    await expect(alert.getByRole("button", { name: "Reload" })).toBeVisible();
    await expect(
      canvas.queryByRole("heading", {
        name: "Your first dashboard starts with a question.",
      })
    ).not.toBeInTheDocument();
  },
};

export const EmptyDashboardLibrary: Story = {
  parameters: {
    appPath: "/dashboards",
    applicationScenario: { dashboards: "empty" },
  },
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByRole("heading", {
        name: "Your first dashboard starts with a question.",
      })
    ).toBeVisible();
    await expect(
      canvas.getByRole("link", { name: "Start a conversation →" })
    ).toHaveAttribute("href", "/");
  },
};

export const PhoneDashboardLibrary: Story = {
  parameters: { appPath: "/dashboards" },
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async (context) => {
    const { play } = DashboardLibrary;
    if (!play) {
      throw new Error("Expected the DashboardLibrary play function");
    }
    await play(context);
    await expectNoHorizontalOverflow(context.canvasElement);
  },
};

export const DeleteDashboard: Story = {
  parameters: { appPath: "/dashboards" },
  play: async ({ canvas, canvasElement, userEvent }) => {
    const remove = await canvas.findByRole("button", {
      name: "Delete Morning overview",
    });
    await userEvent.click(remove);
    const body = within(canvasElement.ownerDocument.body);
    const dialog = within(
      body.getByRole("dialog", { name: "Remove this dashboard?" })
    );
    await expect(
      dialog.getByText(/Your home data and conversation remain unchanged/u)
    ).toBeVisible();
    await userEvent.click(dialog.getByRole("button", { name: "Cancel" }));
    await expect(remove).toHaveFocus();
    await userEvent.click(remove);
    await userEvent.click(
      body.getByRole("button", { name: "Delete dashboard" })
    );
    await expect(
      await canvas.findByRole("heading", {
        name: "Your first dashboard starts with a question.",
      })
    ).toBeVisible();
  },
};

export const PhoneDeleteConfirmation: Story = {
  parameters: { appPath: "/dashboards" },
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await userEvent.click(
      await canvas.findByRole("button", { name: "Delete Morning overview" })
    );
    const dialog = within(
      within(canvasElement.ownerDocument.body).getByRole("dialog", {
        name: "Remove this dashboard?",
      })
    );
    await expect(dialog.getByRole("button", { name: "Cancel" })).toHaveFocus();
    await expect(
      dialog.getByText(/Your home data and conversation remain unchanged/u)
    ).toBeVisible();
    await expectNoHorizontalOverflow(canvasElement);
  },
};

export const SearchConversations: Story = {
  parameters: { applicationScenario: { conversations: "searchable" } },
  play: async ({ canvas, userEvent }) => {
    const search = await canvas.findByRole("searchbox", {
      name: "Search conversations",
    });
    await userEvent.type(search, "garden");
    await expect(
      await canvas.findByText("No conversations match “garden”.")
    ).toBeVisible();
    await userEvent.click(canvas.getByRole("button", { name: "Clear search" }));
    await expect(search).toHaveFocus();
    await expect(
      canvas.getByRole("link", { name: conversationPrompt })
    ).toBeVisible();
    await userEvent.type(search, "garden");
    await expect(
      within(canvas.getByRole("region", { name: "Recent" })).getByRole("status")
    ).toHaveTextContent("No conversations match “garden”.");
  },
};

export const PendingView: Story = {
  parameters: { applicationScenario: { turn: "pending-view" } },
  play: async (context) => {
    await askTemperature(context);
    await expect(
      await context.canvas.findByRole("heading", { name: "Preparing a view" })
    ).toBeVisible();
    await expect(
      context.canvas.getByRole("textbox", { name: "Ask Hearth" })
    ).toBeDisabled();
  },
};

export const SelectedViewMessage: Story = {
  parameters: { appPath: conversationPath },
  play: async (context) => {
    await context.canvas.findByText(conversationReply);
    await context.userEvent.click(
      context.canvas.getByRole("button", { name: "Open in canvas" })
    );
    await editTitle(context, longSelectedTitle);
    await expectSelectedContextFits(context);
    await askTemperature(context);
    // History reconciliation replaces the streamed message after the reply arrives.
    await waitFor(() => {
      expect(
        context.canvas.getByRole("textbox", { name: "Ask Hearth" })
      ).toBeEnabled();
      expect(context.canvas.getByText(sentReply)).toBeVisible();
      expect(
        context.canvas.getByText(`Selected view: ${longSelectedTitle}`)
      ).toBeVisible();
    });
  },
};

export const InvalidConversationDraft: Story = {
  parameters: { appPath: conversationPath },
  play: async (context) => {
    await context.canvas.findByText(conversationReply);
    await context.userEvent.click(
      context.canvas.getByRole("button", { name: "Open in canvas" })
    );
    await editTitle(context, "");
    await expect(
      context.canvas.getByText(
        "Fix or discard invalid draft edits before sending"
      )
    ).toBeVisible();
    await expect(
      context.canvas.getByRole("button", { name: "Send message" })
    ).toBeDisabled();
  },
};

export const ReadingOlderMessages: Story = {
  parameters: {
    appPath: conversationPath,
    applicationScenario: { messages: "long" },
  },
  play: async ({ canvas }) => {
    await canvas.findByText(
      "Room 15 is comfortable. The temperature is 21.5 °C and the lamp is on."
    );
    const log = canvas.getByRole("log");
    await waitFor(() => expect(log.scrollTop).toBeGreaterThan(0));
    log.scrollTo({ top: 0, behavior: "instant" });
    const jump = await canvas.findByRole("button", { name: "Jump to latest" });
    await expect(jump).toBeVisible();
    await expect(getComputedStyle(jump).backgroundColor).toBe(
      "rgb(12, 12, 12)"
    );
  },
};

export const EmptyRecent: Story = {
  parameters: { applicationScenario: { conversations: "empty" } },
  play: async ({ canvas }) => {
    const recent = within(
      await canvas.findByRole("region", { name: "Recent" })
    );
    await expect(
      await recent.findByText("Conversations you start appear here.")
    ).toBeVisible();
    await expect(
      canvas.getByRole("textbox", { name: "Ask Hearth" })
    ).toBeEnabled();
  },
};

export const RecentListFailure: Story = {
  parameters: { applicationScenario: { conversations: "error" } },
  play: async ({ canvas }) => {
    const recent = within(
      await canvas.findByRole("region", { name: "Recent" })
    );
    await expect(await recent.findByRole("alert")).toHaveTextContent(
      "Could not load conversations."
    );
    const retry = recent.getByRole("button", { name: "Retry" });
    await expect(retry).toBeEnabled();
    await expect(getComputedStyle(retry).height).toBe("44px");
  },
};

export const PhoneHistorySearch: Story = {
  parameters: { applicationScenario: { conversations: "searchable" } },
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await userEvent.click(
      await canvas.findByRole("button", { name: "Open navigation" })
    );
    const recent = within(canvas.getByRole("region", { name: "Recent" }));
    const search = await recent.findByRole("searchbox", {
      name: "Search conversations",
    });
    const navigation = canvas.getByRole("navigation", {
      name: "Main navigation",
    });
    const drawer = within(
      canvas.getByRole("complementary", { name: "Hearth navigation" })
    );
    const newConversation = drawer.getByRole("button", {
      name: "New conversation",
    });
    const serverStatus = canvasElement.querySelector(
      '[data-slot="server-status"]'
    );
    const brand = canvasElement.querySelector('[data-slot="sidebar-brand"]');
    await waitFor(() => expect(navigation).toBeVisible());
    await expect(newConversation).toBeVisible();
    await expect(serverStatus).toBeVisible();
    await expect(brand).toBeVisible();
    await userEvent.click(search);
    await expect(navigation).not.toBeVisible();
    await expect(newConversation).not.toBeVisible();
    await expect(serverStatus).not.toBeVisible();
    await expect(brand).not.toBeVisible();
    await userEvent.type(search, "garden");
    await expect(await recent.findByRole("status")).toHaveTextContent(
      "No conversations match “garden”."
    );
    await userEvent.click(recent.getByRole("heading", { name: "Recent" }));
    await expect(search).not.toHaveFocus();
    await expect(search).toHaveValue("garden");
    await expect(navigation).not.toBeVisible();
    await expect(serverStatus).not.toBeVisible();
    await expect(brand).not.toBeVisible();
    await expect(
      recent.getByText("No conversations match “garden”.")
    ).toBeVisible();
    await expectNoHorizontalOverflow(canvasElement);
  },
};

export const RichConversation: Story = {
  parameters: {
    appPath: conversationPath,
    applicationScenario: { messages: "rich" },
  },
  play: async ({ canvas, userEvent }) => {
    const log = within(await canvas.findByRole("log"));
    await expect(await log.findByText("Room readings")).toBeVisible();
    await expect(log.getByRole("table")).toBeVisible();
    await expect(log.getByText('{"temperature":21}')).toBeVisible();
    await expect(log.getByText("Kitchen: 21 °C")).toBeVisible();
    await expect(
      log.getByText(
        "This artifact cannot be shown in this Hearth version. The original tool result is retained."
      )
    ).toBeVisible();
    await userEvent.click(log.getByText("Original artifact"));
    await expect(log.getByText(/hearth.future/u)).toBeVisible();
  },
};

export const StarterQuestion: Story = {
  play: async (context) => {
    await expect(
      await context.canvas.findByRole("textbox", { name: "Ask Hearth" })
    ).toBeEnabled();
    await context.userEvent.click(
      context.canvas.getByRole("button", { name: "Show me the temperatures" })
    );
    await expect(await context.canvas.findByText(sentReply)).toBeVisible();
    await expect(
      within(context.canvas.getByRole("log")).getByText(
        "Show me the temperatures throughout the house."
      )
    ).toBeVisible();
  },
};

export const TabletConversationCanvas: Story = {
  parameters: { appPath: conversationPath },
  globals: { viewport: { value: "tablet", isRotated: false } },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await expect(await canvas.findByText(conversationReply)).toBeVisible();
    await userEvent.click(
      canvas.getByRole("button", { name: "Open in canvas" })
    );
    const view = within(
      await canvas.findByRole("region", { name: "View canvas" })
    );
    await expect(
      await view.findByRole("heading", { name: "Morning overview" })
    ).toBeVisible();
    await expect(
      await view.findByRole("switch", { name: "Hallway lamp power" })
    ).toHaveAttribute("aria-checked", "true");
    await expectNoHorizontalOverflow(canvasElement);
  },
};

const queueDuringReply = async ({ canvas, userEvent }: InteractionContext) => {
  await expect(
    await canvas.findByRole("button", { name: "Show me the temperatures" })
  ).toBeEnabled();
  await userEvent.click(
    canvas.getByRole("button", { name: "Show me the temperatures" })
  );
  await userEvent.click(
    await canvas.findByRole("link", { name: "Empty conversation" })
  );
  const prompt = await canvas.findByRole("textbox", { name: "Ask Hearth" });
  await waitFor(() => expect(prompt).toBeEnabled());
  await userEvent.type(prompt, "Check the hallway.");
  await userEvent.click(canvas.getByRole("button", { name: "Send message" }));
  await expect(
    await canvas.findByText("Message queued until the current reply finishes.")
  ).toBeVisible();
  await expect(
    canvas.getByRole("button", { name: "Cancel queued message" })
  ).toBeEnabled();
};

export const QueuedDuringReply: Story = {
  parameters: { applicationScenario: { turn: "queue-collision" } },
  play: queueDuringReply,
};

export const CancelQueuedMessage: Story = {
  parameters: { applicationScenario: { turn: "queue-collision" } },
  play: async (context) => {
    await queueDuringReply(context);
    await context.userEvent.click(
      context.canvas.getByRole("button", { name: "Cancel queued message" })
    );
    await waitFor(() =>
      expect(
        context.canvas.queryByText(
          "Message queued until the current reply finishes."
        )
      ).not.toBeInTheDocument()
    );
    await expect(
      context.canvas.getByText("Looking through your home…")
    ).toBeVisible();
  },
};

export const QueuedAfterFailedReply: Story = {
  beforeEach({ msw, parameters }) {
    const { promise: queuedTurnFailure, resolve } =
      Promise.withResolvers<undefined>();
    const release = () => {
      resolve(undefined);
    };
    parameters.releaseQueuedTurn = release;
    msw.use(
      ...createApplicationHandlers({
        turn: "queue-collision",
        queuedTurnFailure,
      })
    );
    return () => {
      delete parameters.releaseQueuedTurn;
    };
  },
  play: async (context) => {
    await queueDuringReply(context);
    context.parameters.releaseQueuedTurn();
    await expect(await context.canvas.findByRole("alert")).toHaveTextContent(
      "agent turn failed"
    );
    await expect(
      await context.canvas.findByText("Queued message has not been sent.")
    ).toBeVisible();
    await expect(
      await context.canvas.findByText("Used 1 tool · 1 pending or unknown")
    ).toBeVisible();
    await expect(
      context.canvas.getByRole("button", { name: "Send queued message" })
    ).toBeDisabled();
    await expect(
      context.canvas.getByRole("button", { name: "Discard failed prompt" })
    ).toBeEnabled();
    await expect(
      context.canvas.getByRole("button", { name: "Cancel queued message" })
    ).toBeEnabled();
    await expect(
      context.canvas.getByRole("textbox", { name: "Ask Hearth" })
    ).toHaveValue("Check the hallway.");
  },
};

export const AutomationCreatePreview: Story = {
  parameters: {
    appPath: conversationPath,
    applicationScenario: { messages: "automation-create" },
  },
  play: async ({ canvas }) => {
    const log = within(await canvas.findByRole("log"));
    await expect(
      await log.findByText("Unsaved proposal · not created")
    ).toBeVisible();
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
    await expect(
      log.getByRole("button", { name: "Open in canvas" })
    ).toBeEnabled();
  },
};

export const AutomationShowingInCanvas: Story = {
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
    await expect(
      await canvas.findByRole("region", { name: "Automation canvas" })
    ).toBeVisible();
  },
};

export const AutomationReplacePreview: Story = {
  parameters: {
    appPath: conversationPath,
    applicationScenario: { messages: "automation-replace" },
  },
  play: async ({ canvas }) => {
    const log = within(await canvas.findByRole("log"));
    await expect(
      await log.findByText("Replacement proposal · based on revision 3")
    ).toBeVisible();
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
  },
};

export const AutomationReferencePreview: Story = {
  parameters: {
    appPath: conversationPath,
    applicationScenario: { messages: "automation-reference" },
  },
  play: async ({ canvas }) => {
    const log = within(await canvas.findByRole("log"));
    await expect(
      await log.findByText("Saved reference · current definition")
    ).toBeVisible();
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
  },
};

export const AutomationMissingPreview: Story = {
  parameters: {
    appPath: conversationPath,
    applicationScenario: { messages: "automation-missing" },
  },
  play: async ({ canvas }) => {
    const log = within(await canvas.findByRole("log"));
    await expect(await log.findByRole("alert")).toHaveTextContent(
      "This Automation no longer exists. Its earlier tool result is retained below."
    );
    await expect(
      log.getByRole("button", { name: "Open in canvas" })
    ).toBeEnabled();
  },
};

export const AutomationLoadingPreview: Story = {
  parameters: {
    appPath: conversationPath,
    applicationScenario: { messages: "automation-loading" },
  },
  play: async ({ canvas }) => {
    const log = within(await canvas.findByRole("log"));
    await expect(await log.findByText("Loading Automation…")).toBeVisible();
    await expect(
      log.getByText("Saved reference · current definition")
    ).toBeVisible();
  },
};

export const ShortPhoneConversation: Story = {
  parameters: {
    appPath: conversationPath,
    viewport: {
      options: {
        shortPhone: {
          name: "Short phone",
          styles: { width: "390px", height: "540px" },
        },
      },
    },
  },
  globals: { viewport: { value: "shortPhone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    await expect(await canvas.findByText(conversationReply)).toBeVisible();
    await expect(
      canvas.getByRole("textbox", { name: "Ask Hearth" })
    ).toBeEnabled();
    await expect(window.innerHeight).toBe(540);
    await expectNoHorizontalOverflow(canvasElement);
  },
};
