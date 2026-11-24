import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import {
  conversationPrompt,
  createApplicationHandlers,
} from "../../../.storybook/application-fixtures";
import { ConversationHistory } from "./conversation-history";
import { ConversationsProvider } from "./conversations-provider";

const meta = {
  title: "Components/ConversationHistory",
  component: ConversationHistory,
  tags: ["ai-generated"],
  args: { id: "conversation-history", open: false },
  decorators: [
    (Story) => (
      <ConversationsProvider>
        <div className="flex h-120 w-64 flex-col">
          <Story />
        </div>
      </ConversationsProvider>
    ),
  ],
  parameters: {
    docs: {
      description: {
        component:
          "The sidebar's Recent conversations, grouped by day. Past twelve conversations it adds a search field. It reads the list from `ConversationsProvider`.",
      },
    },
  },
  beforeEach({ msw, parameters }) {
    msw.use(...createApplicationHandlers(parameters.applicationScenario));
  },
} satisfies Meta<typeof ConversationHistory>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Search: Story = {
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

export const Empty: Story = {
  parameters: { applicationScenario: { conversations: "empty" } },
  play: async ({ canvas }) => {
    const recent = within(
      await canvas.findByRole("region", { name: "Recent" })
    );
    await expect(
      await recent.findByText("Conversations you start appear here.")
    ).toBeVisible();
  },
};

export const LoadFailure: Story = {
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
