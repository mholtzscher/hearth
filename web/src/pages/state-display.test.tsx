import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import DevicesPage from "./devices-page";
import EntitiesPage from "./entities-page";

describe("state display", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("shows reported text in both the Entities and Device detail tables", async () => {
    const entity = {
      id: "ent_voc",
      type: "hearth.textsensor/v1",
      name: "VOC Quality",
      adapter_id: "esphome",
      enabled: true,
      availability: { status: "available" },
      state: {
        value: "Good <safe>",
        observation_id: "obs",
        observed_at: "2026-11-04T12:00:00Z",
      },
    };
    const device = { id: "dev_sensor", name: "Sensor", kind: "sensor" };
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const path = new URL(String(input), "http://localhost").pathname;
        let response: unknown;
        if (path === "/v1/entities") {
          response = { items: [entity] };
        } else if (path === "/v1/devices") {
          response = { items: [device] };
        } else {
          response = { ...device, entities: [entity] };
        }
        return Promise.resolve(Response.json(response));
      })
    );
    render(<EntitiesPage />, { wrapper: MemoryRouter });
    await expect(
      screen.findByRole("cell", { name: "Good <safe>" })
    ).resolves.toBeTruthy();
    expect(document.querySelector("safe")).toBeNull();
    cleanup();
    render(<DevicesPage />, { wrapper: MemoryRouter });
    fireEvent.click(await screen.findByRole("button", { name: "Sensor" }));
    await expect(
      screen.findByRole("cell", { name: "Good <safe>" })
    ).resolves.toBeTruthy();
    expect(document.querySelector("safe")).toBeNull();
  });
});
