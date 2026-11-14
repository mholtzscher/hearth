import type { Entity, Quantity } from "../src/api/types";
import type { ViewDefinition, ViewElement } from "../src/features/views/catalog";

/** Synthetic household data, not captured device compatibility evidence. */
export const fixtureNow = "2026-11-09T10:00:30Z";
export const observedAt = "2026-11-09T10:00:00Z";
export const entityId = (number: number) => `ent_019a3e6a-4111-7000-8000-${String(number).padStart(12, "0")}`;
export const deviceId = (number: number) => `dev_019a3e6a-4111-7000-8000-${String(number).padStart(12, "0")}`;
const source = <T extends string>(number: number, entity_type: T) => ({
  kind: "entity-state" as const, entity_id: entityId(number), entity_type,
});
const temperature = (n: number) => source(n, "hearth.temperature/v1");
const numeric = (n: number) => source(n, "hearth.numericsensor/v1");
const power = (n: number) => source(n, "hearth.power/v1");
const door = source(1, "hearth.binarysensor/v1");
const speed = (n: number) => source(n, "hearth.speed/v1");
const quantity = (unit: string, label = unit, fraction_digits = 1, range?: Quantity["range"]): Quantity =>
  ({ unit, label, fraction_digits, ...(range ? { range } : {}) });

function entity(number: number, type: string, name: string, value: unknown, options: {
  device?: number; quantity?: Quantity; quantities?: Record<string, Quantity>;
  writable?: boolean; support?: Record<string, unknown>;
} = {}): Entity {
  return {
    id: entityId(number), device_id: deviceId(options.device ?? 3),
    adapter_id: "adapter_storybook", adapter_name: name, name_override: null,
    name, type: `hearth.${type}/v1`, enabled: true,
    availability: { status: "available", source: "adapter", since: observedAt, evidence_at: observedAt },
    support: { state: options.support ?? {}, operations: options.writable ? { set: {} } : {} },
    quantities: options.quantities ?? (options.quantity ? { "": options.quantity } : undefined),
    state: { value, observation_id: `obs_story_${number}`, adapter_received_at: observedAt, observed_at: observedAt },
  };
}

function forecast(number: number, daily: boolean): Entity {
  const periods = Array.from({ length: daily ? 7 : 12 }, (_, index) => {
    const date = new Date(fixtureNow);
    date.setMinutes(0, 0, 0);
    if (daily) date.setDate(date.getDate() + index);
    else date.setHours(date.getHours() + index);
    return {
      starts_at: date.toISOString(),
      ...(daily ? {
        date: `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`,
        temperature_min: 5 + index, temperature_max: 12 + index,
      } : { temperature: 9 + index / 2 }),
      condition: index % 3 === 0 ? "rain" : "partly-cloudy-day",
      summary: index % 3 === 0 ? "Light rain" : "Clearing",
      precipitation_probability: index % 3 === 0 ? 70 : 20,
    };
  });
  const quantities: Record<string, Quantity> = {};
  periods.forEach((period, index) => Object.entries(period).forEach(([key, value]) => {
    if (typeof value === "number") quantities[`/periods/${index}/${key}`] =
      key.startsWith("temperature") ? quantity("Cel", "°C", 0) : quantity("%", "%", 0);
  }));
  return entity(number, daily ? "dailyforecast" : "hourlyforecast", daily ? "Daily forecast" : "Hourly forecast", { periods }, { quantities });
}

