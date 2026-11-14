import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { entityId } from "../../../../../.storybook/fixtures";
import { entityScenario } from "../../../../../.storybook/msw-handlers";
import { BoundState } from "./State";

const meta = {
  component: BoundState,
  tags: ["ai-generated"],
  args: {
    label: "Front door", source: { entity_id: entityId(1), entity_type: "hearth.binarysensor/v1" },
    words: { true: "Open", false: "Closed" },
  },
  parameters: { docs: { description: { component: "Discrete observed State with household words. Availability and State remain distinct facts." } } },
} satisfies Meta<typeof BoundState>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Closed: Story = {
  play: async ({ canvas }) => { await expect(await canvas.findByText("Closed")).toBeVisible(); },
};
export const NeverObserved: Story = { beforeEach: ({ msw }) => { msw.use(entityScenario(1, "unobserved")); } };
export const Disabled: Story = { beforeEach: ({ msw }) => { msw.use(entityScenario(1, "disabled")); } };
