import { StrictMode, useState, type ReactNode } from "react";
import type { z } from "zod";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { BoundToggleTile } from "./ToggleTile";
import { BoundToggleList } from "../toggle-list/ToggleList";
import { ViewDataProvider } from "../../data/provider";
import { UnitSystemOverride } from "../../../units/household";
import type { FieldEntity } from "../../bindings/entity";
import type { PowerSource } from "../../bindings/schema";
import { ViewRenderer } from "../../renderer/ViewRenderer";
import { ToggleTileEditor } from "./Editor";
import { toggleTileSchema } from "./schema";

const at = "2026-11-09T10:00:00Z";
const id = (number: number) => `ent_019a3e6a-4111-7000-8000-${String(number).padStart(12, "0")}`;
const deviceId = "dev_019a3e6a-4111-7000-8000-000000000001";
const source = (number = 1): PowerSource => ({ kind: "entity-state", entity_id: id(number), entity_type: "hearth.power/v1" });
type Entity = FieldEntity & { device_id: string };
const power = (number = 1, on: boolean | null = true): Entity => ({ id: id(number), device_id: deviceId, name: `Power ${number}`, type: "hearth.power/v1", enabled: true, availability: { status: "available" }, support: { state: {}, operations: { set: {} } }, state: on === null ? null : { value: on, observed_at: at } });
const numeric = (number = 20, unit = "W", value = 7.9): Entity => ({ id: id(number), device_id: deviceId, name: unit === "W" ? "Electrical Power" : "Voltage", type: "hearth.numericsensor/v1", enabled: true, availability: { status: "available" }, support: { state: { minimum: 0, maximum: 10000, unit }, operations: {} }, state: { value, observed_at: at } });
const metricSource = (number = 20) => ({ kind: "entity-state" as const, entity_id: id(number), entity_type: "hearth.numericsensor/v1" });
type Post = { id: string; body: { operation: string; parameters: { value: boolean } }; respond: (response: Response) => void };

/** HTTP-shaped Core double; only Command requests are held, while history remains a read. */
function core(values: Entity[], options: { failChanges?: boolean; failGet?: number; historyValue?: boolean; changedAt?: string; previousValue?: boolean } = {}) {
  const entities = new Map(values.map(entity => [entity.id, entity]));
  const posts: Post[] = [];
  const histories: string[] = [], gets: string[] = [];
  const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const url = String(input), entityId = url.split("/")[3], entity = entities.get(entityId);
    if (url.endsWith("/commands")) return new Promise<Response>(respond => posts.push({ id: entityId, body: JSON.parse(String(init?.body)), respond }));
    if (url.endsWith("/state/changes")) {
      if (options.failChanges) return response({ detail: "History unavailable" }, 503);
      const { range } = JSON.parse(String(init?.body));
      const value = options.historyValue ?? entity?.state?.value;
      const interval = value === undefined ? { kind: "unknown", ...range } : { kind: "value", ...range, value };
      const preceding = options.previousValue === undefined ? { kind: "unknown", from: range.from, to: options.changedAt } : { kind: "value", from: range.from, to: options.changedAt, value: options.previousValue };
      const intervals = options.changedAt ? [preceding, { ...interval, from: options.changedAt }] : [interval];
      return response({ entity_id: entityId, select_field: "", range, kind: "timeline", time_axis: "observed_at", coverage: { basis: "retained_changes", seeded: true }, intervals });
    }
    if (url.endsWith("/state/history")) {
      histories.push(entityId);
      const body = JSON.parse(String(init?.body));
      const step = ({ "1m0s": 60000, "5m0s": 300000, "30m0s": 1800000 } as Record<string, number>)[body.result.bucket.duration];
      const from = Date.parse(body.range.from), to = Date.parse(body.range.to), count = Math.ceil((to - from) / step);
      const buckets = Array.from({ length: count }, (_, index) => {
        const value = index === 20 ? null : Number(entity?.state?.value ?? 0);
        return { range: { from: new Date(from + index * step).toISOString(), to: new Date(Math.min(to, from + (index + 1) * step)).toISOString() }, metrics: { min: value, max: value, mean: value } };
      });
      return response({ range: body.range, buckets });
    }
    gets.push(entityId);
    return response(entity ?? {}, options.failGet ?? (entity ? 200 : 404));
  });
  function satisfy(post: Post) {
    const entity = entities.get(post.id)!;
    entity.state = { value: post.body.parameters.value, observed_at: new Date(Date.parse(entity.state?.observed_at ?? at) + 1000).toISOString() };
    post.respond(response({ command_id: "cmd", status: "satisfied", observation_id: "obs", value: post.body.parameters.value }));
  }
  return { entities, posts, histories, gets, satisfy, fail: (post: Post, status = 502) => post.respond(response({ status, detail: "Rejected" }, status)) };
}
const flush = () => act(() => vi.advanceTimersByTimeAsync(0));
async function mount(children: ReactNode) {
  render(<StrictMode><UnitSystemOverride.Provider value="metric"><ViewDataProvider>{children}</ViewDataProvider></UnitSystemOverride.Provider></StrictMode>, { wrapper: MemoryRouter });
  await flush();
}
beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(new Date("2026-11-09T10:00:30Z")); });
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.useRealTimers(); });

