import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { StateCard } from "./state/State";
import { ReadingList } from "./reading-list/ReadingList";
import { DeviceCard, BoundDevice } from "./device/Device";
import { Metric } from "./metric/Metric";
import { ViewDataProvider } from "../data/provider";
import { BoundState } from "./state/State";
import { BoundMetric } from "./metric/Metric";
import { parseDefinition } from "../catalog/compatibility";
import { deviceReading, lightReading, parseDevice } from "../bindings/device";
import type { FieldEntity } from "../bindings/entity";
import type { Resolved, TextData, SeriesData } from "../bindings/types";

afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.useRealTimers(); });
const id = "ent_019a3e6a-4111-7000-8000-000000000001";
const deviceId = "dev_019a3e6a-4111-7000-8000-000000000001";
const at = "2026-11-06T10:00:00Z";
const ready: Resolved<TextData> = { status: "ready", metadata: { id, label: "Door", enabled: true, availability: "available" }, data: { text: "Closed", observed_at: at } };

it.each([
  ["ready", ready, null, "Closed", "live"],
  ["loading", { status: "loading" }, "Loading…", "—", "empty"],
  ["missing", { status: "missing" }, "Entity not found", "—", "empty"],
  ["error", { status: "error", error: "Request failed" }, "Couldn't load this reading", "—", "empty"],
  ["unobserved", { ...ready, status: "unobserved", data: undefined }, "Never observed", "—", "empty"],
  ["absent", { ...ready, status: "absent", data: undefined }, "No value", "—", "empty"],
  ["unavailable", { ...ready, metadata: { ...ready.metadata!, availability: "unavailable" } }, "Unavailable", "Closed", "dimmed"],
  ["disabled", { ...ready, metadata: { ...ready.metadata!, enabled: false } }, "Disabled", "Closed", "dimmed"],
  ["unknown", { ...ready, metadata: { ...ready.metadata!, availability: "unknown" } }, "Availability unknown", "Closed", "live"],
  ["refresh", { ...ready, refreshFailed: true }, "Couldn't refresh", "Closed", "live"],
] as const)("State renders the %s state without implying Commands", (_, resource, status, text, treatment) => {
  render(<StateCard label="Front door" resource={resource} />, { wrapper: MemoryRouter });
  expect(screen.getByText(text).getAttribute("data-value")).toBe(treatment);
  if (status) expect(screen.getByText(status)).toBeTruthy();
  expect(document.querySelector(".view-card-arrival")).toBeNull();
});

it("State and numeric fields share one validated Entity request", async () => {
  const fetch = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({ id, type: "hearth.colorhs/v1", name: "Lamp", enabled: true, availability: { status: "available" }, support: {}, quantities: { "/hue": { unit: "deg", label: "°", fraction_digits: 0 }, "/saturation": { unit: "%", label: "%", fraction_digits: 0 } }, state: { value: { active: true, hue: 42, saturation: 64 }, observed_at: at } })));
  const source = { entity_id: id, entity_type: "hearth.colorhs/v1" };
  render(<ViewDataProvider><BoundState label="Mode" source={{ ...source, field: "/active" }} /><BoundMetric label="Hue" source={{ ...source, field: "/hue" }} /></ViewDataProvider>, { wrapper: MemoryRouter });
  expect(await screen.findByText("Active")).toBeTruthy();
  expect(await screen.findByText("42")).toBeTruthy();
  expect(fetch).toHaveBeenCalledTimes(1);
});

it("lists state the oldest reading and light a changed older row independently", async () => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date(at));
  const rows = (first: string, second: string, text = "Closed") => [
    { key: "one", name: "Door", resource: { ...ready, data: { text, observed_at: first } } },
    { key: "two", name: "Window", resource: { ...ready, data: { text: "Open", observed_at: second } } },
  ];
  const old = "2026-11-06T08:00:00Z", newer = "2026-11-06T09:00:00Z", changed = "2026-11-06T08:30:00Z";
  const view = render(<ReadingList title="Doors" rows={rows(old, newer)} />, { wrapper: MemoryRouter });
  expect(document.querySelector("time")?.getAttribute("datetime")).toBe(old);
  view.rerender(<ReadingList title="Doors" rows={rows(changed, newer, "Open")} />);
  expect(document.querySelectorAll(".view-card-arrival")).toHaveLength(1);
  expect(document.querySelectorAll(".view-row-arrival")).toHaveLength(1);
  await act(async () => { vi.advanceTimersByTime(60_001); });
  expect(document.querySelector(".view-card-arrival")).toBeNull();
});

