import { StrictMode, type ReactNode } from "react";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { ViewDataProvider } from "../../data/provider";
import { ViewRenderer } from "../../renderer/ViewRenderer";
import { BoundForecast } from "./Forecast";
import { ForecastEditor } from "./Editor";
import { forecastTypes } from "./schema";
import { BoundPressure } from "../pressure/Pressure";

const id = "ent_01900000-0000-7000-8000-000000000201";
const source = <T extends "hourly" | "daily">(type: T) => ({ kind: "entity-state" as const, entity_id: id, entity_type: forecastTypes[type] });
const at = "2026-11-09T10:00:00Z";
function entity(type: "hourly" | "daily", value: unknown, unit: "metric" | "us" = "metric") {
  const quantities: Record<string, { unit: string; label: string; fraction_digits: number }> = {};
  const periods = (value as { periods?: Record<string, unknown>[] }).periods ?? [];
  periods.forEach((period, i) => Object.entries(period).forEach(([field, number]) => {
    if (typeof number !== "number") return;
    quantities[`/periods/${i}/${field}`] = field.startsWith("temperature") || field === "apparent_temperature"
      ? { unit: unit === "us" ? "[degF]" : "Cel", label: unit === "us" ? "°F" : "°C", fraction_digits: 0 }
      : { unit: "%", label: "%", fraction_digits: 0 };
  }));
  return { id, name: "Forecast", type: forecastTypes[type], enabled: true, availability: { status: "available" }, quantities, state: { value, observed_at: at } };
}
const daily = { periods: [
  { starts_at: "2026-11-09T00:00:00Z", date: "2026-11-09", condition: "rain", temperature_min: 0, temperature_max: 10, precipitation_probability: 9 },
  { starts_at: "2026-11-10T00:00:00Z", date: "2026-11-10", condition: "snow", temperature_min: -5, temperature_max: 5, precipitation_probability: 90 },
  { starts_at: "2026-11-11T00:00:00Z", date: "2026-11-11", condition: "fog", temperature_max: 7 },
] };
const hourly = { periods: Array.from({ length: 6 }, (_, i) => ({ starts_at: new Date(Date.parse(at) + i * 3600000).toISOString(), condition: "rain", ...(i === 2 ? {} : { temperature: i }), precipitation_probability: i === 0 ? 9 : 50 })) };
const flush = () => act(() => vi.advanceTimersByTimeAsync(0));
async function mount(children: ReactNode) {
  render(<StrictMode><ViewDataProvider>{children}</ViewDataProvider></StrictMode>, { wrapper: MemoryRouter });
  await flush(); await flush();
}
function core(initial: ReturnType<typeof entity>) {
  let current: ReturnType<typeof entity> | null = initial, failure = 0;
  const fetcher = vi.spyOn(globalThis, "fetch").mockImplementation(async () => new Response(JSON.stringify(current ?? { detail: "missing" }), { status: failure || (current ? 200 : 404) }));
  return { fetcher, set: (next: ReturnType<typeof entity> | null) => { current = next; }, fail: (status: number) => { failure = status; } };
}
beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(new Date("2026-11-09T10:00:30Z")); });
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.useRealTimers(); });

it("renders daily rows, thresholds, units and incomplete ranges", async () => {
  const us = { periods: [{ ...daily.periods[0], temperature_min: 32, temperature_max: 50 }, { ...daily.periods[1], temperature_min: 23, temperature_max: 41 }, { ...daily.periods[2], temperature_max: 44.6 }] };
  core(entity("daily", us, "us"));
  await mount(<BoundForecast source={source("daily")} />);
  const rows = screen.getAllByRole("listitem");
  expect(rows).toHaveLength(3);
  expect(rows[0].getAttribute("aria-label")).toContain("Today: Rain");
  expect(rows[0].textContent).toContain("32°");
  expect(rows[0].getAttribute("aria-label")).toContain("32°F");
  expect(rows[0].textContent).not.toContain("9%");
  expect(rows[1].textContent).toContain("90%");
  expect(rows[0].querySelector(".forecast-range span")).toBeTruthy();
  expect(rows[2].querySelector(".forecast-range span")).toBeNull();
  expect(rows[2].textContent).toContain("—");
  cleanup(); vi.restoreAllMocks();
  core(entity("daily", daily));
  await mount(<BoundForecast source={source("daily")} />);
  expect(screen.getAllByRole("listitem")[0].textContent).toContain("0°");
  expect(screen.getAllByRole("listitem")[0].getAttribute("aria-label")).toContain("0°C");
});

