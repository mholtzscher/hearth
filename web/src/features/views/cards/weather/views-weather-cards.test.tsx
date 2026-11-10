import { StrictMode, type ReactNode } from "react";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import examples from "../../../../../../contracts/views/v1/examples.json";
import { ViewDataProvider } from "../../data/provider";
import { UnitSystemOverride } from "../../../units/household";
import type { UnitSystem } from "../../../units/systems";
import type { FieldEntity } from "../../bindings/entity";
import { ViewRenderer } from "../../renderer/ViewRenderer";
import { BoundRain } from "../rain/Rain";
import { BoundWind } from "../wind/Wind";
import { BoundPressure } from "../pressure/Pressure";
import { BoundOutsideNow } from "../outside-now/OutsideNow";

const at = "2026-11-09T10:00:00Z";
const id = (number: number) => `ent_01900000-0000-7000-8000-${String(number).padStart(12, "0")}`;
const weather = examples.find(example => example.name === "weather-cards")!.definition;
type History = (minutesAgo: number) => number | null;
type Quantity = NonNullable<FieldEntity["quantities"]>[string];

function entity(number: number, type: string, value: number | null, quantity: Quantity, canonicalUnit?: string): FieldEntity {
  return { id: id(number), name: `Sensor ${number}`, type, enabled: true, availability: { status: "available" },
    support: canonicalUnit ? { state: { minimum: 0, maximum: 10000, unit: canonicalUnit }, operations: {} } : { state: {}, operations: {} },
    quantities: { "": quantity }, state: value === null ? null : { value, observed_at: at } };
}

/** Explicit HTTP representations: values are already projected by Core. */
function station(system: UnitSystem = "metric"): FieldEntity[] {
  const us = system === "us", sensor = "hearth.numericsensor/v1";
  const q = (unit: string, label: string, fraction_digits = 1): Quantity => ({ unit, label, fraction_digits });
  return [
    entity(101, "hearth.temperature/v1", us ? 48.92 : 9.4, us ? q("[degF]", "°F") : q("Cel", "°C")),
    entity(102, "hearth.relativehumidity/v1", 94, q("%", "%")),
    entity(103, "hearth.speed/v1", us ? 14.0935 : 6.3, us ? q("[mi_i]/h", "mi/h") : q("m/s", "m/s")),
    entity(104, sensor, 135, q("deg", "°"), "deg"),
    entity(105, sensor, us ? 0.2677 : 6.8, us ? q("[in_i]/h", "in/h", 2) : q("mm/h", "mm/h"), "mm/h"),
    entity(106, sensor, us ? 0.6378 : 16.2, us ? q("[in_i]", "in", 2) : q("mm", "mm"), "mm"),
    entity(107, sensor, us ? 0.7323 : 18.6, us ? q("[in_i]", "in", 2) : q("mm", "mm"), "mm"),
    entity(108, sensor, us ? 0.9016 : 22.9, us ? q("[in_i]", "in", 2) : q("mm", "mm"), "mm"),
    entity(109, sensor, us ? 2.811 : 71.4, us ? q("[in_i]", "in", 2) : q("mm", "mm"), "mm"),
    entity(110, sensor, us ? 27.0906 : 688.1, us ? q("[in_i]", "in", 2) : q("mm", "mm"), "mm"),
    entity(111, "hearth.speed/v1", us ? 25.054 : 11.2, us ? q("[mi_i]/h", "mi/h") : q("m/s", "m/s")),
    entity(112, "hearth.speed/v1", us ? 33.107 : 14.8, us ? q("[mi_i]/h", "mi/h") : q("m/s", "m/s")),
    entity(113, "hearth.pressure/v1", us ? 29.625 : 1003.2, us ? q("[in_i'Hg]", "in Hg", 2) : q("hPa", "hPa")),
  ];
}

