import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import EntityDetailPage from "./EntityDetailPage";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it("offers canonical brightness percentages without support maximum or step", async () => {
  const entity = {
    id: "ent_brightness", type: "hearth.brightness/v1", name: "Lamp brightness",
    device_id: "dev_lamp", adapter_id: "zwavejs", enabled: true,
    support: { state: {}, operations: { set: {} } },
    availability: { status: "available", source: "adapter", since: "2026-11-04T12:00:00Z" },
    state: { value: 51, observation_id: "obs_level", observed_at: "2026-11-04T12:00:00Z" },
  };
  const fetch = vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => {
    const path = new URL(String(input), "http://localhost").pathname;
    if (path.endsWith("/state/history")) return new Response(JSON.stringify({ items: [{
      observation_id: "obs_level", value: 51, disposition: "applied",
      observed_at: "2026-11-04T12:00:00Z", adapter_received_at: "2026-11-04T12:00:00Z",
    }] }));
    return new Response(JSON.stringify(path === "/v1/entities/ent_brightness" ? entity : { items: [], next_cursor: null }));
  });
  vi.stubGlobal("fetch", fetch);
  render(<MemoryRouter initialEntries={["/entities/ent_brightness"]}>
    <Routes><Route path="/entities/:entityId" element={<EntityDetailPage />} /></Routes>
  </MemoryRouter>);
  await screen.findByRole("heading", { name: "Lamp brightness" });
  await screen.findByRole("cell", { name: "51%" });
  expect(screen.getByText("0%", { selector: "svg text" })).toBeTruthy();
  expect(screen.getByText("100%", { selector: "svg text" })).toBeTruthy();
  const parameters = screen.getByLabelText<HTMLTextAreaElement>("parameters (JSON)");
  await waitFor(() => expect(parameters.value).toBe('{"value":50}'));
  for (const value of [0, 50, 100]) {
    fireEvent.click(screen.getByRole("button", { name: `brightness ${value}%` }));
    expect(parameters.value).toBe(`{"value":${value}}`);
  }
  // Selecting a preset only edits the form; it never dispatches a Command.
  expect(fetch.mock.calls.every(([, init]) => !init?.method || init.method === "GET")).toBe(true);
});

it.each([
  { writable: true, operations: { set: {} } },
  { writable: false, operations: {} },
])("shows lock commands only when set is supported (writable=$writable)", async ({ writable, operations }) => {
  const entity = {
    id: "ent_lock", type: "hearth.lock/v1", name: "Door lock",
    device_id: "dev_door", adapter_id: "test", enabled: true,
    support: { state: {}, operations },
    availability: { status: "available", source: "adapter", since: "2026-11-04T12:00:00Z" },
    state: { value: "locked", observation_id: "obs_lock", observed_at: "2026-11-04T12:00:00Z" },
  };
  const fetch = vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => {
    const path = new URL(String(input), "http://localhost").pathname;
    return new Response(JSON.stringify(path === "/v1/entities/ent_lock" ? entity : { items: [], next_cursor: null }));
  });
  vi.stubGlobal("fetch", fetch);
  render(<MemoryRouter initialEntries={["/entities/ent_lock"]}>
    <Routes><Route path="/entities/:entityId" element={<EntityDetailPage />} /></Routes>
  </MemoryRouter>);
  await screen.findByRole("heading", { name: "Door lock" });
  if (writable) {
    const parameters = screen.getByLabelText<HTMLTextAreaElement>("parameters (JSON)");
    await waitFor(() => expect(parameters.value).toBe('{"value":"locked"}'));
    fireEvent.click(screen.getByRole("button", { name: "unlock" }));
    expect(parameters.value).toBe('{"value":"unlocked"}');
    fireEvent.click(screen.getByRole("button", { name: "lock" }));
    expect(parameters.value).toBe('{"value":"locked"}');
  } else {
    expect(screen.queryByLabelText("parameters (JSON)")).toBeNull();
    expect(screen.queryByRole("button", { name: "lock" })).toBeNull();
    expect(screen.queryByRole("button", { name: "unlock" })).toBeNull();
  }
  expect(fetch.mock.calls.every(([, init]) => !init?.method || init.method === "GET")).toBe(true);
});