it("renders an accessible scrollable hourly chart with gaps and threshold bars", async () => {
  core(entity("hourly", hourly));
  await mount(<BoundForecast source={source("hourly")} periods={6} />);
  const plot = screen.getByRole("img");
  expect(plot.getAttribute("aria-label")).toContain("Next 6 hours: low 0°C, high 5°C; precipitation chance peaks at 50%");
  expect(plot.getAttribute("tabindex")).toBe("0");
  expect(plot.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
  expect(plot.querySelectorAll(".forecast-bar")).toHaveLength(5);
  expect(plot.querySelector(".forecast-line")?.getAttribute("d")?.match(/M/g)).toHaveLength(2);
});

it.each([
  ["metric", 16, "16°C", "16°"],
  ["us", 60.8, "61°F", "61°"],
] as const)("uses projected %s hourly values and metadata without conversion or household reads", async (system, value, shown, compact) => {
  const item = entity("hourly", { periods: [
    { starts_at: "2026-11-09T09:00:00Z", temperature: value - 1 },
    { starts_at: at, temperature: value, precipitation_probability: 0 },
  ] }, system);
  item.quantities["/periods/0/temperature"].fraction_digits = 2;
  const api = core(item);
  await mount(<BoundForecast source={source("hourly")} periods={6} />);
  expect(screen.getByRole("img").getAttribute("aria-label")).toContain(`low ${shown}, high ${shown}; precipitation chance below 10%`);
  expect(document.querySelector(".forecast-temp-label")?.textContent).toBe(compact);
  expect(api.fetcher.mock.calls.every(([url]) => !String(url).includes("/household"))).toBe(true);
});

it.each([
  ["metric", 16, "16°C"],
  ["us", 60.8, "61°F"],
] as const)("uses projected %s daily values and labels for rows and ranges", async (system, value, shown) => {
  const item = entity("daily", { periods: [
    { starts_at: "2026-11-09T00:00:00Z", date: "2026-11-09", temperature_min: value, temperature_max: value + 10, precipitation_probability: 10 },
    { starts_at: "2026-11-10T00:00:00Z", date: "2026-11-10", temperature_min: value + 2, temperature_max: value + 8 },
  ] }, system);
  core(item);
  await mount(<BoundForecast source={source("daily")} />);
  const rows = screen.getAllByRole("listitem");
  expect(rows[0].getAttribute("aria-label")).toContain(`low ${shown}`);
  expect(rows[0].textContent).toContain("10%");
  expect((rows[1].querySelector(".forecast-range span") as HTMLElement).style.left).toBe("20%");
  expect((rows[1].querySelector(".forecast-range span") as HTMLElement).style.right).toBe("20%");
});

it("honors per-period precision and labels supplied by Core", async () => {
  const item = entity("hourly", { periods: [{ starts_at: at, temperature: 60.8, precipitation_probability: 50.25 }] }, "us");
  item.quantities["/periods/0/temperature"].fraction_digits = 1;
  item.quantities["/periods/0/precipitation_probability"].fraction_digits = 1;
  item.quantities["/periods/0/temperature"].label = " degrees F";
  core(item);
  await mount(<BoundForecast source={source("hourly")} periods={6} />);
  expect(screen.getByRole("img").getAttribute("aria-label")).toContain("low 60.8 degrees F, high 60.8 degrees F; precipitation chance peaks at 50.3%");
  expect(document.querySelector(".forecast-legend")?.textContent).toContain("Temperature  degrees F");
  expect(document.querySelector(".forecast-temp-label")?.textContent).toBe("60.8°");
});

it("keeps daily Fahrenheit cells compact while exposing full labels and precision", async () => {
  const item = entity("daily", { periods: [{ starts_at: "2026-11-09T00:00:00Z", date: "2026-11-09", temperature_min: -12.3, temperature_max: 100 }] }, "us");
  item.quantities["/periods/0/temperature_min"].fraction_digits = 1;
  core(item);
  await mount(<BoundForecast source={source("daily")} />);
  const row = screen.getByRole("listitem");
  expect([...row.querySelectorAll(".forecast-temp")].map(node => node.textContent)).toEqual(["-12.3°", "100°"]);
  expect(row.getAttribute("aria-label")).toContain("low -12.3°F, high 100°F");
});

it("keeps hourly Fahrenheit labels compact while the chart summary and legend retain the full label", async () => {
  const item = entity("hourly", { periods: [{ starts_at: at, temperature: -12 }, { starts_at: "2026-11-09T11:00:00Z", temperature: 100 }] }, "us");
  core(item);
  await mount(<BoundForecast source={source("hourly")} periods={6} />);
  expect([...document.querySelectorAll(".forecast-temp-label")].map(node => node.textContent)).toEqual(["-12°", "100°"]);
  expect(screen.getByRole("img").getAttribute("aria-label")).toContain("low -12°F, high 100°F");
  expect(document.querySelector(".forecast-legend")?.textContent).toContain("Temperature °F");
});

it.each(["daily", "hourly"] as const)("retains the full label for an unknown %s temperature unit", async type => {
  const item = type === "daily"
    ? entity(type, { periods: [{ starts_at: "2026-11-09T00:00:00Z", date: "2026-11-09", temperature_min: 273.15 }] })
    : entity(type, { periods: [{ starts_at: at, temperature: 273.15 }] });
  const path = type === "daily" ? "/periods/0/temperature_min" : "/periods/0/temperature";
  item.quantities[path] = { unit: "K", label: " K", fraction_digits: 2 };
  core(item);
  await mount(type === "daily" ? <BoundForecast source={source("daily")} /> : <BoundForecast source={source("hourly")} />);
  if (type === "daily") {
    const row = screen.getByRole("listitem");
    expect(row.querySelector(".forecast-low")?.textContent).toBe("273.15 K");
    expect(row.getAttribute("aria-label")).toContain("low 273.15 K");
  } else {
    expect(document.querySelector(".forecast-temp-label")?.textContent).toBe("273.15 K");
    expect(screen.getByRole("img").getAttribute("aria-label")).toContain("low 273.15 K");
  }
});

it("plots projected temperatures and accepts all neutral forecast measurements", async () => {
  const item = entity("hourly", { periods: [
    { starts_at: at, summary: "Rain", temperature: 60.8, apparent_temperature: 59, precipitation_rate: 0.04, humidity: 80, pressure: 29.92, wind_speed: 5, wind_gust: 9, wind_direction: 90, cloud_cover: 75, uv_index: 2 },
    { starts_at: "2026-11-09T11:00:00Z", temperature: 68 },
    { starts_at: "2026-11-09T12:00:00Z", temperature: 86 },
  ] }, "us");
  const q = (unit: string, label: string, fraction_digits: number) => ({ unit, label, fraction_digits });
  item.quantities["/periods/0/precipitation_rate"] = q("[in_i]/h", "in/h", 2);
  item.quantities["/periods/0/pressure"] = q("[in_i'Hg]", "in Hg", 2);
  item.quantities["/periods/0/wind_speed"] = q("[mi_i]/h", "mi/h", 1);
  item.quantities["/periods/0/wind_gust"] = q("[mi_i]/h", "mi/h", 1);
  item.quantities["/periods/0/wind_direction"] = q("deg", "°", 0);
  item.quantities["/periods/0/uv_index"] = q("1", "", 1);
  core(item);
  await mount(<BoundForecast source={source("hourly")} periods={6} />);
  expect(screen.getByRole("img").getAttribute("aria-label")).toContain("low 61°F, high 86°F");
  expect(document.querySelector(".forecast-line")?.getAttribute("d")).toBe("M14.0,128.0L132.0,108.0L250.0,58.0");
});

it("distinguishes absent temperature and precipitation from zero", async () => {
  core(entity("hourly", { periods: [{ starts_at: at }] }));
  await mount(<BoundForecast source={source("hourly")} periods={6} />);
  expect(screen.getByRole("img").getAttribute("aria-label")).toContain("temperature unavailable; precipitation chance unavailable");
  expect(document.querySelector(".forecast-temp-label")).toBeNull();
  expect(document.querySelector(".forecast-bar")).toBeNull();
});

it.each(["missing", "wrong-index", "temperature-unit", "precipitation-unit", "invalid-precision"])("rejects %s forecast quantity metadata safely", async problem => {
  const item = entity("hourly", { periods: [{ starts_at: at, temperature: 16, precipitation_probability: 50 }, { starts_at: "2026-11-09T11:00:00Z", temperature: 17 }] });
  if (problem === "missing") delete item.quantities["/periods/0/temperature"];
  if (problem === "wrong-index") { item.quantities["/periods/2/temperature"] = item.quantities["/periods/1/temperature"]; delete item.quantities["/periods/1/temperature"]; }
  if (problem === "temperature-unit") item.quantities["/periods/1/temperature"] = { unit: "[degF]", label: "°F", fraction_digits: 0 };
  if (problem === "precipitation-unit") item.quantities["/periods/0/precipitation_probability"].unit = "1";
  if (problem === "invalid-precision") item.quantities["/periods/0/temperature"].fraction_digits = -1;
  core(item);
  await mount(<BoundForecast source={source("hourly")} />);
  expect(screen.getByText(/The resource returned incompatible data/)).toBeTruthy();
  expect(screen.queryByRole("img")).toBeNull();
});

it("renders a forecast alongside current weather without a household request", async () => {
  const pressureId = "ent_01900000-0000-7000-8000-000000000202";
  const pressure = { id: pressureId, name: "Pressure", type: "hearth.pressure/v1", enabled: true, availability: { status: "available" }, support: { state: {}, operations: {} }, quantities: { "": { unit: "[in_i'Hg]", label: "in Hg", fraction_digits: 2 } }, state: { value: 29.92, observed_at: at } };
  const forecast = entity("hourly", { periods: [{ starts_at: at, temperature: 60.8 }] }, "us");
  const fetcher = vi.spyOn(globalThis, "fetch").mockImplementation(async input => new Response(JSON.stringify(String(input).includes(pressureId) ? pressure : forecast)));
  await mount(<><BoundForecast source={source("hourly")} /><BoundPressure source={{ kind: "entity-state", entity_id: pressureId, entity_type: "hearth.pressure/v1" }} /></>);
  expect(screen.getByRole("img").getAttribute("aria-label")).toContain("61°F");
  expect(screen.getByText("29.92")).toBeTruthy();
  expect(fetcher.mock.calls.every(([url]) => !String(url).includes("/household"))).toBe(true);
});

it("renders status and retained forecast without inventing data", async () => {
  const item = entity("daily", daily); item.availability.status = "unavailable";
  core(item);
  await mount(<BoundForecast source={source("daily")} />);
  expect(screen.getByText("Unavailable")).toBeTruthy();
  expect(document.querySelector(".forecast-body")?.getAttribute("data-value")).toBe("dimmed");
});

it("shows never-observed and disabled states", async () => {
  const item = entity("daily", daily);
  item.enabled = false;
  core(item);
  await mount(<BoundForecast source={source("daily")} />);
  expect(screen.getByText("Disabled")).toBeTruthy();
  expect(document.querySelector(".forecast-body")?.getAttribute("data-value")).toBe("dimmed");
  cleanup(); vi.restoreAllMocks();
  vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({ ...item, enabled: true, state: null })));
  await mount(<BoundForecast source={source("daily")} />);
  expect(screen.getByText("Never observed")).toBeTruthy();
});

