import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { BoundTimeSeries } from "../cards/time-series/TimeSeries";
import { ViewDataProvider } from "../data/provider";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });
it("shows never-observed State beside an empty historical chart", async () => {
  const id = "ent_019a3e6a-4111-7000-8000-000000000001";
  const from = Date.parse("2026-11-01T04:00:00Z");
  const history = { entity_id: id, from: new Date(from).toISOString(), to: new Date(from + 6 * 3600_000).toISOString(), buckets: Array.from({ length: 288 }, (_, i) => ({ start: new Date(from + i * 75000).toISOString(), end: new Date(from + (i + 1) * 75000).toISOString(), count: 0, min_milli_celsius: null, max_milli_celsius: null, mean_milli_celsius: null })) };
  vi.spyOn(globalThis, "fetch").mockImplementation(async url => new Response(JSON.stringify(String(url).includes("temperature-series") ? history : { id, name: "New sensor", type: "hearth.temperature/v1", enabled: true, availability: { status: "available" }, state: null })));
  render(<ViewDataProvider><BoundTimeSeries title="New sensor history" series={[{ source: { entity_id: id, entity_type: "hearth.temperature/v1", window: "6h" } }]} display={{ unit: "celsius" }} /></ViewDataProvider>);
  expect(await screen.findByText("Never observed")).toBeTruthy();
  expect(screen.getByText("New sensor history")).toBeTruthy();
  expect(screen.queryByText("Request failed")).toBeNull();
});
