import { StrictMode, type ReactNode } from "react";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import examples from "../../../../../../contracts/views/v1/examples.json";
import { ViewDataProvider } from "../../data/provider";
import { UnitSystemOverride } from "../../../units/household";
import type { UnitSystem } from "../../../units/units";
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

function entity(number: number, type: string, value: number | null, unit?: string, availability = "available"): FieldEntity {
  return {
    id: id(number), name: `Sensor ${number}`, type, enabled: true, availability: { status: availability },
    support: unit ? { state: { minimum: 0, maximum: 10000, unit }, operations: {} } : { state: {}, operations: {} },
    state: value === null ? null : { value, observed_at: at },
  };
}

/** The shared example's station during a rainstorm, in canonical units. */
function station(overrides: Record<number, Partial<FieldEntity>> = {}): FieldEntity[] {
  const sensor = "hearth.numericsensor/v1";
  return [
    entity(101, "hearth.temperature/v1", 9400), entity(102, "hearth.relativehumidity/v1", 94), entity(103, "hearth.speed/v1", 6.3),
    entity(104, sensor, 135, "deg"), entity(105, sensor, 6.8, "mm/h"), entity(106, sensor, 16.2, "mm"), entity(107, sensor, 18.6, "mm"),
    entity(108, sensor, 22.9, "mm"), entity(109, sensor, 71.4, "mm"), entity(110, sensor, 688.1, "mm"),
    entity(111, "hearth.speed/v1", 11.2), entity(112, "hearth.speed/v1", 14.8), entity(113, "hearth.pressure/v1", 1003.2),
  ].map(item => ({ ...item, ...overrides[Number(item.id.slice(-3))] }));
}

/** HTTP-shaped Core double. History defaults to the current value in every bucket. */
function core(entities: FieldEntity[], histories: Record<number, History> = {}) {
  const byId = new Map(entities.map(item => [item.id, item]));
  const historyFailures = new Set<string>();
  const posts: string[] = [];
  const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const url = String(input), entityId = url.split("/")[3], found = byId.get(entityId);
    if (url.endsWith("/commands")) { posts.push(entityId); return response({}, 500); }
    if (url.endsWith("/state/history")) {
      if (historyFailures.has(entityId)) return response({ detail: "History unavailable" }, 503);
      const body = JSON.parse(String(init?.body));
      const step = ({ "1m0s": 60000, "5m0s": 300000, "30m0s": 1800000 } as Record<string, number>)[body.result.bucket.duration];
      const from = Date.parse(body.range.from), to = Date.parse(body.range.to), count = Math.ceil((to - from) / step);
      const history = histories[Number(entityId.slice(-3))] ?? (() => Number(found?.state?.value ?? 0));
      const buckets = Array.from({ length: count }, (_, index) => {
        const start = from + index * step, value = history(Math.round((to - start) / 60000));
        return { range: { from: new Date(start).toISOString(), to: new Date(Math.min(to, start + step)).toISOString() }, metrics: { min: value, max: value, mean: value } };
      });
      return response({ range: body.range, buckets });
    }
    return response(found ?? {}, found ? 200 : 404);
  });
  return { posts, historyFailures, entities: byId };
}

const flush = () => act(() => vi.advanceTimersByTimeAsync(0));
async function mount(children: ReactNode, system: UnitSystem = "metric") {
  render(<StrictMode><UnitSystemOverride.Provider value={system}><ViewDataProvider>{children}</ViewDataProvider></UnitSystemOverride.Provider></StrictMode>, { wrapper: MemoryRouter });
  await flush(); await flush();
}
const card = (name: string) => screen.getByRole("article", { name });
beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(new Date("2026-11-09T10:00:30Z")); });
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.useRealTimers(); });