it("renders the portable ToggleTile and ToggleList through the shared renderer without sending Commands", async () => {
  const api = core([power()]);
  const binding = source();
  await mount(<ViewRenderer headingLevel={3} definition={{ schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Power", spec: { root: "root", elements: {
    root: { type: "Grid", props: { columns: 4 }, children: ["tile", "list"] },
    tile: { type: "ToggleTile", props: { source: binding, label: "Fan" } },
    list: { type: "ToggleList", props: { title: "Room", rows: [{ source: binding, label: "Fan" }], all_off: true } },
  } } }} />);
  expect(screen.getAllByRole("article")).toHaveLength(2);
  expect(screen.getAllByRole("switch", { name: "Fan power" })).toHaveLength(2);
  expect(api.gets.filter(value => value === binding.entity_id)).toHaveLength(1);
  await act(() => vi.advanceTimersByTimeAsync(35000));
  expect(api.posts).toHaveLength(0);
});

it("has one whole-tile switch, keeps observed State while pending, and shares the existing row queue", async () => {
  const api = core([power(1, false)]);
  await mount(<><BoundToggleTile source={source()} label="Fan" /><BoundToggleList title="Room" rows={[{ source: source(), label: "Fan" }]} /></>);
  const tile = screen.getByRole("article", { name: "Fan" });
  const control = within(tile).getByRole("switch");
  expect(tile.querySelector(".view-switch")).toBeNull();
  fireEvent.click(control); await flush();
  expect(api.posts.map(post => post.body)).toEqual([{ operation: "set", parameters: { value: true } }]);
  expect(control.getAttribute("aria-checked")).toBe("false");
  expect(within(tile).getByText("Off")).toBeTruthy();
  expect(tile.classList.contains("is-on")).toBe(false);
  expect(screen.getAllByText("Turning on…")).toHaveLength(2);
  fireEvent.click(within(screen.getByRole("article", { name: "Room" })).getByRole("switch"));
  expect(api.posts).toHaveLength(1);
  api.satisfy(api.posts[0]); await flush();
  expect(api.posts.map(post => post.body.parameters.value)).toEqual([true, false]);
  api.satisfy(api.posts[1]); await flush(); await flush();
  expect(within(tile).getByText("Off")).toBeTruthy();
  expect(screen.queryByText("Turning off…")).toBeNull();
});

it.each([[502, "Change rejected. Try again."], [504, "No confirmation. It may still change."]])("exposes a %i failure without inventing observed power", async (status, message) => {
  const api = core([power(1, false)]);
  await mount(<BoundToggleTile source={source()} label="Fan" />);
  fireEvent.click(screen.getByRole("switch")); await flush();
  api.fail(api.posts[0], status); await flush();
  expect(screen.getByRole("alert").textContent).toBe(message);
  expect(screen.getByText("Off")).toBeTruthy();
  expect(screen.getByRole("switch").getAttribute("aria-checked")).toBe("false");
  fireEvent.click(screen.getByRole("switch")); await flush();
  expect(screen.queryByRole("alert")).toBeNull();
});

it("keeps Source independent of the tile's Command target", async () => {
  const api = core([power()]);
  await mount(<BoundToggleTile source={source()} label="Fan" />);
  fireEvent.click(screen.getByRole("button", { name: "Source" }));
  expect(screen.getByRole("link", { name: /Fan/ })).toBeTruthy();
  expect(api.posts).toHaveLength(0);
  fireEvent.click(screen.getByRole("button", { name: "Close details" }));
  expect(api.posts).toHaveLength(0);
});