/** A new mutable household for every story; Command effects never cross stories. */
export function createEntities(): Entity[] {
  const ranged = (unit: string, max: number, label = unit, fraction = 1) =>
    quantity(unit, label, fraction, { minimum: 0, maximum: max });
  const numericEntity = (n: number, name: string, value: number, unit: string, max: number, device = 3) =>
    entity(n, "numericsensor", name, value, { device, quantity: ranged(unit, max),
      support: { minimum: 0, maximum: max, unit } });
  return [
    entity(1, "binarysensor", "Front door", false),
    entity(2, "power", "Porch light", true, { writable: true }),
    entity(3, "power", "Office fan", true, { writable: true }),
    entity(4, "power", "Office plug", false, { device: 4, writable: true }),
    numericEntity(6, "Electrical power", 7.9, "W", 10000, 4),
    numericEntity(7, "Total energy", 2.55, "kWh", 10000, 4),
    entity(10, "enumsetting", "Thermostat mode", "auto", { support: { choices: ["heat", "cool", "auto"] }, writable: true }),
    entity(11, "colormode", "Lamp color mode", "hs", { device: 2 }),
    entity(12, "colorhs", "Lamp color", { active: true, hue: 210, saturation: 64 }, {
      device: 2, writable: true, quantities: {
        "/hue": quantity("deg", "°", 0), "/saturation": ranged("%", 100, "%", 0),
      },
    }),
    entity(13, "brightness", "Lamp brightness", 64, { device: 2, writable: true, quantity: ranged("%", 100, "%", 0) }),
    entity(14, "power", "Lamp power", true, { device: 2, writable: true }),
    entity(15, "temperature", "Living room", 21.5, { quantity: quantity("Cel", "°C") }),
    entity(101, "temperature", "Outdoor temperature", 9.4, { quantity: quantity("Cel", "°C") }),
    entity(102, "relativehumidity", "Outdoor humidity", 94, { quantity: ranged("%", 100) }),
    entity(103, "speed", "Wind speed", 6.3, { quantity: quantity("m/s") }),
    numericEntity(104, "Wind direction", 135, "deg", 360),
    numericEntity(105, "Rain rate", 6.8, "mm/h", 100),
    ...[16.2, 18.6, 22.9, 71.4, 688.1].map((value, index) =>
      numericEntity(106 + index, ["Rain today", "Storm rain", "Rain this week", "Rain this month", "Rain this year"][index], value, "mm", 1000)),
    entity(111, "speed", "Wind gust", 11.2, { quantity: quantity("m/s") }),
    entity(112, "speed", "Maximum gust", 14.8, { quantity: quantity("m/s") }),
    entity(113, "pressure", "Pressure", 1003.2, { quantity: quantity("hPa") }),
    entity(114, "temperature", "Bedroom temperature", 18.2, { quantity: quantity("Cel", "°C") }),
    forecast(201, false), forecast(202, true),
    entity(301, "power", "Purifier power", true, { device: 1, writable: true }),
    numericEntity(302, "PM2.5", 7, "µg/m³", 500, 1),
    numericEntity(303, "Filter life", 6, "%", 100, 1),
    entity(304, "enumsetting", "Purifier mode", "manual", { device: 1, writable: true, support: { choices: ["auto", "sleep", "manual"] } }),
    entity(305, "enumsetting", "Fan level", "2", { device: 1, writable: true, support: { choices: ["1", "2", "3", "4"] } }),
    entity(306, "power", "Purifier display", true, { device: 1, writable: true }),
    entity(307, "power", "Child lock", false, { device: 1, writable: true }),
  ];
}

