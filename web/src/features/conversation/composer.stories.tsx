import type { Meta, StoryObj } from "@storybook/react-vite";
import { useRef } from "react";
import type { ComponentProps } from "react";
import { expect, fn, within } from "storybook/test";

import { FixtureConversationRuntime } from "../../../.storybook/conversation";
import { createFixtureConversation } from "../../../.storybook/fixture-conversation";
import type { FixtureConversation } from "../../../.storybook/fixture-conversation";
import { ConversationComposer } from "./composer";

/** ConversationComposer attaches `prompt` to a mounted element; give each render its own ref so that element never sits in story args. */
const ComposerWithOwnRef = (
  props: ComponentProps<typeof ConversationComposer>
) => {
  const prompt = useRef<HTMLTextAreaElement>(null);
  return <ConversationComposer {...props} prompt={prompt} />;
};

const prompt = "What is the living room temperature?";

const meta = {
  title: "Components/ConversationComposer",
  component: ConversationComposer,
  tags: ["ai-generated"],
  args: {
    prompt: { current: null },
    busy: false,
    loading: false,
    failedPrompt: "",
    automationName: undefined,
    viewTitle: undefined,
    saving: false,
    editorValid: true,
    onShow: fn(),
    onCloseDraft: fn(),
  },
  argTypes: { prompt: { control: false } },
  render: (args) => <ComposerWithOwnRef {...args} />,
  decorators: [
    (Story, { args, loaded }) => (
      <FixtureConversationRuntime
        conversation={loaded.conversation}
        saving={args.saving}
        editorValid={args.editorValid}
      >
        <div className="max-w-[640px] [--pad:0px]">
          <Story />
        </div>
      </FixtureConversationRuntime>
    ),
  ],
  parameters: {
    docs: {
      description: {
        component:
          "The prompt field, the selected view or Automation that travels with the next message, and Send. It needs an assistant-ui runtime; these stories use the fixture conversation, so Send records the prompt instead of calling Core.",
      },
    },
  },
  loaders: [() => ({ conversation: createFixtureConversation() })],
} satisfies Meta<typeof ConversationComposer>;
export default meta;
type Story = StoryObj<typeof meta>;
type StoryContext = Parameters<NonNullable<Story["play"]>>[0];

const type = async ({ canvas, userEvent }: StoryContext) => {
  await userEvent.click(canvas.getByRole("textbox", { name: "Ask Hearth" }));
  await userEvent.paste(prompt);
};

export const Ready: Story = {
  play: async (context) => {
    const { canvas, userEvent } = context;
    const send = canvas.getByRole("button", { name: "Send message" });
    await expect(send).toBeDisabled();
    await type(context);
    await expect(send).toBeEnabled();
    await userEvent.click(send);
    const conversation: FixtureConversation = context.loaded.conversation;
    await expect(conversation.getSnapshot().optimistic?.text).toBe(prompt);
    await expect(
      canvas.getByRole("textbox", { name: "Ask Hearth" })
    ).toHaveValue("");
  },
};

export const SelectedView: Story = {
  args: { viewTitle: "Morning overview" },
  play: async ({ args, canvas, userEvent }) => {
    const selected = within(
      canvas.getByRole("group", { name: "Selected view" })
    );
    await userEvent.click(
      selected.getByRole("button", {
        name: "Show selected view: Morning overview",
      })
    );
    await expect(args.onShow).toHaveBeenCalledOnce();
    await userEvent.click(
      selected.getByRole("button", { name: "Close selected view" })
    );
    await expect(args.onCloseDraft).toHaveBeenCalledOnce();
  },
};

export const SelectedAutomation: Story = {
  args: { automationName: "Hallway light at dusk" },
  play: async ({ canvas }) => {
    const selected = within(
      canvas.getByRole("group", { name: "Selected Automation" })
    );
    await expect(
      selected.getByRole("button", {
        name: "Show selected Automation: Hallway light at dusk",
      })
    ).toBeVisible();
  },
};

/** A save in progress holds the message and keeps the selected view in place. */
export const SavingDraft: Story = {
  args: { viewTitle: "Morning overview", saving: true },
  play: async (context) => {
    await type(context);
    await expect(
      context.canvas.getByRole("button", { name: "Send message" })
    ).toBeDisabled();
    await expect(
      context.canvas.getByRole("button", { name: "Close selected view" })
    ).toBeDisabled();
  },
};

export const InvalidDraft: Story = {
  args: { viewTitle: "Morning overview", editorValid: false },
  play: async (context) => {
    await expect(
      context.canvas.getByText(
        "Fix or discard invalid draft edits before sending"
      )
    ).toBeVisible();
    await type(context);
    await expect(
      context.canvas.getByRole("button", { name: "Send message" })
    ).toBeDisabled();
  },
};

/** After a failed send, the preserved prompt is retried from the same button. */
export const FailedPrompt: Story = {
  args: { failedPrompt: prompt },
  play: async ({ canvas }) => {
    await expect(
      canvas.getByRole("button", { name: "Retry message" })
    ).toHaveTextContent("Retry");
  },
};

/** While a reply runs, the prompt field waits. */
export const Busy: Story = {
  args: { busy: true },
  play: async ({ canvas }) => {
    await expect(
      canvas.getByRole("textbox", { name: "Ask Hearth" })
    ).toBeDisabled();
  },
};
