import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { entityId } from "../../../../../.storybook/fixtures";
import { entityScenario } from "../../../../../.storybook/msw-handlers";
import { expectNoHorizontalOverflow } from "../../../../../.storybook/play";
import { BoundWeekPattern } from "./WeekPattern";

const meta = {
  title: "features/views/cards/WeekPattern",
  component: BoundWeekPattern,
  tags: ["ai-generated"],
  args: {
    label: "Front door", source: { kind: "entity-state", entity_id: entityId(1), entity_type: "hearth.binarysensor/v1" },
    words: { true: "Open", false: "Closed" },
  },
} satisfies Meta<typeof BoundWeekPattern>;
export default meta;
type Story = StoryObj<typeof meta>;

export const RecordedWeek: Story = {
  play: async ({ canvas }) => {
    await expect(await canvas.findByRole("region", { name: "Front door, last 7 days" })).toHaveTextContent("Some hours have no history.");
  },
};
export const CurrentStateUnavailable: Story = {
  beforeEach: ({ msw }) => { msw.use(entityScenario(1, "unavailable")); },
  play: async ({ canvas }) => { await expect(await canvas.findByText("Unavailable")).toBeVisible(); },
};
export const Phone: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    await canvas.findByRole("region", { name: "Front door, last 7 days" });
    await expectNoHorizontalOverflow(canvasElement);
  },
};