it.each(["disabled", "unavailable", "read-only", "never-observed"])("handles %s power without conflating support, availability and observed State", async state => {
  const entity = power(1, state === "never-observed" ? null : true);
  if (state === "disabled") entity.enabled = false;
  if (state === "unavailable") entity.availability.status = "unavailable";
  if (state === "read-only") entity.support = { state: {}, operations: {} };
  const api = core([entity]);
  await mount(<BoundToggleTile source={source()} label="Fan" />);
  if (state === "read-only") {
    expect(screen.queryByRole("switch")).toBeNull();
    expect(screen.getByText("Read-only")).toBeTruthy();
  } else {
    const button = screen.getByRole("switch") as HTMLButtonElement;
    expect(button.disabled).toBe(state === "disabled");
    fireEvent.click(button); await flush();
    expect(api.posts).toHaveLength(state === "disabled" ? 0 : 1);
  }
  if (state === "never-observed") {
    expect(screen.getByText("Never observed")).toBeTruthy();
    expect(document.querySelector(".view-metric-value")?.textContent).toBe("—");
  }
});

it.each([404, 503])("shows a %i read failure without offering a speculative control", async status => {
  const api = core([power()], { failGet: status });
  await mount(<BoundToggleTile source={source()} label="Fan" />);
  expect(screen.queryByRole("switch")).toBeNull();
  expect(document.querySelector(".view-metric-value")?.textContent).toBe("—");
  expect(api.posts).toHaveLength(0);
});

it("keeps power controllable when since-history fails, and never calls observation age the change time", async () => {
  const api = core([power()], { failChanges: true });
  await mount(<BoundToggleTile source={source()} label="Fan" />);
  expect(screen.getByRole("switch")).toBeTruthy();
  expect(screen.queryByText(/^Since/)).toBeNull();
  expect(screen.getByText("On")).toBeTruthy();
  expect(api.posts).toHaveLength(0);
});

it("states bounded coverage rather than pretending a seeded interval began at the window edge", async () => {
  core([power()]);
  await mount(<BoundToggleTile source={source()} label="Fan" />);
  expect(screen.getByText("On throughout last 24h")).toBeTruthy();
  expect(screen.queryByText(/^Since/)).toBeNull();
});

it("shows a since time only for recorded history matching the current value", async () => {
  core([power()], { changedAt: "2026-11-09T09:50:00Z", previousValue: false });
  await mount(<BoundToggleTile source={source()} label="Fan" />);
  expect(screen.getByText(/^Since/)).toBeTruthy();
  cleanup();
  core([power()], { historyValue: false, changedAt: "2026-11-09T09:50:00Z" });
  await mount(<BoundToggleTile source={source()} label="Fan" />);
  expect(screen.queryByText(/^Since/)).toBeNull();
});

it("does not present the first observation after unknown history as a switch time", async () => {
  core([power()], { changedAt: "2026-11-09T09:50:00Z" });
  await mount(<><BoundToggleTile source={source()} label="Fan" /><BoundToggleList title="Room" rows={[{ source: source(), label: "Fan" }]} /></>);
  expect(screen.getAllByText("On")).toHaveLength(2);
  expect(screen.queryByText(/^Since/)).toBeNull();
  expect(screen.queryByText(/throughout last 24h/)).toBeNull();
});

it("uses one explicit metric for the reading and gap-preserving sparkline without deriving it from power", async () => {
  const watts = numeric();
  const api = core([power(), watts]);
  await mount(<BoundToggleTile source={source()} label="Plug" metric={{ source: metricSource(), window: "24h" }} />);
  expect(document.querySelector(".view-power-metric")?.textContent).toBe("7.9 W");
  expect(api.histories).toEqual([watts.id]);
  expect(document.querySelector(".view-trend-mean")?.getAttribute("d")?.match(/M/g)).toHaveLength(2);
  fireEvent.click(screen.getByRole("switch")); await flush();
  api.satisfy(api.posts[0]); await flush(); await flush();
  expect(document.querySelector(".view-metric-value")?.textContent).toBe("Off7.9 W");
  expect(api.histories).toHaveLength(1);
  watts.state = { value: 0, observed_at: "2026-11-09T10:00:02Z" };
  await act(() => vi.advanceTimersByTimeAsync(10000));
  expect(document.querySelector(".view-power-metric")?.textContent).toBe("0.0 W");
  expect(api.histories).toHaveLength(1);
});

