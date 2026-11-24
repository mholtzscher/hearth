import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, waitFor, within } from "storybook/test";

import { animationNames } from "../../../.storybook/application";
import {
  conversationHistory,
  conversationPrompt,
  conversationReply,
  createApplicationHandlers,
  fixtureTurn,
  sentReply,
} from "../../../.storybook/application-fixtures";
import { FixtureConversationThread } from "../../../.storybook/conversation";
import { createFixtureConversation } from "../../../.storybook/fixture-conversation";
import type { FixtureConversation } from "../../../.storybook/fixture-conversation";
import { prefersReducedMotion } from "../../lib/motion";
import { ConversationThread } from "./conversation-thread";

const meta = {
  title: "Components/ConversationThread",
  component: ConversationThread,
  tags: ["ai-generated"],
  args: {
    state: {
      loading: false,
      empty: true,
      busy: false,
      error: "",
      failedPrompt: "",
      needsRecovery: false,
      queued: null,
      saving: false,
      editorValid: true,
    },
    actions: {
      onSendQueued: fn(),
      onDiscardFailed: fn(),
      onCancelQueued: fn(),
      onRetryHistory: fn(),
    },
    display: {
      select: fn(),
      openId: null,
      visible: true,
      threadKey: "story-conversation",
      messageCount: 0,
      canvasOpen: false,
      hidden: false,
    },
    composer: {
      // FixtureConversationThread attaches its own ref.
      prompt: { current: null },
      automationName: undefined,
      viewTitle: undefined,
      onShow: fn(),
      onCloseDraft: fn(),
    },
  },
  argTypes: { composer: { control: false } },
  decorators: [
    (Story) => (
      <div className="flex h-[640px] flex-col">
        <Story />
      </div>
    ),
  ],
  parameters: {
    docs: {
      description: {
        component:
          "The conversation log and composer, over a story-only fixture conversation (`.storybook/fixture-conversation.ts`) instead of a ConversationSession. Session state comes from `parameters.conversation`; a play function sends through the composer and applies the same turn events Core streams. Views and Automations still read synthetic Core HTTP.",
      },
    },
  },
  loaders: [
    ({ parameters }) => ({
      conversation: createFixtureConversation(parameters.conversation),
    }),
  ],
  beforeEach({ msw, parameters }) {
    msw.use(...createApplicationHandlers(parameters.applicationScenario));
  },
  render: (args, { loaded }) => (
    <FixtureConversationThread conversation={loaded.conversation} {...args} />
  ),
} satisfies Meta<typeof ConversationThread>;
export default meta;
type Story = StoryObj<typeof meta>;
type StoryContext = Parameters<NonNullable<Story["play"]>>[0];

const prompt = "What is the living room temperature?";
const loaded = { conversation: { history: conversationHistory() } };

/** Send through the composer, as the household does; the fixture starts the turn. */
const ask = async ({ canvas, userEvent }: StoryContext) => {
  const textbox = await canvas.findByRole("textbox", { name: "Ask Hearth" });
  await waitFor(() => expect(textbox).toBeEnabled());
  await userEvent.click(textbox);
  await userEvent.paste(prompt);
  await userEvent.click(canvas.getByRole("button", { name: "Send message" }));
};

const conversationOf = (context: StoryContext): FixtureConversation =>
  context.loaded.conversation;

/** Send, then apply the turn's opening events. */
const startTurn = async (
  context: StoryContext,
  turn: Parameters<typeof fixtureTurn>[0]
) => {
  await ask(context);
  const script = fixtureTurn(turn, [], { text: prompt });
  conversationOf(context).apply(script.events);
  return script;
};

const outcomeOf = (script: ReturnType<typeof fixtureTurn>) => {
  if (!script.outcome) {
    throw new Error("Expected a turn that completes");
  }
  return script.outcome;
};

export const LoadedConversation: Story = {
  parameters: loaded,
  play: async ({ args, canvas, userEvent }) => {
    await expect(await canvas.findByText(conversationReply)).toBeVisible();
    await userEvent.click(canvas.getByText("Used 1 tool", { exact: true }));
    await expect(canvas.getByText("compose_view · complete")).toBeVisible();
    const open = canvas.getByRole("button", { name: "Open in canvas" });
    await expect(open).toBeVisible();
    await expect(
      within(canvas.getByRole("log")).getByText(conversationPrompt)
    ).toBeVisible();
    await expect(
      await canvas.findByRole("switch", { name: "Hallway lamp power" })
    ).toHaveAttribute("aria-checked", "true");
    await userEvent.click(open);
    await expect(args.display.select).toHaveBeenCalledOnce();
  },
};

export const LegacyViewConversation: Story = {
  parameters: { conversation: { history: conversationHistory("legacy-view") } },
  play: async ({ canvas, userEvent }) => {
    await expect(await canvas.findByText(conversationReply)).toBeVisible();
    await userEvent.click(canvas.getByText("Used 1 tool", { exact: true }));
    await expect(canvas.getByText("present_view · complete")).toBeVisible();
    await expect(
      canvas.getByRole("button", { name: "Open in canvas" })
    ).toBeVisible();
  },
};

