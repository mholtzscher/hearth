import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import DevicesPage from "./DevicesPage";
import EntitiesPage from "./EntitiesPage";

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

it("shows reported text in both the Entities and Device detail tables", async () => {
  const entity = {
    id: "ent_voc", type: "hearth.textsensor/v1", name: "VOC Quality", adapter_id: "esphome", enabled: true,
    availability: { status: "available" }, state: { value: "Good <safe>", observation_id: "obs", observed_at: "2026-11-04T12:00:00Z" },
  };
  const device = { id: "dev_sensor", name: "Sensor", kind: "sensor" };
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const path = new URL(String(input), "http://localhost").pathname;
    const response = path === "/v1/entities" ? { items: [entity] }
      : path === "/v1/devices" ? { items: [device] } : { ...device, entities: [entity] };
    return new Response(JSON.stringify(response));
  }));
  render(<EntitiesPage />, { wrapper: MemoryRouter });
  expect(await screen.findByRole("cell", { name: "Good <safe>" })).toBeTruthy();
  expect(document.querySelector("safe")).toBeNull();
  cleanup();
  render(<DevicesPage />, { wrapper: MemoryRouter });
  fireEvent.click(await screen.findByRole("button", { name: "Sensor" }));
  expect(await screen.findByRole("cell", { name: "Good <safe>" })).toBeTruthy();
  expect(document.querySelector("safe")).toBeNull();
});
