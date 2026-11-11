import { StrictMode, type ReactNode } from "react";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import examples from "../../../../../../contracts/views/v1/examples.json";
import { ViewDataProvider } from "../../data/provider";
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
type UnitSystem = "metric" | "us";
type State = NonNullable<FieldEntity["state"]>;

const fixedUnits: Record<string, string> = { "hearth.temperature/v1": "mCel", "hearth.relativehumidity/v1": "%", "hearth.speed/v1": "m/s", "hearth.pressure/v1": "hPa" };
type Projection = { unit: string; label: string; fraction_digits: number; convert: (value: number) => number };
const same = (unit: string, label: string): Projection => ({ unit, label, fraction_digits: 1, convert: value => value });
const shared: Record<string, Projection> = { "%": same("%", "%"), deg: same("deg", "°") };
/** Core's household projections of the station's canonical units. */
const projections: Record<UnitSystem, Record<string, Projection>> = {
  metric: { ...shared, mCel: { ...same("Cel", "°C"), convert: value => value / 1000 }, "m/s": same("m/s", "m/s"), hPa: same("hPa", "hPa"), "mm/h": same("mm/h", "mm/h"), mm: same("mm", "mm") },
  us: {
    ...shared,
    mCel: { unit: "[degF]", label: "°F", fraction_digits: 1, convert: value => value * 9 / 5000 + 32 },
    "m/s": { unit: "[mi_i]/h", label: "mi/h", fraction_digits: 1, convert: value => value * 3600 / 1609.344 },
    hPa: { unit: "[in_i'Hg]", label: "in Hg", fraction_digits: 2, convert: value => value / 33.863788 },
    "mm/h": { unit: "[in_i]/h", label: "in/h", fraction_digits: 2, convert: value => value / 25.4 },
    mm: { unit: "[in_i]", label: "in", fraction_digits: 2, convert: value => value / 25.4 },
  },
};

function entity(number: number, type: string, value: number | null, unit?: string, quantity_kind?: string): FieldEntity {
  return {
    id: id(number), name: `Sensor ${number}`, type, enabled: true, availability: { status: "available" },
    support: unit ? { state: { minimum: 0, maximum: 10000, unit, ...(quantity_kind ? { quantity_kind } : {}) }, operations: {} } : { state: {}, operations: {} },
    state: value === null ? null : { value, observed_at: at },
  };
}

/** The shared example's station during a rainstorm, in canonical units. */
function station(overrides: Record<number, Partial<FieldEntity>> = {}): FieldEntity[] {
  const sensor = "hearth.numericsensor/v1";
  return [
    entity(101, "hearth.temperature/v1", 9400), entity(102, "hearth.relativehumidity/v1", 94), entity(103, "hearth.speed/v1", 6.3),
    entity(104, sensor, 135, "deg", "wind_direction"), entity(105, sensor, 6.8, "mm/h", "precipitation_rate"),
    ...([[106, 16.2], [107, 18.6], [108, 22.9], [109, 71.4], [110, 688.1]] as const).map(([number, value]) => entity(number, sensor, value, "mm", "precipitation_depth")),
    entity(111, "hearth.speed/v1", 11.2), entity(112, "hearth.speed/v1", 14.8), entity(113, "hearth.pressure/v1", 1003.2),
  ].map(item => ({ ...item, ...overrides[Number(item.id.slice(-3))] }));
}

/**
 * HTTP-shaped Core double. It stores canonical State and history, projects
 * them into the household system by default, and returns them unchanged for
 * `units=canonical`. Like Core, it projects a generic sensor only when the
 * sensor declares a quantity kind. History defaults to the current value in
 * every bucket.
 */