it("shows pinned ranges in words and keeps gaps in a trend", () => {
  const data: SeriesData = { from: at, to: at, format: { fraction_digits: 0, unit_label: "%" }, buckets: [10, 20, null, 30, 40].map(value => ({ start: at, end: at, min: value, max: value, mean: value })) };
  render(<Metric label="Brightness" range trend={{ window: "1h", resource: { ...ready, data } }} resource={{ ...ready, data: { value: 120, format: data.format, range: { minimum: 0, maximum: 100 }, observed_at: at } }} />, { wrapper: MemoryRouter });
  expect(screen.getByText("Above the 100 % range")).toBeTruthy();
  expect(document.querySelector<HTMLElement>(".view-range-marker")?.style.left).toBe("100%");
  expect(document.querySelector(".view-trend-mean")?.getAttribute("d")?.match(/M/g)).toHaveLength(2);
  expect(document.querySelector(".view-trend-now")).toBeNull();
  expect(screen.getByText("1 h range 10–40 %")).toBeTruthy();
});

const entity = (type: string, value: unknown, i = 1): FieldEntity => ({ id: id.slice(0, -1) + i, type: `hearth.${type}/v1`, name: type, enabled: true, availability: { status: "available" }, support: {},
  quantities: type === "brightness" ? { "": { unit: "%", label: "%", fraction_digits: 0, range: { minimum: 0, maximum: 100 } } }
    : type === "colorhs" ? { "/hue": { unit: "deg", label: "°", fraction_digits: 0 }, "/saturation": { unit: "%", label: "%", fraction_digits: 0, range: { minimum: 0, maximum: 100 } } }
    : type === "colorxy" ? { "/x": { unit: "1", label: "", fraction_digits: 4 }, "/y": { unit: "1", label: "", fraction_digits: 4 } }
    : type === "numericsetting" ? { "/value": { unit: "1", label: "", fraction_digits: 1 } } : {},
  state: value === undefined ? null : { value, observed_at: at, observation_id: `obs_${i}` } });

it("Device handles never observed colors and skips event sources, not malformed State", () => {
  const device = parseDevice({ id: deviceId, name: "Lamp", entities: [entity("power", true), entity("brightness", 64, 2), entity("colorhs", undefined, 3), { type: "hearth.enumevent/v1" }] }, deviceId);
  const light = lightReading(device.entities);
  expect(light?.on).toBe(true);
  expect(light?.swatch).toBeUndefined();
  render(<DeviceCard deviceId={deviceId} resource={{ status: "ready", data: device }} />, { wrapper: MemoryRouter });
  expect(screen.getByText("Never observed")).toBeTruthy();
  expect(() => parseDevice({ id: deviceId, name: "Lamp", entities: [entity("brightness", "bad")] }, deviceId)).toThrow();
});

