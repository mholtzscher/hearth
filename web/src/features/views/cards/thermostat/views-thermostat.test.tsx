import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { StrictMode } from "react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { pairedEntityResponse } from "../../../../../.storybook/paired-fixtures";
import { thermostatEntities } from "../../../../../.storybook/thermostat-fixtures";
import type {
  ThermostatFixture,
  ThermostatState,
} from "../../../../../.storybook/thermostat-fixtures";
import { ViewDataProvider } from "../../data/provider";
import type { ThermostatProps } from "./schema";
import { BoundThermostat } from "./thermostat";

const id = "ent_019a3e6a-4111-7000-8000-000000000501";
const props: ThermostatProps = {
  label: "Hallway thermostat",
  source: {
    kind: "entity-state",
    entity_id: id,
    entity_type: "hearth.thermostat/v1",
  },
};

const heatCool: ThermostatState = {
  mode: "heat_cool",
  activity: "heating",
  current_temperature: 20_000,
  target_temperature_low: 21_000,
  target_temperature_high: 24_444,
  fan_mode: "Automatic",
  current_humidity: 54,
};

interface Post {
  operation: string;
  parameters: Record<string, unknown>;
  respond: (response: Response) => void;
}

const core = (fixture: Omit<ThermostatFixture, "id">) => {
  const posts: Post[] = [];
  const api = {
    fixture: { id, ...fixture } as ThermostatFixture,
    posts,
    report(state: Partial<ThermostatState>) {
      api.fixture = {
        ...api.fixture,
        state: { ...(api.fixture.state as ThermostatState), ...state },
        observedAt: new Date().toISOString(),
      };
    },
    satisfy(post: Post) {
      post.respond(
        Response.json({
          command_id: "cmd",
          status: "satisfied",
          observation_id: "obs",
          value: post.parameters,
        })
      );
    },
    fail(post: Post, status: number) {
      post.respond(Response.json({ status, detail: "failed" }, { status }));
    },
  };
  vi.spyOn(globalThis, "fetch").mockImplementation((input, init) => {
    if (String(input).endsWith("/commands")) {
      const { promise, resolve } = Promise.withResolvers<Response>();
      posts.push({ ...JSON.parse(String(init?.body)), respond: resolve });
      return promise;
    }
    const { display, canonical } = thermostatEntities(api.fixture);
    return Promise.resolve(
      Response.json(pairedEntityResponse(display, canonical))
    );
  });
  return api;
};

const flush = () => act(() => vi.advanceTimersByTimeAsync(0));

const mount = async () => {
  render(
    <StrictMode>
      <ViewDataProvider>
        <BoundThermostat {...props} />
      </ViewDataProvider>
    </StrictMode>,
    { wrapper: MemoryRouter }
  );
  await flush();
};

const targets = () =>
  [...document.querySelectorAll('[data-slot="thermostat-target"]')].map(
    (slot) => slot.textContent
  );
const detail = () =>
  document.querySelector('[data-slot="thermostat-detail"]')?.textContent;
const button = (name: RegExp) => screen.getByRole("button", { name });
const disabled = (name: RegExp) => button(name).hasAttribute("disabled");
const reading = (label: string) =>
  [...document.querySelectorAll('[data-slot="thermostat-target"]')]
    .find((slot) => slot.textContent?.startsWith(label))
    ?.querySelector('[data-slot="reading"]');

