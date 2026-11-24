import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";

import { createApplicationHandlers } from "../../../.storybook/application-fixtures";
import { ServerStatus } from "./server-status";

const meta = {
  title: "Components/ServerStatus",
  component: ServerStatus,
  tags: ["ai-generated"],
  args: { rail: false },
  parameters: {
    docs: {
      description: {
        component:
          "The sidebar's Hearth server readiness, polled from `/readyz`. It shows the last completed check while the next one runs.",
      },
    },
  },
  beforeEach({ msw, parameters }) {
    msw.use(...createApplicationHandlers(parameters.applicationScenario));
  },
} satisfies Meta<typeof ServerStatus>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Ready: Story = {
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByText("Ready", { exact: true })
    ).toBeVisible();
  },
};

export const NotReady: Story = {
  parameters: { applicationScenario: { server: "not-ready" } },
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByText("Not ready", { exact: true })
    ).toBeVisible();
  },
};

export const Unavailable: Story = {
  parameters: { applicationScenario: { server: "unavailable" } },
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByText("Status unavailable", { exact: true })
    ).toBeVisible();
  },
};
