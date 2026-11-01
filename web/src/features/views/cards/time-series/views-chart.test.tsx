import { cloneElement, type ReactElement } from "react";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { TimeSeries } from "./TimeSeries";
import type { SeriesData } from "../../bindings/types";

// jsdom has no layout; give the actual Recharts plot a deterministic viewport.
vi.mock("../../../../components/ui/chart", () => ({ ChartContainer: ({ children }: { children: ReactElement<{ width: number; height: number }> }) => cloneElement(children, { width: 600, height: 240 }) }));
afterEach(cleanup);
const data = (hour: number, value: number): SeriesData => ({
  from: `2026-11-01T0${hour}:00:00Z`, to: `2026-11-01T${hour + 6}:00:00Z`,
  format: { unit_label: "%", fraction_digits: 0 },
  buckets: [{ start: `2026-11-01T0${hour}:00:00Z`, end: `2026-11-01T0${hour}:01:15Z`, count: 1, min: value, max: value, mean: value }],
});
it("keeps the last coherent plot after a sibling refresh fails and honors adapter precision", async () => {
  const one = data(4, 65), two = data(4, 75);
  const { container, rerender } = render(<TimeSeries title="Battery history" series={[
    { resource: { status: "ready", data: one } }, { resource: { status: "ready", data: two } },
  ]} />);
  expect(container.querySelector("svg")).not.toBeNull();
  await waitFor(() => expect(container.querySelectorAll(".recharts-yAxis-tick-labels text").length).toBeGreaterThan(0));
  const ticks = [...container.querySelectorAll(".recharts-yAxis-tick-labels text")].map(node => node.textContent);
  expect(ticks).toContain("75");
  expect(ticks.every(tick => /^\d+$/.test(tick ?? ""))).toBe(true);
  rerender(<TimeSeries title="Battery history" series={[
    { resource: { status: "ready", data: data(5, 85) } },
    { resource: { status: "ready", data: two, refreshFailed: true } },
  ]} />);
  expect(container.querySelector("svg")).not.toBeNull();
  expect(screen.getByText("Showing the last synchronized interval while readings refresh.")).toBeTruthy();
  expect(screen.getByText("Refresh failed · showing last successful data")).toBeTruthy();
  expect([...container.querySelectorAll(".recharts-yAxis-tick-labels text")].map(node => node.textContent)).toEqual(ticks);
});