it("summarizes a forecast snapshot instead of printing its State inline", async () => {
  const value = { periods: [
    { starts_at: "2026-11-09T06:00:00Z", date: "2026-11-09", condition: "rain", temperature_min: 1, temperature_max: 9 },
    { starts_at: "2026-11-10T06:00:00Z", date: "2026-11-10", condition: "clear-day", temperature_min: -2, temperature_max: 5 },
  ] };
  const entity = {
    id: "ent_daily", type: "hearth.dailyforecast/v1", name: "Daily forecast",
    device_id: "dev_weather", adapter_id: "pirateweather", enabled: true,
    support: { state: {}, operations: {} },
    availability: { status: "available", source: "adapter", since: "2026-11-09T12:00:00Z" },
    state: { value, observation_id: "obs_daily", observed_at: "2026-11-09T12:00:00Z" },
  };
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const path = new URL(String(input), "http://localhost").pathname;
    if (path.endsWith("/state/history")) return new Response(JSON.stringify({ items: [{
      observation_id: "obs_daily", value, disposition: "applied",
      observed_at: "2026-11-09T12:00:00Z", adapter_received_at: "2026-11-09T12:00:00Z",
    }] }));
    return new Response(JSON.stringify(path === "/v1/entities/ent_daily" ? entity : { items: [], next_cursor: null }));
  }));
  render(<MemoryRouter initialEntries={["/entities/ent_daily"]}>
    <Routes><Route path="/entities/:entityId" element={<EntityDetailPage />} /></Routes>
  </MemoryRouter>);
  await screen.findByRole("heading", { name: "Daily forecast" });
  await screen.findByRole("cell", { name: "2 daily periods from Mon, Nov 9" });
  expect(screen.getByText(/^2 daily periods from Mon, Nov 9 obs=obs_daily/)).toBeTruthy();
  expect(screen.getByText("Forecast snapshots are listed below; no chart.")).toBeTruthy();
  expect(screen.queryByText(/"periods"/)).toBeNull();
});

it("offers RGB presets and an example without sending a command", async () => {
  const entity = {
    id: "ent_rgb", type: "hearth.colorrgb/v1", name: "Lamp color",
    device_id: "dev_lamp", adapter_id: "esphome", enabled: true,
    support: { state: {}, operations: { set: {} } },
    availability: { status: "available", source: "adapter", since: "2026-11-04T12:00:00Z" },
    state: { value: { active: true, red: 12, green: 128, blue: 255 }, observation_id: "obs_rgb", observed_at: "2026-11-04T12:00:00Z" },
  };
  const fetch = vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => {
    const path = new URL(String(input), "http://localhost").pathname;
    return new Response(JSON.stringify(path === "/v1/entities/ent_rgb" ? entity : { items: [], next_cursor: null }));
  });
  vi.stubGlobal("fetch", fetch);
  render(<MemoryRouter initialEntries={["/entities/ent_rgb"]}>
    <Routes><Route path="/entities/:entityId" element={<EntityDetailPage />} /></Routes>
  </MemoryRouter>);
  await screen.findByText("RGB 12, 128, 255, active");
  const parameters = screen.getByLabelText<HTMLTextAreaElement>("parameters (JSON)");
  await waitFor(() => expect(parameters.value).toBe('{"red":255,"green":0,"blue":0}'));
  for (const [label, value] of [
    ["red", '{"red":255,"green":0,"blue":0}'],
    ["green", '{"red":0,"green":255,"blue":0}'],
    ["blue", '{"red":0,"green":0,"blue":255}'],
    ["white", '{"red":255,"green":255,"blue":255}'],
  ]) {
    fireEvent.click(screen.getByRole("button", { name: label }));
    expect(parameters.value).toBe(value);
  }
  expect(fetch.mock.calls.every(([, init]) => !init?.method || init.method === "GET")).toBe(true);
});

it.each(["Good", ""])('shows read-only text State %j without JSON quotes', async (value) => {
  const entity = {
    id: "ent_voc", type: "hearth.textsensor/v1", name: "VOC Quality",
    device_id: "dev_sensor", adapter_id: "esphome", enabled: true,
    support: { state: {}, operations: {} },
    availability: { status: "available", source: "adapter", since: "2026-11-04T12:00:00Z" },
    state: { value, observation_id: "obs_voc", observed_at: "2026-11-04T12:00:00Z" },
  };
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const path = new URL(String(input), "http://localhost").pathname;
    return new Response(JSON.stringify(path === "/v1/entities/ent_voc" ? entity : { items: [], next_cursor: null }));
  }));
  render(<MemoryRouter initialEntries={["/entities/ent_voc"]}>
    <Routes><Route path="/entities/:entityId" element={<EntityDetailPage />} /></Routes>
  </MemoryRouter>);
  await screen.findByRole("heading", { name: "VOC Quality" });
  const stateRow = screen.getByRole("cell", { name: "State" }).closest("tr")!;
  expect(stateRow.querySelectorAll("td")[1].textContent).toBe(`${value} obs=obs_voc at 2026-11-04T12:00:00Z`);
  expect(screen.getByText("Read-only entity")).toBeTruthy();
  expect(screen.queryByLabelText("parameters (JSON)")).toBeNull();
});
