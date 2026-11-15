import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { StrictMode } from "react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { BoundDevice } from "../cards/device/device";
import { ViewDataProvider } from "../data/provider";
import { KEYBOARD_SETTLE_MS } from "./brightness-slider";

const deviceId = "dev_019a3e6a-4111-7000-8000-000000000001";
const powerId = "ent_019a3e6a-4111-7000-8000-000000000001";
const brightnessId = "ent_019a3e6a-4111-7000-8000-000000000002";
const settable = { state: {}, operations: { set: {} } };

interface Light {
  on: boolean;
  level: number;
  observedAt: string;
  powerSupport?: unknown;
  powerEnabled?: boolean;
  xy?: boolean;
  canonical?: { on: boolean; level: number; fail?: boolean };
}
interface Post {
  entityId: string;
  body: { operation: string; parameters: { value: unknown } };
  respond: (response: Response) => void;
}

/**
 * A Core that serves one light and holds each Command until the test answers
 * it. Answering with a satisfied outcome first applies the value, as Core does.
 */
const fakeCore = (light: Light) => {
  const posts: Post[] = [];
  const gets: string[] = [];
  const device = (canonical: boolean) => ({
    id: deviceId,
    name: "Lamp",
    next_entity_cursor: null,
    entities: [
      {
        id: powerId,
        type: "hearth.power/v1",
        name: "Power",
        enabled: light.powerEnabled ?? true,
        availability: { status: "available" },
        support: light.powerSupport ?? settable,
        state: {
          value: canonical && light.canonical ? light.canonical.on : light.on,
          observed_at: light.observedAt,
        },
      },
      {
        id: brightnessId,
        type: "hearth.brightness/v1",
        name: "Brightness",
        enabled: true,
        availability: { status: "available" },
        support: settable,
        quantities: {
          "": {
            unit: "%",
            label: "%",
            fraction_digits: 0,
            range: { minimum: 0, maximum: 100 },
          },
        },
        state: {
          value:
            canonical && light.canonical ? light.canonical.level : light.level,
          observed_at: light.observedAt,
        },
      },
      ...(light.xy
        ? [
            {
              id: "ent_color",
              type: "hearth.colorxy/v1",
              name: "Color",
              enabled: true,
              availability: { status: "available" },
              support: {},
              quantities: {
                "/x": {
                  unit: canonical ? "10*-4" : "1",
                  label: canonical ? "10*-4" : "",
                  fraction_digits: canonical ? 0 : 4,
                },
                "/y": {
                  unit: canonical ? "10*-4" : "1",
                  label: canonical ? "10*-4" : "",
                  fraction_digits: canonical ? 0 : 4,
                },
              },
              state: {
                value: canonical
                  ? { active: true, x: 3127, y: 3290 }
                  : { active: true, x: 0.3127, y: 0.329 },
                observation_id: "obs_color",
                observed_at: light.observedAt,
              },
            },
          ]
        : []),
    ],
  });
  vi.spyOn(globalThis, "fetch").mockImplementation((input, init) => {
    const url = String(input);
    if (init?.method === "POST") {
      const { 3: rawEntityId } = url.split("/");
      const entityId = decodeURIComponent(rawEntityId);
      const pending = Promise.withResolvers<Response>();
      posts.push({
        entityId,
        body: JSON.parse(String(init.body)),
        respond: pending.resolve,
      });
      return pending.promise;
    }
    gets.push(url);
    const canonical = url.includes("units=canonical");
    if (canonical && light.canonical?.fail) {
      return Promise.resolve(Response.json({}, { status: 503 }));
    }
    return Promise.resolve(Response.json(device(canonical)));
  });
  const satisfy = (post: Post) => {
    if (post.entityId === powerId) {
      light.on = post.body.parameters.value as boolean;
    } else {
      light.level = post.body.parameters.value as number;
    }
    light.observedAt = new Date(
      Date.parse(light.observedAt) + 1000
    ).toISOString();
    post.respond(
      Response.json({
        command_id: "cmd",
        status: "satisfied",
        observation_id: "obs",
        value: post.body.parameters.value,
      })
    );
  };
  const fail = (post: Post, status: number) =>
    post.respond(
      Response.json({ title: "Problem", status, detail: "detail" }, { status })
    );
  return { posts, gets, satisfy, fail };
};