it("retains data with a refresh warning after a request failure", async () => {
  const api = core(entity("daily", daily));
  await mount(<BoundForecast source={source("daily")} />);
  api.fail(503);
  await act(() => vi.advanceTimersByTimeAsync(10000));
  expect(screen.getByText(/Couldn't refresh/)).toBeTruthy();
  expect(screen.getByText("Today")).toBeTruthy();
});

it("drops ended periods on the minute clock without a new read", async () => {
  vi.setSystemTime(new Date("2026-11-09T10:59:30Z"));
  const api = core(entity("hourly", hourly));
  await mount(<BoundForecast source={source("hourly")} periods={6} />);
  const calls = api.fetcher.mock.calls.length;
  Object.defineProperty(document, "visibilityState", { configurable: true, value: "hidden" });
  document.dispatchEvent(new Event("visibilitychange"));
  await act(() => vi.advanceTimersByTimeAsync(60000));
  expect(screen.getByRole("img").getAttribute("aria-label")).toContain("Next 5 hours");
  expect(api.fetcher).toHaveBeenCalledTimes(calls);
  Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
});

it("explains expired forecasts and unavailable partial daily forecasts", async () => {
  const past = entity("hourly", { periods: [{ starts_at: "2026-11-09T08:00:00Z" }] });
  core(past);
  await mount(<BoundForecast source={source("hourly")} />);
  expect(screen.getByText("Every hour in the last forecast has passed.")).toBeTruthy();
  cleanup(); vi.restoreAllMocks();
  const item = entity("daily", { periods: [{ starts_at: "2026-11-08T00:00:00Z", date: "2026-11-08" }, ...daily.periods] });
  item.availability.status = "unavailable";
  core(item);
  await mount(<BoundForecast source={source("daily")} />);
  expect(screen.getByText(/Showing the remaining days of a forecast from/)).toBeTruthy();
});

it("marks incompatible forecast state and missing entities", async () => {
  const api = core(entity("hourly", { periods: [] }));
  await mount(<BoundForecast source={source("hourly")} />);
  expect(screen.getByText(/The resource returned incompatible data/)).toBeTruthy();
  cleanup(); vi.restoreAllMocks();
  api.set(null);
  vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({ detail: "missing" }), { status: 404 }));
  await mount(<BoundForecast source={source("hourly")} />);
  expect(screen.getByText(/Entity not found/)).toBeTruthy();
});

