import { expect, it } from "vitest";
import { chartLabelStep, upcomingDays, upcomingHours } from "./upcoming";

it("drops ended hours and local past dates before limiting", () => {
  const now = new Date(2026, 10, 9, 10, 30).getTime();
  const hours = [8, 9, 10, 11, 12].map(h => ({ starts_at: new Date(2026, 10, 9, h).toISOString() }));
  expect(upcomingHours(hours, now, 2)).toEqual(hours.slice(2, 4));
  const days = ["2026-11-08", "2026-11-09", "2026-11-10"].map(date => ({ date, starts_at: "2026-11-09T00:00:00Z" }));
  expect(upcomingDays(days, now, 1)).toEqual(days.slice(1, 2));
});

it.each([[6, 1], [12, 2], [24, 3], [48, 6]])("uses the label step for %i hours", (count, step) => {
  expect(chartLabelStep(count)).toBe(step);
});