it("renders the shared weather example in household units without sending Commands", async () => {
  const api = core(station(), { 101: minutes => 9400 + (minutes > 600 ? 1500 : 0) });
  await mount(<ViewRenderer headingLevel={3} definition={weather} />, "us");
  expect(screen.getAllByRole("article")).toHaveLength(4);
  const outside = card("Outside");
  expect(outside.querySelector(".view-metric-value")!.textContent).toBe("48.9°F");
  expect(within(outside).getByText("Rain · Breezy")).toBeTruthy();
  // 9.4 °C with 6.3 m/s wind is 43.5 °F by NWS wind chill; dew point from 94 % humidity.
  expect(within(outside).getByText("Feels like 43.5 °F · Dew point 47.3 °F")).toBeTruthy();
  expect(within(outside).getByText("24 h high 51.6, low 48.9 °F")).toBeTruthy();
  expect(within(outside).getByText("Wind from SE")).toBeTruthy();
  expect(within(card("Wind")).getByText("Moderate breeze")).toBeTruthy();
  expect(within(card("Wind")).getByText("Gusts 25.1 mi/h")).toBeTruthy();
  expect(card("Pressure").querySelector(".view-metric-value")!.textContent).toMatch(/^29\.62in\sHg$/u);
  // Rain stays in millimetres in both unit systems.
  expect(within(card("Rain")).getByText("This storm · Rain now, 6.8 mm/h")).toBeTruthy();
  await act(() => vi.advanceTimersByTimeAsync(35000));
  expect(api.posts).toHaveLength(0);
});

it("leads with the storm total, then the rate or Dry, and keeps totals as the station's counters", async () => {
  const rate = { kind: "entity-state" as const, entity_id: id(105), entity_type: "hearth.numericsensor/v1" as const };
  const total = (number: number) => ({ ...rate, entity_id: id(number) });
  const props = { rate, storm: total(107), totals: { day: total(106), week: total(108), month: total(109), year: total(110) } };
  core(station());
  await mount(<BoundRain {...props} />);
  expect(card("Rain").querySelector(".view-metric-value")!.textContent).toBe("18.6mm");
  expect(card("Rain").querySelector(".view-weather-inline")!.textContent).toBe("Today 16.2 · This week 22.9 · This month 71.4 · This year 688.1 mm");
  cleanup(); vi.restoreAllMocks();

  core(station({ 105: { state: { value: 0, observed_at: at } }, 107: { state: { value: 0, observed_at: at } } }));
  await mount(<BoundRain {...props} />);
  expect(card("Rain").querySelector(".view-metric-value")!.textContent).toBe("Dry");
  expect(within(card("Rain")).getByText("Rate 0.0 mm/h")).toBeTruthy();
  cleanup(); vi.restoreAllMocks();

  core(station({ 107: { state: { value: 0, observed_at: at } } }));
  await mount(<BoundRain {...props} />);
  expect(card("Rain").querySelector(".view-metric-value")!.textContent).toBe("6.8mm/h");
  expect(card("Rain").querySelector(".view-weather-conditions")!.textContent).toBe("Rain");
});

it("states one shared status for totals from a station that stopped reporting", async () => {
  const rate = { kind: "entity-state" as const, entity_id: id(105), entity_type: "hearth.numericsensor/v1" as const };
  const total = (number: number) => ({ ...rate, entity_id: id(number) });
  const unavailable = { availability: { status: "unavailable" } };
  core(station({ 106: unavailable, 108: unavailable, 109: unavailable }));
  await mount(<BoundRain rate={rate} totals={{ day: total(106), week: total(108), month: total(109) }} />);
  const statuses = card("Rain").querySelectorAll(".view-weather-slot-status");
  expect([...statuses].map(status => status.textContent)).toEqual(["Rain totalsUnavailable"]);
  cleanup(); vi.restoreAllMocks();

  core(station({ 106: unavailable }));
  await mount(<BoundRain rate={rate} totals={{ day: total(106), week: total(108) }} />);
  expect([...card("Rain").querySelectorAll(".view-weather-slot-status")].map(status => status.textContent)).toEqual(["TodayUnavailable"]);
});