function core(entities: FieldEntity[], histories: Record<number, History> = {}, system: UnitSystem = "metric") {
  const byId = new Map(entities.map(item => [item.id, item]));
  const historyFailures = new Set<string>(), householdHistoryFailures = new Set<string>();
  const canonicalFailures = new Set<string>(), canonicalStates = new Map<string, State>();
  const posts: string[] = [], requests: string[] = [];
  const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
  const present = (item: FieldEntity, canonical: boolean) => {
    const support = (item.support as { state: { unit?: string; quantity_kind?: string } }).state;
    const unit = fixedUnits[item.type] ?? support.unit!;
    const projection = fixedUnits[item.type] || support.quantity_kind ? projections[system][unit] : same(unit, unit);
    return {
      quantity: canonical ? { unit, label: unit, fraction_digits: 1 } : { unit: projection.unit, label: projection.label, fraction_digits: projection.fraction_digits },
      value: (value: number | null) => canonical || value === null ? value : projection.convert(value),
    };
  };
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const url = String(input), [path, query] = url.split("?"), canonical = query === "units=canonical";
    const entityId = path.split("/")[3], found = byId.get(entityId);
    requests.push(url);
    if (path.endsWith("/commands")) { posts.push(entityId); return response({}, 500); }
    if (canonical && canonicalFailures.has(entityId)) return response({ detail: "Canonical read unavailable" }, 503);
    if (path.endsWith("/state/history")) {
      if (historyFailures.has(entityId) || (!canonical && householdHistoryFailures.has(entityId))) return response({ detail: "History unavailable" }, 503);
      if (!found) return response({}, 404);
      const body = JSON.parse(String(init?.body)), shown = present(found, canonical);
      const step = ({ "1m0s": 60000, "5m0s": 300000, "30m0s": 1800000 } as Record<string, number>)[body.result.bucket.duration];
      const from = Date.parse(body.range.from), to = Date.parse(body.range.to), count = Math.ceil((to - from) / step);
      const history = histories[Number(entityId.slice(-3))] ?? (() => Number(found.state?.value ?? 0));
      const buckets = Array.from({ length: count }, (_, index) => {
        const start = from + index * step, value = shown.value(history(Math.round((to - start) / 60000)));
        return { range: { from: new Date(start).toISOString(), to: new Date(Math.min(to, start + step)).toISOString() }, metrics: { min: value, max: value, mean: value } };
      });
      return response({ range: body.range, quantity: shown.quantity, buckets });
    }
    if (!found) return response({}, 404);
    const shown = present(found, canonical), state = (canonical && canonicalStates.get(entityId)) || found.state;
    return response({ ...found, quantities: { "": shown.quantity }, state: state && { ...state, value: shown.value(state.value as number) } });
  });
  return { posts, requests, historyFailures, householdHistoryFailures, canonicalFailures, canonicalStates, entities: byId };
}

const flush = () => act(() => vi.advanceTimersByTimeAsync(0));
async function mount(children: ReactNode) {
  render(<StrictMode><ViewDataProvider>{children}</ViewDataProvider></StrictMode>, { wrapper: MemoryRouter });
  await flush(); await flush();
}
const card = (name: string) => screen.getByRole("article", { name });
const source = <T extends string>(number: number, type: T) => ({ kind: "entity-state" as const, entity_id: id(number), entity_type: type });
beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(new Date("2026-11-09T10:00:30Z")); });
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.useRealTimers(); });

it.each(["metric", "us"] as const)("words the shared weather example from canonical readings in a %s household", async system => {
  const api = core(station(), { 101: minutes => 9400 + (minutes > 600 ? 1500 : 0) }, system);
  await mount(<ViewRenderer headingLevel={3} definition={weather} />);
  const us = system === "us";
  expect(screen.getAllByRole("article")).toHaveLength(4);
  const outside = card("Outside");
  expect(outside.querySelector(".view-metric-value")!.textContent).toBe(us ? "48.9°F" : "9.4°C");
  // 6.8 mm/h is Rain and 6.3 m/s is force 4, whatever unit the card shows.
  expect(within(outside).getByText("Rain · Breezy")).toBeTruthy();
  expect(within(outside).getByText(us ? "24 h high 51.6, low 48.9 °F" : "24 h high 10.9, low 9.4 °C")).toBeTruthy();
  expect(within(outside).getByText("Wind from SE")).toBeTruthy();
  expect(within(card("Wind")).getByText("Moderate breeze")).toBeTruthy();
  expect(within(card("Wind")).getByText(us ? "Gusts 25.1 mi/h" : "Gusts 11.2 m/s")).toBeTruthy();
  expect(card("Pressure").querySelector(".view-metric-value")!.textContent).toMatch(us ? /^29\.62in\sHg$/u : /^1003\.2hPa$/u);
  expect(within(card("Rain")).getByText(us ? "This storm · Rain now, 0.27 in/h" : "This storm · Rain now, 6.8 mm/h")).toBeTruthy();
  expect(card("Rain").querySelector(".view-metric-value")!.textContent).toBe(us ? "0.73in" : "18.6mm");
  expect(document.body.textContent).not.toMatch(/Feels like|Dew point/);
  // Only formula inputs are read canonically: wind speed, rain rate, and pressure history.
  const canonical = new Set(api.requests.filter(url => url.endsWith("?units=canonical")).map(url => url.split("?")[0].split("/")[3].slice(-3) + (url.includes("/state/history") ? " history" : "")));
  expect([...canonical].sort()).toEqual(["103", "105", "113", "113 history"]);
  await act(() => vi.advanceTimersByTimeAsync(35000));
  expect(api.posts).toHaveLength(0);
});

