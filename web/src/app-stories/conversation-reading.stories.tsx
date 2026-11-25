import type { Meta } from "@storybook/react-vite";
import { expect, waitFor, within } from "storybook/test";

import { applicationMeta } from "../../.storybook/application";
import type { ApplicationStory } from "../../.storybook/application";
import {
  conversationPath,
  conversationReply,
  generatedStarterQuestions,
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

export const StarterQuestion: ApplicationStory = {
  play: async (context) => {
    await expect(
      await context.canvas.findByRole("textbox", { name: "Ask Hearth" })
    ).toBeEnabled();
    await context.userEvent.click(
      await context.canvas.findByRole("button", {
        name: "Show me the temperatures",
      })
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

export const GeneratedStarterQuestions: ApplicationStory = {
  parameters: { applicationScenario: { starterQuestions: "generated" } },
  play: async (context) => {
    const [first] = generatedStarterQuestions;
    const starter = await context.canvas.findByRole("button", {
      name: first.title,
    });
    await expect(
      context.canvas.queryByRole("button", { name: "Show me the temperatures" })
    ).toBeNull();
    await context.userEvent.click(starter);
    const reply = await context.canvas.findByText(sentReply);
    await waitFor(() => expect(reply).toBeVisible());
    await expect(
      within(context.canvas.getByRole("log")).getByText(first.prompt)
    ).toBeVisible();
  },
};
