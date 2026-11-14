import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { BoundTimeSeries } from "../cards/time-series/TimeSeries";
import { ViewDataProvider } from "../data/provider";
import { BoundMetric } from "../cards/metric/Metric";
import { BoundReadingList } from "../cards/reading-list/ReadingList";
import { BoundDevice } from "../cards/device/Device";
import { pairedEntityResponse, pairedHistoryResponse } from "../../../../.storybook/paired-fixtures";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

/** A 360-bucket empty six-hour summary for the range the read admitted. */
function emptyHistory(entity: string, rangeTo: string) {
  const to = Date.parse(rangeTo);
  const from = Math.ceil(to / 60_000) * 60_000 - 6 * 3600_000;
  const display = {
    entity_id: entity,
    quantity: { unit: "Cel", label: "°C", fraction_digits: 1 },
    range: { from: new Date(from).toISOString(), to: rangeTo },
    bucket: { duration: "1m0s", alignment: "range_start" },
    buckets: Array.from({ length: 360 }, (_, i) => ({
      range: {
        from: new Date(from + i * 60000).toISOString(),
        to: new Date(Math.min(from + (i + 1) * 60000, to)).toISOString(),
      },
      metrics: { min: null, max: null, mean: null },
    })),
  };
  return pairedHistoryResponse(display, { quantity: { unit: "mCel", label: "mCel", fraction_digits: 0 }, buckets: display.buckets });
}

it("shows never-observed State beside an empty historical chart", async () => {
  const id = "ent_019a3e6a-4111-7000-8000-000000000001";
  const unobserved = { id, name: "New sensor", type: "hearth.temperature/v1", enabled: true, availability: { status: "available" }, state: null };
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url, init) => new Response(
    JSON.stringify(String(url).includes("/state/history") && init?.method === "POST"
      ? emptyHistory(id, JSON.parse(String(init?.body)).range.to)
      : pairedEntityResponse(unobserved, unobserved)),
  ));
  render(<ViewDataProvider><BoundTimeSeries title="New sensor history" series={[{ source: { entity_id: id, entity_type: "hearth.temperature/v1", window: "6h" } }]} /></ViewDataProvider>, { wrapper: MemoryRouter });
  expect(await screen.findByText("Never observed")).toBeTruthy();
  expect(screen.getByText("New sensor history")).toBeTruthy();
  expect(screen.queryByText("Request failed")).toBeNull();
});

it("renders numeric readings, history, and Device rows without a household settings response", async () => {
  const id = "ent_019a3e6a-4111-7000-8000-000000000001";
  const source = { entity_id: id, entity_type: "hearth.temperature/v1" };
  const entity = { id, name: "Temperature", type: source.entity_type, enabled: true, availability: { status: "available" }, support: {},
    quantities: { "": { unit: "[degF]", label: "°F", fraction_digits: 1 } },
    state: { value: 70.7, observed_at: "2026-11-01T10:00:00Z" } };
  const fetch = vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const url = String(input);
    // If a consumer still requests settings, keep it pending rather than allowing its fallback.
    if (url.includes("/v1/household")) return new Promise<Response>(() => {});
    if (url.includes("/state/history")) {
      const history = emptyHistory(id, JSON.parse(String(init?.body)).range.to);
      history.buckets.forEach(bucket => { bucket.display = { ...bucket.display, ...entity.quantities[""] }; });
      return new Response(JSON.stringify(history));
    }
    if (url.includes("/v1/devices/")) return new Response(JSON.stringify({ id: "dev_temperature", name: "Sensor", entities: [entity] }));
    const canonical = { ...entity, quantities: { "": { unit: "mCel", label: "mCel", fraction_digits: 0 } }, state: { ...entity.state, value: 21500 } };
    return new Response(JSON.stringify(pairedEntityResponse(entity, canonical)));
  });
  render(<ViewDataProvider>
    <BoundMetric label="Current" source={source} />
    <BoundReadingList title="Readings" rows={[{ source, label: "Temperature" }]} />
    <BoundTimeSeries title="History" series={[{ source: { ...source, window: "6h" } }]} />
    <BoundDevice device={{ kind: "device", device_id: "dev_temperature" }} />
  </ViewDataProvider>, { wrapper: MemoryRouter });
  expect(await within(screen.getByRole("article", { name: "Current" })).findByText("70.7")).toBeTruthy();
  expect(await within(screen.getByRole("article", { name: "Readings" })).findByText("70.7")).toBeTruthy();
  expect(await within(screen.getByRole("article", { name: "Sensor" })).findByText("70.7")).toBeTruthy();
  expect(await within(screen.getByRole("article", { name: "History" })).findByText(/no samples over the last/)).toBeTruthy();
  expect(fetch.mock.calls.some(([url]) => String(url).includes("/v1/household"))).toBe(false);
});

it("shares one Entity response across hue and saturation cards", async () => {
  const source = { entity_id: "ent_019a3e6a-4111-7000-8000-000000000001", entity_type: "hearth.colorhs/v1" };
  const entity = {
    id: source.entity_id, type: source.entity_type, name: "Lamp", enabled: true,
    availability: { status: "unavailable" }, support: {},
    quantities: { "/hue": { unit: "deg", label: "°", fraction_digits: 0 }, "/saturation": { unit: "%", label: "%", fraction_digits: 0 } },
    state: { value: { active: false, hue: 210, saturation: 64 }, observed_at: "2026-11-01T10:00:00Z" },
  };
  const fetch = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify(pairedEntityResponse(entity, entity))));
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
    const entity = { id, type, name: "Setting", enabled: true,
      availability: { status: "available" }, support: {},
      quantities: { "/value": { unit: "1", label: "", fraction_digits: 1 } },
      state: id.endsWith("3") ? null : { value, observed_at: "2026-11-01T10:00:00Z" },
    };
    return new Response(JSON.stringify(pairedEntityResponse(entity, entity)));
  });
  render(<ViewDataProvider>{["Absent", "Zero", "Unobserved"].map((label, i) =>
    <BoundMetric key={label} label={label} source={{ entity_id: `ent_019a3e6a-4111-7000-8000-00000000000${i + 1}`, entity_type: type, field: "/value" }} />,
  )}</ViewDataProvider>, { wrapper: MemoryRouter });
  expect(await within(screen.getByRole("article", { name: "Absent" })).findByText("No value")).toBeTruthy();
  expect(screen.getByText("The field isn't in the current State.")).toBeTruthy();
  expect(await within(screen.getByRole("article", { name: "Zero" })).findByText("0.0")).toBeTruthy();
  expect(await within(screen.getByRole("article", { name: "Unobserved" })).findByText("Never observed")).toBeTruthy();
});
