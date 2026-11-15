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

import examples from "../../../../../../contracts/views/v1/examples.json";
import {
  pairedEntityResponse,
  pairedHistoryResponse,
} from "../../../../../.storybook/paired-fixtures";
import type { FieldEntity } from "../../bindings/entity";
import { ViewDataProvider } from "../../data/provider";
import { ViewRenderer } from "../../renderer/view-renderer";
import { BoundOutsideNow } from "../outside-now/outside-now";
import { BoundPressure } from "../pressure/pressure";
import { BoundRain } from "../rain/rain";
import { BoundWind } from "../wind/wind";

const at = "2026-11-09T10:00:00Z";
const id = (number: number) =>
  `ent_01900000-0000-7000-8000-${String(number).padStart(12, "0")}`;
const exampleByName = (name: string) => {
  const found = examples.find((item) => item.name === name);
  if (!found) {
    throw new Error(`Missing ${name} example`);
  }
  return found;
};
const weather = exampleByName("weather-cards").definition;
type History = (minutesAgo: number) => number | null;
type UnitSystem = "metric" | "us";

const fixedUnits: Record<string, string> = {
  "hearth.temperature/v1": "mCel",
  "hearth.relativehumidity/v1": "%",
  "hearth.speed/v1": "m/s",
  "hearth.pressure/v1": "hPa",
};
interface Projection {
  unit: string;
  label: string;
  fraction_digits: number;
  convert: (value: number) => number;
}
const same = (unit: string, label: string): Projection => ({
  unit,
  label,
  fraction_digits: 1,
  convert: (value) => value,
});
const shared: Record<string, Projection> = {
  "%": same("%", "%"),
  deg: same("deg", "°"),
};
/** Core's household projections of the station's canonical units. */
const projections: Record<UnitSystem, Record<string, Projection>> = {
  metric: {
    ...shared,
    mCel: { ...same("Cel", "°C"), convert: (value) => value / 1000 },
    "m/s": same("m/s", "m/s"),
    hPa: same("hPa", "hPa"),
    "mm/h": same("mm/h", "mm/h"),
    mm: same("mm", "mm"),
  },
  us: {
    ...shared,
    mCel: {
      unit: "[degF]",
      label: "°F",
      fraction_digits: 1,
      convert: (value) => (value * 9) / 5000 + 32,
    },
    "m/s": {
      unit: "[mi_i]/h",
      label: "mi/h",
      fraction_digits: 1,
      convert: (value) => (value * 3600) / 1609.344,
    },
    hPa: {
      unit: "[in_i'Hg]",
      label: "in Hg",
      fraction_digits: 2,
      convert: (value) => value / 33.863788,
    },
    "mm/h": {
      unit: "[in_i]/h",
      label: "in/h",
      fraction_digits: 2,
      convert: (value) => value / 25.4,
    },
    mm: {
      unit: "[in_i]",
      label: "in",
      fraction_digits: 2,
      convert: (value) => value / 25.4,
    },
  },
};

const entity = (
  number: number,
  type: string,
  value: number | null,
  unit?: string,
  quantity_kind?: string
): FieldEntity => ({
  id: id(number),
  name: `Sensor ${number}`,
  type,
  enabled: true,
  availability: { status: "available" },
  support: unit
    ? {
        state: {
          minimum: 0,
          maximum: 10_000,
          unit,
          ...(quantity_kind ? { quantity_kind } : {}),
        },
        operations: {},
      }
    : { state: {}, operations: {} },
  state: value === null ? null : { value, observed_at: at },
});

/** The shared example's station during a rainstorm, in canonical units. */
const station = (
  overrides: Record<number, Partial<FieldEntity>> = {}
): FieldEntity[] => {
  const sensor = "hearth.numericsensor/v1";
  return [
    entity(101, "hearth.temperature/v1", 9400),
    entity(102, "hearth.relativehumidity/v1", 94),
    entity(103, "hearth.speed/v1", 6.3),
    entity(104, sensor, 135, "deg", "wind_direction"),
    entity(105, sensor, 6.8, "mm/h", "precipitation_rate"),
    ...(
      [
        [106, 16.2],
        [107, 18.6],
        [108, 22.9],
        [109, 71.4],
        [110, 688.1],
      ] as const
    ).map(([number, value]) =>
      entity(number, sensor, value, "mm", "precipitation_depth")
    ),
    entity(111, "hearth.speed/v1", 11.2),
    entity(112, "hearth.speed/v1", 14.8),
    entity(113, "hearth.pressure/v1", 1003.2),
  ].map((item) => ({ ...item, ...overrides[Number(item.id.slice(-3))] }));
};

const response = (body: unknown, status = 200) =>
  Response.json(body, { status });

/**
 * HTTP-shaped Core double. It stores canonical State and history, projects
 * them into the household system and pairs both projections. Like Core, it projects a generic sensor only when the
 * sensor declares a quantity kind. History defaults to the current value in
 * every bucket.
 */
