import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { StrictMode } from "react";
import type { ReactNode } from "react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { pairedEntityResponse } from "../../../../../.storybook/paired-fixtures";
import type { FieldEntity } from "../../bindings/entity";
import { ViewDataProvider } from "../../data/provider";
import { BoundAirPurifier } from "./air-purifier";
import { airCategory } from "./category";
import type { AirPurifierProps } from "./schema";

const at = "2026-11-09T10:00:00Z";
const id = (n: number) =>
  `ent_019a3e6a-4111-7000-8000-${String(n).padStart(12, "0")}`;
const device = "dev_019a3e6a-4111-7000-8000-000000000001";
type Entity = FieldEntity & { device_id: string };
const binding = (n: number, type: string) => ({
  kind: "entity-state" as const,
  entity_id: id(n),
  entity_type: type,
});
const powerBinding = (n: number) => ({
  ...binding(n, "hearth.power/v1"),
  entity_type: "hearth.power/v1" as const,
});
const choiceBinding = (n: number) => ({
  ...binding(n, "hearth.enumsetting/v1"),
  entity_type: "hearth.enumsetting/v1" as const,
});
const numericBinding = (n: number) => ({
  ...binding(n, "hearth.numericsensor/v1"),
  entity_type: "hearth.numericsensor/v1" as const,
});
const props: AirPurifierProps = {
  layout: "card",
  label: "Purifier",
  power: powerBinding(1),
  pm25: numericBinding(2),
  filter_life: numericBinding(3),
  mode: choiceBinding(4),
  fan_level: choiceBinding(5),
  display: powerBinding(6),
  child_lock: powerBinding(7),
};
const entity = (
  n: number,
  type: string,
  value: unknown,
  state: object = {},
  set = false
): Entity => ({
  id: id(n),
  device_id: device,
  name: `Entity ${n}`,
  type,
  enabled: true,
  availability: { status: "available" },
  support: { state, operations: set ? { set: {} } : {} },
  state: { value, observed_at: at },
});
const numeric = (n: number, value: number, unit: string): Entity => {
  const result = entity(n, "hearth.numericsensor/v1", value, {
    minimum: 0,
    maximum: 1000,
    unit,
  });
  result.quantities = {
    "": {
      unit,
      label: unit,
      fraction_digits: 0,
      range: { minimum: 0, maximum: 1000 },
    },
  };
  return result;
};
const fixture = () => [
  entity(1, "hearth.power/v1", true, {}, true),
  numeric(2, 7, "µg/m³"),
  numeric(3, 6, "%"),
  entity(
    4,
    "hearth.enumsetting/v1",
    "manual",
    { choices: ["auto", "sleep", "manual"] },
    true
  ),
  entity(
    5,
    "hearth.enumsetting/v1",
    "2",
    { choices: ["1", "2", "3", "4"] },
    true
  ),
  entity(6, "hearth.power/v1", true, {}, true),
  entity(7, "hearth.power/v1", false, {}, true),
];
const autoDisabledFixture = () => {
  const values = fixture();
  if (!values[3].state) {
    throw new Error("fixture mode has no State");
  }
  values[3].state.value = "auto";
  values[3].enabled = false;
  values[4].enabled = false;
  values[5].enabled = false;
  values[1].availability.status = "unavailable";
  return values;
};
interface Post {
  id: string;
  body: { parameters: { value: unknown } };
  respond: (response: Response) => void;
}
const response = (body: unknown, status = 200) =>
  Response.json(body, { status });