function core(entities: FieldEntity[], histories: Record<number, History> = {}) {
  const byId = new Map(entities.map(item => [item.id, item])), failures = new Set<string>(), posts: string[] = [], requests: string[] = [];
  const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const url = String(input), entityId = url.split("/")[3].split("?")[0], found = byId.get(entityId);
    requests.push(url);
    if (url.endsWith("/commands")) { posts.push(entityId); return response({}, 500); }
    if (url.endsWith("/state/history")) {
      if (failures.has(entityId)) return response({ detail: "History unavailable" }, 503);
      const body = JSON.parse(String(init?.body));
      const step = ({ "1m0s": 60000, "5m0s": 300000, "30m0s": 1800000 } as Record<string, number>)[body.result.bucket.duration];
      const from = Date.parse(body.range.from), to = Date.parse(body.range.to), count = Math.ceil((to - from) / step);
      const history = histories[Number(entityId.slice(-3))] ?? (() => Number(found?.state?.value ?? 0));
      const buckets = Array.from({ length: count }, (_, index) => {
        const start = from + index * step, value = history(Math.round((to - start) / 60000));
        return { range: { from: new Date(start).toISOString(), to: new Date(Math.min(to, start + step)).toISOString() }, metrics: { min: value, max: value, mean: value } };
      });
      return response({ range: body.range, quantity: found?.quantities?.[""], buckets });
    }
    return response(found ?? {}, found ? 200 : 404);
  });
  return { posts, requests, historyFailures: failures, entities: byId };
}

const flush = () => act(() => vi.advanceTimersByTimeAsync(0));
async function mount(children: ReactNode, system: UnitSystem = "metric") {
  render(<StrictMode><UnitSystemOverride.Provider value={system}><ViewDataProvider>{children}</ViewDataProvider></UnitSystemOverride.Provider></StrictMode>, { wrapper: MemoryRouter });
  await flush(); await flush();
}
const card = (name: string) => screen.getByRole("article", { name });
const source = <T extends string>(number: number, type: T) => ({ kind: "entity-state" as const, entity_id: id(number), entity_type: type });
beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(new Date("2026-11-09T10:00:30Z")); });
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.useRealTimers(); });

it.each(["metric", "us"] as const)("renders only delivered weather readings and aggregate extrema (%s)", async system => {
  const api = core(station(system), { 101: minutes => system === "us" ? (minutes > 600 ? 51.62 : 48.92) : (minutes > 600 ? 10.9 : 9.4) });
  await mount(<ViewRenderer headingLevel={3} definition={weather} />, system);
  expect(screen.getAllByRole("article")).toHaveLength(4);
  expect(card("Outside").querySelector(".view-metric-value")!.textContent).toBe(system === "us" ? "48.9°F" : "9.4°C");
  expect(within(card("Outside")).getByText(system === "us" ? "24 h high 51.6, low 48.9 °F" : "24 h high 10.9, low 9.4 °C")).toBeTruthy();
  expect(card("Wind").textContent).toContain(system === "us" ? "14.1mi/h" : "6.3m/s");
  expect(card("Wind").textContent).toContain("135.0°");
  expect(card("Rain").textContent).toContain(system === "us" ? "0.27in/h" : "6.8mm/h");
  expect(card("Rain").textContent).toContain(system === "us" ? "0.64in" : "16.2mm");
  expect(card("Pressure").textContent).toContain(system === "us" ? "29.63" : "1003.2");
  expect(document.body.textContent).not.toMatch(/Feels like|Dew point|Breezy|Beaufort|Tendency|Dry/);
  expect(document.querySelector(".view-weather-compass")).toBeNull();
  await act(() => vi.advanceTimersByTimeAsync(35000));
  expect(api.posts).toHaveLength(0);
});

it("keeps zero rain as a numeric reading and station totals independent", async () => {
  const items = station(), rate = items.find(item => item.id === id(105))!;
  rate.state = { value: 0, observed_at: at };
  core(items);
  const rain = source(105, "hearth.numericsensor/v1"), total = source(106, "hearth.numericsensor/v1");
  await mount(<BoundRain rate={rain} totals={{ day: total }} />);
  expect(card("Rain").querySelector(".view-metric-value")!.textContent).toBe("0.0mm/h");
  expect(card("Rain").textContent).toContain("16.2mm");
  expect(card("Rain").textContent).not.toContain("Dry");
});

it.each(["unavailable", "disabled"] as const)("keeps independent wind and rain statuses and recovers from %s", async status => {
  const items = station(), affected = items.find(item => item.id === id(104))!;
  if (status === "disabled") affected.enabled = false; else affected.availability = { status };
  const api = core(items);
  const speed = source(103, "hearth.speed/v1"), direction = source(104, "hearth.numericsensor/v1");
  await mount(<BoundWind speed={speed} direction={direction} />);
  expect(card("Wind").textContent).toContain("135.0°");
  expect(within(card("Wind")).getByText(status === "disabled" ? "Disabled" : "Unavailable")).toBeTruthy();
  affected.enabled = true; affected.availability = { status: "available" };
  await act(() => vi.advanceTimersByTimeAsync(10_000));
  expect(within(card("Wind")).queryByText(status === "disabled" ? "Disabled" : "Unavailable")).toBeNull();
  expect(api.posts).toHaveLength(0);
});