const core = (
  entities: FieldEntity[],
  histories: Record<number, History> = {},
  system: UnitSystem = "metric"
) => {
  const byId = new Map(entities.map((item) => [item.id, item]));
  const historyFailures = new Set<string>();
  const stateFailures = new Set<string>();
  const heldReads = new Map<string, Promise<void>>();
  const posts: string[] = [];
  const requests: string[] = [];
  const present = (item: FieldEntity, canonical: boolean) => {
    const support = (
      item.support as { state: { unit?: string; quantity_kind?: string } }
    ).state;
    const unit = fixedUnits[item.type] ?? support.unit;
    if (!unit) {
      throw new Error(`Missing unit for ${item.type}`);
    }
    const projection =
      fixedUnits[item.type] || support.quantity_kind
        ? projections[system][unit]
        : same(unit, unit);
    return {
      quantity: canonical
        ? { unit, label: unit, fraction_digits: 1 }
        : {
            unit: projection.unit,
            label: projection.label,
            fraction_digits: projection.fraction_digits,
          },
      value: (value: number | null) =>
        canonical || value === null ? value : projection.convert(value),
    };
  };
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const url = String(input);
    const [path] = url.split("?");
    const [entityId] = path.split("/").slice(3);
    const found = byId.get(entityId);
    requests.push(url);
    if (!path.endsWith("/state/history")) {
      await heldReads.get(entityId);
    }
    if (path.endsWith("/commands")) {
      posts.push(entityId);
      return response({}, 500);
    }
    if (!path.endsWith("/state/history") && stateFailures.has(entityId)) {
      return response({ detail: "Reading unavailable" }, 503);
    }
    if (path.endsWith("/state/history")) {
      if (historyFailures.has(entityId)) {
        return response({ detail: "History unavailable" }, 503);
      }
      if (!found) {
        return response({}, 404);
      }
      const body = JSON.parse(String(init?.body));
      const shown = present(found, false);
      const step = (
        { "1m0s": 60_000, "5m0s": 300_000, "30m0s": 1_800_000 } as Record<
          string,
          number
        >
      )[body.result.bucket.duration];
      const from = Date.parse(body.range.from);
      const to = Date.parse(body.range.to);
      const count = Math.ceil((to - from) / step);
      const history =
        histories[Number(entityId.slice(-3))] ??
        (() => Number(found.state?.value ?? 0));
      const buckets = (working: boolean) =>
        Array.from({ length: count }, (_, index) => {
          const start = from + index * step;
          const value = present(found, working).value(
            history(Math.round((to - start) / 60_000))
          );
          return {
            range: {
              from: new Date(start).toISOString(),
              to: new Date(Math.min(to, start + step)).toISOString(),
            },
            metrics: { min: value, max: value, mean: value },
          };
        });
      const summary = {
        range: body.range,
        quantity: shown.quantity,
        buckets: buckets(false),
      };
      return response(
        pairedHistoryResponse(summary, {
          quantity: present(found, true).quantity,
          buckets: buckets(true),
        })
      );
    }
    if (!found) {
      return response({}, 404);
    }
    const representation = (working: boolean) => {
      const shown = present(found, working);
      const { state } = found;
      return {
        ...found,
        quantities: { "": shown.quantity },
        state: state && { ...state, value: shown.value(state.value as number) },
      };
    };
    return response(
      pairedEntityResponse(representation(false), representation(true))
    );
  });
  return {
    posts,
    requests,
    historyFailures,
    stateFailures,
    heldReads,
    entities: byId,
  };
};

const entityOf = (api: ReturnType<typeof core>, number: number) => {
  const found = api.entities.get(id(number));
  if (!found) {
    throw new Error(`Missing entity ${number}`);
  }
  return found;
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
  await flush();
};
const card = (name: string) => screen.getByRole("article", { name });
const source = <T extends string>(number: number, type: T) => ({
  kind: "entity-state" as const,
  entity_id: id(number),
  entity_type: type,
});
const slot = <T extends Element = HTMLElement>(
  root: ParentNode | null | undefined,
  name: string
) => root?.querySelector<T>(`[data-slot="${name}"]`) ?? null;
const reading = (root: ParentNode) => slot(root, "reading")?.textContent;
const ascending = (values: number[]) =>
  values.every((value, index) => index === 0 || values[index - 1] <= value);

/**
 * A pending read the test releases explicitly, standing in for a Core request
 * that has not answered yet.
 */
const holdRead = () => {
  const { promise, resolve } = Promise.withResolvers<undefined>();
  return { pending: promise, release: () => resolve(undefined) };
};

/** The cached temperature history interval shown in the card's Source panel. */
const historyInterval = () =>
  ["Temperature history from", "Temperature history to"].map((label) => {
    const time = screen.getByText(label).parentElement?.querySelector("time");
    if (!time) {
      throw new Error(`Missing ${label} time`);
    }
    return time.dateTime;
  });

const rainSources = () => {
  const rate = source(105, "hearth.numericsensor/v1");
  const total = (number: number) => ({ ...rate, entity_id: id(number) });
  return {
    rate,
    storm: total(107),
    totals: {
      day: total(106),
      week: total(108),
      month: total(109),
      year: total(110),
    },
  };
};

