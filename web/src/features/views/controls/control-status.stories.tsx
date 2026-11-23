import type { Meta, StoryObj } from "@storybook/react-vite";
import { delay, http, HttpResponse } from "msw";
import type { ComponentProps } from "react";
import { expect, waitFor } from "storybook/test";

import { entityId, fixtureEntity } from "../../../../.storybook/fixtures";
import { createCoreHandlers } from "../../../../.storybook/msw-handlers";
import { useObservedEntity } from "../../../../.storybook/use-observed-entity";
import { ControlStatus } from "./control-status";
import { PowerSwitch } from "./power-switch";

const WithSwitch = (args: ComponentProps<typeof ControlStatus>) => {
  const entity = useObservedEntity(fixtureEntity(4));
  if (!entity) {
    return null;
  }
  return (
    <>
      <PowerSwitch entity={entity} label="Office plug power" />
      <ControlStatus {...args} />
    </>
  );
};

const meta = {
  title: "Components/Controls/ControlStatus",
  component: ControlStatus,
  tags: ["ai-generated"],
  args: {
    entityId: entityId(4),
    subject: "power",
    idle: "Since 3:20 AM",
    pending: (requested) =>
      requested === true ? "Turning on…" : "Turning off…",
  },
  // The status line reads the Command queue, so each story pairs it with the switch that sends the Command.
  render: (args) => <WithSwitch {...args} />,
  parameters: {
    docs: {
      description: {
        component:
          "The reserved line under a control. Pending text is a polite status and a failure is an alert that stays until the next request.",
      },
    },
  },
} satisfies Meta<typeof ControlStatus>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Idle: Story = {
  play: async ({ canvas }) => {
    await expect(canvas.getByRole("status")).toHaveTextContent("Since 3:20 AM");
  },
};
export const MotionPending: Story = {
  name: "Motion · Pending dot",
  tags: ["motion"],
  beforeEach: ({ msw }) => {
    msw.use(...createCoreHandlers([], { commandDelayMs: 700 }));
  },
  play: async ({ canvas, canvasElement, userEvent }) => {
    const control = canvas.getByRole("switch");
    await userEvent.click(control);
    await expect(await canvas.findByText("Turning on…")).toBeVisible();
    const dot = canvasElement.querySelector(
      '[data-slot="pending-command-dot"]'
    );
    await expect(dot).toBeInTheDocument();
    if (dot) {
      await expect(getComputedStyle(dot).animationName).toBe(
        matchMedia("(prefers-reduced-motion: reduce)").matches
          ? "none"
          : "view-pending"
      );
    }
    await waitFor(() => expect(control).not.toHaveAttribute("aria-busy"));
    await expect(control).toHaveAttribute("aria-checked", "true");
    await expect(dot).not.toBeInTheDocument();
  },
};
export const Rejected: Story = {
  name: "Motion · Rejection status",
  tags: ["motion"],
  beforeEach: ({ msw }) => {
    msw.use(
      http.post("/v1/entities/:id/commands", async () => {
        await delay(700);
        return HttpResponse.json({ detail: "Rejected" }, { status: 502 });
      })
    );
  },
  play: async ({ canvas, userEvent }) => {
    await userEvent.click(canvas.getByRole("switch"));
    await expect(await canvas.findByRole("alert")).toHaveTextContent(
      "Change rejected. Try again."
    );
  },
};
