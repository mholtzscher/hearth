import type { Meta } from "@storybook/react-vite";
import { expect, waitFor, within } from "storybook/test";

import { applicationMeta } from "../../.storybook/application";
import type { ApplicationStory } from "../../.storybook/application";
import {
  conversationPrompt,
  conversationReply,
} from "../../.storybook/application-fixtures";
import { expectNoHorizontalOverflow } from "../../.storybook/play";
import App from "../app";

const meta = {
  title: "Application/Navigation",
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

const expectSidebarNewTreatment = async (button: HTMLElement) => {
  const style = getComputedStyle(button);
  await expect(style.height).toBe("44px");
  await expect(style.borderRadius).toBe("8px");
  await expect(style.backgroundColor).toBe("rgb(12, 12, 12)");
  await expect(style.borderColor).toBe("rgb(31, 31, 31)");
};

export const DesktopHome: ApplicationStory = {
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

export const PhoneNavigation: ApplicationStory = {
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

export const TabletRecentDrawer: ApplicationStory = {
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

export const RecentConversation: ApplicationStory = {
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

export const ServerNotReady: ApplicationStory = {
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

export const PhoneHistorySearch: ApplicationStory = {
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

export const MotionTabletDrawer: ApplicationStory = {
  ...TabletRecentDrawer,
  name: "Motion · Tablet Recent drawer",
  tags: ["motion"],
};

export const MotionPhoneDrawer: ApplicationStory = {
  ...PhoneNavigation,
  name: "Motion · Phone navigation drawer",
  tags: ["motion"],
};