const core = (values: Entity[]) => {
  const entities = new Map(values.map((value) => [value.id, value]));
  const posts: Post[] = [];
  vi.spyOn(globalThis, "fetch").mockImplementation((input, init) => {
    const url = String(input);
    const entityId =
      new URL(url, "http://hearth.test").pathname.split("/").at(3) ?? "";
    if (url.endsWith("/commands")) {
      const answer = Promise.withResolvers<Response>();
      posts.push({
        id: entityId,
        body: JSON.parse(String(init?.body)),
        respond: answer.resolve,
      });
      return answer.promise;
    }
    const value = entities.get(entityId);
    return Promise.resolve(
      response(
        value ? pairedEntityResponse(value, value) : {},
        value ? 200 : 404
      )
    );
  });
  return {
    entities,
    posts,
    satisfy(post: Post) {
      const value = entities.get(post.id);
      if (!value) {
        throw new Error(`Unknown Entity ${post.id}`);
      }
      value.state = {
        value: post.body.parameters.value,
        observed_at: "2026-11-09T10:00:01Z",
      };
      post.respond(
        response({
          command_id: "cmd",
          status: "satisfied",
          observation_id: "obs",
          value: post.body.parameters.value,
        })
      );
    },
    fail(post: Post) {
      post.respond(response({ status: 502, detail: "Rejected" }, 502));
    },
  };
};
const flush = () => act(() => vi.advanceTimersByTimeAsync(0));
const mount = async (children: ReactNode) => {
  render(
    <StrictMode>
      <ViewDataProvider>{children}</ViewDataProvider>
    </StrictMode>,
    { wrapper: MemoryRouter }
  );
  await flush();
};