it("shows readings without words when canonical reads fail or describe another observation", async () => {
  const api = core(station(), {}, "us");
  api.canonicalFailures.add(id(105));
  api.canonicalStates.set(id(103), { value: 0.2, observed_at: "2026-11-09T09:55:00Z" });
  await mount(<ViewRenderer headingLevel={3} definition={weather} />);
  expect(card("Wind").querySelector(".view-metric-value")!.textContent).toBe("14.1mi/h");
  expect(within(card("Wind")).queryByText(/Calm|Moderate breeze/)).toBeNull();
  expect(card("Wind").querySelector(".view-weather-compass-arrow")).toBeTruthy();
  expect(card("Outside").querySelector(".view-weather-conditions")).toBeNull();
  // Without their words, the rate and direction keep their own rows.
  expect(within(card("Outside")).getByText("Wind")).toBeTruthy();
  expect(within(card("Outside")).getByText("Wind direction")).toBeTruthy();
  expect(within(card("Outside")).getByText("Rain rate")).toBeTruthy();
  expect(card("Outside").querySelector(".view-weather-rows")!.textContent).toContain("0.27in/h");
  expect(within(card("Rain")).getByText("This storm · Rate 0.27 in/h")).toBeTruthy();

  api.canonicalFailures.clear(); api.canonicalStates.clear();
  await act(() => vi.advanceTimersByTimeAsync(10_000));
  expect(within(card("Wind")).getByText("Moderate breeze")).toBeTruthy();
  expect(within(card("Outside")).getByText("Rain · Breezy")).toBeTruthy();
  expect(within(card("Outside")).getByText("Wind from SE")).toBeTruthy();
  expect(within(card("Outside")).queryByText("Wind direction")).toBeNull();
  expect(within(card("Outside")).queryByText("Rain rate")).toBeNull();
  expect(within(card("Rain")).getByText("This storm · Rain now, 0.27 in/h")).toBeTruthy();
});

