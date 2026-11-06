import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { MetricData, Resolved, SeriesData } from "../bindings/types";
import { Metric } from "./metric/Metric";
import { ARRIVAL_GLOW_MS } from "./observation";
import { TimeSeries } from "./time-series/TimeSeries";

afterEach(() => { cleanup(); vi.useRealTimers(); });

const metadata = (availability: string) => ({ id: "ent_1", label: "Lamp", enabled: true, availability });
const reading = (observed_at: string, availability = "available"): Resolved<MetricData> => ({
  status: "ready", metadata: metadata(availability),
  data: { value: 21.5, observed_at, format: { unit_label: "°C", fraction_digits: 1 } },
});
const glowing = () => document.querySelector(".view-card-arrival");

it("glows a reading's top edge when a newer observation arrives, then clears after a minute", () => {
  vi.useFakeTimers();
  const { rerender } = render(<Metric label="Living room" resource={reading("2026-11-05T22:00:00Z")} />);
  // Opening a view shows the current reading without lighting it.
  expect(glowing()).toBeNull();
  rerender(<Metric label="Living room" resource={reading("2026-11-05T22:00:30Z")} />);
  expect(glowing()).not.toBeNull();
  act(() => { vi.advanceTimersByTime(ARRIVAL_GLOW_MS); });
  expect(glowing()).toBeNull();
});

it("never glows for an unavailable Entity", () => {
  const { rerender } = render(<Metric label="Garage" resource={reading("2026-11-05T22:00:00Z", "unavailable")} />);
  rerender(<Metric label="Garage" resource={reading("2026-11-05T22:00:30Z", "unavailable")} />);
  expect(glowing()).toBeNull();
});

it("glows a chart when one of its series receives a new observation", () => {
  const history = (observed_at: string): Resolved<SeriesData> => ({
    status: "ready", metadata: metadata("available"),
    data: { from: "2026-11-05T16:00:00Z", to: "2026-11-05T22:00:00Z", observed_at, format: { unit_label: "°C", fraction_digits: 1 },
      buckets: [{ start: "2026-11-05T21:58:45Z", end: "2026-11-05T22:00:00Z", min: 21, max: 22, mean: 21.5 }] },
  });
  const { rerender } = render(<TimeSeries title="Temperature" series={[{ resource: history("2026-11-05T21:59:00Z") }]} />);
  expect(glowing()).toBeNull();
  rerender(<TimeSeries title="Temperature" series={[{ resource: history("2026-11-05T22:00:00Z") }]} />);
  expect(screen.getByRole("article", { name: "Temperature" }).querySelector(".view-card-arrival")).not.toBeNull();
});