describe(BoundThermostat, () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-11-09T10:00:30Z"));
  });
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("leads with the targets Heat & cool uses, then the mode, activity, and readings", async () => {
    core({ state: heatCool });
    await mount();
    expect({
      targets: targets(),
      detail: detail(),
      inside: screen.getByText("Inside").parentElement?.textContent,
      humidity: screen.getByText("Humidity").parentElement?.textContent,
      mode: screen.getByRole<HTMLSelectElement>("combobox", { name: "Mode" })
        .value,
      fan: screen.getByRole<HTMLSelectElement>("combobox", { name: "Fan" })
        .value,
    }).toStrictEqual({
      targets: ["Heat to69.8°", "Cool to76°"],
      detail: "Heat & cool · Heating",
      inside: "Inside68.0°F",
      humidity: "Humidity54%",
      mode: "heat_cool",
      fan: "Automatic",
    });
  });

  it("sends a stepped °F heat target with the cool target and shows it as requested", async () => {
    const api = core({ state: heatCool });
    await mount();
    fireEvent.click(button(/^Raise heat target/u));
    await flush();
    expect(
      api.posts.map(({ operation, parameters }) => ({ operation, parameters }))
    ).toStrictEqual([
      { operation: "set_range", parameters: { low: 21_111, high: 24_444 } },
    ]);
    expect(targets()).toStrictEqual(["Heat to70°", "Cool to76°"]);
    expect(reading("Heat to")?.className).toContain("text-violet-text");
    expect(reading("Cool to")?.className).not.toContain("text-violet-text");
    expect(screen.getByText("Setting heat to 70 °F…").textContent).toBeTruthy();
  });

  it("shows the reported target once the Command is satisfied", async () => {
    const api = core({ state: heatCool });
    await mount();
    fireEvent.click(button(/^Raise heat target/u));
    await flush();
    api.report({ target_temperature_low: 21_111 });
    api.satisfy(api.posts[0]);
    await flush();
    await flush();
    expect(targets()[0]).toBe("Heat to70°");
    expect(reading("Heat to")?.className).not.toContain("text-violet-text");
    expect(screen.queryByText("Setting heat to 70 °F…")).toBeNull();
  });

  it("returns a rejected target to observed State and says why", async () => {
    const api = core({ state: heatCool });
    await mount();
    fireEvent.click(button(/^Lower cool target/u));
    await flush();
    api.fail(api.posts[0], 502);
    await flush();
    await flush();
    expect(targets()[1]).toBe("Cool to76°");
    expect(screen.getByRole("alert").textContent).toContain(
      "Targets: Change rejected. Try again."
    );
  });

  it("sends a single target in half degrees for a Celsius household", async () => {
    const api = core({
      units: "metric",
      operations: ["set_mode", "set_target"],
      modes: ["off", "heat", "cool"],
      fanModes: [],
      state: {
        mode: "heat",
        activity: "idle",
        current_temperature: 20_500,
        target_temperature: 21_000,
      },
    });
    await mount();
    expect(targets()).toStrictEqual(["Heat to21°"]);
    fireEvent.click(button(/^Raise heat target/u));
    await flush();
    expect(api.posts[0]).toMatchObject({
      operation: "set_target",
      parameters: { temperature: 21_500 },
    });
    expect(
      screen.getByText("Setting heat to 21.5 °C…").textContent
    ).toBeTruthy();
    expect(screen.getByRole("group", { name: "Mode" }).tagName).toBe(
      "FIELDSET"
    );
    expect(screen.queryByRole("combobox", { name: "Fan" })).toBeNull();
  });

  it("disables a step that would put the heat target above the cool target", async () => {
    core({
      state: {
        ...heatCool,
        target_temperature_low: 24_444,
        target_temperature_high: 24_444,
      },
    });
    await mount();
    expect({
      raiseHeat: disabled(/^Raise heat target/u),
      lowerHeat: disabled(/^Lower heat target/u),
      raiseCool: disabled(/^Raise cool target/u),
      lowerCool: disabled(/^Lower cool target/u),
    }).toStrictEqual({
      raiseHeat: true,
      lowerHeat: false,
      raiseCool: false,
      lowerCool: true,
    });
  });

  it("disables a step past the advertised maximum", async () => {
    core({ state: { ...heatCool, target_temperature_high: 32_222 } });
    await mount();
    expect(button(/^Raise cool target/u)).toHaveProperty("disabled", true);
    expect(button(/^Lower cool target/u)).toHaveProperty("disabled", false);
  });

  it("names the mode where the targets go when the mode uses none", async () => {
    core({ state: { ...heatCool, mode: "off", activity: "off" } });
    await mount();
    expect(targets()).toStrictEqual(["ModeOffNo target in this mode."]);
    expect(detail()).toBe("");
    expect(screen.queryByRole("button", { name: /target/u })).toBeNull();
  });

  it("shows the requested mode's targets while the mode Command is pending", async () => {
    const api = core({ state: heatCool });
    await mount();
    fireEvent.change(screen.getByRole("combobox", { name: "Mode" }), {
      target: { value: "cool" },
    });
    await flush();
    expect(api.posts[0]).toMatchObject({
      operation: "set_mode",
      parameters: { mode: "cool" },
    });
    expect(targets()).toStrictEqual(["Cool to76°"]);
    expect(detail()).toBe("Heat & cool · Heating");
    expect(screen.getByText("Switching to Cool…").textContent).toBeTruthy();
  });

  it("sends the exact advertised fan choice", async () => {
    const api = core({ state: heatCool });
    await mount();
    fireEvent.change(screen.getByRole("combobox", { name: "Fan" }), {
      target: { value: "Speed 1" },
    });
    await flush();
    expect(api.posts[0]).toMatchObject({
      operation: "set_fan_mode",
      parameters: { fan_mode: "Speed 1" },
    });
  });

  it("dims a disabled thermostat and disables its controls", async () => {
    core({ state: heatCool, enabled: false });
    await mount();
    expect(screen.getByText("Disabled").textContent).toBeTruthy();
    expect(button(/^Raise heat target/u)).toHaveProperty("disabled", true);
    expect(screen.getByRole("combobox", { name: "Mode" })).toHaveProperty(
      "disabled",
      true
    );
    expect(reading("Heat to")?.dataset.value).toBe("dimmed");
  });

  it("keeps targets read-only when the household unit can't be converted", async () => {
    core({ state: heatCool, displayUnit: { unit: "K", label: "K" } });
    await mount();
    expect(button(/^Raise heat target/u)).toHaveProperty("disabled", true);
    expect(button(/^Lower cool target/u)).toHaveProperty("disabled", true);
  });

  it("shows a read-only thermostat's targets without −/+ controls", async () => {
    core({ state: heatCool, operations: [] });
    await mount();
    expect(targets()).toStrictEqual(["Heat to69.8°", "Cool to76°"]);
    expect(screen.queryByRole("button", { name: /target/u })).toBeNull();
    expect(screen.queryByRole("combobox", { name: "Mode" })).toBeNull();
  });
});
