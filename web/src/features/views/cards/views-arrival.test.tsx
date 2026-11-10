import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import type { MetricData, Resolved, SeriesData } from "../bindings/types";
import { Metric } from "./metric/Metric";
import { ARRIVAL_GLOW_MS } from "./observation";
import { TimeSeries } from "./time-series/TimeSeries";

afterEach(() => { cleanup(); vi.useRealTimers(); });

const metadata = (availability: string) => ({ id: "ent_1", label: "Lamp", enabled: true, availability });
const reading = (observed_at: string, availability = "available", value = 21.5): Resolved<MetricData> => ({
  status: "ready", metadata: metadata(availability),
  data: { value, observed_at, format: { unit_label: "°C", fraction_digits: 1 } },
});
const glowing = () => document.querySelector(".view-card-arrival");

it("glows a reading's top edge when a newer observation changes its value, then clears", () => {
  vi.useFakeTimers();
  const { rerender } = render(<Metric label="Living room" resource={reading("2026-11-05T22:00:00Z")} />, { wrapper: MemoryRouter });
  // Opening a view shows the current reading without lighting it.
  expect(glowing()).toBeNull();
  // A re-report of the same value stays dark.
  rerender(<Metric label="Living room" resource={reading("2026-11-05T22:00:30Z")} />);
  expect(glowing()).toBeNull();
  // A raw change below the card's precision still displays 21.5 and stays dark.
  rerender(<Metric label="Living room" resource={reading("2026-11-05T22:00:45Z", "available", 21.54)} />);
  expect(glowing()).toBeNull();
  rerender(<Metric label="Living room" resource={reading("2026-11-05T22:01:00Z", "available", 21.6)} />);
  expect(glowing()).not.toBeNull();
  act(() => { vi.advanceTimersByTime(ARRIVAL_GLOW_MS / 2); });
  // Re-reporting the changed value does not extend the existing glow.
  rerender(<Metric label="Living room" resource={reading("2026-11-05T22:01:05Z", "available", 21.6)} />);
  act(() => { vi.advanceTimersByTime(ARRIVAL_GLOW_MS / 2); });
  expect(glowing()).toBeNull();
});

it("never glows for an unavailable Entity", () => {
  const { rerender } = render(<Metric label="Garage" resource={reading("2026-11-05T22:00:00Z", "unavailable")} />, { wrapper: MemoryRouter });
  rerender(<Metric label="Garage" resource={reading("2026-11-05T22:00:30Z", "unavailable", 22)} />);
  expect(glowing()).toBeNull();
});

it("glows a chart when refreshed history changes a series' latest displayed value", () => {
  const history = (to: string, mean = 21.5): Resolved<SeriesData> => ({
    status: "ready", metadata: metadata("available"),
    data: { from: "2026-11-05T16:00:00Z", to, format: { unit_label: "°C", fraction_digits: 1 },
      buckets: [{ start: "2026-11-05T21:58:45Z", end: "2026-11-05T22:00:00Z", min: 21, max: 22, mean }] },
  });
  const { rerender } = render(<TimeSeries title="Temperature" series={[{ resource: history("2026-11-05T21:59:00Z") }]} />, { wrapper: MemoryRouter });
  expect(glowing()).toBeNull();
  rerender(<TimeSeries title="Temperature" series={[{ resource: history("2026-11-05T21:59:30Z") }]} />);
  expect(glowing()).toBeNull();
  rerender(<TimeSeries title="Temperature" series={[{ resource: history("2026-11-05T21:59:45Z", 21.54) }]} />);
  expect(glowing()).toBeNull();
  rerender(<TimeSeries title="Temperature" series={[{ resource: history("2026-11-05T22:00:00Z", 21.8) }]} />);
  expect(screen.getByRole("article", { name: "Temperature" }).querySelector(".view-card-arrival")).not.toBeNull();
});

it("waits for a coherent chart refresh before glowing for a changed sibling series", () => {
  const history = (to: string, mean: number): Resolved<SeriesData> => ({
    status: "ready", metadata: metadata("available"),
    data: { from: "2026-11-05T16:00:00Z", to, format: { unit_label: "°C", fraction_digits: 1 },
      buckets: [{ start: "2026-11-05T21:58:45Z", end: "2026-11-05T21:59:00Z", min: mean, max: mean, mean }] },
  });
  const old = "2026-11-05T21:59:00Z", next = "2026-11-05T22:00:00Z";
  const { rerender } = render(<TimeSeries title="Rooms" series={[
    { label: "One", resource: history(old, 21.5) }, { label: "Two", resource: history(old, 22) },
  ]} />, { wrapper: MemoryRouter });
  rerender(<TimeSeries title="Rooms" series={[
    { label: "One", resource: history(next, 21.5) }, { label: "Two", resource: history(old, 22) },
  ]} />);
  expect(glowing()).toBeNull();
  expect(screen.getByText("Showing the last synchronized interval while readings refresh.")).toBeTruthy();
  rerender(<TimeSeries title="Rooms" series={[
    { label: "One", resource: history(next, 21.5) }, { label: "Two", resource: history(next, 22.4) },
  ]} />);
  expect(glowing()).not.toBeNull();
});

it.each(["unavailable", "disabled"] as const)("ends an active Metric glow immediately when its Entity becomes %s", state => {
  vi.useFakeTimers();
  const first = reading("2026-11-05T22:00:00Z");
  const changed = reading("2026-11-05T22:01:00Z", "available", 21.6);
  const { rerender } = render(<Metric resource={first} />, { wrapper: MemoryRouter });
  rerender(<Metric resource={changed} />);
  expect(glowing()).not.toBeNull();
  const ineligible = { ...changed, metadata: { ...changed.metadata!, enabled: state !== "disabled", availability: state === "unavailable" ? "unavailable" : "available" } };
  rerender(<Metric resource={ineligible} />);
  expect(glowing()).toBeNull();
  expect(screen.getByText(state === "disabled" ? "Disabled" : "Unavailable")).toBeTruthy();
  rerender(<Metric resource={changed} />);
  expect(glowing()).toBeNull();
  rerender(<Metric resource={reading("2026-11-05T22:02:00Z", "available", 21.7)} />);
  expect(glowing()).not.toBeNull();
});

it("does not replay a history change already displayed while unavailable", () => {
  const history = (to: string, mean: number, availability: string): Resolved<SeriesData> => ({
    status: "ready", metadata: metadata(availability),
    data: { from: "2026-11-05T16:00:00Z", to, format: { unit_label: "°C", fraction_digits: 1 },
      buckets: [{ start: "2026-11-05T21:58:45Z", end: to, min: mean, max: mean, mean }] },
  });
  const { rerender } = render(<TimeSeries title="Temperature" series={[{ resource: history("2026-11-05T22:00:00Z", 21, "available") }]} />, { wrapper: MemoryRouter });
  rerender(<TimeSeries title="Temperature" series={[{ resource: history("2026-11-05T22:01:00Z", 22, "unavailable") }]} />);
  expect(glowing()).toBeNull();
  rerender(<TimeSeries title="Temperature" series={[{ resource: history("2026-11-05T22:01:00Z", 22, "available") }]} />);
  expect(glowing()).toBeNull();
});
