import type { Meta } from "@storybook/react-vite";
import { expect, waitFor, within } from "storybook/test";

import {
  animationNames,
  applicationMeta,
  askTemperature,
} from "../../.storybook/application";
import type { ApplicationStory } from "../../.storybook/application";
import {
  conversationPath,
  conversationPrompt,
  conversationReply,
  sentReply,
} from "../../.storybook/application-fixtures";
import { expectNoHorizontalOverflow } from "../../.storybook/play";
import App from "../app";

const meta = {
  title: "Application/Conversation/Reading",
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

export const LoadedConversation: ApplicationStory = {
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

export const PhoneConversation: ApplicationStory = {
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

export const ShortPhoneConversation: ApplicationStory = {
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

export const RichConversation: ApplicationStory = {
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

export const ReadingOlderMessages: ApplicationStory = {
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

export const HistoryFailure: ApplicationStory = {
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

export const StarterQuestion: ApplicationStory = {
  play: async (context) => {
    await expect(
      await context.canvas.findByRole("textbox", { name: "Ask Hearth" })
    ).toBeEnabled();
    await context.userEvent.click(
      context.canvas.getByRole("button", { name: "Show me the temperatures" })
    );
    const reply = await context.canvas.findByText(sentReply);
    await waitFor(() => expect(reply).toBeVisible());
    await expect(
      within(context.canvas.getByRole("log")).getByText(
        "Show me the temperatures throughout the house."
      )
    ).toBeVisible();
  },
};

/** The Quiet Load Rule: an opened conversation's history appears without motion. */
export const MotionOpenedConversation: ApplicationStory = {
  name: "Motion · Opened conversation",
  tags: ["motion"],
  parameters: { appPath: conversationPath },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText(conversationReply)).toBeVisible();
    const names = animationNames(canvas.getByRole("log"));
    await expect(names).not.toContain("motion-arrive");
    await expect(names).not.toContain("motion-resolve");
  },
};

/** Copy confirms with the check growing in as the copy icon shrinks out. */
export const MotionCopy: ApplicationStory = {
  name: "Motion · Copy",
  tags: ["motion"],
  // The test browser grants no clipboard access; the story checks the confirmation only.
  beforeEach() {
    const clipboard = Object.getOwnPropertyDescriptor(navigator, "clipboard");
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: () => Promise.resolve() },
    });
    return () => {
      if (clipboard) {
        Object.defineProperty(navigator, "clipboard", clipboard);
      } else {
        Reflect.deleteProperty(navigator, "clipboard");
      }
    };
  },
  play: async (context) => {
    await askTemperature(context);
    await context.canvas.findByText(sentReply);
    const copy = await context.canvas.findByRole("button", {
      name: "Copy response",
    });
    await context.userEvent.click(copy);
    await expect(
      await context.canvas.findByRole("button", { name: "Response copied" })
    ).toHaveTextContent("Copied");
  },
};
