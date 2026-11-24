import type { Meta } from "@storybook/react-vite";
import { expect, waitFor } from "storybook/test";

import {
  animationNames,
  applicationMeta,
  askTemperature,
} from "../../.storybook/application";
import type { ApplicationStory } from "../../.storybook/application";
import { conversationReply } from "../../.storybook/application-fixtures";
import App from "../app";
import { prefersReducedMotion } from "../lib/motion";

const meta = {
  title: "Application/Conversation/Tools and views",
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

export const PendingView: ApplicationStory = {
  parameters: { applicationScenario: { turn: "pending-view" } },
  play: async (context) => {
    await askTemperature(context);
    const preparing = await context.canvas.findByRole("heading", {
      name: "Preparing a view",
    });
    await waitFor(() => expect(preparing).toBeVisible());
    await expect(
      context.canvas.getByRole("textbox", { name: "Ask Hearth" })
    ).toBeDisabled();
  },
};

/** A pending tool's dashed icon turns while the reply runs, and the activity grows open. */
export const MotionToolActivity: ApplicationStory = {
  name: "Motion · Tool activity",
  tags: ["motion"],
  parameters: { applicationScenario: { turn: "pending" } },
  play: async (context) => {
    await askTemperature(context);
    const summary = await context.canvas.findByText(
      "Used 1 tool · 1 pending or unknown"
    );
    const group = summary.closest("details");
    await expect(animationNames(group?.querySelector("summary svg"))).toEqual(
      prefersReducedMotion() ? [] : ["motion-turn"]
    );
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
export const MotionToolComplete: ApplicationStory = {
  name: "Motion · Tool completes",
  tags: ["motion"],
  parameters: { applicationScenario: { turn: "tool-complete" } },
  play: async (context) => {
    await askTemperature(context);
    const waiting = await context.canvas.findByText(
      "Used 1 tool · 1 pending or unknown"
    );
    const pendingIcon = waiting
      .closest("summary")
      ?.querySelector('[data-slot="tool-pending-icon"]');
    await waitFor(() => expect(pendingIcon).toBeVisible());
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
    ).toEqual(reduced ? ["opacity"] : ["opacity", "scale"]);
  },
};

/** The pending placeholders hand off to the committed view in the same reply. */
export const MotionViewReady: ApplicationStory = {
  name: "Motion · View ready",
  tags: ["motion"],
  parameters: { applicationScenario: { turn: "view-ready" } },
  play: async (context) => {
    await askTemperature(context);
    const { canvas } = context;
    await canvas.findByRole("heading", { name: "Preparing a view" });
    const ready = await canvas.findByRole("heading", {
      name: "Morning overview",
    });
    await waitFor(() => expect(ready).toBeVisible());
    await expect(
      canvas.queryByRole("heading", { name: "Preparing a view" })
    ).not.toBeInTheDocument();
    await expect(
      await canvas.findByRole("switch", { name: "Hallway lamp power" })
    ).toHaveAttribute("aria-checked", "true");
  },
};

/** Two batches in one reply can use the same provider call ID. */
export const MotionReusedViewCall: ApplicationStory = {
  name: "Motion · Two views with a reused call ID",
  tags: ["motion"],
  parameters: { applicationScenario: { turn: "reused-view-call" } },
  play: async (context) => {
    await askTemperature(context);
    const { canvas } = context;
    const firstPending = await canvas.findByRole("heading", {
      name: "Preparing a view",
    });
    const morning = await canvas.findByRole("heading", {
      name: "Morning overview",
    });
    const secondPending = await canvas.findByRole("heading", {
      name: "Preparing a view",
    });
    await expect(secondPending).not.toBe(firstPending);
    await expect(
      await canvas.findByRole("heading", {
        name: "Evening overview",
      })
    ).toBeVisible();
    await expect(await canvas.findByText(conversationReply)).toBeVisible();
    await expect(morning).toBeVisible();
    await expect(firstPending).not.toBeInTheDocument();
    await expect(secondPending).not.toBeInTheDocument();
  },
};