export const RichConversation: Story = {
  parameters: { conversation: { history: conversationHistory("rich") } },
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

export const ReadingOlderMessages: Story = {
  parameters: { conversation: { history: conversationHistory("long") } },
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

export const HistoryFailure: Story = {
  parameters: {
    conversation: {
      error: "Conversation history unavailable",
      needsRecovery: true,
    },
  },
  play: async ({ args, canvas, userEvent }) => {
    const retry = await canvas.findByRole("button", {
      name: "Retry loading history",
    });
    await expect(retry).toBeVisible();
    await expect(canvas.getByRole("alert")).toHaveTextContent(
      "Conversation history unavailable"
    );
    await expect(
      canvas.getByRole("button", { name: "Send message" })
    ).toBeDisabled();
    await userEvent.click(retry);
    await expect(args.actions.onRetryHistory).toHaveBeenCalledOnce();
  },
};

export const PendingReply: Story = {
  play: async (context) => {
    await startTurn(context, "pending");
    const tools = await context.canvas.findByText(
      "Used 1 tool · 1 pending or unknown"
    );
    await waitFor(() => expect(tools).toBeVisible());
    // Pending words wait 600ms before they fade in.
    await waitFor(() =>
      expect(
        context.canvas.getByText("Looking through your home…")
      ).toBeVisible()
    );
    await expect(
      context.canvas.getByRole("textbox", { name: "Ask Hearth" })
    ).toBeDisabled();
  },
};

export const PendingView: Story = {
  play: async (context) => {
    await startTurn(context, "pending-view");
    const preparing = await context.canvas.findByRole("heading", {
      name: "Preparing a view",
    });
    await waitFor(() => expect(preparing).toBeVisible());
    await expect(
      context.canvas.getByRole("textbox", { name: "Ask Hearth" })
    ).toBeDisabled();
  },
};

export const AutomationCreatePreview: Story = {
  parameters: {
    applicationScenario: { messages: "automation-create" },
    conversation: { history: conversationHistory("automation-create") },
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

export const AutomationReplacePreview: Story = {
  parameters: {
    applicationScenario: { messages: "automation-replace" },
    conversation: { history: conversationHistory("automation-replace") },
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
    applicationScenario: { messages: "automation-reference" },
    conversation: { history: conversationHistory("automation-reference") },
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

/** The Quiet Load Rule: an opened conversation's history appears without motion. */
export const MotionOpenedConversation: Story = {
  name: "Motion · Opened conversation",
  tags: ["motion"],
  parameters: loaded,
  play: async ({ canvas }) => {
    await expect(await canvas.findByText(conversationReply)).toBeVisible();
    const names = animationNames(canvas.getByRole("log"));
    await expect(names).not.toContain("motion-arrive");
    await expect(names).not.toContain("motion-resolve");
  },
};

/** Copy confirms with the check growing in as the copy icon shrinks out. */
export const MotionCopy: Story = {
  name: "Motion · Copy",
  tags: ["motion"],
  parameters: loaded,
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
  play: async ({ canvas, userEvent }) => {
    await canvas.findByText(conversationReply);
    const copy = await canvas.findByRole("button", { name: "Copy response" });
    await userEvent.click(copy);
    await expect(
      await canvas.findByRole("button", { name: "Response copied" })
    ).toHaveTextContent("Copied");
  },
};

/**
 * A sent message arrives, the reply arrives with its Hearth label, and its
 * text resolves. The stored history replaces both without remounting them, so
 * their motion completes.
 */
export const MotionMessageArrival: Story = {
  name: "Motion · Message arrival",
  tags: ["motion"],
  play: async (context) => {
    const script = await startTurn(context, "reply");
    const outcome = outcomeOf(script);
    conversationOf(context).apply(outcome.results);
    const log = within(context.canvas.getByRole("log"));
    const reply = await log.findByText(sentReply);
    const sent = log.getByText(prompt);
    const reduced = prefersReducedMotion();
    await expect(
      animationNames(sent.closest("[data-reduced-motion]"))
    ).toContain(reduced ? "motion-fade" : "motion-arrive");
    await expect(
      animationNames(reply.closest('[data-slot="message-markdown"]'))
    ).toContain(reduced ? "motion-fade" : "motion-resolve");
    conversationOf(context).commit(outcome.history);
    await waitFor(() =>
      expect(
        context.canvas.queryByText("Looking through your home…")
      ).not.toBeInTheDocument()
    );
    // Still the arriving elements: the stored copies did not remount them.
    await expect(sent).toBeInTheDocument();
    await expect(reply).toBeInTheDocument();
  },
};

/** While a reply runs, the Hearth icon breathes and the pending words wait 600ms. */
export const MotionWorking: Story = {
  name: "Motion · Working",
  tags: ["motion"],
  play: async (context) => {
    await startTurn(context, "pending");
    await context.canvas.findByText("Used 1 tool · 1 pending or unknown");
    const icon = context.canvasElement.querySelector(
      '[data-slot="reply-icon"]'
    );
    await expect(animationNames(icon)).toStrictEqual(
      prefersReducedMotion() ? [] : ["view-pending"]
    );
    const words = context.canvas.getByText("Looking through your home…");
    await expect(animationNames(words)).toContain("motion-fade");
    await waitFor(() => expect(words).toBeVisible());
  },
};

/** A pending tool's dashed icon turns while the reply runs, and the activity grows open. */
export const MotionToolActivity: Story = {
  name: "Motion · Tool activity",
  tags: ["motion"],
  play: async (context) => {
    await startTurn(context, "pending");
    const summary = await context.canvas.findByText(
      "Used 1 tool · 1 pending or unknown"
    );
    const group = summary.closest("details");
    await expect(
      animationNames(group?.querySelector("summary svg"))
    ).toStrictEqual(prefersReducedMotion() ? [] : ["motion-turn"]);
    await context.userEvent.click(summary);
    await expect(group).toHaveAttribute("open");
    await waitFor(() =>
      expect(
        context.canvas.getByText("get_entity · outcome unknown or pending")
      ).toBeVisible()
    );
  },
};

/** The same tool goes from waiting to confirmed in the live reply. */
export const MotionToolComplete: Story = {
  name: "Motion · Tool completes",
  tags: ["motion"],
  play: async (context) => {
    const script = await startTurn(context, "tool-complete");
    const waiting = await context.canvas.findByText(
      "Used 1 tool · 1 pending or unknown"
    );
    const pendingIcon = waiting
      .closest("summary")
      ?.querySelector('[data-slot="tool-pending-icon"]');
    await waitFor(() => expect(pendingIcon).toBeVisible());
    conversationOf(context).apply(outcomeOf(script).results);
    const summary = await context.canvas.findByText("Used 1 tool", {
      exact: true,
    });
    const reduced = prefersReducedMotion();
    const completeIcon = summary
      .closest("summary")
      ?.querySelector('[data-slot="tool-complete-icon"]');
    await expect(animationNames(completeIcon)).toContain(
      reduced ? "motion-fade" : "motion-draw"
    );
    await expect(pendingIcon).toBeInTheDocument();
    await expect(
      pendingIcon
        ?.getAnimations()
        .flatMap((animation) =>
          animation instanceof CSSTransition
            ? [animation.transitionProperty]
            : []
        )
    ).toStrictEqual(reduced ? ["opacity"] : ["opacity", "scale"]);
  },
};

/** The pending placeholders hand off to the committed view in the same reply. */
export const MotionViewReady: Story = {
  name: "Motion · View ready",
  tags: ["motion"],
  play: async (context) => {
    const { canvas } = context;
    const script = await startTurn(context, "view-ready");
    const preparing = await canvas.findByRole("heading", {
      name: "Preparing a view",
    });
    // Core sends the view after the placeholder has appeared, not in the same frame.
    await waitFor(() => expect(preparing).toBeVisible());
    const outcome = outcomeOf(script);
    conversationOf(context).apply(outcome.results);
    const ready = await canvas.findByRole("heading", {
      name: "Morning overview",
    });
    await waitFor(() => expect(ready).toBeVisible());
    await expect(
      canvas.queryByRole("heading", { name: "Preparing a view" })
    ).not.toBeInTheDocument();
    conversationOf(context).commit(outcome.history);
    await expect(
      await canvas.findByRole("switch", { name: "Hallway lamp power" })
    ).toHaveAttribute("aria-checked", "true");
  },
};

/** Two batches in one reply can use the same provider call ID. */
export const MotionReusedViewCall: Story = {
  name: "Motion · Two views with a reused call ID",
  tags: ["motion"],
  play: async (context) => {
    const { canvas } = context;
    const script = await startTurn(context, "reused-view-call");
    const outcome = outcomeOf(script);
    const firstPending = await canvas.findByRole("heading", {
      name: "Preparing a view",
    });
    // Core spaces the batches out; let each placeholder appear before the next one.
    await waitFor(() => expect(firstPending).toBeVisible());
    conversationOf(context).apply(outcome.results);
    const morning = await canvas.findByRole("heading", {
      name: "Morning overview",
    });
    const secondPending = await canvas.findByRole("heading", {
      name: "Preparing a view",
    });
    await expect(secondPending).not.toBe(firstPending);
    await waitFor(() => expect(secondPending).toBeVisible());
    conversationOf(context).apply(outcome.followup ?? []);
    const evening = await canvas.findByRole("heading", {
      name: "Evening overview",
    });
    await waitFor(() => expect(evening).toBeVisible());
    await expect(await canvas.findByText(conversationReply)).toBeVisible();
    conversationOf(context).commit(outcome.history);
    await waitFor(() => expect(morning).toBeVisible());
    await expect(firstPending).not.toBeInTheDocument();
    await expect(secondPending).not.toBeInTheDocument();
  },
};
