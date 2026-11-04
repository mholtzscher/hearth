import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { BoundTimeSeries } from "../cards/time-series/TimeSeries";
import { seriesBuckets } from "../bindings/history";
import { ViewDataProvider } from "../data/provider";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

/** A 288-bucket empty summary for the exact range the read admitted. */
function emptyHistory(entity: string, rangeTo: string) {
  const to = Date.parse(rangeTo);
  const from = to - 6 * 3600_000;
  return {
    entity_id: entity,
    range: { from: new Date(from).toISOString(), to: rangeTo },
    bucket: { duration: "1m15s", alignment: "range_start" },
    buckets: Array.from({ length: seriesBuckets }, (_, i) => ({
      range: {
        from: new Date(from + i * 75000).toISOString(),
        to: new Date(from + (i + 1) * 75000).toISOString(),
      },
      metrics: { min: null, max: null, mean: null },
    })),
  };
}

it("shows never-observed State beside an empty historical chart", async () => {
  const id = "ent_019a3e6a-4111-7000-8000-000000000001";
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url, init) => new Response(
    JSON.stringify(String(url).includes("/state/history") && init?.method === "POST"
      ? emptyHistory(id, JSON.parse(String(init?.body)).range.to)
      : { id, name: "New sensor", type: "hearth.temperature/v1", enabled: true, availability: { status: "available" }, state: null }),
  ));
  render(<ViewDataProvider><BoundTimeSeries title="New sensor history" series={[{ source: { entity_id: id, entity_type: "hearth.temperature/v1", window: "6h" } }]} display={{ unit: "celsius" }} /></ViewDataProvider>);
  expect(await screen.findByText("Never observed")).toBeTruthy();
  expect(screen.getByText("New sensor history")).toBeTruthy();
  expect(screen.queryByText("Request failed")).toBeNull();
});