it("draws where the wind comes from, but no direction when calm or missing", async () => {
  const speed = { kind: "entity-state" as const, entity_id: id(103), entity_type: "hearth.speed/v1" as const };
  const direction = { kind: "entity-state" as const, entity_id: id(104), entity_type: "hearth.numericsensor/v1" as const };
  core(station());
  await mount(<BoundWind speed={speed} direction={direction} />);
  expect(within(card("Wind")).getByText(/From SE · 135°/)).toBeTruthy();
  expect(card("Wind").querySelector(".view-weather-compass-arrow")!.getAttribute("transform")).toBe("rotate(315)");
  cleanup(); vi.restoreAllMocks();

  core(station({ 103: { state: { value: 0.2, observed_at: at } } }));
  await mount(<BoundWind speed={speed} direction={direction} />);
  expect(within(card("Wind")).getByText("Calm")).toBeTruthy();
  expect(within(card("Wind")).queryByText(/From/)).toBeNull();
  expect(card("Wind").querySelector(".view-weather-compass-arrow")).toBeNull();
  cleanup(); vi.restoreAllMocks();

  core(station().filter(item => item.id !== id(104)));
  await mount(<BoundWind speed={speed} direction={direction} />);
  expect(within(card("Wind")).getByText("Wind direction")).toBeTruthy();
  expect(within(card("Wind")).getByText("Entity not found")).toBeTruthy();
  expect(card("Wind").querySelector(".view-weather-compass-arrow")).toBeNull();
});

it("words the three-hour pressure tendency, and never guesses without a reading from 3 h ago", async () => {
  const source = { kind: "entity-state" as const, entity_id: id(113), entity_type: "hearth.pressure/v1" as const };
  core(station(), { 113: minutes => 1003.2 + Math.min(minutes, 180) / 180 * 3.1 });
  await mount(<BoundPressure source={source} />);
  expect(within(card("Pressure")).getByText("Falling")).toBeTruthy();
  expect(within(card("Pressure")).getByText("−3.1 hPa in 3 h")).toBeTruthy();
  cleanup(); vi.restoreAllMocks();

  core(station(), { 113: minutes => minutes > 150 ? null : 1003.2 });
  await mount(<BoundPressure source={source} trend="24h" />);
  expect(within(card("Pressure")).getByText("Tendency unavailable · no recent or 3 h reading")).toBeTruthy();
  expect(card("Pressure").querySelector(".view-trend")).toBeTruthy();
  cleanup(); vi.restoreAllMocks();

  // A change that ended an hour ago is not the current tendency.
  core(station(), { 113: minutes => minutes < 60 ? null : 1003.2 + Math.min(minutes, 240) / 240 * 3.1 });
  await mount(<BoundPressure source={source} />);
  expect(within(card("Pressure")).getByText("Tendency unavailable · no recent or 3 h reading")).toBeTruthy();
});

it("dims derived values and totals whose readings are unavailable", async () => {
  const unavailable = { availability: { status: "unavailable" } };
  core(station({ 102: unavailable, 108: unavailable }));
  await mount(<ViewRenderer headingLevel={3} definition={weather} />);
  const derived = within(card("Outside")).getByText(/^Feels like .* · Dew point /);
  expect(derived.getAttribute("data-value")).toBe("dimmed");
  const week = within(card("Rain")).getByText(/This week 22\.9/);
  expect(week.getAttribute("data-value")).toBe("dimmed");
  expect(within(card("Rain")).getByText(/Today 16\.2/).getAttribute("data-value")).toBe("live");
});

