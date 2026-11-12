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
