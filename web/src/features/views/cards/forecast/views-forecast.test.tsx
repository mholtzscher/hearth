import { StrictMode, type ReactNode } from "react";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { UnitSystemOverride } from "../../../units/household";
import { ViewDataProvider } from "../../data/provider";
import { ViewRenderer } from "../../renderer/ViewRenderer";
import { BoundForecast } from "./Forecast";
import { ForecastEditor } from "./Editor";
import { forecastTypes } from "./schema";

const id = "ent_01900000-0000-7000-8000-000000000201";
const source = <T extends "hourly" | "daily">(type: T) => ({ kind: "entity-state" as const, entity_id: id, entity_type: forecastTypes[type] });
const at = "2026-11-09T10:00:00Z";
function entity(type: "hourly" | "daily", value: unknown) {
  return { id, name: "Forecast", type: forecastTypes[type], enabled: true, availability: { status: "available" }, state: { value, observed_at: at } };
}
const daily = { periods: [
  { starts_at: "2026-11-09T00:00:00Z", date: "2026-11-09", condition: "rain", temperature_min_c: 0, temperature_max_c: 10, precipitation_probability_percent: 9 },
  { starts_at: "2026-11-10T00:00:00Z", date: "2026-11-10", condition: "snow", temperature_min_c: -5, temperature_max_c: 5, precipitation_probability_percent: 90 },
  { starts_at: "2026-11-11T00:00:00Z", date: "2026-11-11", condition: "fog", temperature_max_c: 7 },
] };
const hourly = { periods: Array.from({ length: 6 }, (_, i) => ({ starts_at: new Date(Date.parse(at) + i * 3600000).toISOString(), condition: "rain", ...(i === 2 ? {} : { temperature_c: i }), precipitation_probability_percent: i === 0 ? 9 : 50 })) };
const flush = () => act(() => vi.advanceTimersByTimeAsync(0));
async function mount(children: ReactNode, units: "metric" | "us" = "metric") {
  render(<StrictMode><UnitSystemOverride.Provider value={units}><ViewDataProvider>{children}</ViewDataProvider></UnitSystemOverride.Provider></StrictMode>, { wrapper: MemoryRouter });
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
  core(entity("daily", daily));
  await mount(<BoundForecast source={source("daily")} />, "us");
  const rows = screen.getAllByRole("listitem");
  expect(rows).toHaveLength(3);
  expect(rows[0].getAttribute("aria-label")).toContain("Today: Rain");
  expect(rows[0].textContent).toContain("32°");
  expect(rows[0].textContent).not.toContain("9%");
  expect(rows[1].textContent).toContain("90%");
  expect(rows[0].querySelector(".forecast-range span")).toBeTruthy();
  expect(rows[2].querySelector(".forecast-range span")).toBeNull();
  expect(rows[2].textContent).toContain("—");
  cleanup(); vi.restoreAllMocks();
  core(entity("daily", daily));
  await mount(<BoundForecast source={source("daily")} />, "metric");
  expect(screen.getAllByRole("listitem")[0].textContent).toContain("0°");
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
  render(<UnitSystemOverride.Provider value="metric"><ViewDataProvider><BoundForecast source={source("hourly")} /></ViewDataProvider></UnitSystemOverride.Provider>, { wrapper: MemoryRouter });
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
  const changed = entity("daily", { periods: [{ ...daily.periods[0], temperature_max_c: 20 }] }); changed.state.observed_at = "2026-11-09T10:02:00Z"; api.set(changed);
  await act(() => vi.advanceTimersByTimeAsync(10000));
  expect(document.querySelector(".view-card-arrival")).toBeTruthy();
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
