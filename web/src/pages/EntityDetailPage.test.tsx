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