it("leads with the storm total, then the rate or Dry, and keeps totals as the station's counters", async () => {
  const rate = source(105, "hearth.numericsensor/v1");
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

it("labels each total when Core projects the counters into different units", async () => {
  const rate = source(105, "hearth.numericsensor/v1");
  const total = (number: number) => ({ ...rate, entity_id: id(number) });
  const unclassified = { support: { state: { minimum: 0, maximum: 10000, unit: "mm" }, operations: {} } };
  core(station({ 108: unclassified }), {}, "us");
  await mount(<BoundRain rate={rate} totals={{ day: total(106), week: total(108) }} />);
  expect(card("Rain").querySelector(".view-weather-inline")!.textContent).toBe("Today 0.64 in · This week 22.9 mm");
  cleanup(); vi.restoreAllMocks();

  core(station(), {}, "us");
  await mount(<BoundRain rate={rate} totals={{ day: total(106), week: total(108) }} />);
  expect(card("Rain").querySelector(".view-weather-inline")!.textContent).toBe("Today 0.64 · This week 0.90 in");
});

it("states one shared status for totals from a station that stopped reporting", async () => {
  const rate = source(105, "hearth.numericsensor/v1");
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

it.each(["unavailable", "disabled"].flatMap(state => [
  { state, mode: "storm rain", stormValue: 18.6, rateValue: 6.8, number: 105 },
  { state, mode: "storm stopped", stormValue: 18.6, rateValue: 0, number: 105 },
  { state, mode: "rate only", stormValue: 0, rateValue: 6.8, number: 105 },
  { state, mode: "storm total", stormValue: 18.6, rateValue: 6.8, number: 107 },
]))("dims retained Rain conditions for $state $mode and restores them on recovery", async ({ state, stormValue, rateValue, number }) => {
  const rate = source(105, "hearth.numericsensor/v1");
  const storm = { ...rate, entity_id: id(107) };
  const api = core(station({
    105: { state: { value: rateValue, observed_at: at } },
    107: { state: { value: stormValue, observed_at: at } },
  }));
  const failed = api.entities.get(id(number))!;
  if (state === "unavailable") failed.availability = { status: "unavailable" };
  else failed.enabled = false;
  await mount(<BoundRain rate={rate} storm={storm} />);

  const conditions = card("Rain").querySelector(".view-weather-conditions")!;
  expect(conditions.textContent).toBe(stormValue > 0
    ? rateValue > 0 ? "This storm · Rain now, 6.8 mm/h" : "This storm · Stopped for now"
    : "Rain");
  expect(conditions.getAttribute("data-value")).toBe("dimmed");
  expect(within(card("Rain")).getByText(state === "unavailable" ? "Unavailable" : "Disabled")).toBeTruthy();

  failed.availability = { status: "available" };
  failed.enabled = true;
  await act(() => vi.advanceTimersByTimeAsync(10000));
  expect(card("Rain").querySelector(".view-weather-conditions")!.getAttribute("data-value")).toBe("live");
  expect(within(card("Rain")).queryByText(state === "unavailable" ? "Unavailable" : "Disabled")).toBeNull();
  expect(api.posts).toHaveLength(0);
});

it("keeps speed, gusts, Beaufort words, and direction in reading order", async () => {
  const api = core(station());
  await mount(<BoundWind speed={source(103, "hearth.speed/v1")} gust={source(111, "hearth.speed/v1")} direction={source(104, "hearth.numericsensor/v1")} />);
  const text = card("Wind").textContent!;
  const order = ["6.3m/s", "Gusts 11.2 m/s", "Moderate breeze", "From SE"].map(reading => text.indexOf(reading));
  expect(order.every(index => index >= 0)).toBe(true);
  expect(order).toEqual([...order].sort((a, b) => a - b));
  expect(card("Wind").querySelector(".view-trend")).toBeNull();
  expect(api.requests.some(url => url.includes("/state/history"))).toBe(false);
});

it.each(["metric", "us"] as const)("shows household Wind speed history with gaps and a Source method (%s)", async system => {
  const api = core(station(), { 103: minutes => minutes >= 200 && minutes <= 240 ? null : minutes > 600 ? 9.4 : 6.3 }, system);
  await mount(<BoundWind speed={source(103, "hearth.speed/v1")} gust={source(111, "hearth.speed/v1")} direction={source(104, "hearth.numericsensor/v1")} trend="24h" />);
  const wind = card("Wind");
  expect(within(wind).getByText(system === "us" ? "24 h range 14.1–21.0 mi/h" : "24 h range 6.3–9.4 m/s")).toBeTruthy();
  expect(wind.querySelector(".view-trend-mean")!.getAttribute("d")!.match(/M/g)).toHaveLength(2);
  expect(wind.querySelector(".view-trend-band")).toBeTruthy();
  expect(wind.querySelector(".view-weather-main .view-weather-wind")).toBeTruthy();
  expect(wind.querySelector(".view-weather-aside .view-trend")).toBeTruthy();
  const histories = api.requests.filter(url => url.includes("/state/history"));
  expect(histories.length).toBeGreaterThan(0);
  expect(histories.every(url => url === `/v1/entities/${id(103)}/state/history`)).toBe(true);
  fireEvent.click(within(wind).getByRole("button", { name: "Source" }));
  expect(screen.getByText("The trend shows sustained wind speed, not gusts or direction.")).toBeTruthy();
  expect(screen.getByText(/5-minute interval; the shaded band/)).toBeTruthy();
  expect(api.posts).toHaveLength(0);
});

it.each(["empty", "failed"] as const)("keeps current Wind readings when history is %s", async state => {
  const api = core(station(), { 103: () => null });
  if (state === "failed") api.historyFailures.add(id(103));
  await mount(<BoundWind speed={source(103, "hearth.speed/v1")} trend="24h" />);
  expect(card("Wind").querySelector(".view-metric-value")!.textContent).toBe("6.3m/s");
  expect(within(card("Wind")).getByText("Moderate breeze")).toBeTruthy();
  expect(within(card("Wind")).getByText(state === "empty" ? "No history in the last 24 h." : "Couldn't load history.")).toBeTruthy();
  expect(card("Wind").querySelector(".view-trend-mean")).toBeNull();
});

it("retains Wind history through refresh failure and restores it without hiding current State", async () => {
  const histories: Record<number, History> = { 103: minutes => minutes > 600 ? 9.4 : 6.3 };
  const api = core(station(), histories);
  await mount(<BoundWind speed={source(103, "hearth.speed/v1")} trend="24h" />);
  expect(within(card("Wind")).getByText("24 h range 6.3–9.4 m/s")).toBeTruthy();
  api.historyFailures.add(id(103));
  api.entities.get(id(103))!.state = { value: 7, observed_at: "2026-11-09T10:01:00Z" };
  await act(() => vi.advanceTimersByTimeAsync(60_000));
  expect(card("Wind").querySelector(".view-metric-value")!.textContent).toBe("7.0m/s");
  expect(within(card("Wind")).getByText("Couldn't refresh history. Showing the last interval.")).toBeTruthy();
  expect(within(card("Wind")).getByText("24 h range 6.3–9.4 m/s")).toBeTruthy();
  api.historyFailures.clear(); histories[103] = () => 7;
  await act(() => vi.advanceTimersByTimeAsync(60_000));
  expect(within(card("Wind")).queryByText(/Couldn't refresh history/)).toBeNull();
  expect(within(card("Wind")).getByText("24 h range 7.0–7.0 m/s")).toBeTruthy();
  expect(api.posts).toHaveLength(0);
});

it("draws where the wind comes from, but no direction when calm or missing", async () => {
  const speed = source(103, "hearth.speed/v1");
  const direction = source(104, "hearth.numericsensor/v1");
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

it.each([
  { slot: "speed", state: "unavailable", number: 103 },
  { slot: "direction", state: "unavailable", number: 104 },
  { slot: "speed", state: "disabled", number: 103 },
  { slot: "direction", state: "disabled", number: 104 },
])("dims wind descriptions for $state $slot and restores them on recovery", async ({ slot, state, number }) => {
  const speed = source(103, "hearth.speed/v1");
  const direction = source(104, "hearth.numericsensor/v1");
  const api = core(station());
  const failed = api.entities.get(id(number))!;
  if (state === "unavailable") failed.availability = { status: "unavailable" };
  else failed.enabled = false;
  await mount(<BoundWind speed={speed} direction={direction} trend="24h" />);

  expect(card("Wind").querySelector(".view-trend")!.getAttribute("data-value")).toBe(slot === "speed" ? "dimmed" : "live");
  expect(!!card("Wind").querySelector(".view-trend-band")).toBe(slot !== "speed");
  expect(within(card("Wind")).getByText("Moderate breeze").getAttribute("data-value")).toBe(slot === "speed" ? "dimmed" : "live");
  expect(within(card("Wind")).getByText(/From SE · 135°/).getAttribute("data-value")).toBe("dimmed");
  expect(within(card("Wind")).getByText(state === "unavailable" ? "Unavailable" : "Disabled")).toBeTruthy();
  expect(card("Wind").querySelector(".view-metric-value")!.textContent).toBe("6.3m/s");

  failed.availability = { status: "available" };
  failed.enabled = true;
  await act(() => vi.advanceTimersByTimeAsync(10000));
  expect(card("Wind").querySelector(".view-trend")!.getAttribute("data-value")).toBe("live");
  expect(card("Wind").querySelector(".view-trend-band")).toBeTruthy();
  expect(within(card("Wind")).getByText("Moderate breeze").getAttribute("data-value")).toBe("live");
  expect(within(card("Wind")).getByText(/From SE · 135°/).getAttribute("data-value")).toBe("live");
  expect(within(card("Wind")).queryByText(state === "unavailable" ? "Unavailable" : "Disabled")).toBeNull();
  expect(api.posts).toHaveLength(0);
});

it("words the three-hour pressure tendency, and never guesses without a reading from 3 h ago", async () => {
  const pressure = source(113, "hearth.pressure/v1");
  core(station(), { 113: minutes => 1003.2 + Math.min(minutes, 180) / 180 * 3.1 });
  await mount(<BoundPressure source={pressure} />);
  expect(within(card("Pressure")).getByText("Falling")).toBeTruthy();
  expect(within(card("Pressure")).getByText("−3.1 hPa in 3 h")).toBeTruthy();
  cleanup(); vi.restoreAllMocks();

  core(station(), { 113: minutes => minutes > 150 ? null : 1003.2 });
  await mount(<BoundPressure source={pressure} trend="24h" />);
  expect(within(card("Pressure")).getByText("Tendency unavailable · no recent or 3 h reading")).toBeTruthy();
  expect(card("Pressure").querySelector(".view-trend")).toBeTruthy();
  cleanup(); vi.restoreAllMocks();

  // A change that ended an hour ago is not the current tendency.
  core(station(), { 113: minutes => minutes < 60 ? null : 1003.2 + Math.min(minutes, 240) / 240 * 3.1 });
  await mount(<BoundPressure source={pressure} />);
  expect(within(card("Pressure")).getByText("Tendency unavailable · no recent or 3 h reading")).toBeTruthy();
});

it("words a US pressure tendency from hPa and shows the change in inches of mercury", async () => {
  // −3.1 hPa is Falling; the same change in in Hg would read as Steady.
  core(station(), { 113: minutes => 1003.2 + Math.min(minutes, 180) / 180 * 3.1 }, "us");
  await mount(<BoundPressure source={source(113, "hearth.pressure/v1")} />);
  expect(within(card("Pressure")).getByText("Falling")).toBeTruthy();
  expect(within(card("Pressure")).getByText("−0.09 in Hg in 3 h")).toBeTruthy();
});

it("dims conditions and totals whose readings are unavailable", async () => {
  const unavailable = { availability: { status: "unavailable" } };
  core(station({ 103: unavailable, 108: unavailable }));
  await mount(<ViewRenderer headingLevel={3} definition={weather} />);
  expect(within(card("Outside")).getByText("Rain · Breezy").getAttribute("data-value")).toBe("dimmed");
  const week = within(card("Rain")).getByText(/This week 22\.9/);
  expect(week.getAttribute("data-value")).toBe("dimmed");
  expect(within(card("Rain")).getByText(/Today 16\.2/).getAttribute("data-value")).toBe("live");
});

it("dims conditions only with the readings that produced words", async () => {
  core(station({ 105: { state: { value: 0, observed_at: at }, availability: { status: "unavailable" } } }));
  await mount(<ViewRenderer headingLevel={3} definition={weather} />);
  expect(within(card("Outside")).getByText("Breezy").getAttribute("data-value")).toBe("live");
});

it("withholds the tendency while the two pressure histories use different buckets", async () => {
  const pressure = source(113, "hearth.pressure/v1");
  const api = core(station(), { 113: minutes => 1003.2 + Math.min(minutes, 180) / 180 * 3.1 });
  await mount(<BoundPressure source={pressure} />);
  expect(within(card("Pressure")).getByText("Falling")).toBeTruthy();

  // Only the household history fails. One minute later both still share their
  // buckets; after two, the canonical history has moved to later buckets.
  api.householdHistoryFailures.add(pressure.entity_id);
  await act(() => vi.advanceTimersByTimeAsync(60_000));
  expect(within(card("Pressure")).getByText("Falling")).toBeTruthy();
  await act(() => vi.advanceTimersByTimeAsync(60_000));
  expect(within(card("Pressure")).getByText("Couldn't refresh pressure history")).toBeTruthy();
  expect(within(card("Pressure")).getByText("Tendency unavailable · no recent or 3 h reading")).toBeTruthy();

  api.householdHistoryFailures.clear();
  await act(() => vi.advanceTimersByTimeAsync(60_000));
  expect(within(card("Pressure")).getByText("Falling")).toBeTruthy();
  expect(within(card("Pressure")).getByText("−3.1 hPa in 3 h")).toBeTruthy();
});

it.each([undefined, "6h", "24h"] as const)("warns, expires cached tendency, and recovers while State stays live (trend %s)", async trend => {
  const pressure = source(113, "hearth.pressure/v1");
  const histories = { 113: (minutes: number) => 1003.2 + Math.min(minutes, 180) / 180 * 3.1 };
  const api = core(station(), histories);
  await mount(<BoundPressure source={pressure} trend={trend} />);
  expect(within(card("Pressure")).getByText("Falling")).toBeTruthy();

  api.historyFailures.add(pressure.entity_id);
  api.entities.get(pressure.entity_id)!.state = { value: 1004, observed_at: "2026-11-09T10:01:00Z" };
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
  const temperature = source(101, "hearth.temperature/v1");
  const histories: Record<number, History> = { 101: minutes => minutes > 600 ? 10900 : 9400 };
  const api = core(station(), histories, system);
  await mount(<BoundOutsideNow temperature={temperature} />);
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
