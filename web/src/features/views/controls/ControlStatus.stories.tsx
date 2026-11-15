import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { delay, http, HttpResponse } from "msw";
import { entityId, fixtureEntity } from "../../../../.storybook/fixtures";
import { ControlStatus } from "./ControlStatus";
import { PowerSwitch } from "./PowerSwitch";
import { useObservedEntity } from "../../../../.storybook/use-observed-entity";

const meta = {
  title: "Components/Controls/ControlStatus",
  component: ControlStatus,
  tags: ["ai-generated"],
  args: {
    entityId: entityId(4), subject: "power", idle: "Since 3:20 AM",
    pending: requested => requested === true ? "Turning on…" : "Turning off…",
  },
  // The status line reads the Command queue, so each story pairs it with the switch that sends the Command.
  render: function WithSwitch(args) {
    const entity = useObservedEntity(fixtureEntity(4))!;
    return <>
      <PowerSwitch entity={entity} label="Office plug power" />
      <ControlStatus {...args} />
    </>;
  },
  parameters: { docs: { description: { component: "The reserved line under a control. Pending text is a polite status and a failure is an alert that stays until the next request." } } },
} satisfies Meta<typeof ControlStatus>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Idle: Story = {
  play: async ({ canvas }) => { await expect(canvas.getByRole("status")).toHaveTextContent("Since 3:20 AM"); },
};
export const Pending: Story = {
  beforeEach: ({ msw }) => { msw.use(http.post("/v1/entities/:id/commands", async () => { await delay("infinite"); return new HttpResponse(null); })); },
  play: async ({ canvas, userEvent }) => {
    await userEvent.click(canvas.getByRole("switch"));
    await expect(await canvas.findByText("Turning on…")).toBeVisible();
  },
};
export const Rejected: Story = {
  beforeEach: ({ msw }) => { msw.use(http.post("/v1/entities/:id/commands", () => HttpResponse.json({ detail: "Rejected" }, { status: 502 }))); },
  play: async ({ canvas, userEvent }) => {
    await userEvent.click(canvas.getByRole("switch"));
    await expect(await canvas.findByRole("alert")).toHaveTextContent("Change rejected. Try again.");
  },
};
