import type { Meta, StoryObj } from "@storybook/react-vite";
import { useRef } from "react";
import type { ComponentProps } from "react";
import { expect, fn, waitFor, within } from "storybook/test";

import {
  conversationPrompt,
  createApplicationHandlers,
} from "../../../.storybook/application-fixtures";
import { expectNoHorizontalOverflow } from "../../../.storybook/play";
import { cn } from "../../lib/utils";
import { ConversationsProvider } from "../conversation/conversations-provider";
import { HouseholdSidebar } from "./sidebar";

/** HouseholdSidebar attaches `historyButton` to a mounted element; give each render its own ref so that element never sits in story args. */
const SidebarWithOwnRef = (props: ComponentProps<typeof HouseholdSidebar>) => {
  const historyButton = useRef<HTMLButtonElement>(null);
  return <HouseholdSidebar {...props} historyButton={historyButton} />;
};

const meta = {
  title: "Components/HouseholdSidebar",
  component: HouseholdSidebar,
  tags: ["ai-generated"],
  args: {
    nav: "full",
    drawerOpen: false,
    home: true,
    historyButton: { current: null },
    onCloseDrawer: fn(),
    onNavigate: fn(),
    onNewConversation: fn(),
    onToggleRecent: fn(),
    setServer: fn(),
  },
  argTypes: { historyButton: { control: false } },
  render: (args) => <SidebarWithOwnRef {...args} />,
  decorators: [
    // The shell places the sidebar in its first grid column at the viewport's left edge.
    (Story, { args }) => (
      <ConversationsProvider>
        <div
          className={cn(
            "fixed inset-y-0 left-0 grid w-[300px]",
            args.nav === "rail" && "w-[104px]"
          )}
        >
          <Story />
        </div>
      </ConversationsProvider>
    ),
  ],
  parameters: {
    docs: {
      description: {
        component:
          "The household sidebar in its three forms: full width, a 104px rail whose Recent list opens as a drawer, and the phone drawer. `HouseholdShell` owns the drawer state and focus; the sidebar reports actions through its callbacks.",
      },
    },
  },
  beforeEach({ msw }) {
    msw.use(...createApplicationHandlers());
  },
} satisfies Meta<typeof HouseholdSidebar>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Full: Story = {
  play: async ({ args, canvas, userEvent }) => {
    const sidebar = within(
      canvas.getByRole("complementary", { name: "Hearth navigation" })
    );
    await expect(
      await sidebar.findByRole("link", { name: conversationPrompt })
    ).toBeVisible();
    await expect(
      await sidebar.findByText("Ready", { exact: true })
    ).toBeVisible();
    await waitFor(() => expect(args.setServer).toHaveBeenCalledWith("ready"));
    await expect(sidebar.getByRole("link", { name: "Home" })).toHaveAttribute(
      "aria-current",
      "page"
    );
    await userEvent.click(
      sidebar.getByRole("button", { name: "New conversation" })
    );
    await expect(args.onNewConversation).toHaveBeenCalledOnce();
  },
};

export const Rail: Story = {
  args: { nav: "rail" },
  play: async ({ args, canvas, userEvent }) => {
    const recent = await canvas.findByRole("button", {
      name: "Recent conversation history",
    });
    await expect(recent).toHaveAttribute("aria-expanded", "false");
    await expect(
      canvas.queryByRole("region", { name: "Recent" })
    ).not.toBeInTheDocument();
    await userEvent.click(recent);
    await expect(args.onToggleRecent).toHaveBeenCalledOnce();
  },
};

export const RailRecentDrawer: Story = {
  args: { nav: "rail", drawerOpen: true },
  play: async ({ args, canvas, userEvent }) => {
    await expect(
      canvas.getByRole("button", { name: "Recent conversation history" })
    ).toHaveAttribute("aria-expanded", "true");
    const recent = within(canvas.getByRole("region", { name: "Recent" }));
    await waitFor(async () =>
      expect(
        await recent.findByRole("link", { name: conversationPrompt })
      ).toBeVisible()
    );
    await userEvent.click(
      recent.getByRole("button", { name: "Close recent conversations" })
    );
    await expect(args.onCloseDrawer).toHaveBeenCalledOnce();
  },
};

export const PhoneDrawer: Story = {
  args: { nav: "bar", drawerOpen: true },
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ args, canvas, canvasElement, userEvent }) => {
    const sidebar = within(
      canvas.getByRole("complementary", { name: "Hearth navigation" })
    );
    await waitFor(async () =>
      expect(
        await sidebar.findByRole("link", { name: conversationPrompt })
      ).toBeVisible()
    );
    await userEvent.click(
      sidebar.getByRole("link", { name: conversationPrompt })
    );
    await expect(args.onNavigate).toHaveBeenCalledOnce();
    await userEvent.click(
      sidebar.getByRole("button", { name: "Close navigation" })
    );
    await expect(args.onCloseDrawer).toHaveBeenCalledOnce();
    await expectNoHorizontalOverflow(canvasElement);
  },
};
