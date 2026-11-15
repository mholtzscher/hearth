import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";
import { entityId, fixtureEntity, fixtureNow } from "../../../../../.storybook/fixtures";
import { createCoreHandlers, entityScenario } from "../../../../../.storybook/msw-handlers";
import { BoundForecast } from "./Forecast";
import { localDate } from "./upcoming";

function nextMidnight() {
  const midnight = new Date(fixtureNow);
  midnight.setHours(24, 0, 0, 0);
  return midnight;
}

const meta = {
  component: BoundForecast,
  tags: ["ai-generated"],
  args: {
    label: "Next 12 hours", periods: 12,
    source: { kind: "entity-state", entity_id: entityId(201), entity_type: "hearth.hourlyforecast/v1" },
  },
} satisfies Meta<typeof BoundForecast>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Hourly: Story = {
  play: async ({ canvas }) => { await expect(await canvas.findByRole("img", { name: /Next 12 hours/ })).toBeVisible(); },
};
export const Daily: Story = {
  args: { label: "Next 7 days", periods: 7, source: { kind: "entity-state", entity_id: entityId(202), entity_type: "hearth.dailyforecast/v1" } },
  play: async ({ canvas }) => {
    await canvas.findByRole("listitem", { name: /^Today:/ });
    await expect(canvas.getAllByRole("listitem")).toHaveLength(7);
  },
};
export const NeverObserved: Story = {
  beforeEach: ({ msw }) => { msw.use(entityScenario(201, "unobserved")); },
  play: async ({ canvas }) => { await expect(await canvas.findByText("Never observed")).toBeVisible(); },
};
export const CrossingMidnight: Story = {
  beforeEach: ({ msw }) => {
    const forecast = fixtureEntity(201);
    const value = forecast.state!.value as { periods: { starts_at: string }[] };
    const currentHour = new Date(fixtureNow);
    currentHour.setMinutes(0, 0, 0);
    const start = Math.max(currentHour.getTime(), nextMidnight().getTime() - 6 * 3_600_000);
    value.periods.forEach((period, index) => {
      period.starts_at = new Date(start + index * 3_600_000).toISOString();
    });
    msw.use(...createCoreHandlers([forecast]));
  },
  play: async ({ canvas }) => {
    const chart = await canvas.findByRole("img", { name: /Next 12 hours/ });
    const weekday = new Intl.DateTimeFormat("en-US", { weekday: "short" }).format(nextMidnight());
    const label = within(chart).getByText(weekday);
    await expect(label.previousElementSibling?.tagName.toLowerCase()).toBe("line");
    await expect(label.previousElementSibling).toHaveAttribute("y1", "10");
    await expect(label.previousElementSibling).toHaveAttribute("y2", "176");
  },
};
export const PartialUnavailableForecast: Story = {
  args: Daily.args,
  beforeEach: ({ msw }) => {
    const forecast = fixtureEntity(202);
    const value = forecast.state!.value as { periods: { date: string }[] };
    const yesterday = new Date(fixtureNow);
    yesterday.setDate(yesterday.getDate() - 1);
    value.periods[0].date = localDate(yesterday.getTime());
    forecast.availability.status = "unavailable";
    msw.use(...createCoreHandlers([forecast]));
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText(/Showing the remaining days of a forecast/)).toBeVisible();
    await expect(canvas.getAllByRole("listitem", { name: /low/ })).toHaveLength(6);
  },
};