/** The text of every control status line on screen; idle lines are empty. */
const statuses = () =>
  screen
    .getAllByRole("status")
    .map((status) => status.textContent)
    .filter(Boolean);
const flush = () => act(() => vi.advanceTimersByTimeAsync(0));

/** Rendered under StrictMode, as the app is. The queue unit tests cover reuse after disposal. */
const renderLight = async (cards = 1) => {
  render(
    <StrictMode>
      <ViewDataProvider>
        {Array.from({ length: cards }, (_, i) => (
          <BoundDevice
            key={i}
            device={{ kind: "device", device_id: deviceId }}
          />
        ))}
      </ViewDataProvider>
    </StrictMode>,
    { wrapper: MemoryRouter }
  );
  await flush();
};

const rows = () => [
  ...document.querySelectorAll('[data-slot="control-status"]'),
];
describe("Device light controls", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-11-06T10:00:30Z"));
  });
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it("offers a power switch and a brightness slider for a light whose Entities support set", async () => {
    const core = fakeCore({
      on: false,
      level: 64,
      observedAt: "2026-11-06T10:00:00Z",
    });
    await renderLight();
    const power = screen.getByRole("switch", { name: "Lamp power" });
    expect(power.getAttribute("aria-checked")).toBe("false");
    const slider = screen.getByRole("slider", { name: "Lamp brightness" });
    expect(slider.getAttribute("aria-valuetext")).toBe("64 %");
    expect(slider.getAttribute("min")).toBe("1");
    expect(
      core.gets.some((url) => url.includes("units=canonical"))
    ).toBeFalsy();
    // The slider states the level, so the detail line doesn't repeat it.
    expect(screen.queryByText(/Brightness 64 %/u)).toBeNull();
  });

  it("uses displayed power and brightness while a canonical color read is stale", async () => {
    const light: Light = {
      on: true,
      level: 75,
      observedAt: "2026-11-06T10:00:00Z",
      xy: true,
      canonical: { on: false, level: 25 },
    };
    const core = fakeCore(light);
    await renderLight();
    expect(
      core.gets.some((url) => url.includes("units=canonical"))
    ).toBeTruthy();
    const power = screen.getByRole("switch", { name: "Lamp power" });
    const slider = screen.getByRole("slider", { name: "Lamp brightness" });
    expect(power.getAttribute("aria-checked")).toBe("true");
    expect(slider.getAttribute("aria-valuetext")).toBe("75 %");
  });

  it("keeps displayed power and brightness and explains a failed canonical read", async () => {
    const light: Light = {
      on: true,
      level: 75,
      observedAt: "2026-11-06T10:00:00Z",
      xy: true,
      canonical: { on: false, level: 25 },
    };
    fakeCore(light);
    await renderLight();
    const power = screen.getByRole("switch", { name: "Lamp power" });
    const slider = screen.getByRole("slider", { name: "Lamp brightness" });
    light.canonical = { on: false, level: 25, fail: true };
    light.level = 80;
    await act(() => vi.advanceTimersByTimeAsync(10_000));
    expect(power.getAttribute("aria-checked")).toBe("true");
    expect(slider.getAttribute("aria-valuetext")).toBe("80 %");
    expect(
      screen.getByText(/Couldn't load canonical light State/u)
    ).toBeTruthy();
    expect(
      screen.getByText(
        "Color preview unavailable; canonical reading is missing or out of date."
      )
    ).toBeTruthy();
  });

  it("sends Commands after a canonical color read fails", async () => {
    const light: Light = {
      on: true,
      level: 75,
      observedAt: "2026-11-06T10:00:00Z",
      xy: true,
      canonical: { on: false, level: 25 },
    };
    const core = fakeCore(light);
    await renderLight();
    const power = screen.getByRole("switch", { name: "Lamp power" });
    const slider = screen.getByRole("slider", { name: "Lamp brightness" });
    light.canonical = { on: false, level: 25, fail: true };
    light.level = 80;
    await act(() => vi.advanceTimersByTimeAsync(10_000));
    fireEvent.click(power);
    await flush();
    expect(core.posts[0].body).toStrictEqual({
      operation: "set",
      parameters: { value: false },
    });
    fireEvent.change(slider, { target: { value: "25" } });
    await act(() => vi.advanceTimersByTimeAsync(KEYBOARD_SETTLE_MS));
    expect(core.posts[1].body.parameters.value).toBe(25);
  });

  it("leaves out a switch the power Entity doesn't support, and disables one for a disabled Entity", async () => {
    fakeCore({
      on: true,
      level: 64,
      observedAt: "2026-11-06T10:00:00Z",
      powerSupport: { state: {}, operations: {} },
    });
    await renderLight();
    expect(screen.queryByRole("switch")).toBeNull();
    expect(screen.getByRole("slider")).toBeTruthy();
    cleanup();
    fakeCore({
      on: true,
      level: 64,
      observedAt: "2026-11-06T10:00:00Z",
      powerEnabled: false,
    });
    await renderLight();
    expect(
      (screen.getByRole("switch") as HTMLButtonElement).disabled
    ).toBeTruthy();
  });

  it("sends no Command while rendering and refreshing", async () => {
    const core = fakeCore({
      on: false,
      level: 64,
      observedAt: "2026-11-06T10:00:00Z",
    });
    await renderLight();
    await act(() => vi.advanceTimersByTimeAsync(35_000));
    expect(core.gets.length).toBeGreaterThan(1);
    expect(core.posts).toHaveLength(0);
  });

  it("shows a press as requested before a fresh read confirms it", async () => {
    const core = fakeCore({
      on: false,
      level: 64,
      observedAt: "2026-11-06T10:00:00Z",
    });
    await renderLight();
    const power = screen.getByRole("switch", { name: "Lamp power" });
    fireEvent.click(power);
    await flush();
    expect(core.posts.map((post) => [post.entityId, post.body])).toStrictEqual([
      [powerId, { operation: "set", parameters: { value: true } }],
    ]);
    expect(power.getAttribute("aria-checked")).toBe("true");
    expect(statuses()).toStrictEqual(["Turning on…"]);
    expect(screen.getByText("Off")).toBeTruthy();
  });

  it("returns to observed State after a fresh read confirms the change", async () => {
    const core = fakeCore({
      on: false,
      level: 64,
      observedAt: "2026-11-06T10:00:00Z",
    });
    await renderLight();
    const power = screen.getByRole("switch", { name: "Lamp power" });
    fireEvent.click(power);
    await flush();
    const reads = core.gets.length;
    core.satisfy(core.posts[0]);
    await flush();
    await flush();
    expect(core.gets.length).toBeGreaterThan(reads);
    expect(screen.getByText("On")).toBeTruthy();
    expect(statuses()).toStrictEqual([]);
    expect(power.getAttribute("aria-checked")).toBe("true");
  });

  it("sends one brightness Command when a drag ends", async () => {
    const core = fakeCore({
      on: true,
      level: 40,
      observedAt: "2026-11-06T10:00:00Z",
    });
    await renderLight();
    const slider = screen.getByRole("slider", { name: "Lamp brightness" });
    fireEvent.pointerDown(slider);
    fireEvent.change(slider, { target: { value: "60" } });
    fireEvent.change(slider, { target: { value: "75" } });
    await flush();
    expect(core.posts).toHaveLength(0);
    expect(slider.getAttribute("aria-valuetext")).toBe("75 %");
    fireEvent.pointerUp(slider);
    await flush();
    expect(core.posts.map((post) => post.body.parameters.value)).toStrictEqual([
      75,
    ]);
    expect(statuses()).toStrictEqual(["Setting brightness to 75 %…"]);
  });

  it("sends one brightness Command after keyboard steps settle", async () => {
    const core = fakeCore({
      on: true,
      level: 40,
      observedAt: "2026-11-06T10:00:00Z",
    });
    await renderLight();
    const slider = screen.getByRole("slider", { name: "Lamp brightness" });
    fireEvent.pointerDown(slider);
    fireEvent.change(slider, { target: { value: "60" } });
    fireEvent.change(slider, { target: { value: "75" } });
    await flush();
    fireEvent.pointerUp(slider);
    await flush();
    core.satisfy(core.posts[0]);
    await flush();
    await flush();

    for (const value of ["76", "77", "78", "79", "80"]) {
      fireEvent.change(slider, { target: { value } });
    }
    await act(() => vi.advanceTimersByTimeAsync(KEYBOARD_SETTLE_MS - 1));
    expect(core.posts).toHaveLength(1);
    await act(() => vi.advanceTimersByTimeAsync(1));
    expect(core.posts.map((post) => post.body.parameters.value)).toStrictEqual([
      75, 80,
    ]);
  });

  it("lets a drag replace keyboard steps that haven't settled", async () => {
    const core = fakeCore({
      on: true,
      level: 40,
      observedAt: "2026-11-06T10:00:00Z",
    });
    await renderLight();
    const slider = screen.getByRole("slider", { name: "Lamp brightness" });
    fireEvent.change(slider, { target: { value: "41" } });
    await act(() => vi.advanceTimersByTimeAsync(KEYBOARD_SETTLE_MS - 100));
    fireEvent.pointerDown(slider);
    await act(() => vi.advanceTimersByTimeAsync(200));
    fireEvent.change(slider, { target: { value: "70" } });
    expect(core.posts).toHaveLength(0);
    fireEvent.pointerUp(slider);
    await flush();
    expect(core.posts.map((post) => post.body.parameters.value)).toStrictEqual([
      70,
    ]);
  });

  it("keeps each control's status row in place while a Command is pending", async () => {
    fakeCore({
      on: false,
      level: 64,
      observedAt: "2026-11-06T10:00:00Z",
    });
    await renderLight();
    const idle = rows();
    const unchanged = () => {
      for (const [index, row] of rows().entries()) {
        expect(row).toBe(idle[index]);
      }
    };
    expect(idle).toHaveLength(2);
    expect(idle.map((row) => row.textContent)).toStrictEqual(["", ""]);
    fireEvent.click(screen.getByRole("switch", { name: "Lamp power" }));
    await flush();
    expect(rows()).toHaveLength(2);
    unchanged();
    expect(idle[0].textContent).toBe("Turning on…");
  });

  it("keeps each control's status row in place when a Command fails", async () => {
    const core = fakeCore({
      on: false,
      level: 64,
      observedAt: "2026-11-06T10:00:00Z",
    });
    await renderLight();
    const idle = rows();
    const unchanged = () => {
      for (const [index, row] of rows().entries()) {
        expect(row).toBe(idle[index]);
      }
    };
    fireEvent.click(screen.getByRole("switch", { name: "Lamp power" }));
    await flush();
    core.fail(core.posts[0], 502);
    await flush();
    expect(rows()).toHaveLength(2);
    unchanged();
    expect(within(idle[0] as HTMLElement).getByRole("alert").textContent).toBe(
      "The light rejected the change."
    );
  });

  it("sends the first press and then only the last of the presses made while it was pending", async () => {
    const core = fakeCore({
      on: false,
      level: 64,
      observedAt: "2026-11-06T10:00:00Z",
    });
    await renderLight();
    const power = screen.getByRole("switch", { name: "Lamp power" });
    fireEvent.click(power);
    fireEvent.click(power);
    fireEvent.click(power);
    await flush();
    expect(core.posts).toHaveLength(1);
    core.satisfy(core.posts[0]);
    await flush();
    expect(core.posts.map((post) => post.body.parameters.value)).toStrictEqual([
      true,
      true,
    ]);
  });

  it.each([
    [502, "The light rejected the change."],
    [504, "The light didn't confirm in time. It may still change."],
    [503, "Couldn't reach the light. Its adapter or the light is offline."],
  ])(
    "names a %i failure until the next press and returns to observed State",
    async (status, text) => {
      const core = fakeCore({
        on: false,
        level: 64,
        observedAt: "2026-11-06T10:00:00Z",
      });
      await renderLight();
      const power = screen.getByRole("switch", { name: "Lamp power" });
      fireEvent.click(power);
      await flush();
      core.fail(core.posts[0], status);
      await flush();
      expect(screen.getByRole("alert").textContent).toBe(text);
      expect(power.getAttribute("aria-checked")).toBe("false");
      await act(() => vi.advanceTimersByTimeAsync(20_000));
      expect(screen.getByRole("alert").textContent).toBe(text);
      fireEvent.click(power);
      await flush();
      expect(screen.queryByRole("alert")).toBeNull();
    }
  );

  it("shares one control state between cards showing the same light", async () => {
    fakeCore({ on: false, level: 64, observedAt: "2026-11-06T10:00:00Z" });
    await renderLight(2);
    const cards = screen.getAllByRole("article");
    fireEvent.click(within(cards[0]).getByRole("switch"));
    await flush();
    for (const card of cards) {
      expect(
        within(card).getByRole("switch").getAttribute("aria-checked")
      ).toBe("true");
      expect(
        within(card)
          .getAllByRole("status")
          .some((status) => status.textContent === "Turning on…")
      ).toBeTruthy();
    }
  });
});