describe("air purifier view", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-11-09T10:00:30Z"));
  });
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it("renders readings, filter warning and observed mode", async () => {
    core(fixture());
    await mount(<BoundAirPurifier {...props} />);
    expect(screen.getByText("On")).toBeTruthy();
    expect(screen.getByText("Manual · fan 2")).toBeTruthy();
    expect(screen.getByText("Good")).toBeTruthy();
    expect(screen.getByText("Replace the filter soon")).toBeTruthy();
  });

  it("offers writable controls and switches", async () => {
    core(fixture());
    await mount(<BoundAirPurifier {...props} />);
    expect(screen.getByRole("group", { name: "Mode" })).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Raise fan level" })
    ).toBeTruthy();
    expect(screen.getByRole("switch", { name: "Display" })).toBeTruthy();
    expect(screen.getByRole("switch", { name: "Child lock" })).toBeTruthy();
  });

  it("reveals independent Source links", async () => {
    core(fixture());
    await mount(<BoundAirPurifier {...props} />);
    fireEvent.click(screen.getByRole("button", { name: "Source" }));
    expect(
      screen.getByRole("link", { name: "Device details" }).getAttribute("href")
    ).toBe(`/devices/${device}`);
    expect(screen.getAllByRole("link")).toHaveLength(8);
  });

  it("omits unbound roles and does not offer controls without set support", async () => {
    const values = fixture();
    values[0].support = { state: {}, operations: {} };
    core(values);
    await mount(
      <BoundAirPurifier layout="card" power={props.power} pm25={props.pm25} />
    );
    expect(screen.queryByRole("switch")).toBeNull();
    expect(screen.queryByText("Filter life")).toBeNull();
    expect(screen.queryByRole("group", { name: "Mode" })).toBeNull();
  });

  it("keeps bound read-only roles in Source without rendering their controls", async () => {
    const values = fixture();
    for (const value of values) {
      const support = value.support as { operations: object };
      support.operations = {};
    }
    core(values);
    await mount(<BoundAirPurifier {...props} />);
    expect(screen.queryByRole("switch")).toBeNull();
    expect(screen.queryByRole("group", { name: "Mode" })).toBeNull();
    expect(screen.queryByRole("group", { name: "Fan level" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Source" }));
    expect(screen.getAllByRole("link")).toHaveLength(8);
  });

  it("sends a mode string and shows the requested mode without changing the observed reading", async () => {
    const api = core(fixture());
    await mount(<BoundAirPurifier {...props} />);
    fireEvent.click(screen.getByRole("button", { name: "Sleep" }));
    await flush();
    expect(api.posts[0].body.parameters).toStrictEqual({ value: "sleep" });
    expect(
      screen.getByRole("button", { name: "Sleep" }).getAttribute("aria-pressed")
    ).toBe("true");
    expect(screen.getByText("Switching to Sleep…")).toBeTruthy();
    expect(screen.getByText("Manual · fan 2")).toBeTruthy();
  });

  it("sends a fan string while a mode change is pending", async () => {
    const api = core(fixture());
    await mount(<BoundAirPurifier {...props} />);
    fireEvent.click(screen.getByRole("button", { name: "Sleep" }));
    await flush();
    fireEvent.click(screen.getByRole("button", { name: "Raise fan level" }));
    await flush();
    expect(api.posts[1].body.parameters).toStrictEqual({ value: "3" });
    expect(screen.getByText("Setting fan level to 3…")).toBeTruthy();
    expect(screen.getByText("Manual · fan 2")).toBeTruthy();
    api.satisfy(api.posts[1]);
    await flush();
    await flush();
    expect(screen.getByText("Switching to Sleep…")).toBeTruthy();
    api.satisfy(api.posts[0]);
    await flush();
    await flush();
    expect(
      document.querySelector('[data-slot="purifier-detail"]')?.textContent
    ).toBe("Sleep");
  });

  it("leaves every mode unselected when no mode has been observed", async () => {
    const values = fixture();
    Reflect.deleteProperty(values[3], "state");
    const api = core(values);
    await mount(<BoundAirPurifier {...props} />);
    const mode = screen.getByRole("group", { name: "Mode" });
    expect(
      within(mode)
        .getAllByRole("button")
        .map((button) => button.getAttribute("aria-pressed"))
    ).toStrictEqual(["false", "false", "false"]);
    expect(api.posts).toHaveLength(0);
    fireEvent.click(within(mode).getByRole("button", { name: "Sleep" }));
    await flush();
    expect(api.posts[0].body.parameters).toStrictEqual({ value: "sleep" });
  });

  it("keeps the most recent failure until that control is requested again", async () => {
    const api = core(fixture());
    await mount(<BoundAirPurifier {...props} />);
    expect(
      screen.getByRole("article").querySelector('[data-slot="control-status"]')
    ).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Raise fan level" }));
    await flush();
    api.fail(api.posts[0]);
    await flush();
    expect(
      within(screen.getByRole("alert")).getByText(
        "Fan level: Change rejected. Try again."
      )
    ).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Raise fan level" }));
    await flush();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByText("Setting fan level to 3…")).toBeTruthy();
  });

  it("names a power request in the shared status", async () => {
    const api = core(fixture());
    await mount(<BoundAirPurifier {...props} />);
    fireEvent.click(screen.getByRole("switch", { name: "Purifier power" }));
    await flush();
    expect(api.posts[0].body.parameters).toStrictEqual({ value: false });
    expect(screen.getByText("Turning off…")).toBeTruthy();
  });

  it("names display and child-lock requests in the shared status", async () => {
    const api = core(fixture());
    await mount(<BoundAirPurifier {...props} />);
    fireEvent.click(screen.getByRole("switch", { name: "Purifier power" }));
    await flush();
    fireEvent.click(screen.getByRole("switch", { name: "Display" }));
    await flush();
    expect(api.posts[1].body.parameters).toStrictEqual({ value: false });
    expect(screen.getByText("Turning the display off…")).toBeTruthy();
    fireEvent.click(screen.getByRole("switch", { name: "Child lock" }));
    await flush();
    expect(api.posts[2].body.parameters).toStrictEqual({ value: true });
    expect(screen.getByText("Locking the controls…")).toBeTruthy();
  });

  it("marks the stored manual level hollow in auto", async () => {
    const values = autoDisabledFixture();
    core(values);
    await mount(<BoundAirPurifier {...props} />);
    expect(screen.getByText("Level 2 kept for Manual")).toBeTruthy();
    expect(
      document.querySelector<HTMLElement>('[data-slot="fan-pips"]')?.dataset
        .stored
    ).toBeDefined();
  });

  it("disables controls for disabled and unavailable Entities", async () => {
    const values = autoDisabledFixture();
    core(values);
    await mount(<BoundAirPurifier {...props} />);
    expect(
      (screen.getByRole("switch", { name: "Display" }) as HTMLButtonElement)
        .disabled
    ).toBeTruthy();
    expect(
      (screen.getByRole("button", { name: "Sleep" }) as HTMLButtonElement)
        .disabled
    ).toBeTruthy();
    expect(
      (
        screen.getByRole("button", {
          name: "Raise fan level",
        }) as HTMLButtonElement
      ).disabled
    ).toBeTruthy();
    const row = screen.getByText("PM2.5").closest("li");
    if (!row) {
      throw new Error("PM2.5 row is missing");
    }
    expect(within(row).getByText("Unavailable")).toBeTruthy();
  });

  it("treats a fan as active when the mode has no manual choice", async () => {
    const values = fixture();
    if (!values[3].state) {
      throw new Error("fixture mode has no State");
    }
    values[3].state.value = "auto";
    values[3].support = {
      state: { choices: ["auto", "sleep"] },
      operations: { set: {} },
    };
    core(values);
    await mount(<BoundAirPurifier {...props} />);
    expect(screen.getByText("Level 2 of 4")).toBeTruthy();
    expect(
      document.querySelector<HTMLElement>('[data-slot="fan-pips"]')?.dataset
        .stored
    ).toBeUndefined();
  });

  it("glows only for a newer changed air or filter reading on the card", async () => {
    const values = fixture();
    core(values);
    await mount(<BoundAirPurifier {...props} />);
    expect(document.querySelector('[data-slot="card-arrival"]')).toBeNull();
    values[0].state = { value: false, observed_at: "2026-11-09T10:00:01Z" };
    await act(() => vi.advanceTimersByTimeAsync(10_000));
    expect(document.querySelector('[data-slot="card-arrival"]')).toBeNull();
    values[1].state = { value: 15, observed_at: "2026-11-09T10:00:02Z" };
    await act(() => vi.advanceTimersByTimeAsync(10_000));
    expect(document.querySelector('[data-slot="card-arrival"]')).toBeTruthy();
    expect(
      screen
        .getByText("PM2.5")
        .closest("li")
        ?.querySelector(':scope > span[aria-hidden="true"]')
    ).toBeTruthy();
  });

  it("shows all-unavailable at card level", async () => {
    const values = fixture();
    for (const value of values) {
      value.availability.status = "unavailable";
    }
    core(values);
    await mount(<BoundAirPurifier {...props} />);
    expect(
      screen.getByRole("article").querySelector("header")?.textContent
    ).toContain("Unavailable");
  });

  it.each([
    [9, "Good"],
    [9.1, "Moderate"],
    [35.5, "Unhealthy for sensitive groups"],
    [55.5, "Unhealthy"],
    [125.5, "Very unhealthy"],
    [225.5, "Hazardous"],
  ])("classifies %s as %s", (value, words) => {
    expect(airCategory(value).words).toBe(words);
  });

  it("renders the quick tile", async () => {
    core(fixture());
    await mount(<BoundAirPurifier {...props} layout="tile" />);
    const card = screen.getByRole("article");
    expect(within(card).getByText("Manual")).toBeTruthy();
    expect(within(card).getByText("Good")).toBeTruthy();
    expect(card.textContent).toContain("PM2.5 7 µg/m³");
  });

  it("sends a single power change from the quick tile", async () => {
    const api = core(fixture());
    await mount(<BoundAirPurifier {...props} layout="tile" />);
    const card = screen.getByRole("article");
    fireEvent.click(within(card).getByRole("switch"));
    await flush();
    expect(api.posts).toHaveLength(1);
    expect(api.posts[0].body.parameters).toStrictEqual({ value: false });
    expect(within(card).getByText("Turning off…")).toBeTruthy();
  });

  it("does not make a read-only tile a press target", async () => {
    const values = fixture();
    values[0].support = { state: {}, operations: {} };
    const api = core(values);
    await mount(<BoundAirPurifier {...props} layout="tile" />);
    expect(screen.queryByRole("switch")).toBeNull();
    expect(screen.getByText("Read-only")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Source" }));
    expect(api.posts).toHaveLength(0);
  });

  it("states the oldest reading it shows, not a hidden read-only setting", async () => {
    const values = fixture();
    values[6].support = { state: {}, operations: {} };
    values[6].state = { value: false, observed_at: "2026-11-01T10:00:00Z" };
    core(values);
    await mount(<BoundAirPurifier {...props} />);
    expect(
      screen.getByRole("article").querySelector("time")?.textContent
    ).not.toMatch(/d ago/u);
  });

  it("lights the tile's edge for a changed PM2.5 reading but not for a power change", async () => {
    const values = fixture();
    core(values);
    await mount(<BoundAirPurifier {...props} layout="tile" />);
    values[0].state = { value: false, observed_at: "2026-11-09T10:00:01Z" };
    await act(() => vi.advanceTimersByTimeAsync(10_000));
    expect(document.querySelector('[data-slot="card-arrival"]')).toBeNull();
    values[1].state = { value: 15, observed_at: "2026-11-09T10:00:02Z" };
    await act(() => vi.advanceTimersByTimeAsync(10_000));
    expect(document.querySelector('[data-slot="card-arrival"]')).toBeTruthy();
  });
});
