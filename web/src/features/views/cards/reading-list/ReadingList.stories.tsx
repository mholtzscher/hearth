import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { entityId } from "../../../../../.storybook/fixtures";
import { entityScenario } from "../../../../../.storybook/msw-handlers";
import { expectNoHorizontalOverflow } from "../../../../../.storybook/play";
import { BoundReadingList } from "./ReadingList";

const meta = {
  title: "features/views/cards/ReadingList",
  component: BoundReadingList,
  tags: ["ai-generated"],
  args: {
    title: "Rooms", rows: [
      { label: "Living room", source: { entity_id: entityId(15), entity_type: "hearth.temperature/v1" } },
      { label: "Outdoor humidity", source: { entity_id: entityId(102), entity_type: "hearth.relativehumidity/v1" } },
      { label: "Front door", source: { entity_id: entityId(1), entity_type: "hearth.binarysensor/v1" }, words: { true: "Open", false: "Closed" } },
    ],
  },
} satisfies Meta<typeof BoundReadingList>;
export default meta;
type Story = StoryObj<typeof meta>;

export const MixedReadings: Story = {
  play: async ({ canvas }) => { await expect(await canvas.findByText("21.5")).toBeVisible(); await expect(await canvas.findByText("Closed")).toBeVisible(); },
};
/** One failed row does not hide the readings that loaded. */
export const PartialFailure: Story = {
  beforeEach: ({ msw }) => { msw.use(entityScenario(15, "error")); },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Couldn't load this reading")).toBeVisible();
    await expect(await canvas.findByText("Closed")).toBeVisible();
  },
};
export const Phone: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    await canvas.findByText("21.5");
    await expectNoHorizontalOverflow(canvasElement);
  },
};