describe("Weather cards", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-11-09T10:00:30Z"));
  });
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it.each(["metric", "us"] as const)(
    "words the shared weather example from canonical readings in a %s household",
    async (system) => {
      core(
        station(),
        { 101: (minutes) => 9400 + (minutes > 600 ? 1500 : 0) },
        system
      );
      await mount(<ViewRenderer headingLevel={3} definition={weather} />);
      const us = system === "us";
      expect(screen.getAllByRole("article")).toHaveLength(4);
      const outside = card("Outside");
      expect(reading(outside)).toBe(us ? "48.9°F" : "9.4°C");
      // 6.8 mm/h is Rain and 6.3 m/s is force 4, whatever unit the card shows.
      expect(within(outside).getByText("Rain · Breezy")).toBeTruthy();
      expect(
        within(outside).getByText(
          us ? "24 h high 51.6, low 48.9 °F" : "24 h high 10.9, low 9.4 °C"
        )
      ).toBeTruthy();
      expect(within(outside).getByText("Wind from SE")).toBeTruthy();
    }
  );

  it.each(["metric", "us"] as const)(
    "words the Wind and Pressure cards in a %s household",
    async (system) => {
      core(
        station(),
        { 101: (minutes) => 9400 + (minutes > 600 ? 1500 : 0) },
        system
      );
      await mount(<ViewRenderer headingLevel={3} definition={weather} />);
      const us = system === "us";
      expect(within(card("Wind")).getByText("Moderate breeze")).toBeTruthy();
      expect(
        within(card("Wind")).getByText(
          us ? "Gusts 25.1 mi/h" : "Gusts 11.2 m/s"
        )
      ).toBeTruthy();
      expect(reading(card("Pressure"))).toMatch(
        us ? /^29\.62in\sHg$/u : /^1003\.2hPa$/u
      );
    }
  );

  it.each(["metric", "us"] as const)(
    "words the Rain card and its absent secondary readings in a %s household",
    async (system) => {
      core(
        station(),
        { 101: (minutes) => 9400 + (minutes > 600 ? 1500 : 0) },
        system
      );
      await mount(<ViewRenderer headingLevel={3} definition={weather} />);
      const us = system === "us";
      expect(
        within(card("Rain")).getByText(
          us
            ? "This storm · Rain now, 0.27 in/h"
            : "This storm · Rain now, 6.8 mm/h"
        )
      ).toBeTruthy();
      expect(reading(card("Rain"))).toBe(us ? "0.73in" : "18.6mm");
      expect(document.body.textContent).not.toMatch(/Feels like|Dew point/u);
    }
  );

  it.each(["metric", "us"] as const)(
    "reads each shared station entity once in a %s household",
    async (system) => {
      const api = core(
        station(),
        { 101: (minutes) => 9400 + (minutes > 600 ? 1500 : 0) },
        system
      );
      await mount(<ViewRenderer headingLevel={3} definition={weather} />);
      expect(api.requests.every((url) => !url.includes("?"))).toBeTruthy();
      const speedReads = api.requests.filter(
        (url) => url === `/v1/entities/${id(103)}`
      );
      // Outside and Wind share one read, including their formula inputs.
      expect(speedReads).toHaveLength(1);
      await act(() => vi.advanceTimersByTimeAsync(35_000));
      expect(api.posts).toHaveLength(0);
    }
  );

  it("keeps the complete weather reading visible until a refresh arrives", async () => {
    const api = core(station(), {}, "us");
    await mount(<ViewRenderer headingLevel={3} definition={weather} />);
    const held = holdRead();
    api.heldReads.set(id(103), held.pending);
    entityOf(api, 103).state = {
      value: 9.4,
      observed_at: "2026-11-09T10:00:40Z",
    };

    await act(() => vi.advanceTimersByTimeAsync(10_000));
    expect(reading(card("Wind"))).toBe("14.1mi/h");
    expect(within(card("Wind")).getByText("Moderate breeze")).toBeTruthy();
    expect(within(card("Outside")).getByText("Rain · Breezy")).toBeTruthy();
    expect(within(card("Outside")).queryByText("Wind direction")).toBeNull();

    held.release();
    await flush();
  });

  it("shows the refreshed weather reading once a held read arrives", async () => {
    const api = core(station(), {}, "us");
    await mount(<ViewRenderer headingLevel={3} definition={weather} />);
    const held = holdRead();
    api.heldReads.set(id(103), held.pending);
    entityOf(api, 103).state = {
      value: 9.4,
      observed_at: "2026-11-09T10:00:40Z",
    };

    await act(() => vi.advanceTimersByTimeAsync(10_000));
    held.release();
    await flush();
    expect(reading(card("Wind"))).toBe("21.0mi/h");
    expect(within(card("Wind")).getByText("Fresh breeze")).toBeTruthy();
    expect(within(card("Outside")).getByText("Rain · Windy")).toBeTruthy();
    expect(api.posts).toHaveLength(0);
  });

  it("retains both representations and their weather words when a refresh fails", async () => {
    const api = core(station(), {}, "us");
    await mount(<ViewRenderer headingLevel={3} definition={weather} />);
    api.stateFailures.add(id(105));
    api.stateFailures.add(id(103));
    await act(() => vi.advanceTimersByTimeAsync(10_000));
    expect(reading(card("Wind"))).toBe("14.1mi/h");
    expect(within(card("Wind")).getByText("Moderate breeze")).toBeTruthy();
    expect(slot(card("Wind"), "weather-compass-arrow")).toBeTruthy();
    expect(within(card("Outside")).getByText("Rain · Breezy")).toBeTruthy();
    expect(within(card("Outside")).queryByText("Wind direction")).toBeNull();
  });

  it("keeps the rain report while its readings cannot refresh", async () => {
    const api = core(station(), {}, "us");
    await mount(<ViewRenderer headingLevel={3} definition={weather} />);
    api.stateFailures.add(id(105));
    api.stateFailures.add(id(103));
    await act(() => vi.advanceTimersByTimeAsync(10_000));
    expect(slot(card("Outside"), "weather-rows")?.textContent).not.toContain(
      "Rain rate"
    );
    expect(
      within(card("Rain")).getByText("This storm · Rain now, 0.27 in/h")
    ).toBeTruthy();
    expect(within(card("Wind")).getByText("Couldn't refresh")).toBeTruthy();
  });

  it("restores the weather words when a refresh recovers", async () => {
    const api = core(station(), {}, "us");
    await mount(<ViewRenderer headingLevel={3} definition={weather} />);
    api.stateFailures.add(id(105));
    api.stateFailures.add(id(103));
    await act(() => vi.advanceTimersByTimeAsync(10_000));

    api.stateFailures.clear();
    await act(() => vi.advanceTimersByTimeAsync(10_000));
    expect(within(card("Wind")).getByText("Moderate breeze")).toBeTruthy();
    expect(within(card("Outside")).getByText("Rain · Breezy")).toBeTruthy();
    expect(within(card("Outside")).getByText("Wind from SE")).toBeTruthy();
  });

  it("hides fallback rows and keeps the storm total after a refresh recovers", async () => {
    const api = core(station(), {}, "us");
    await mount(<ViewRenderer headingLevel={3} definition={weather} />);
    api.stateFailures.add(id(105));
    api.stateFailures.add(id(103));
    await act(() => vi.advanceTimersByTimeAsync(10_000));

    api.stateFailures.clear();
    await act(() => vi.advanceTimersByTimeAsync(10_000));
    expect(within(card("Outside")).queryByText("Wind direction")).toBeNull();
    expect(within(card("Outside")).queryByText("Rain rate")).toBeNull();
    expect(
      within(card("Rain")).getByText("This storm · Rain now, 0.27 in/h")
    ).toBeTruthy();
    expect(api.posts).toHaveLength(0);
  });

  it("leads with the storm total and keeps totals as the station's counters", async () => {
    core(station());
    await mount(<BoundRain {...rainSources()} />);
    expect(reading(card("Rain"))).toBe("18.6mm");
    expect(slot(card("Rain"), "rain-totals")?.textContent).toBe(
      "Today 16.2 · This week 22.9 · This month 71.4 · This year 688.1 mm"
    );
  });

  it("shows Dry when the storm and its rate have stopped", async () => {
    core(
      station({
        105: { state: { value: 0, observed_at: at } },
        107: { state: { value: 0, observed_at: at } },
      })
    );
    await mount(<BoundRain {...rainSources()} />);
    expect(reading(card("Rain"))).toBe("Dry");
    expect(within(card("Rain")).getByText("Rate 0.0 mm/h")).toBeTruthy();
  });

  it("leads with the current rate when the storm has stopped", async () => {
    core(station({ 107: { state: { value: 0, observed_at: at } } }));
    await mount(<BoundRain {...rainSources()} />);
    expect(reading(card("Rain"))).toBe("6.8mm/h");
    expect(slot(card("Rain"), "weather-conditions")?.textContent).toBe("Rain");
  });

  it("labels each total when Core projects the counters into different units", async () => {
    const rate = source(105, "hearth.numericsensor/v1");
    const total = (number: number) => ({ ...rate, entity_id: id(number) });
    const unclassified = {
      support: {
        state: { minimum: 0, maximum: 10_000, unit: "mm" },
        operations: {},
      },
    };
    core(station({ 108: unclassified }), {}, "us");
    await mount(
      <BoundRain rate={rate} totals={{ day: total(106), week: total(108) }} />
    );
    expect(slot(card("Rain"), "rain-totals")?.textContent).toBe(
      "Today 0.64 in · This week 22.9 mm"
    );
    cleanup();
    vi.restoreAllMocks();

    core(station(), {}, "us");
    await mount(
      <BoundRain rate={rate} totals={{ day: total(106), week: total(108) }} />
    );
    expect(slot(card("Rain"), "rain-totals")?.textContent).toBe(
      "Today 0.64 · This week 0.90 in"
    );
  });

  it("states one shared status for totals from a station that stopped reporting", async () => {
    const rate = source(105, "hearth.numericsensor/v1");
    const total = (number: number) => ({ ...rate, entity_id: id(number) });
    const unavailable = { availability: { status: "unavailable" } };
    core(station({ 106: unavailable, 108: unavailable, 109: unavailable }));
    await mount(
      <BoundRain
        rate={rate}
        totals={{ day: total(106), week: total(108), month: total(109) }}
      />
    );
    const statuses = card("Rain").querySelectorAll(
      '[data-slot="weather-slot-status"]'
    );
    expect([...statuses].map((status) => status.textContent)).toStrictEqual([
      "Rain totalsUnavailable",
    ]);
    cleanup();
    vi.restoreAllMocks();

    core(station({ 106: unavailable }));
    await mount(
      <BoundRain rate={rate} totals={{ day: total(106), week: total(108) }} />
    );
    expect(
      [
        ...card("Rain").querySelectorAll('[data-slot="weather-slot-status"]'),
      ].map((status) => status.textContent)
    ).toStrictEqual(["TodayUnavailable"]);
  });

  it.each(
    ["unavailable", "disabled"].flatMap((state) => [
      {
        state,
        mode: "storm rain",
        stormValue: 18.6,
        rateValue: 6.8,
        number: 105,
      },
      {
        state,
        mode: "storm stopped",
        stormValue: 18.6,
        rateValue: 0,
        number: 105,
      },
      { state, mode: "rate only", stormValue: 0, rateValue: 6.8, number: 105 },
      {
        state,
        mode: "storm total",
        stormValue: 18.6,
        rateValue: 6.8,
        number: 107,
      },
    ])
  )(
    "dims retained Rain conditions for $state $mode",
    async ({ state, stormValue, rateValue, number }) => {
      const rate = source(105, "hearth.numericsensor/v1");
      const storm = { ...rate, entity_id: id(107) };
      const api = core(
        station({
          105: { state: { value: rateValue, observed_at: at } },
          107: { state: { value: stormValue, observed_at: at } },
        })
      );
      const failed = entityOf(api, number);
      if (state === "unavailable") {
        failed.availability = { status: "unavailable" };
      } else {
        failed.enabled = false;
      }
      await mount(<BoundRain rate={rate} storm={storm} />);

      const conditions = slot(card("Rain"), "weather-conditions");
      let conditionsText = "Rain";
      if (stormValue > 0) {
        conditionsText =
          rateValue > 0
            ? "This storm · Rain now, 6.8 mm/h"
            : "This storm · Stopped for now";
      }
      expect(conditions?.textContent).toBe(conditionsText);
      expect(conditions?.dataset.value).toBe("dimmed");
      expect(
        within(card("Rain")).getByText(
          state === "unavailable" ? "Unavailable" : "Disabled"
        )
      ).toBeTruthy();
    }
  );

  it.each(
    ["unavailable", "disabled"].flatMap((state) => [
      {
        state,
        mode: "storm rain",
        stormValue: 18.6,
        rateValue: 6.8,
        number: 105,
      },
      {
        state,
        mode: "storm stopped",
        stormValue: 18.6,
        rateValue: 0,
        number: 105,
      },
      { state, mode: "rate only", stormValue: 0, rateValue: 6.8, number: 105 },
      {
        state,
        mode: "storm total",
        stormValue: 18.6,
        rateValue: 6.8,
        number: 107,
      },
    ])
  )(
    "restores retained Rain conditions for $state $mode on recovery",
    async ({ state, stormValue, rateValue, number }) => {
      const rate = source(105, "hearth.numericsensor/v1");
      const storm = { ...rate, entity_id: id(107) };
      const api = core(
        station({
          105: { state: { value: rateValue, observed_at: at } },
          107: { state: { value: stormValue, observed_at: at } },
        })
      );
      const failed = entityOf(api, number);
      if (state === "unavailable") {
        failed.availability = { status: "unavailable" };
      } else {
        failed.enabled = false;
      }
      await mount(<BoundRain rate={rate} storm={storm} />);

      failed.availability = { status: "available" };
      failed.enabled = true;
      await act(() => vi.advanceTimersByTimeAsync(10_000));
      expect(slot(card("Rain"), "weather-conditions")?.dataset.value).toBe(
        "live"
      );
      expect(
        within(card("Rain")).queryByText(
          state === "unavailable" ? "Unavailable" : "Disabled"
        )
      ).toBeNull();
      expect(api.posts).toHaveLength(0);
    }
  );

  it("keeps speed, gusts, Beaufort words, and direction in reading order", async () => {
    const api = core(station());
    await mount(
      <BoundWind
        speed={source(103, "hearth.speed/v1")}
        gust={source(111, "hearth.speed/v1")}
        direction={source(104, "hearth.numericsensor/v1")}
      />
    );
    const text = card("Wind").textContent ?? "";
    const order = [
      "6.3m/s",
      "Gusts 11.2 m/s",
      "Moderate breeze",
      "From SE",
    ].map((phrase) => text.indexOf(phrase));
    expect(order.every((index) => index >= 0)).toBeTruthy();
    expect(ascending(order)).toBeTruthy();
    expect(slot(card("Wind"), "trend")).toBeNull();
    expect(
      api.requests.some((url) => url.includes("/state/history"))
    ).toBeFalsy();
  });

  it.each(["metric", "us"] as const)(
    "shows household Wind speed history with gaps and a Source method (%s)",
    async (system) => {
      core(
        station(),
        {
          103: (minutes) => {
            if (minutes >= 200 && minutes <= 240) {
              return null;
            }
            return minutes > 600 ? 9.4 : 6.3;
          },
        },
        system
      );
      await mount(
        <BoundWind
          speed={source(103, "hearth.speed/v1")}
          gust={source(111, "hearth.speed/v1")}
          direction={source(104, "hearth.numericsensor/v1")}
          trend="24h"
        />
      );
      const wind = card("Wind");
      expect(
        within(wind).getByText(
          system === "us"
            ? "24 h range 14.1–21.0 mi/h"
            : "24 h range 6.3–9.4 m/s"
        )
      ).toBeTruthy();
      expect(
        slot(wind, "trend-mean")?.getAttribute("d")?.match(/M/gu)
      ).toHaveLength(2);
      expect(slot(wind, "trend-band")).toBeTruthy();
      expect(
        wind.querySelector(
          '[data-slot="weather-main"] [data-slot="wind-current"]'
        )
      ).toBeTruthy();
      expect(
        wind.querySelector('[data-slot="weather-aside"] [data-slot="trend"]')
      ).toBeTruthy();
    }
  );

  it.each(["metric", "us"] as const)(
    "loads Wind history only for the speed reading and explains the trend (%s)",
    async (system) => {
      const api = core(
        station(),
        {
          103: (minutes) => {
            if (minutes >= 200 && minutes <= 240) {
              return null;
            }
            return minutes > 600 ? 9.4 : 6.3;
          },
        },
        system
      );
      await mount(
        <BoundWind
          speed={source(103, "hearth.speed/v1")}
          gust={source(111, "hearth.speed/v1")}
          direction={source(104, "hearth.numericsensor/v1")}
          trend="24h"
        />
      );
      const histories = api.requests.filter((url) =>
        url.includes("/state/history")
      );
      expect(histories.length).toBeGreaterThan(0);
      expect(
        histories.every(
          (url) => url === `/v1/entities/${id(103)}/state/history`
        )
      ).toBeTruthy();
      fireEvent.click(
        within(card("Wind")).getByRole("button", { name: "Source" })
      );
      expect(
        screen.getByText(
          "The trend shows sustained wind speed, not gusts or direction."
        )
      ).toBeTruthy();
      expect(
        screen.getByText(/5-minute interval; the shaded band/u)
      ).toBeTruthy();
      expect(api.posts).toHaveLength(0);
    }
  );

  it.each(["empty", "failed"] as const)(
    "keeps current Wind readings when history is %s",
    async (state) => {
      const api = core(station(), { 103: () => null });
      if (state === "failed") {
        api.historyFailures.add(id(103));
      }
      await mount(
        <BoundWind speed={source(103, "hearth.speed/v1")} trend="24h" />
      );
      expect(reading(card("Wind"))).toBe("6.3m/s");
      expect(within(card("Wind")).getByText("Moderate breeze")).toBeTruthy();
      expect(
        within(card("Wind")).getByText(
          state === "empty"
            ? "No history in the last 24 h."
            : "Couldn't load history."
        )
      ).toBeTruthy();
      expect(slot(card("Wind"), "trend-mean")).toBeNull();
    }
  );

  it("retains Wind history through refresh failure without hiding current State", async () => {
    const histories: Record<number, History> = {
      103: (minutes) => (minutes > 600 ? 9.4 : 6.3),
    };
    const api = core(station(), histories);
    await mount(
      <BoundWind speed={source(103, "hearth.speed/v1")} trend="24h" />
    );
    expect(
      within(card("Wind")).getByText("24 h range 6.3–9.4 m/s")
    ).toBeTruthy();
    api.historyFailures.add(id(103));
    entityOf(api, 103).state = {
      value: 7,
      observed_at: "2026-11-09T10:01:00Z",
    };
    await act(() => vi.advanceTimersByTimeAsync(60_000));
    expect(reading(card("Wind"))).toBe("7.0m/s");
    expect(
      within(card("Wind")).getByText(
        "Couldn't refresh history. Showing the last interval."
      )
    ).toBeTruthy();
    expect(
      within(card("Wind")).getByText("24 h range 6.3–9.4 m/s")
    ).toBeTruthy();
  });

  it("restores Wind history after a refresh failure", async () => {
    const histories: Record<number, History> = {
      103: (minutes) => (minutes > 600 ? 9.4 : 6.3),
    };
    const api = core(station(), histories);
    await mount(
      <BoundWind speed={source(103, "hearth.speed/v1")} trend="24h" />
    );
    api.historyFailures.add(id(103));
    entityOf(api, 103).state = {
      value: 7,
      observed_at: "2026-11-09T10:01:00Z",
    };
    await act(() => vi.advanceTimersByTimeAsync(60_000));

    api.historyFailures.clear();
    histories[103] = () => 7;
    await act(() => vi.advanceTimersByTimeAsync(60_000));
    expect(
      within(card("Wind")).queryByText(/Couldn't refresh history/u)
    ).toBeNull();
    expect(
      within(card("Wind")).getByText("24 h range 7.0–7.0 m/s")
    ).toBeTruthy();
    expect(api.posts).toHaveLength(0);
  });

  it("draws where the wind comes from", async () => {
    core(station());
    await mount(
      <BoundWind
        speed={source(103, "hearth.speed/v1")}
        direction={source(104, "hearth.numericsensor/v1")}
      />
    );
    expect(within(card("Wind")).getByText(/From SE · 135°/u)).toBeTruthy();
    expect(
      slot(card("Wind"), "weather-compass-arrow")?.getAttribute("transform")
    ).toBe("rotate(315)");
  });

  it("draws no direction when the wind is calm", async () => {
    core(station({ 103: { state: { value: 0.2, observed_at: at } } }));
    await mount(
      <BoundWind
        speed={source(103, "hearth.speed/v1")}
        direction={source(104, "hearth.numericsensor/v1")}
      />
    );
    expect(within(card("Wind")).getByText("Calm")).toBeTruthy();
    expect(within(card("Wind")).queryByText(/From/u)).toBeNull();
    expect(slot(card("Wind"), "weather-compass-arrow")).toBeNull();
  });

  it("reports a direction entity that is missing", async () => {
    core(station().filter((item) => item.id !== id(104)));
    await mount(
      <BoundWind
        speed={source(103, "hearth.speed/v1")}
        direction={source(104, "hearth.numericsensor/v1")}
      />
    );
    expect(within(card("Wind")).getByText("Wind direction")).toBeTruthy();
    expect(within(card("Wind")).getByText("Entity not found")).toBeTruthy();
    expect(slot(card("Wind"), "weather-compass-arrow")).toBeNull();
  });

  it.each([
    { slot: "speed", state: "unavailable", number: 103 },
    { slot: "direction", state: "unavailable", number: 104 },
    { slot: "speed", state: "disabled", number: 103 },
    { slot: "direction", state: "disabled", number: 104 },
  ])(
    "dims wind descriptions for $state $slot",
    async ({ slot: dimmedSlot, state, number }) => {
      const speed = source(103, "hearth.speed/v1");
      const direction = source(104, "hearth.numericsensor/v1");
      const api = core(station());
      const failed = entityOf(api, number);
      if (state === "unavailable") {
        failed.availability = { status: "unavailable" };
      } else {
        failed.enabled = false;
      }
      await mount(
        <BoundWind speed={speed} direction={direction} trend="24h" />
      );

      expect(slot(card("Wind"), "trend")?.dataset.value).toBe(
        dimmedSlot === "speed" ? "dimmed" : "live"
      );
      expect(slot(card("Wind"), "trend-band") !== null).toBe(
        dimmedSlot !== "speed"
      );
      expect(
        within(card("Wind")).getByText("Moderate breeze").dataset.value
      ).toBe(dimmedSlot === "speed" ? "dimmed" : "live");
      expect(
        within(card("Wind")).getByText(/From SE · 135°/u).dataset.value
      ).toBe("dimmed");
    }
  );

  it.each([
    { slot: "speed", state: "unavailable", number: 103 },
    { slot: "direction", state: "unavailable", number: 104 },
    { slot: "speed", state: "disabled", number: 103 },
    { slot: "direction", state: "disabled", number: 104 },
  ])(
    "reports the $state $slot and keeps the wind reading",
    async ({ state, number }) => {
      const speed = source(103, "hearth.speed/v1");
      const direction = source(104, "hearth.numericsensor/v1");
      const api = core(station());
      const failed = entityOf(api, number);
      if (state === "unavailable") {
        failed.availability = { status: "unavailable" };
      } else {
        failed.enabled = false;
      }
      await mount(
        <BoundWind speed={speed} direction={direction} trend="24h" />
      );

      expect(
        within(card("Wind")).getByText(
          state === "unavailable" ? "Unavailable" : "Disabled"
        )
      ).toBeTruthy();
      expect(reading(card("Wind"))).toBe("6.3m/s");
    }
  );

  it.each([
    { slot: "speed", state: "unavailable", number: 103 },
    { slot: "direction", state: "unavailable", number: 104 },
    { slot: "speed", state: "disabled", number: 103 },
    { slot: "direction", state: "disabled", number: 104 },
  ])(
    "restores live wind descriptions for $state $slot on recovery",
    async ({ state, number }) => {
      const speed = source(103, "hearth.speed/v1");
      const direction = source(104, "hearth.numericsensor/v1");
      const api = core(station());
      const failed = entityOf(api, number);
      if (state === "unavailable") {
        failed.availability = { status: "unavailable" };
      } else {
        failed.enabled = false;
      }
      await mount(
        <BoundWind speed={speed} direction={direction} trend="24h" />
      );

      failed.availability = { status: "available" };
      failed.enabled = true;
      await act(() => vi.advanceTimersByTimeAsync(10_000));
      expect(slot(card("Wind"), "trend")?.dataset.value).toBe("live");
      expect(slot(card("Wind"), "trend-band")).toBeTruthy();
      expect(
        within(card("Wind")).getByText("Moderate breeze").dataset.value
      ).toBe("live");
      expect(
        within(card("Wind")).getByText(/From SE · 135°/u).dataset.value
      ).toBe("live");
    }
  );

  it.each([
    { slot: "speed", state: "unavailable", number: 103 },
    { slot: "direction", state: "unavailable", number: 104 },
    { slot: "speed", state: "disabled", number: 103 },
    { slot: "direction", state: "disabled", number: 104 },
  ])(
    "clears the $state $slot status on recovery",
    async ({ state, number }) => {
      const speed = source(103, "hearth.speed/v1");
      const direction = source(104, "hearth.numericsensor/v1");
      const api = core(station());
      const failed = entityOf(api, number);
      if (state === "unavailable") {
        failed.availability = { status: "unavailable" };
      } else {
        failed.enabled = false;
      }
      await mount(
        <BoundWind speed={speed} direction={direction} trend="24h" />
      );

      failed.availability = { status: "available" };
      failed.enabled = true;
      await act(() => vi.advanceTimersByTimeAsync(10_000));
      expect(
        within(card("Wind")).queryByText(
          state === "unavailable" ? "Unavailable" : "Disabled"
        )
      ).toBeNull();
      expect(api.posts).toHaveLength(0);
    }
  );

  it("words the three-hour pressure tendency, and never guesses without a reading from 3 h ago", async () => {
    const pressure = source(113, "hearth.pressure/v1");
    core(station(), {
      113: (minutes) => 1003.2 + (Math.min(minutes, 180) / 180) * 3.1,
    });
    await mount(<BoundPressure source={pressure} />);
    expect(within(card("Pressure")).getByText("Falling")).toBeTruthy();
    expect(within(card("Pressure")).getByText("−3.1 hPa in 3 h")).toBeTruthy();
    cleanup();
    vi.restoreAllMocks();

    core(station(), { 113: (minutes) => (minutes > 150 ? null : 1003.2) });
    await mount(<BoundPressure source={pressure} trend="24h" />);
    expect(
      within(card("Pressure")).getByText(
        "Tendency unavailable · no recent or 3 h reading"
      )
    ).toBeTruthy();
    expect(slot(card("Pressure"), "trend")).toBeTruthy();
    cleanup();
    vi.restoreAllMocks();

    // A change that ended an hour ago is not the current tendency.
    core(station(), {
      113: (minutes) =>
        minutes < 60 ? null : 1003.2 + (Math.min(minutes, 240) / 240) * 3.1,
    });
    await mount(<BoundPressure source={pressure} />);
    expect(
      within(card("Pressure")).getByText(
        "Tendency unavailable · no recent or 3 h reading"
      )
    ).toBeTruthy();
  });

  it("words a US pressure tendency from hPa and shows the change in inches of mercury", async () => {
    // −3.1 hPa is Falling; the same change in in Hg would read as Steady.
    core(
      station(),
      { 113: (minutes) => 1003.2 + (Math.min(minutes, 180) / 180) * 3.1 },
      "us"
    );
    await mount(<BoundPressure source={source(113, "hearth.pressure/v1")} />);
    expect(within(card("Pressure")).getByText("Falling")).toBeTruthy();
    expect(
      within(card("Pressure")).getByText("−0.09 in Hg in 3 h")
    ).toBeTruthy();
  });

  it.each([
    {
      now: 1003.3,
      past: 1003.2,
      words: "Rising slowly",
      change: "+0.1 hPa in 3 h",
    },
    {
      now: 1003.2,
      past: 1003.3,
      words: "Falling slowly",
      change: "−0.1 hPa in 3 h",
    },
  ])(
    "words a real 0.1 hPa subtraction as $words",
    async ({ now, past, words, change }) => {
      core(station({ 113: { state: { value: now, observed_at: at } } }), {
        113: (minutes) => (minutes > 180 ? past : now),
      });
      await mount(<BoundPressure source={source(113, "hearth.pressure/v1")} />);
      expect(within(card("Pressure")).getByText(words)).toBeTruthy();
      expect(within(card("Pressure")).getByText(change)).toBeTruthy();
    }
  );

  it.each([
    { number: 104, state: "unavailable" },
    { number: 104, state: "disabled" },
    { number: 103, state: "unavailable" },
    { number: 103, state: "disabled" },
  ])(
    "dims OutsideNow's derived bearing for $state Entity $number without dimming a live speed",
    async ({ number, state }) => {
      const api = core(station());
      const failed = entityOf(api, number);
      if (state === "unavailable") {
        failed.availability = { status: "unavailable" };
      } else {
        failed.enabled = false;
      }
      await mount(
        <BoundOutsideNow
          temperature={source(101, "hearth.temperature/v1")}
          wind={source(103, "hearth.speed/v1")}
          wind_direction={source(104, "hearth.numericsensor/v1")}
        />
      );
      const label = within(card("Outside")).getByText("Wind from SE");
      expect(label.dataset.value).toBe("dimmed");
      const value = slot(label.closest("li"), "reading-value");
      expect(value?.dataset.value).toBe(number === 104 ? "live" : "dimmed");
      expect(value?.textContent).toBe("6.3m/s");
      expect(
        within(card("Outside")).getByText(
          state === "disabled" ? "Disabled" : "Unavailable"
        )
      ).toBeTruthy();
    }
  );

  it.each([
    { number: 104, state: "unavailable" },
    { number: 104, state: "disabled" },
    { number: 103, state: "unavailable" },
    { number: 103, state: "disabled" },
  ])(
    "clears OutsideNow's bearing dimming for $state Entity $number on recovery",
    async ({ number, state }) => {
      const api = core(station());
      const failed = entityOf(api, number);
      if (state === "unavailable") {
        failed.availability = { status: "unavailable" };
      } else {
        failed.enabled = false;
      }
      await mount(
        <BoundOutsideNow
          temperature={source(101, "hearth.temperature/v1")}
          wind={source(103, "hearth.speed/v1")}
          wind_direction={source(104, "hearth.numericsensor/v1")}
        />
      );

      failed.enabled = true;
      failed.availability = { status: "available" };
      await act(() => vi.advanceTimersByTimeAsync(10_000));
      expect(
        within(card("Outside")).getByText("Wind from SE").dataset.value
      ).toBe("live");
      expect(
        within(card("Outside")).queryByText(
          state === "disabled" ? "Disabled" : "Unavailable"
        )
      ).toBeNull();
    }
  );

  it("dims conditions and totals whose readings are unavailable", async () => {
    const unavailable = { availability: { status: "unavailable" } };
    core(station({ 103: unavailable, 108: unavailable }));
    await mount(<ViewRenderer headingLevel={3} definition={weather} />);
    expect(
      within(card("Outside")).getByText("Rain · Breezy").dataset.value
    ).toBe("dimmed");
    const week = within(card("Rain")).getByText(/This week 22\.9/u);
    expect(week.dataset.value).toBe("dimmed");
    expect(within(card("Rain")).getByText(/Today 16\.2/u).dataset.value).toBe(
      "live"
    );
  });

  it("dims conditions only with the readings that produced words", async () => {
    core(
      station({
        105: {
          state: { value: 0, observed_at: at },
          availability: { status: "unavailable" },
        },
      })
    );
    await mount(<ViewRenderer headingLevel={3} definition={weather} />);
    expect(within(card("Outside")).getByText("Breezy").dataset.value).toBe(
      "live"
    );
  });

  it("retains the complete pressure interval when its paired history refresh fails", async () => {
    const pressure = source(113, "hearth.pressure/v1");
    const api = core(station(), {
      113: (minutes) => 1003.2 + (Math.min(minutes, 180) / 180) * 3.1,
    });
    await mount(<BoundPressure source={pressure} />);
    expect(within(card("Pressure")).getByText("Falling")).toBeTruthy();

    api.historyFailures.add(pressure.entity_id);
    await act(() => vi.advanceTimersByTimeAsync(60_000));
    expect(within(card("Pressure")).getByText("Falling")).toBeTruthy();
    await act(() => vi.advanceTimersByTimeAsync(60_000));
    expect(
      within(card("Pressure")).getByText("Couldn't refresh pressure history")
    ).toBeTruthy();
    expect(within(card("Pressure")).getByText("Falling")).toBeTruthy();
  });

  it("restores the pressure interval after its paired history refresh recovers", async () => {
    const pressure = source(113, "hearth.pressure/v1");
    const api = core(station(), {
      113: (minutes) => 1003.2 + (Math.min(minutes, 180) / 180) * 3.1,
    });
    await mount(<BoundPressure source={pressure} />);
    api.historyFailures.add(pressure.entity_id);
    await act(() => vi.advanceTimersByTimeAsync(60_000));
    await act(() => vi.advanceTimersByTimeAsync(60_000));

    api.historyFailures.clear();
    await act(() => vi.advanceTimersByTimeAsync(60_000));
    expect(within(card("Pressure")).getByText("Falling")).toBeTruthy();
    expect(within(card("Pressure")).getByText("−3.1 hPa in 3 h")).toBeTruthy();
  });

  it.each([undefined, "6h", "24h"] as const)(
    "warns and keeps the last pressure interval while State stays live (trend %s)",
    async (trend) => {
      const pressure = source(113, "hearth.pressure/v1");
      const histories = {
        113: (minutes: number) => 1003.2 + (Math.min(minutes, 180) / 180) * 3.1,
      };
      const api = core(station(), histories);
      await mount(<BoundPressure source={pressure} trend={trend} />);
      expect(within(card("Pressure")).getByText("Falling")).toBeTruthy();

      api.historyFailures.add(pressure.entity_id);
      entityOf(api, 113).state = {
        value: 1004,
        observed_at: "2026-11-09T10:01:00Z",
      };
      await act(() => vi.advanceTimersByTimeAsync(60_000));
      expect(
        within(card("Pressure")).getByText("Couldn't refresh pressure history")
      ).toBeTruthy();
      expect(reading(card("Pressure"))).toBe("1004.0hPa");
      // The last interval can still describe the tendency until its samples expire.
      expect(within(card("Pressure")).getByText("Falling")).toBeTruthy();
    }
  );

  it.each([undefined, "6h", "24h"] as const)(
    "expires a cached pressure tendency that cannot refresh (trend %s)",
    async (trend) => {
      const pressure = source(113, "hearth.pressure/v1");
      const histories = {
        113: (minutes: number) => 1003.2 + (Math.min(minutes, 180) / 180) * 3.1,
      };
      const api = core(station(), histories);
      await mount(<BoundPressure source={pressure} trend={trend} />);
      api.historyFailures.add(pressure.entity_id);
      entityOf(api, 113).state = {
        value: 1004,
        observed_at: "2026-11-09T10:01:00Z",
      };
      await act(() => vi.advanceTimersByTimeAsync(60_000));

      // No successful history response arrives to move its cached window end forward.
      await act(() => vi.advanceTimersByTimeAsync(10 * 60_000));
      expect(slot(card("Pressure"), "pressure-tendency")).toBeNull();
      expect(
        within(card("Pressure")).getByText("Couldn't refresh pressure history")
      ).toBeTruthy();
      expect(
        within(card("Pressure")).getByText(
          "Tendency unavailable · no recent or 3 h reading"
        )
      ).toBeTruthy();
    }
  );

  it.each([undefined, "6h", "24h"] as const)(
    "recovers a fresh pressure tendency after history returns (trend %s)",
    async (trend) => {
      const pressure = source(113, "hearth.pressure/v1");
      const histories = {
        113: (minutes: number) => 1003.2 + (Math.min(minutes, 180) / 180) * 3.1,
      };
      const api = core(station(), histories);
      await mount(<BoundPressure source={pressure} trend={trend} />);
      api.historyFailures.add(pressure.entity_id);
      entityOf(api, 113).state = {
        value: 1004,
        observed_at: "2026-11-09T10:01:00Z",
      };
      await act(() => vi.advanceTimersByTimeAsync(60_000));
      await act(() => vi.advanceTimersByTimeAsync(10 * 60_000));

      api.historyFailures.clear();
      histories[113] = (minutes) => 1004 - (Math.min(minutes, 180) / 180) * 2;
      await act(() => vi.advanceTimersByTimeAsync(60_000));
      expect(
        within(card("Pressure")).queryByText(
          "Couldn't refresh pressure history"
        )
      ).toBeNull();
      expect(within(card("Pressure")).getByText("Rising")).toBeTruthy();
      expect(
        within(card("Pressure")).getByText("+2.0 hPa in 3 h")
      ).toBeTruthy();
      expect(api.posts).toHaveLength(0);
    }
  );

  it.each(["metric", "us"] as const)(
    "identifies cached outdoor extrema and their interval (%s)",
    async (system) => {
      const temperature = source(101, "hearth.temperature/v1");
      const histories: Record<number, History> = {
        101: (minutes) => (minutes > 600 ? 10_900 : 9400),
      };
      core(station(), histories, system);
      await mount(<BoundOutsideNow temperature={temperature} />);
      const highLow =
        system === "metric"
          ? "high 10.9, low 9.4 °C"
          : "high 51.6, low 48.9 °F";
      expect(within(card("Outside")).getByText(`24 h ${highLow}`)).toBeTruthy();

      fireEvent.click(
        within(card("Outside")).getByRole("button", { name: "Source" })
      );
      expect(historyInterval()).toStrictEqual([
        "2026-11-08T10:05:00.000Z",
        "2026-11-09T10:00:30.000Z",
      ]);
    }
  );

  it.each(["metric", "us"] as const)(
    "keeps cached outdoor extrema through a failed refresh (%s)",
    async (system) => {
      const temperature = source(101, "hearth.temperature/v1");
      const histories: Record<number, History> = {
        101: (minutes) => (minutes > 600 ? 10_900 : 9400),
      };
      const api = core(station(), histories, system);
      await mount(<BoundOutsideNow temperature={temperature} />);
      const highLow =
        system === "metric"
          ? "high 10.9, low 9.4 °C"
          : "high 51.6, low 48.9 °F";
      fireEvent.click(
        within(card("Outside")).getByRole("button", { name: "Source" })
      );
      const loadedInterval = historyInterval();
      fireEvent.click(screen.getByRole("button", { name: "Close details" }));

      api.historyFailures.add(temperature.entity_id);
      entityOf(api, 101).state = {
        value: 20_000,
        observed_at: "2026-11-09T10:01:00Z",
      };
      await act(() => vi.advanceTimersByTimeAsync(6 * 60_000));
      expect(
        within(card("Outside")).getByText(
          "Couldn't refresh temperature history"
        )
      ).toBeTruthy();
      expect(
        within(card("Outside")).getByText(`Last loaded 24 h: ${highLow}`)
      ).toBeTruthy();
      expect(within(card("Outside")).queryByText(`24 h ${highLow}`)).toBeNull();
      fireEvent.click(
        within(card("Outside")).getByRole("button", { name: "Source" })
      );
      expect(historyInterval()).toStrictEqual(loadedInterval);
    }
  );

  it.each(["metric", "us"] as const)(
    "refreshes outdoor extrema and their interval after a failed history (%s)",
    async (system) => {
      const temperature = source(101, "hearth.temperature/v1");
      const histories: Record<number, History> = {
        101: (minutes) => (minutes > 600 ? 10_900 : 9400),
      };
      const api = core(station(), histories, system);
      await mount(<BoundOutsideNow temperature={temperature} />);
      fireEvent.click(
        within(card("Outside")).getByRole("button", { name: "Source" })
      );
      const loadedInterval = historyInterval();
      fireEvent.click(screen.getByRole("button", { name: "Close details" }));

      api.historyFailures.add(temperature.entity_id);
      entityOf(api, 101).state = {
        value: 20_000,
        observed_at: "2026-11-09T10:01:00Z",
      };
      await act(() => vi.advanceTimersByTimeAsync(6 * 60_000));
      expect(reading(card("Outside"))).toBe(
        system === "metric" ? "20.0°C" : "68.0°F"
      );

      api.historyFailures.clear();
      histories[101] = (minutes) => (minutes > 600 ? 22_000 : 20_000);
      await act(() => vi.advanceTimersByTimeAsync(60_000));
      expect(
        within(card("Outside")).queryByText(
          "Couldn't refresh temperature history"
        )
      ).toBeNull();
      expect(
        within(card("Outside")).getByText(
          system === "metric"
            ? "24 h high 22.0, low 20.0 °C"
            : "24 h high 71.6, low 68.0 °F"
        )
      ).toBeTruthy();
      fireEvent.click(
        within(card("Outside")).getByRole("button", { name: "Source" })
      );
      const [loadedFrom, loadedTo] = loadedInterval;
      const [refreshedFrom, refreshedTo] = historyInterval();
      expect([
        Date.parse(refreshedFrom) > Date.parse(loadedFrom),
        Date.parse(refreshedTo) > Date.parse(loadedTo),
      ]).toStrictEqual([true, true]);
      expect(api.posts).toHaveLength(0);
    }
  );
});
