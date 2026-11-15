import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { pairedEntityResponse } from "../../.storybook/paired-fixtures";
import EntityDetailPage from "./entity-detail-page";

const renderEntity = (entityId: string) => {
  render(
    <MemoryRouter initialEntries={[`/entities/${entityId}`]}>
      <Routes>
        <Route path="/entities/:entityId" element={<EntityDetailPage />} />
      </Routes>
    </MemoryRouter>
  );
};

const brightnessEntity = {
  id: "ent_brightness",
  type: "hearth.brightness/v1",
  name: "Lamp brightness",
  device_id: "dev_lamp",
  adapter_id: "zwavejs",
  enabled: true,
  support: { state: {}, operations: { set: {} } },
  quantities: { "": { unit: "%", label: "%", fraction_digits: 0 } },
  availability: {
    status: "available",
    source: "adapter",
    since: "2026-11-04T12:00:00Z",
  },
  state: {
    value: 51,
    observation_id: "obs_level",
    observed_at: "2026-11-04T12:00:00Z",
  },
};

const mountBrightness = () => {
  const fetchMock = vi.fn<typeof fetch>((input) => {
    const path = new URL(String(input), "http://localhost").pathname;
    if (path.endsWith("/state/history")) {
      return Promise.resolve(
        Response.json({
          items: [
            {
              observation_id: "obs_level",
              value: 51,
              disposition: "applied",
              observed_at: "2026-11-04T12:00:00Z",
              adapter_received_at: "2026-11-04T12:00:00Z",
            },
          ],
        })
      );
    }
    return Promise.resolve(
      Response.json(
        path === "/v1/entities/ent_brightness"
          ? pairedEntityResponse(brightnessEntity, brightnessEntity)
          : { items: [], next_cursor: null }
      )
    );
  });
  vi.stubGlobal("fetch", fetchMock);
  renderEntity("ent_brightness");
  return fetchMock;
};

const lockEntity = (operations: object) => ({
  id: "ent_lock",
  type: "hearth.lock/v1",
  name: "Door lock",
  device_id: "dev_door",
  adapter_id: "test",
  enabled: true,
  support: { state: {}, operations },
  availability: {
    status: "available",
    source: "adapter",
    since: "2026-11-04T12:00:00Z",
  },
  state: {
    value: "locked",
    observation_id: "obs_lock",
    observed_at: "2026-11-04T12:00:00Z",
  },
});

const mountLock = (operations: object) => {
  const entity = lockEntity(operations);
  const fetchMock = vi.fn<typeof fetch>((input) => {
    const path = new URL(String(input), "http://localhost").pathname;
    return Promise.resolve(
      Response.json(
        path === "/v1/entities/ent_lock"
          ? pairedEntityResponse(entity, entity)
          : { items: [], next_cursor: null }
      )
    );
  });
  vi.stubGlobal("fetch", fetchMock);
  renderEntity("ent_lock");
  return fetchMock;
};

const isReadOnlyRequest = ([, init]: [RequestInfo | URL, RequestInit?]) =>
  !init?.method || init.method === "GET";