it("reports loading, request failure and a mismatched Entity type", async () => {
  let finish!: (response: Response) => void;
  vi.spyOn(globalThis, "fetch").mockImplementation(() => new Promise(resolve => { finish = resolve; }));
  render(<ViewDataProvider><BoundForecast source={source("hourly")} /></ViewDataProvider>, { wrapper: MemoryRouter });
  expect(screen.getByText("Loading…")).toBeTruthy();
  await act(async () => { finish(new Response(JSON.stringify({ detail: "Server unavailable" }), { status: 503 })); });
  expect(screen.getByText(/Couldn't load this reading/)).toBeTruthy();
  cleanup(); vi.restoreAllMocks();
  core(entity("daily", daily));
  await mount(<BoundForecast source={source("hourly")} />);
  expect(screen.getByText(/The resource returned incompatible data/)).toBeTruthy();
});

it("glows for changed display but not identical republications", async () => {
  const api = core(entity("daily", daily));
  await mount(<BoundForecast source={source("daily")} />);
  const same = entity("daily", daily); same.state.observed_at = "2026-11-09T10:01:00Z"; api.set(same);
  await act(() => vi.advanceTimersByTimeAsync(10000));
  expect(document.querySelector(".view-card-arrival")).toBeNull();
  const changed = entity("daily", { periods: [{ ...daily.periods[0], temperature_max: 20 }] }); changed.state.observed_at = "2026-11-09T10:02:00Z"; api.set(changed);
  await act(() => vi.advanceTimersByTimeAsync(10000));
  expect(document.querySelector(".view-card-arrival")).toBeTruthy();
  await act(() => vi.advanceTimersByTimeAsync(10000));
  expect(document.querySelector(".view-card-arrival")).toBeNull();
});

it("glows when only a day or night condition variant changes", async () => {
  const clear = { periods: [{ ...daily.periods[0], condition: "clear-day" }] };
  const api = core(entity("daily", clear));
  await mount(<BoundForecast source={source("daily")} />);
  const night = entity("daily", { periods: [{ ...clear.periods[0], condition: "clear-night" }] });
  night.state.observed_at = "2026-11-09T10:01:00Z"; api.set(night);
  await act(() => vi.advanceTimersByTimeAsync(10000));
  expect(document.querySelector(".view-card-arrival")).toBeTruthy();
});

it("keeps the chart summary within the 10% precipitation threshold", async () => {
  const dry = { periods: hourly.periods.map(p => ({ ...p, precipitation_probability: 5 })) };
  core(entity("hourly", dry));
  await mount(<BoundForecast source={source("hourly")} periods={6} />);
  const plot = screen.getByRole("img");
  expect(plot.getAttribute("aria-label")).toContain("precipitation chance below 10%");
  expect(plot.querySelectorAll(".forecast-bar")).toHaveLength(0);
});

it("labels half-hour local times and places the day rule at local midnight", async () => {
  const previous = process.env.TZ; process.env.TZ = "Asia/Kolkata";
  try {
    vi.setSystemTime(new Date("2026-11-09T16:00:30Z"));
    const night = { periods: Array.from({ length: 12 }, (_, i) => ({ starts_at: new Date(Date.UTC(2026, 10, 9, 16 + i)).toISOString(), temperature: 20 + i })) };
    core(entity("hourly", night));
    await mount(<BoundForecast source={source("hourly")} periods={12} />);
    const labels = [...document.querySelectorAll(".forecast-chart text")].map(text => text.textContent);
    expect(labels).toContain("11:30 PM");
    expect(labels).toContain("Tue");
    // Periods fall at 11:30 PM (index 2) and 12:30 AM (index 3) in India. Local midnight is halfway
    // between them, not at the 12:30 AM point.
    const rules = document.querySelectorAll(".forecast-midnight");
    expect(rules).toHaveLength(1);
    const width = 264, step = (width - 28) / 11;
    expect(Number(rules[0].getAttribute("x1"))).toBeCloseTo(14 + 2.5 * step, 5);
  } finally { if (previous === undefined) delete process.env.TZ; else process.env.TZ = previous; }
});

it("treats an impossible daily date as incompatible data", async () => {
  core(entity("daily", { periods: [{ ...daily.periods[0], date: "2026-02-30" }] }));
  await mount(<BoundForecast source={source("daily")} />);
  expect(screen.getByText(/The resource returned incompatible data/)).toBeTruthy();
});

it("wires Forecast through the renderer and offers only forecast choices", async () => {
  core(entity("daily", daily));
  const definition = { schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Forecast", spec: { root: "card", elements: { card: { type: "Forecast", props: { source: source("daily") } } } } };
  await mount(<ViewRenderer definition={definition} headingLevel={3} />);
  expect(screen.getByRole("article", { name: "Next 8 days" })).toBeTruthy();
  cleanup();
  const onChange = vi.fn();
  render(<ForecastEditor value={{ source: source("daily"), periods: 8 }} choices={[{ id, name: "Daily", type: forecastTypes.daily }, { id: "hourly", name: "Hourly", type: forecastTypes.hourly }, { id: "other", name: "Temperature", type: "hearth.temperature/v1" }]} onChange={onChange} />);
  const select = screen.getByLabelText("Forecast Entity");
  expect(within(select).queryByText("Temperature")).toBeNull();
  fireEvent.change(select, { target: { value: "hourly" } });
  expect(onChange.mock.calls[0][0].source.entity_type).toBe(forecastTypes.hourly);
  expect(screen.getByLabelText("Days").getAttribute("max")).toBe("8");
  cleanup();
  onChange.mockClear();
  render(<ForecastEditor value={{ source: source("hourly"), periods: 24 }} choices={[{ id, name: "Hourly", type: forecastTypes.hourly }, { id: "daily", name: "Daily", type: forecastTypes.daily }]} onChange={onChange} />);
  expect(screen.getByLabelText("Hours").getAttribute("min")).toBe("6");
  expect(screen.getByLabelText("Hours").getAttribute("max")).toBe("48");
  fireEvent.change(screen.getByLabelText("Forecast Entity"), { target: { value: "daily" } });
  expect(onChange.mock.calls[0][0].periods).toBeUndefined();
});