it("shows a missing wind direction without inventing a direction", async () => {
  core(station().filter(item => item.id !== id(104)));
  await mount(<BoundWind speed={source(103, "hearth.speed/v1")} direction={source(104, "hearth.numericsensor/v1")} />);
  expect(within(card("Wind")).getByText("Entity not found")).toBeTruthy();
  expect(card("Wind").querySelector(".view-weather-compass")).toBeNull();
});

it("reads pressure history only when requested and retains a failed trend", async () => {
  const api = core(station());
  const pressure = source(113, "hearth.pressure/v1");
  await mount(<BoundPressure source={pressure} />);
  expect(api.requests.some(url => url.includes("/state/history"))).toBe(false);
  cleanup(); vi.restoreAllMocks();
  const next = core(station());
  await mount(<BoundPressure source={pressure} trend="6h" />);
  expect(card("Pressure").querySelector(".view-trend")).toBeTruthy();
  next.historyFailures.add(pressure.entity_id);
  await act(() => vi.advanceTimersByTimeAsync(60_000));
  expect(within(card("Pressure")).getByText("Couldn't refresh history. Showing the last interval.")).toBeTruthy();
  next.historyFailures.clear();
  await act(() => vi.advanceTimersByTimeAsync(60_000));
  expect(within(card("Pressure")).queryByText("Couldn't refresh history. Showing the last interval.")).toBeNull();
  expect(card("Pressure").textContent).not.toMatch(/Rising|Falling|Tendency/);
});

it.each(["metric", "us"] as const)("keeps the actual loaded outdoor history interval through failure and recovery (%s)", async system => {
  const temperature = source(101, "hearth.temperature/v1");
  const histories: Record<number, History> = { 101: minutes => system === "us" ? (minutes > 600 ? 51.62 : 48.92) : (minutes > 600 ? 10.9 : 9.4) };
  const api = core(station(system), histories);
  await mount(<BoundOutsideNow temperature={temperature} />, system);
  const highLow = system === "metric" ? "high 10.9, low 9.4 °C" : "high 51.6, low 48.9 °F";
  expect(within(card("Outside")).getByText(`24 h ${highLow}`)).toBeTruthy();
  const interval = () => ["Temperature history from", "Temperature history to"].map(label => screen.getByText(label).parentElement!.querySelector("time")!.dateTime);
  fireEvent.click(within(card("Outside")).getByRole("button", { name: "Source" }));
  const loaded = interval();
  expect(loaded).toEqual(["2026-11-08T10:05:00.000Z", "2026-11-09T10:00:30.000Z"]);
  fireEvent.click(screen.getByRole("button", { name: "Close details" }));
  api.historyFailures.add(temperature.entity_id);
  api.entities.get(temperature.entity_id)!.state = { value: system === "us" ? 68 : 20, observed_at: "2026-11-09T10:01:00Z" };
  await act(() => vi.advanceTimersByTimeAsync(6 * 60_000));
  expect(within(card("Outside")).getByText("Couldn't refresh temperature history")).toBeTruthy();
  expect(within(card("Outside")).getByText(`Last loaded 24 h: ${highLow}`)).toBeTruthy();
  expect(card("Outside").querySelector(".view-metric-value")!.textContent).toBe(system === "metric" ? "20.0°C" : "68.0°F");
  fireEvent.click(within(card("Outside")).getByRole("button", { name: "Source" }));
  expect(interval()).toEqual(loaded);
  fireEvent.click(screen.getByRole("button", { name: "Close details" }));
  api.historyFailures.clear();
  histories[101] = minutes => system === "us" ? (minutes > 600 ? 71.6 : 68) : (minutes > 600 ? 22 : 20);
  await act(() => vi.advanceTimersByTimeAsync(60_000));
  expect(within(card("Outside")).queryByText("Couldn't refresh temperature history")).toBeNull();
  expect(within(card("Outside")).getByText(system === "metric" ? "24 h high 22.0, low 20.0 °C" : "24 h high 71.6, low 68.0 °F")).toBeTruthy();
  expect(api.posts).toHaveLength(0);
});