/** Every current catalog element has a typed, reusable example. */
export const catalogExamples = {
  Stack: { type: "Stack", props: { gap: "medium" }, children: ["heading", "note"] },
  Grid: { type: "Grid", props: { columns: 2 }, children: ["heading", "note"] },
  Heading: { type: "Heading", props: { text: "House at a glance" } },
  Note: { type: "Note", props: { text: "Synthetic readings. Controls affect only this story's household." } },
  Metric: { type: "Metric", props: { label: "Outdoor humidity", source: source(102, "hearth.relativehumidity/v1"), trend: "24h", range: true } },
  TimeSeries: { type: "TimeSeries", props: { title: "Temperatures", series: [
    { label: "Outdoor", source: { ...temperature(101), kind: "entity-series", window: "24h" } },
    { label: "Bedroom", source: { ...temperature(114), kind: "entity-series", window: "24h" } },
  ] } },
  State: { type: "State", props: { label: "Front door", source: door, words: { true: "Open", false: "Closed" } } },
  ReadingList: { type: "ReadingList", props: { title: "Rooms", rows: [
    { label: "Living room", source: temperature(15) },
    { label: "Outdoor humidity", source: source(102, "hearth.relativehumidity/v1") },
    { label: "Front door", source: door, words: { true: "Open", false: "Closed" } },
  ] } },
  Device: { type: "Device", props: { label: "Hallway lamp", device: { kind: "device", device_id: deviceId(2) } } },
  StateTimeline: { type: "StateTimeline", props: { title: "Around the house", style: "ledger", window: "24h", rows: [
    { label: "Front door", source: door, words: { true: "Open", false: "Closed" } },
    { label: "Porch light", source: power(2) },
    { label: "Thermostat", source: source(10, "hearth.enumsetting/v1") },
  ] } },
  WeekPattern: { type: "WeekPattern", props: { label: "Front door", source: door, words: { true: "Open", false: "Closed" } } },
  ToggleTile: { type: "ToggleTile", props: { label: "Office plug", source: power(4), metric: {
    source: numeric(6), window: "24h", detail: { source: numeric(7), label: "Total energy" },
  } } },
  ToggleList: { type: "ToggleList", props: { title: "Office", all_off: true, rows: [
    { label: "Fan", source: power(3) }, { label: "Plug", source: power(4) },
  ] } },
  OutsideNow: { type: "OutsideNow", props: { label: "Outside", temperature: temperature(101),
    humidity: source(102, "hearth.relativehumidity/v1"), wind: speed(103), wind_direction: numeric(104), rain_rate: numeric(105), rain_today: numeric(106) } },
  Rain: { type: "Rain", props: { label: "Rain", rate: numeric(105), storm: numeric(107),
    totals: { day: numeric(106), week: numeric(108), month: numeric(109), year: numeric(110) }, trend: "24h" } },
  Wind: { type: "Wind", props: { label: "Wind", speed: speed(103), gust: speed(111), direction: numeric(104), max_gust: speed(112), trend: "24h" } },
  Pressure: { type: "Pressure", props: { label: "Pressure", source: source(113, "hearth.pressure/v1"), trend: "24h" } },
  Forecast: { type: "Forecast", props: { label: "Next 12 hours", source: source(201, "hearth.hourlyforecast/v1"), periods: 12 } },
  AirPurifier: { type: "AirPurifier", props: { layout: "card", label: "Living room purifier",
    power: power(301), pm25: numeric(302), filter_life: numeric(303),
    mode: source(304, "hearth.enumsetting/v1"), fan_level: source(305, "hearth.enumsetting/v1"),
    display: power(306), child_lock: power(307) } },
} satisfies { [K in ViewElement["type"]]: Extract<ViewElement, { type: K }> };

export function definitionFor(element: ViewElement): ViewDefinition {
  return {
    schema_version: 1, catalog_version: "hearth.dashboard/v1", title: `Example ${element.type}`,
    spec: { root: "root", elements: {
      root: element, ...("children" in element ? { heading: catalogExamples.Heading, note: catalogExamples.Note } : {}),
    } },
  };
}

/** Compositions stay within the real 16-reference limit, rather than bypassing validation. */
export const dashboardDefinition: ViewDefinition = {
  schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Morning overview",
  spec: { root: "root", elements: {
    root: { type: "Stack", props: { gap: "large" }, children: ["heading", "note", "readings", "history"] },
    heading: catalogExamples.Heading, note: catalogExamples.Note,
    readings: { type: "Grid", props: { columns: 3 }, children: ["outside", "humidity", "lamp"] },
    outside: catalogExamples.OutsideNow, humidity: catalogExamples.Metric, lamp: catalogExamples.Device,
    history: catalogExamples.TimeSeries,
  } },
};
