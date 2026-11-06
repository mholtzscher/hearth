import { cloneElement, type ReactElement } from "react";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { TimeSeries } from "./TimeSeries";
import type { SeriesData } from "../../bindings/types";

// jsdom has no layout; give the actual Recharts plot a deterministic viewport.
vi.mock("../../../../components/ui/chart", () => ({ ChartContainer: ({ children }: { children: ReactElement<{ width: number; height: number }> }) => cloneElement(children, { width: 600, height: 240 }) }));
afterEach(cleanup);
const data = (hour: number, value: number): SeriesData => ({
  from: `2026-11-01T0${hour}:00:00Z`, to: `2026-11-01T${hour + 6}:00:00Z`,
  format: { unit_label: "%", fraction_digits: 0 },
  buckets: [{ start: `2026-11-01T0${hour}:00:00Z`, end: `2026-11-01T0${hour}:01:15Z`, min: value, max: value, mean: value }],
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
  expect(screen.getByText("Couldn't refresh")).toBeTruthy();
  expect(screen.getByText("Showing the last reading.")).toBeTruthy();
  expect([...container.querySelectorAll(".recharts-yAxis-tick-labels text")].map(node => node.textContent)).toEqual(ticks);
});

it("breaks sample-mean paths at null buckets, including refreshed gaps", async () => {
  const history = data(4, 20);
  history.buckets = Array.from({ length: 5 }, (_, i) => ({
    start: new Date(Date.parse(history.from) + i * 75000).toISOString(),
    end: new Date(Date.parse(history.from) + (i + 1) * 75000).toISOString(),
    min: 20 + i, max: 20 + i, mean: 20 + i,
  }));
  const { container, rerender } = render(<TimeSeries title="Setting history" series={[{ resource: { status: "ready", data: history } }]} />);
  const segments = () => container.querySelector(".recharts-line-curve")?.getAttribute("d")?.match(/M/g)?.length;
  await waitFor(() => expect(segments()).toBe(1));
  const gap = { ...history, buckets: history.buckets.map((bucket, i) => i === 2 ? { ...bucket, min: null, max: null, mean: null } : bucket) };
  rerender(<TimeSeries title="Setting history" series={[{ resource: { status: "ready", data: gap } }]} />);
  await waitFor(() => expect(segments()).toBe(2));
  // The method lives in Source rather than in a caption on the card.
  expect(screen.getByText(/Each point averages the samples/).closest("[hidden]")).not.toBeNull();
});

it("draws eight distinct series, dashes an unavailable one, and offers one tab stop with a text summary", async () => {
  const history = data(4, 20);
  history.buckets = Array.from({ length: 3 }, (_, i) => ({
    start: new Date(Date.parse(history.from) + i * 75000).toISOString(),
    end: new Date(Date.parse(history.from) + (i + 1) * 75000).toISOString(),
    min: 20, max: 20, mean: 20,
  }));
  const series = Array.from({ length: 8 }, (_, i) => ({
    label: `Room ${i + 1}`,
    resource: {
      status: "ready" as const, data: { ...history, buckets: history.buckets.map(b => ({ ...b, min: 20 + i, max: 21 + i, mean: 20.5 + i })) },
      metadata: { id: `ent_${i}`, label: `Sensor ${i + 1}`, enabled: true, availability: i === 7 ? "unavailable" : "available" },
    },
  }));
  const { container } = render(<TimeSeries title="Rooms" series={series} />);
  await waitFor(() => expect(container.querySelectorAll(".recharts-line-curve").length).toBe(8));
  const strokes = [...container.querySelectorAll(".recharts-line-curve")].map(path => path.getAttribute("stroke"));
  expect(new Set(strokes).size).toBe(8);
  expect(strokes).not.toContain("var(--orange)");
  const dashed = [...container.querySelectorAll(".recharts-line-curve")].filter(path => path.getAttribute("stroke-dasharray"));
  expect(dashed.map(path => path.getAttribute("stroke"))).toEqual(["var(--chart-8)"]);
  expect(within(screen.getByRole("article", { name: "Rooms" })).getByText("Unavailable")).toBeTruthy();
  // Source links stay out of the tab order until the disclosure opens.
  const focusable = [...container.querySelectorAll("a[href], button, [tabindex]:not([tabindex='-1'])")].filter(node => !node.closest("[hidden]"));
  expect(focusable.map(node => node.getAttribute("role") ?? node.tagName.toLowerCase())).toEqual(["region", "button"]);
  expect(screen.getByText("Room 2: latest 22 %, range 21 to 22 over the last 6 hours.")).toBeTruthy();
  expect(screen.queryByText(/arithmetic sample mean|Retained observations/)).toBeNull();
});
