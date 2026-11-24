import type { Meta } from "@storybook/react-vite";
import { expect, waitFor } from "storybook/test";

import {
  applicationMeta,
  askTemperature,
  editTitle,
  expectSelectedContextFits,
  longSelectedTitle,
  sendAndWaitForHistory,
} from "../../.storybook/application";
import type {
  ApplicationContext,
  ApplicationStory,
} from "../../.storybook/application";
import {
  conversationPath,
  conversationReply,
  createApplicationHandlers,
  sentReply,
} from "../../.storybook/application-fixtures";
import App from "../app";

const meta = {
  title: "Application/Conversation/Sending",
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

const queueDuringReply = async ({ canvas, userEvent }: ApplicationContext) => {
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

export const SendMessage: ApplicationStory = {
  play: async (context) => {
    await askTemperature(context);
    // A reply arrives over a few frames before it is fully visible.
    const reply = await context.canvas.findByText(sentReply);
    await waitFor(() => expect(reply).toBeVisible());
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

export const RetryMessage: ApplicationStory = {
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

export const InvalidConversationDraft: ApplicationStory = {
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

export const QueuedDuringReply: ApplicationStory = {
  parameters: { applicationScenario: { turn: "queue-collision" } },
  play: queueDuringReply,
};

export const CancelQueuedMessage: ApplicationStory = {
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
    await waitFor(() =>
      expect(
        context.canvas.getByText("Looking through your home…")
      ).toBeVisible()
    );
  },
};

export const QueuedAfterFailedReply: ApplicationStory = {
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

export const SelectedViewMessage: ApplicationStory = {
  parameters: { appPath: conversationPath },
  play: async (context) => {
    await context.canvas.findByText(conversationReply);
    await context.userEvent.click(
      context.canvas.getByRole("button", { name: "Open in canvas" })
    );
    await editTitle(context, longSelectedTitle);
    await expectSelectedContextFits(context);
    await sendAndWaitForHistory(context, () => askTemperature(context));
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

export const MotionReplyContinuity: ApplicationStory = {
  name: "Motion · Previous reply stays open",
  tags: ["motion"],
  parameters: { appPath: conversationPath },
  play: async (context) => {
    const { canvas, userEvent } = context;
    await canvas.findByText(conversationReply);
    await sendAndWaitForHistory(context, () => askTemperature(context));
    const prompt = canvas.getByRole("textbox", { name: "Ask Hearth" });
    await waitFor(() => expect(prompt).toBeEnabled());
    await expect(await canvas.findByText(sentReply)).toBeVisible();

    const [summary] = canvas
      .getAllByText("Used 1 tool", { exact: true })
      .slice(-1);
    const disclosure = summary.closest("details");
    await userEvent.click(summary);
    await expect(disclosure).toHaveAttribute("open");
    await sendAndWaitForHistory(context, async () => {
      await userEvent.click(prompt);
      await userEvent.paste("Check the hallway too.");
      await userEvent.click(
        canvas.getByRole("button", { name: "Send message" })
      );
    });
    await waitFor(() => expect(prompt).toBeEnabled());
    await expect(canvas.getByText("Check the hallway too.")).toBeVisible();
    await expect(disclosure).toBeInTheDocument();
    await expect(disclosure).toHaveAttribute("open");
  },
};