describe(EntityDetailPage, () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("offers canonical brightness percentages without support maximum or step", async () => {
    const fetchMock = mountBrightness();
    await screen.findByRole("heading", { name: "Lamp brightness" });
    await screen.findByRole("cell", { name: "51%" });
    expect(screen.getByText("0%", { selector: "svg text" })).toBeTruthy();
    expect(screen.getByText("100%", { selector: "svg text" })).toBeTruthy();
    const parameters =
      screen.getByLabelText<HTMLTextAreaElement>("parameters (JSON)");
    await waitFor(() => expect(parameters.value).toBe('{"value":50}'));
    for (const value of [0, 50, 100]) {
      fireEvent.click(
        screen.getByRole("button", { name: `brightness ${value}%` })
      );
      expect(parameters.value).toBe(`{"value":${value}}`);
    }
    // Selecting a preset only edits the form; it never dispatches a Command.
    expect(fetchMock.mock.calls.every(isReadOnlyRequest)).toBeTruthy();
  });

  it("shows paired raw entity JSON without redundant fields", async () => {
    mountBrightness();
    await screen.findByRole("heading", { name: "Lamp brightness" });
    fireEvent.click(screen.getByRole("button", { name: "Raw entity JSON" }));
    const rawText = document.querySelector("pre")?.textContent;
    if (!rawText) {
      throw new Error("raw entity JSON is missing");
    }
    const raw = JSON.parse(rawText);
    expect(raw.state).toMatchObject({
      canonical: { value: 51, unit: "%" },
      display: { value: 51, unit: "%" },
    });
    expect(raw.state).not.toHaveProperty("value");
    expect(raw).not.toHaveProperty("quantities");
  });

  it("shows lock commands when set is supported", async () => {
    const fetchMock = mountLock({ set: {} });
    await screen.findByRole("heading", { name: "Door lock" });
    const parameters =
      screen.getByLabelText<HTMLTextAreaElement>("parameters (JSON)");
    await waitFor(() => expect(parameters.value).toBe('{"value":"locked"}'));
    fireEvent.click(screen.getByRole("button", { name: "unlock" }));
    expect(parameters.value).toBe('{"value":"unlocked"}');
    fireEvent.click(screen.getByRole("button", { name: "lock" }));
    expect(parameters.value).toBe('{"value":"locked"}');
    expect(fetchMock.mock.calls.every(isReadOnlyRequest)).toBeTruthy();
  });

  it("omits lock controls when set is unsupported", async () => {
    const fetchMock = mountLock({});
    await screen.findByRole("heading", { name: "Door lock" });
    expect(screen.queryByLabelText("parameters (JSON)")).toBeNull();
    expect(screen.queryByRole("button", { name: "lock" })).toBeNull();
    expect(screen.queryByRole("button", { name: "unlock" })).toBeNull();
    expect(fetchMock.mock.calls.every(isReadOnlyRequest)).toBeTruthy();
  });

  it("summarizes a forecast snapshot instead of printing its State inline", async () => {
    const value = {
      periods: [
        {
          starts_at: "2026-11-09T06:00:00Z",
          date: "2026-11-09",
          condition: "rain",
          temperature_min: 1,
          temperature_max: 9,
        },
        {
          starts_at: "2026-11-10T06:00:00Z",
          date: "2026-11-10",
          condition: "clear-day",
          temperature_min: -2,
          temperature_max: 5,
        },
      ],
    };
    const entity = {
      id: "ent_daily",
      type: "hearth.dailyforecast/v1",
      name: "Daily forecast",
      device_id: "dev_weather",
      adapter_id: "pirateweather",
      enabled: true,
      support: { state: {}, operations: {} },
      availability: {
        status: "available",
        source: "adapter",
        since: "2026-11-09T12:00:00Z",
      },
      quantities: Object.fromEntries(
        value.periods.flatMap((_, index) =>
          ["temperature_min", "temperature_max"].map((field) => [
            `/periods/${index}/${field}`,
            { unit: "Cel", label: "°C", fraction_digits: 1 },
          ])
        )
      ),
      state: {
        value,
        observation_id: "obs_daily",
        observed_at: "2026-11-09T12:00:00Z",
      },
    };
    vi.stubGlobal(
      "fetch",
      vi.fn<typeof fetch>((input) => {
        const path = new URL(String(input), "http://localhost").pathname;
        if (path.endsWith("/state/history")) {
          return Promise.resolve(
            Response.json({
              items: [
                {
                  observation_id: "obs_daily",
                  value,
                  disposition: "applied",
                  observed_at: "2026-11-09T12:00:00Z",
                  adapter_received_at: "2026-11-09T12:00:00Z",
                },
              ],
            })
          );
        }
        const canonical = {
          ...entity,
          quantities: Object.fromEntries(
            Object.keys(entity.quantities).map((field) => [
              field,
              { unit: "Cel", label: "Cel", fraction_digits: 1 },
            ])
          ),
        };
        return Promise.resolve(
          Response.json(
            path === "/v1/entities/ent_daily"
              ? pairedEntityResponse(entity, canonical)
              : { items: [], next_cursor: null }
          )
        );
      })
    );
    renderEntity("ent_daily");
    await screen.findByRole("heading", { name: "Daily forecast" });
    await screen.findByRole("cell", {
      name: "2 daily periods from Mon, Nov 9",
    });
    expect(
      screen.getByText(/^2 daily periods from Mon, Nov 9 obs=obs_daily/u)
    ).toBeTruthy();
    expect(
      screen.getByText("Forecast snapshots are listed below; no chart.")
    ).toBeTruthy();
    expect(screen.queryByText(/"periods"/u)).toBeNull();
  });

  it("offers RGB presets and an example without sending a command", async () => {
    const entity = {
      id: "ent_rgb",
      type: "hearth.colorrgb/v1",
      name: "Lamp color",
      device_id: "dev_lamp",
      adapter_id: "esphome",
      enabled: true,
      support: { state: {}, operations: { set: {} } },
      quantities: Object.fromEntries(
        ["red", "green", "blue"].map((field) => [
          `/${field}`,
          { unit: "1", label: "", fraction_digits: 0 },
        ])
      ),
      availability: {
        status: "available",
        source: "adapter",
        since: "2026-11-04T12:00:00Z",
      },
      state: {
        value: { active: true, red: 12, green: 128, blue: 255 },
        observation_id: "obs_rgb",
        observed_at: "2026-11-04T12:00:00Z",
      },
    };
    const fetchMock = vi.fn<typeof fetch>((input) => {
      const path = new URL(String(input), "http://localhost").pathname;
      return Promise.resolve(
        Response.json(
          path === "/v1/entities/ent_rgb"
            ? pairedEntityResponse(entity, entity)
            : { items: [], next_cursor: null }
        )
      );
    });
    vi.stubGlobal("fetch", fetchMock);
    renderEntity("ent_rgb");
    await screen.findByText("RGB 12, 128, 255, active");
    const parameters =
      screen.getByLabelText<HTMLTextAreaElement>("parameters (JSON)");
    await waitFor(() =>
      expect(parameters.value).toBe('{"red":255,"green":0,"blue":0}')
    );
    for (const [label, value] of [
      ["red", '{"red":255,"green":0,"blue":0}'],
      ["green", '{"red":0,"green":255,"blue":0}'],
      ["blue", '{"red":0,"green":0,"blue":255}'],
      ["white", '{"red":255,"green":255,"blue":255}'],
    ]) {
      fireEvent.click(screen.getByRole("button", { name: label }));
      expect(parameters.value).toBe(value);
    }
    expect(fetchMock.mock.calls.every(isReadOnlyRequest)).toBeTruthy();
  });

  it.each(["Good", ""])(
    "shows read-only text State %j without JSON quotes",
    async (value) => {
      const entity = {
        id: "ent_voc",
        type: "hearth.textsensor/v1",
        name: "VOC Quality",
        device_id: "dev_sensor",
        adapter_id: "esphome",
        enabled: true,
        support: { state: {}, operations: {} },
        availability: {
          status: "available",
          source: "adapter",
          since: "2026-11-04T12:00:00Z",
        },
        state: {
          value,
          observation_id: "obs_voc",
          observed_at: "2026-11-04T12:00:00Z",
        },
      };
      vi.stubGlobal(
        "fetch",
        vi.fn<typeof fetch>((input) => {
          const path = new URL(String(input), "http://localhost").pathname;
          return Promise.resolve(
            Response.json(
              path === "/v1/entities/ent_voc"
                ? pairedEntityResponse(entity, entity)
                : { items: [], next_cursor: null }
            )
          );
        })
      );
      renderEntity("ent_voc");
      await screen.findByRole("heading", { name: "VOC Quality" });
      const stateRow = screen
        .getByRole("cell", { name: "State" })
        .closest("tr");
      if (!stateRow) {
        throw new Error("State row is missing");
      }
      const [, stateCell] = stateRow.querySelectorAll("td");
      expect(stateCell.textContent).toBe(
        `${value} obs=obs_voc at 2026-11-04T12:00:00Z`
      );
      expect(screen.getByText("Read-only entity")).toBeTruthy();
      expect(screen.queryByLabelText("parameters (JSON)")).toBeNull();
    }
  );
});
