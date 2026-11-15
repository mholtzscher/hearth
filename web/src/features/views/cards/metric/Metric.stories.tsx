import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { entityId } from "../../../../../.storybook/fixtures";
import { entityScenario } from "../../../../../.storybook/msw-handlers";
import { BoundMetric } from "./Metric";

const meta = {
  component: BoundMetric,
  tags: ["ai-generated"],
  args: {
    label: "Outdoor humidity", source: { entity_id: entityId(102), entity_type: "hearth.relativehumidity/v1" },
    trend: "24h", range: true,
  },
  parameters: { docs: { description: { component: "One quantity reading with optional trend and catalog-supported range. Never invent a reading for missing State." } } },
} satisfies Meta<typeof BoundMetric>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Observed: Story = {
  play: async ({ canvas }) => { await expect(await canvas.findByText("94.0")).toBeVisible(); },
};
export const Loading: Story = {
  beforeEach: ({ msw }) => { msw.use(entityScenario(102, "loading")); },
  play: async ({ canvas }) => { await expect(await canvas.findByText("Loading…")).toBeVisible(); },
};
export const NeverObserved: Story = {
  beforeEach: ({ msw }) => { msw.use(entityScenario(102, "unobserved")); },
  play: async ({ canvas }) => { await expect(await canvas.findByText("Never observed")).toBeVisible(); },
};
/** The last reading stays visible beside the availability status. */
export const Unavailable: Story = {
  beforeEach: ({ msw }) => { msw.use(entityScenario(102, "unavailable")); },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Unavailable")).toBeVisible();
    await expect(canvas.getByText("94.0")).toBeVisible();
  },
};
export const ReadFailed: Story = {
  beforeEach: ({ msw }) => { msw.use(entityScenario(102, "error")); },
  play: async ({ canvas }) => { await expect(await canvas.findByText(/Couldn't load this reading/)).toBeVisible(); },
};
export const RangeNotDeclared: Story = {
  args: { label: "Outdoor temperature", source: { entity_id: entityId(101), entity_type: "hearth.temperature/v1" }, trend: undefined },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("9.4")).toBeVisible();
    await expect(canvas.getByText("Range unavailable.")).toBeVisible();
  },
};

/** The card's content width, rather than the viewport, controls the compact reading. */
export const NarrowCard: Story = {
  args: { trend: undefined, range: undefined },
  decorators: [Story => <div style={{ width: 236 }}><Story /></div>],
  play: async ({ canvas }) => {
    const reading = await canvas.findByText("94.0");
    const card = canvas.getByRole("article", { name: "Outdoor humidity" });
    const time = card.querySelector("time")!;
    const cardStyle = getComputedStyle(card);
    await expect(card.clientWidth - parseFloat(cardStyle.paddingLeft) - parseFloat(cardStyle.paddingRight)).toBeLessThanOrEqual(200);
    await expect(getComputedStyle(reading).fontSize).toBe("32px");
    await expect(time).toHaveAttribute("aria-label", "Observed just now");
    await expect(time.firstElementChild).not.toBeVisible();
    await expect(canvas.getByRole("button", { name: "Source" })).toBeVisible();
  },
};
