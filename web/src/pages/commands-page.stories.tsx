import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, waitFor } from "storybook/test";

import { entityId, fixtureNow } from "../../.storybook/fixtures";
import type { CommandRecord } from "../api/types";
import CommandsPage from "./commands-page";

const meta = {
  title: "Pages/CommandsPage",
  component: CommandsPage,
  tags: ["ai-generated"],
  parameters: {
    route: { path: "/commands", entry: "/commands" },
    docs: {
      description: {
        component:
          "The debug dashboard's Command history with status and Entity filters. It takes no props.",
      },
    },
  },
} satisfies Meta<typeof CommandsPage>;
export default meta;
type Story = StoryObj<typeof meta>;

/** The status filter keeps native selection and sends the selected status to Core. */
export const StatusFilter: Story = {
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
