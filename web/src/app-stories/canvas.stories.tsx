import type { Meta } from "@storybook/react-vite";
import { expect, waitFor, within } from "storybook/test";

import {
  applicationMeta,
  editTitle,
  expectSelectedContextFits,
  longSelectedTitle,
} from "../../.storybook/application";
import type { ApplicationStory } from "../../.storybook/application";
import {
  conversationPath,
  conversationReply,
  dashboardPath,
} from "../../.storybook/application-fixtures";
import { expectNoHorizontalOverflow } from "../../.storybook/play";
import App from "../app";

const meta = {
  title: "Application/Canvas",
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

export const PhoneCanvas: ApplicationStory = {
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

export const TabletConversationCanvas: ApplicationStory = {
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

export const ExpandedCanvas: ApplicationStory = {
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
export const ExpandedCanvasRetainsInvalidInput: ApplicationStory = {
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
export const ExpandedCanvasNestedSource: ApplicationStory = {
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

/** Leave the view in the reply so navigation to the story doesn't consume Travel. */
export const MotionCanvasTravel: ApplicationStory = {
  name: "Motion · Canvas travel",
  tags: ["motion"],
  parameters: {
    appPath: conversationPath,
    applicationScenario: { messages: "compact-view" },
    docs: {
      description: {
        story:
          "Select Open in canvas, then Close view to replay both directions.",
      },
    },
  },
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByRole("switch", { name: "Hallway lamp power" })
    ).toHaveAttribute("aria-checked", "true");
    await expect(
      await canvas.findByRole("button", { name: "Open in canvas" })
    ).toBeVisible();
  },
};

export const MotionPhoneCanvas: ApplicationStory = {
  ...PhoneCanvas,
  name: "Motion · Phone canvas without travel",
  tags: ["motion"],
};
