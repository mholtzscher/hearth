import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { BoundTimeSeries } from "../cards/time-series/TimeSeries";
import { seriesBuckets } from "../bindings/history";
import { ViewDataProvider } from "../data/provider";
import { BoundMetric } from "../cards/metric/Metric";

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
  render(<ViewDataProvider><BoundTimeSeries title="New sensor history" series={[{ source: { entity_id: id, entity_type: "hearth.temperature/v1", window: "6h" } }]} /></ViewDataProvider>, { wrapper: MemoryRouter });
  expect(await screen.findByText("Never observed")).toBeTruthy();
  expect(screen.getByText("New sensor history")).toBeTruthy();
  expect(screen.queryByText("Request failed")).toBeNull();
});

it("shares one Entity response across hue and saturation cards", async () => {
  const source = { entity_id: "ent_019a3e6a-4111-7000-8000-000000000001", entity_type: "hearth.colorhs/v1" };
  const fetch = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({
    id: source.entity_id, type: source.entity_type, name: "Lamp", enabled: true,
    availability: { status: "unavailable" }, support: {},
    state: { value: { active: false, hue: 210, saturation: 64 }, observed_at: "2026-11-01T10:00:00Z" },
  })));
  render(<ViewDataProvider>
    <BoundMetric label="Hue" source={{ ...source, field: "/hue" }} />
    <BoundMetric label="Saturation" source={{ ...source, field: "/saturation" }} />
  </ViewDataProvider>, { wrapper: MemoryRouter });
  expect(await within(screen.getByRole("article", { name: "Hue" })).findByText("210")).toBeTruthy();
  expect(await within(screen.getByRole("article", { name: "Saturation" })).findByText("64")).toBeTruthy();
  expect(screen.getAllByText("Unavailable")).toHaveLength(2);
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(fetch.mock.calls[0][0]).toBe(`/v1/entities/${source.entity_id}`);
});

it("renders absent optional fields separately from never-observed and zero", async () => {
  const type = "hearth.numericsetting/v1";
  vi.spyOn(globalThis, "fetch").mockImplementation(async url => {
    const id = String(url).split("/").pop()!;
    const value = id.endsWith("1") ? { mode: "choice", choice: "auto" } : { mode: "value", value: 0 };
    return new Response(JSON.stringify({ id, type, name: "Setting", enabled: true,
      availability: { status: "available" }, support: {},
      state: id.endsWith("3") ? null : { value, observed_at: "2026-11-01T10:00:00Z" },
    }));
  });
  render(<ViewDataProvider>{["Absent", "Zero", "Unobserved"].map((label, i) =>
    <BoundMetric key={label} label={label} source={{ entity_id: `ent_019a3e6a-4111-7000-8000-00000000000${i + 1}`, entity_type: type, field: "/value" }} />,
  )}</ViewDataProvider>, { wrapper: MemoryRouter });
  expect(await within(screen.getByRole("article", { name: "Absent" })).findByText("No value")).toBeTruthy();
  expect(screen.getByText("The field isn't in the current State.")).toBeTruthy();
  expect(await within(screen.getByRole("article", { name: "Zero" })).findByText("0.0")).toBeTruthy();
  expect(await within(screen.getByRole("article", { name: "Unobserved" })).findByText("Never observed")).toBeTruthy();
});