it("Device settings use their selected choice and honor the twelve row cap", () => {
  const setting = { ...entity("numericsetting", { mode: "choice", choice: "Auto" }), support: { state: { minimum: 0, maximum: 100, choices: ["Auto"] } } };
  expect(deviceReading(setting).data).toEqual({ text: "Auto", observed_at: at });
  const device = parseDevice({ id: deviceId, name: "Many", entities: Array.from({ length: 14 }, (_, i) => ({ ...entity("power", true), id: `entity-${i}` })) }, deviceId);
  render(<DeviceCard deviceId={deviceId} resource={{ status: "ready", data: device }} />, { wrapper: MemoryRouter });
  expect(document.querySelectorAll(".view-row")).toHaveLength(12);
  expect(screen.getByText(/Showing 12 of 14/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Source" }));
  expect(screen.getAllByRole("link")).toHaveLength(15);
});

it("Device follows paginated membership and names a missing Device", async () => {
  const fetch = vi.spyOn(globalThis, "fetch").mockImplementation(async url => new Response(JSON.stringify({ id: deviceId, name: "Rooms", entities: [entity("power", true, String(url).includes("entity_cursor") ? 2 : 1)], next_entity_cursor: String(url).includes("entity_cursor") ? null : "next" })));
  render(<ViewDataProvider><BoundDevice device={{ kind: "device", device_id: deviceId }} /></ViewDataProvider>, { wrapper: MemoryRouter });
  const card = await screen.findByRole("article", { name: "Rooms" });
  expect(within(card).getAllByText("On")).toHaveLength(2);
  expect(fetch.mock.calls.filter(([url]) => String(url).includes(`/v1/devices/`))).toHaveLength(2);
  cleanup();
  fetch.mockImplementation(async () => new Response("{}", { status: 404 }));
  render(<ViewDataProvider><BoundDevice device={{ kind: "device", device_id: deviceId }} /></ViewDataProvider>, { wrapper: MemoryRouter });
  expect(await screen.findByText("Device not found")).toBeTruthy();
});

it("renders projected color coordinates but requests canonical coordinates for the swatch", async () => {
  const projected = entity("colorxy", { active: true, x: 0.3127, y: 0.329 }, 3);
  const canonical = { ...projected, quantities: {
    "/x": { unit: "10*-4", label: "10*-4", fraction_digits: 0 }, "/y": { unit: "10*-4", label: "10*-4", fraction_digits: 0 },
  }, state: { value: { active: true, x: 3127, y: 3290 }, observed_at: at, observation_id: "obs_3" } };
  const fetch = vi.spyOn(globalThis, "fetch").mockImplementation(async input => {
    const isCanonical = String(input).includes("units=canonical");
    return new Response(JSON.stringify({ id: deviceId, name: "Lamp", entities: [entity("power", true), entity("brightness", 64, 2), isCanonical ? canonical : projected] }));
  });
  render(<ViewDataProvider><BoundDevice device={{ kind: "device", device_id: deviceId }} /></ViewDataProvider>, { wrapper: MemoryRouter });
  expect(await screen.findByText(/X 0\.3127/)).toBeTruthy();
  await waitFor(() => expect(document.querySelector<HTMLElement>(".view-light-swatch")?.style.backgroundColor).toBe("rgb(255, 255, 255)"));
  expect(fetch.mock.calls.some(([url]) => String(url).includes("units=canonical"))).toBe(true);
  expect(fetch.mock.calls.some(([url]) => !String(url).includes("units=canonical"))).toBe(true);
});

it("hides a stale XY swatch until its observation matches, including after a canonical refresh failure", () => {
  const color = entity("colorxy", { active: true, x: 0.3127, y: 0.329 }, 3);
  const device = parseDevice({ id: deviceId, name: "Lamp", entities: [entity("power", true), entity("brightness", 64, 2), color] }, deviceId);
  const canonicalColor = { ...color, quantities: {
    "/x": { unit: "10*-4", label: "10*-4", fraction_digits: 0 }, "/y": { unit: "10*-4", label: "10*-4", fraction_digits: 0 },
  }, state: { value: { active: true, x: 3127, y: 3290 }, observed_at: at, observation_id: "obs_old" } };
  const canonical = parseDevice({ id: deviceId, name: "Lamp", entities: [canonicalColor] }, deviceId);
  const card = (data: typeof canonical, error?: string) => <DeviceCard deviceId={deviceId} resource={{ status: "ready", data: device }} canonical={data} canonicalError={error} />;
  const view = render(card(canonical), { wrapper: MemoryRouter });
  const swatch = () => document.querySelector<HTMLElement>(".view-light-swatch")!.style.backgroundColor;
  expect(swatch()).toBe("");
  expect(screen.getByText("Color preview unavailable; canonical reading is missing or out of date.")).toBeTruthy();
  const matching = { ...canonical, entities: canonical.entities.map(item => ({ ...item, state: { ...item.state!, observation_id: "obs_3" } })) };
  view.rerender(card(matching));
  expect(swatch()).toBe("rgb(255, 255, 255)");
  expect(screen.queryByText("Color preview unavailable; canonical reading is missing or out of date.")).toBeNull();
  view.rerender(card(matching, "Request failed"));
  expect(swatch()).toBe("");
  expect(screen.getByText("Color preview unavailable; canonical reading is missing or out of date.")).toBeTruthy();
  view.rerender(card(matching));
  expect(swatch()).toBe("rgb(255, 255, 255)");
});

it("strict definitions reject text words on strings and ranges without facts", () => {
  const definition = (node: unknown) => ({ schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Test", spec: { root: "card", elements: { card: node } } });
  expect(parseDefinition(definition({ type: "State", props: { source: { kind: "entity-state", entity_id: id, entity_type: "hearth.colormode/v1" }, words: { true: "X", false: "Y" } } }))).toBeNull();
  expect(parseDefinition(definition({ type: "Metric", props: { source: { kind: "entity-state", entity_id: id, entity_type: "hearth.temperature/v1" }, range: true } }))).toBeNull();
});

it("does not replay a displayed unavailable change when availability returns", () => {
  const view = render(<StateCard resource={ready} />, { wrapper: MemoryRouter });
  const changed = { ...ready, data: { text: "Open", observed_at: "2026-11-06T10:01:00Z" } };
  view.rerender(<StateCard resource={{ ...changed, metadata: { ...ready.metadata!, availability: "unavailable" } }} />);
  expect(document.querySelector(".view-card-arrival")).toBeNull();
  view.rerender(<StateCard resource={changed} />);
  expect(document.querySelector(".view-card-arrival")).toBeNull();
});

it("does not use a hidden inactive color for the Device footer or afterglow", () => {
  const entities = [entity("power", true), entity("brightness", 64, 2), entity("colorhs", { active: true, hue: 20, saturation: 64 }, 3),
    { ...entity("colorxy", { active: false, x: 0.4, y: 0.4 }, 4), state: { value: { active: false, x: 0.4, y: 0.4 }, observed_at: "2026-11-06T08:00:00Z" } }];
  const data = parseDevice({ id: deviceId, name: "Lamp", entities }, deviceId);
  const view = render(<DeviceCard deviceId={deviceId} resource={{ status: "ready", data }} />, { wrapper: MemoryRouter });
  expect(document.querySelector("time")?.getAttribute("datetime")).toBe(at);
  const updated = { ...data, entities: data.entities.map(e => e.type === "hearth.colorxy/v1" ? { ...e, state: { value: { active: false, x: 0.5, y: 0.4 }, observed_at: "2026-11-06T10:02:00Z" } } : e) };
  view.rerender(<DeviceCard deviceId={deviceId} resource={{ status: "ready", data: updated }} />);
  expect(document.querySelector(".view-card-arrival")).toBeNull();
  const unavailable = { ...data, entities: data.entities.map(e => e.type === "hearth.colorhs/v1" ? { ...e, availability: { status: "unavailable" } } : e) };
  view.rerender(<DeviceCard deviceId={deviceId} resource={{ status: "ready", data: unavailable }} />);
  expect(document.querySelector(".view-light-swatch")?.hasAttribute("data-dimmed")).toBe(true);
  expect(document.querySelector(".view-light-detail [data-dimmed]")).toBeTruthy();
});

it("names a refresh failure even with an empty Device or an empty history interval", () => {
  render(<DeviceCard deviceId={deviceId} resource={{ status: "ready", refreshFailed: true, data: { id: deviceId, name: "Empty", entities: [] } }} />, { wrapper: MemoryRouter });
  expect(screen.getByText("Couldn't refresh")).toBeTruthy();
  cleanup();
  const data = { from: at, to: at, format: { fraction_digits: 0, unit_label: "%" }, buckets: [{ start: at, end: at, min: null, max: null, mean: null }] };
  render(<Metric resource={{ ...ready, data: { value: 64, format: data.format, observed_at: at } }} trend={{ window: "1h", resource: { status: "ready", data, refreshFailed: true } }} />, { wrapper: MemoryRouter });
  expect(screen.getByText("Couldn't refresh history. Showing the last interval.")).toBeTruthy();
});

it("observes color-mode changes that select a new light color and reports selector availability", () => {
  const entities = [entity("power", true), entity("brightness", 64, 2), { ...entity("colorhs", undefined, 3), state: { value: { active: false, hue: 20, saturation: 64 }, observed_at: "2026-11-06T12:00:00Z" } },
    entity("colorxy", { active: false, x: 0.4, y: 0.4 }, 4), entity("colormode", "hs", 5)];
  const data = parseDevice({ id: deviceId, name: "Lamp", entities }, deviceId);
  const view = render(<DeviceCard deviceId={deviceId} resource={{ status: "ready", data }} />, { wrapper: MemoryRouter });
  const updated = { ...data, entities: data.entities.map(e => e.type === "hearth.colormode/v1" ? { ...e, state: { value: "xy", observed_at: "2026-11-06T10:02:00Z" } } : e) };
  view.rerender(<DeviceCard deviceId={deviceId} resource={{ status: "ready", data: updated }} />);
  expect(document.querySelector(".view-card-arrival")).toBeTruthy();
  const unavailable = { ...updated, entities: updated.entities.map(e => e.type === "hearth.colormode/v1" ? { ...e, availability: { status: "unavailable" } } : e) };
  view.rerender(<DeviceCard deviceId={deviceId} resource={{ status: "ready", data: unavailable }} />);
  expect(screen.getByText("Unavailable")).toBeTruthy();
  expect(document.querySelector(".view-light-status")?.textContent).toContain("colormode");
});

it("dims an off light's retained brightness independently of color", () => {
  const data = parseDevice({ id: deviceId, name: "Lamp", entities: [entity("power", false), { ...entity("brightness", 64, 2), availability: { status: "unavailable" } }] }, deviceId);
  render(<DeviceCard deviceId={deviceId} resource={{ status: "ready", data }} />, { wrapper: MemoryRouter });
  expect(screen.getByText("Brightness 64 %").hasAttribute("data-dimmed")).toBe(true);
});