it("does not fall back to a different numeric Entity when the selected metric is missing", async () => {
  core([power(), numeric(21, "V", 120.7)]);
  await mount(<BoundToggleTile source={source()} label="Plug" metric={{ source: metricSource(), window: "24h" }} />);
  expect(document.querySelector(".view-power-metric")?.textContent).toBe("—");
  expect(screen.getByText("Entity not found")).toBeTruthy();
  expect(screen.queryByText(/120\.7/)).toBeNull();
  expect(screen.getByRole("switch")).toBeTruthy();
});

it("keeps an optional detail reading independently bound and states the oldest displayed observation", async () => {
  const energy = numeric(22, "kW.h", 2.55);
  energy.name = "Energy";
  energy.state!.observed_at = "2026-11-09T09:45:00Z";
  const api = core([power(), numeric(), energy]);
  await mount(<BoundToggleTile source={source()} label="Plug" metric={{ source: metricSource(), window: "24h", detail: { source: metricSource(22), label: "Total energy" } }} />);
  expect(screen.getByText("Total energy")).toBeTruthy();
  expect(document.querySelector(".view-card-footer time")?.getAttribute("datetime")).toBe(energy.state!.observed_at);
  expect(document.querySelectorAll(".view-trend")).toHaveLength(1);
  fireEvent.click(screen.getByRole("button", { name: "Source" }));
  expect(screen.getAllByRole("link")).toHaveLength(3);
  expect(api.posts).toHaveLength(0);
});

it("edits one same-Device metric binding for both reading and sparkline without sending Commands", async () => {
  const relay = power(), watts = numeric(), volts = numeric(21, "V", 120.7), elsewhere = numeric(22, "W", 99);
  elsewhere.device_id = "dev_019a3e6a-4111-7000-8000-000000000002"; elsewhere.name = "Other plug draw";
  const api = core([relay, watts, volts, elsewhere]);
  function EditorPreview() {
    const [value, setValue] = useState<z.infer<typeof toggleTileSchema>>({ source: source(), label: "Plug", metric: { source: metricSource(), window: "24h" } });
    return <><ToggleTileEditor value={value} choices={[relay, watts, volts, elsewhere]} onChange={setValue} /><BoundToggleTile {...value} /></>;
  }
  await mount(<EditorPreview />);
  expect(screen.queryByRole("option", { name: /Other plug draw/ })).toBeNull();
  fireEvent.change(screen.getByLabelText("Sparkline source"), { target: { value: `${volts.id}:` } });
  await flush();
  expect(document.querySelector(".view-power-metric")?.textContent).toBe("120.7 V");
  expect(document.querySelector(".view-trend")?.textContent).toContain("V");
  expect(api.histories).toContain(volts.id);
  expect(api.posts).toHaveLength(0);
  fireEvent.change(screen.getByLabelText("Sparkline source"), { target: { value: "" } });
  await flush();
  expect(document.querySelector(".view-power-metric")).toBeNull();
  expect(document.querySelector(".view-trend")).toBeNull();
  expect(api.posts).toHaveLength(0);
});

it("targets eligible group rows once and reports observed Off plus partial failure, not admission", async () => {
  const readonly = power(3), disabled = power(4), unavailable = power(5);
  readonly.support = { state: {}, operations: {} }; disabled.enabled = false; unavailable.availability.status = "unavailable";
  const api = core([power(), power(2, false), readonly, disabled, unavailable]);
  await mount(<BoundToggleList title="Room" all_off rows={[1, 2, 3, 4, 5].map(i => ({ source: source(i), label: `Switch ${i}` }))} />);
  fireEvent.click(screen.getByRole("button", { name: "Turn all off" })); await flush();
  expect(api.posts.map(post => post.id)).toEqual([id(1), id(5)]);
  expect(api.posts.every(post => post.body.parameters.value === false)).toBe(true);
  expect(screen.getByText("Updating 2 switches…")).toBeTruthy();
  expect((screen.getByRole("button", { name: "Turn all off" }) as HTMLButtonElement).disabled).toBe(true);
  api.satisfy(api.posts[0]); api.fail(api.posts[1]); await flush(); await flush();
  expect(screen.getByText("1 of 2 Off · 1 failed")).toBeTruthy();
  expect(screen.getByRole("alert").textContent).toBe("Change rejected. Try again.");
});