it.each([undefined, "6h", "24h"] as const)("warns, expires cached tendency, and recovers while State stays live (trend %s)", async trend => {
  const source = { kind: "entity-state" as const, entity_id: id(113), entity_type: "hearth.pressure/v1" as const };
  const histories = { 113: (minutes: number) => 1003.2 + Math.min(minutes, 180) / 180 * 3.1 };
  const api = core(station(), histories);
  await mount(<BoundPressure source={source} trend={trend} />);
  expect(within(card("Pressure")).getByText("Falling")).toBeTruthy();

  api.historyFailures.add(source.entity_id);
  api.entities.get(source.entity_id)!.state = { value: 1004, observed_at: "2026-11-09T10:01:00Z" };
  await act(() => vi.advanceTimersByTimeAsync(60_000));
  expect(within(card("Pressure")).getByText("Couldn't refresh pressure history")).toBeTruthy();
  expect(card("Pressure").querySelector(".view-metric-value")!.textContent).toBe("1004.0hPa");
  // The last interval can still describe the tendency until its samples expire.
  expect(within(card("Pressure")).getByText("Falling")).toBeTruthy();

  // No successful history response arrives to move its cached window end forward.
  await act(() => vi.advanceTimersByTimeAsync(10 * 60_000));
  expect(card("Pressure").querySelector(".view-weather-tendency")).toBeNull();
  expect(within(card("Pressure")).getByText("Couldn't refresh pressure history")).toBeTruthy();
  expect(within(card("Pressure")).getByText("Tendency unavailable · no recent or 3 h reading")).toBeTruthy();

  api.historyFailures.clear();
  histories[113] = minutes => 1004 - Math.min(minutes, 180) / 180 * 2;
  await act(() => vi.advanceTimersByTimeAsync(60_000));
  expect(within(card("Pressure")).queryByText("Couldn't refresh pressure history")).toBeNull();
  expect(within(card("Pressure")).getByText("Rising")).toBeTruthy();
  expect(within(card("Pressure")).getByText("+2.0 hPa in 3 h")).toBeTruthy();
  expect(api.posts).toHaveLength(0);
});

it.each(["metric", "us"] as const)("identifies cached outdoor extrema and their interval through failure and recovery (%s)", async system => {
  const temperature = { kind: "entity-state" as const, entity_id: id(101), entity_type: "hearth.temperature/v1" as const };
  const histories: Record<number, History> = { 101: minutes => minutes > 600 ? 10900 : 9400 };
  const api = core(station(), histories);
  await mount(<BoundOutsideNow temperature={temperature} />, system);
  const highLow = system === "metric" ? "high 10.9, low 9.4 °C" : "high 51.6, low 48.9 °F";
  expect(within(card("Outside")).getByText(`24 h ${highLow}`)).toBeTruthy();

  function historyInterval() {
    return ["Temperature history from", "Temperature history to"].map(label =>
      screen.getByText(label).parentElement!.querySelector("time")!.dateTime);
  }
  fireEvent.click(within(card("Outside")).getByRole("button", { name: "Source" }));
  const loadedInterval = historyInterval();
  expect(loadedInterval).toEqual(["2026-11-08T10:05:00.000Z", "2026-11-09T10:00:30.000Z"]);
  fireEvent.click(screen.getByRole("button", { name: "Close details" }));

  api.historyFailures.add(temperature.entity_id);
  api.entities.get(temperature.entity_id)!.state = { value: 20000, observed_at: "2026-11-09T10:01:00Z" };
  await act(() => vi.advanceTimersByTimeAsync(6 * 60_000));
  expect(within(card("Outside")).getByText("Couldn't refresh temperature history")).toBeTruthy();
  expect(within(card("Outside")).getByText(`Last loaded 24 h: ${highLow}`)).toBeTruthy();
  expect(within(card("Outside")).queryByText(`24 h ${highLow}`)).toBeNull();
  expect(card("Outside").querySelector(".view-metric-value")!.textContent).toBe(system === "metric" ? "20.0°C" : "68.0°F");
  fireEvent.click(within(card("Outside")).getByRole("button", { name: "Source" }));
  expect(historyInterval()).toEqual(loadedInterval);
  fireEvent.click(screen.getByRole("button", { name: "Close details" }));

  api.historyFailures.clear();
  histories[101] = minutes => minutes > 600 ? 22000 : 20000;
  await act(() => vi.advanceTimersByTimeAsync(60_000));
  expect(within(card("Outside")).queryByText("Couldn't refresh temperature history")).toBeNull();
  expect(within(card("Outside")).getByText(system === "metric" ? "24 h high 22.0, low 20.0 °C" : "24 h high 71.6, low 68.0 °F")).toBeTruthy();
  fireEvent.click(within(card("Outside")).getByRole("button", { name: "Source" }));
  const refreshedInterval = historyInterval();
  expect(Date.parse(refreshedInterval[0])).toBeGreaterThan(Date.parse(loadedInterval[0]));
  expect(Date.parse(refreshedInterval[1])).toBeGreaterThan(Date.parse(loadedInterval[1]));
  expect(api.posts).toHaveLength(0);
});
