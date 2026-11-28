import { z } from "zod";

import { quantityField, stateField, textField } from "../bindings/state-fields";
import type { Words } from "../bindings/types";
import { boundRoles } from "../cards/air-purifier/schema";
import { definitionSchema, elementSchema } from "./index";
import type { ViewDefinition, ViewElement } from "./index";
import { childrenSchema, elementKey, plainText } from "./primitives";

export const MAX_DEFINITION_BYTES = 16 * 1024;
const storedNode = z
  .object({
    type: z.string().min(1).max(120),
    props: z.record(z.string(), z.unknown()),
    children: childrenSchema.optional(),
  })
  .passthrough();
const storedEnvelope = z.strictObject({
  schema_version: z.number().int(),
  catalog_version: z.string().min(1).max(120),
  title: plainText(80),
  spec: z.strictObject({
    root: elementKey,
    elements: z.record(elementKey, storedNode),
  }),
});
type Graph = z.infer<typeof storedEnvelope>["spec"];
export type ReadView =
  | { status: "unavailable"; raw: unknown; reason: string }
  | { status: "supported"; raw: unknown; definition: ViewDefinition }
  | {
      status: "partial";
      raw: unknown;
      title: string;
      root: string;
      elements: Record<string, ViewElement | null>;
      children: Record<string, string[]>;
    };

const bounded = (value: unknown): boolean => {
  try {
    return (
      new TextEncoder().encode(JSON.stringify(value)).length <=
      MAX_DEFINITION_BYTES
    );
  } catch {
    return false;
  }
};

const validGraph = (spec: Graph): boolean => {
  const count = Object.keys(spec.elements).length;
  if (count < 1 || count > 32) {
    return false;
  }
  const visited = new Set<string>();
  const visit = (key: string, depth: number): boolean => {
    if (depth > 8 || visited.has(key) || !Object.hasOwn(spec.elements, key)) {
      return false;
    }
    visited.add(key);
    return (spec.elements[key].children ?? []).every((child) =>
      visit(child, depth + 1)
    );
  };
  return visit(spec.root, 1) && visited.size === count;
};

/** Words belong to yes/no fields only. */
const validWords = (
  source: { entity_id: string; entity_type: string; field?: string },
  words?: Words
): boolean =>
  words === undefined || textField(source)?.value_type === "boolean";

type WeatherElement = Extract<
  ViewElement,
  { type: "OutsideNow" | "Rain" | "Wind" | "Pressure" }
>;
type ElementOf<Type extends ViewElement["type"]> = Extract<
  ViewElement,
  { type: Type }
>;

/** Every slot a weather card can bind; schemas fix each slot's type and root field. */
const weatherSlots = ({
  type,
  props,
}: WeatherElement): ({ entity_id: string } | undefined)[] => {
  switch (type) {
    case "OutsideNow": {
      return [
        props.temperature,
        props.humidity,
        props.wind,
        props.wind_direction,
        props.rain_rate,
        props.rain_today,
      ];
    }
    case "Rain": {
      return [
        props.rate,
        props.storm,
        props.totals?.day,
        props.totals?.week,
        props.totals?.month,
        props.totals?.year,
      ];
    }
    case "Wind": {
      return [props.speed, props.gust, props.direction, props.max_gust];
    }
    case "Pressure": {
      return [props.source];
    }
    default: {
      return [];
    }
  }
};

/** Entity and Device references a view reads; one view reads at most 16. */
export const MAX_VIEW_REFERENCES = 16;

const validMetric = (
  { props }: ElementOf<"Metric">,
  ids: Set<string>
): boolean => {
  const field = quantityField(props.source);
  if (!field || (props.range && !field.quantity.range)) {
    return false;
  }
  ids.add(props.source.entity_id);
  return true;
};

const validState = (
  { props }: ElementOf<"State">,
  ids: Set<string>
): boolean => {
  if (!textField(props.source) || !validWords(props.source, props.words)) {
    return false;
  }
  ids.add(props.source.entity_id);
  return true;
};

const validReadingList = (
  { props }: ElementOf<"ReadingList">,
  ids: Set<string>
): boolean => {
  if (
    props.rows.some(
      (row) => !stateField(row.source) || !validWords(row.source, row.words)
    )
  ) {
    return false;
  }
  for (const row of props.rows) {
    ids.add(row.source.entity_id);
  }
  return true;
};

const validDevice = (
  { props }: ElementOf<"Device">,
  ids: Set<string>
): boolean => {
  ids.add(props.device.device_id);
  return true;
};

/** Cards bound to one Entity's whole State. */
type SingleSourceElement = ElementOf<
  "Forecast" | "Cover" | "Thermostat" | "MediaPlayer"
>;

const isSingleSource = (element: ViewElement): element is SingleSourceElement =>
  element.type === "Forecast" ||
  element.type === "Cover" ||
  element.type === "Thermostat" ||
  element.type === "MediaPlayer";

const validSingleSource = (
  { props }: SingleSourceElement,
  ids: Set<string>
): boolean => {
  ids.add(props.source.entity_id);
  return true;
};

const validAirPurifier = (
  { props }: ElementOf<"AirPurifier">,
  ids: Set<string>
): boolean => {
  // Each role names a different Entity; Core checks types, units, choices, and the shared Device.
  const roleIDs = boundRoles(props).map(([, source]) => source.entity_id);
  if (
    new Set(roleIDs).size !== roleIDs.length ||
    !quantityField(props.pm25) ||
    (props.filter_life && !quantityField(props.filter_life))
  ) {
    return false;
  }
  for (const id of roleIDs) {
    ids.add(id);
  }
  return true;
};

const validToggleTile = (
  { props }: ElementOf<"ToggleTile">,
  ids: Set<string>
): boolean => {
  ids.add(props.source.entity_id);
  const { metric } = props;
  if (!metric) {
    return true;
  }
  if (!quantityField(metric.source)) {
    return false;
  }
  ids.add(metric.source.entity_id);
  if (!metric.detail) {
    return true;
  }
  if (!quantityField(metric.detail.source)) {
    return false;
  }
  ids.add(metric.detail.source.entity_id);
  return true;
};

const validToggleList = (
  { props }: ElementOf<"ToggleList">,
  ids: Set<string>
): boolean => {
  const rowIDs = props.rows.map((row) => row.source.entity_id);
  if (new Set(rowIDs).size !== rowIDs.length) {
    return false;
  }
  for (const id of rowIDs) {
    ids.add(id);
  }
  return true;
};

const validWeather = (element: WeatherElement, ids: Set<string>): boolean => {
  for (const source of weatherSlots(element)) {
    if (source) {
      ids.add(source.entity_id);
    }
  }
  return true;
};

const validStateTimeline = (
  { props }: ElementOf<"StateTimeline">,
  ids: Set<string>
): boolean => {
  const { rows } = props;
  if (
    rows.some(
      (row) => !textField(row.source) || !validWords(row.source, row.words)
    ) ||
    new Set(
      rows.map((row) => `${row.source.entity_id}:${row.source.field ?? ""}`)
    ).size !== rows.length ||
    staticUnits(rows.map((row) => row.source)).size > 1
  ) {
    return false;
  }
  for (const row of rows) {
    ids.add(row.source.entity_id);
  }
  return true;
};

const validWeekPattern = (
  { props }: ElementOf<"WeekPattern">,
  ids: Set<string>
): boolean => {
  if (textField(props.source)?.value_type !== "boolean") {
    return false;
  }
  ids.add(props.source.entity_id);
  return true;
};

const validWeekGrid = (
  { props }: ElementOf<"WeekGrid">,
  ids: Set<string>
): boolean => {
  if (!quantityField(props.source)) {
    return false;
  }
  ids.add(props.source.entity_id);
  return true;
};

const validTimeInRange = (
  { props }: ElementOf<"TimeInRange">,
  ids: Set<string>
): boolean => {
  const [low, high] = props.thresholds;
  if (
    !quantityField(props.source) ||
    !props.thresholds.every(Number.isFinite) ||
    (high !== undefined && high <= low)
  ) {
    return false;
  }
  ids.add(props.source.entity_id);
  return true;
};

const validPeriodTotals = (
  { props }: ElementOf<"PeriodTotals">,
  ids: Set<string>
): boolean => {
  if (
    textField(props.source)?.value_type !== "boolean" ||
    !validWords(props.source, props.words)
  ) {
    return false;
  }
  ids.add(props.source.entity_id);
  return true;
};

const validRanking = (
  { props }: ElementOf<"Ranking">,
  ids: Set<string>
): boolean => {
  const { rows } = props;
  if (
    rows.some((row) => !quantityField(row.source)) ||
    new Set(
      rows.map((row) => `${row.source.entity_id}:${row.source.field ?? ""}`)
    ).size !== rows.length
  ) {
    return false;
  }
  for (const row of rows) {
    ids.add(row.source.entity_id);
  }
  return true;
};

/**
 * The canonical units the field catalog fixes for these sources. A unit that
 * comes from an Entity's support is unknown here; presenting or saving the view
 * checks it, and so does the card when it reads.
 */
const staticUnits = (
  sources: { entity_id: string; entity_type: string; field?: string }[]
): Set<string> =>
  new Set(
    sources.flatMap((source) => {
      const unit = quantityField(source)?.quantity.unit;
      return typeof unit === "string" ? [unit] : [];
    })
  );

const validTimeSeries = (
  { props }: ElementOf<"TimeSeries">,
  ids: Set<string>
): boolean => {
  const { series } = props;
  if (
    series.some((item) => !quantityField(item.source)) ||
    new Set(
      series.map(
        (item) => `${item.source.entity_id}:${item.source.field ?? ""}`
      )
    ).size !== series.length ||
    new Set(series.map((item) => item.source.window)).size !== 1 ||
    staticUnits(series.map((item) => item.source)).size > 1 ||
    (props.compare !== undefined &&
      (series.length !== 1 || series[0].source.window !== "24h"))
  ) {
    return false;
  }
  for (const item of series) {
    ids.add(item.source.entity_id);
  }
  return true;
};

const validChartForm = (element: ViewElement, ids: Set<string>): boolean => {
  switch (element.type) {
    case "WeekGrid": {
      return validWeekGrid(element, ids);
    }
    case "TimeInRange": {
      return validTimeInRange(element, ids);
    }
    case "PeriodTotals": {
      return validPeriodTotals(element, ids);
    }
    case "Ranking": {
      return validRanking(element, ids);
    }
    default: {
      return true;
    }
  }
};

const validReferences = (elements: ViewElement[]): boolean => {
  const ids = new Set<string>();
  for (const element of elements) {
    if (isSingleSource(element)) {
      validSingleSource(element, ids);
      continue;
    }
    let valid = true;
    switch (element.type) {
      case "Metric": {
        valid = validMetric(element, ids);
        break;
      }
      case "State": {
        valid = validState(element, ids);
        break;
      }
      case "ReadingList": {
        valid = validReadingList(element, ids);
        break;
      }
      case "Device": {
        valid = validDevice(element, ids);
        break;
      }
      case "AirPurifier": {
        valid = validAirPurifier(element, ids);
        break;
      }
      case "ToggleTile": {
        valid = validToggleTile(element, ids);
        break;
      }
      case "ToggleList": {
        valid = validToggleList(element, ids);
        break;
      }
      case "OutsideNow":
      case "Rain":
      case "Wind":
      case "Pressure": {
        valid = validWeather(element, ids);
        break;
      }
      case "StateTimeline": {
        valid = validStateTimeline(element, ids);
        break;
      }
      case "WeekPattern": {
        valid = validWeekPattern(element, ids);
        break;
      }
      case "TimeSeries": {
        valid = validTimeSeries(element, ids);
        break;
      }
      default: {
        valid = validChartForm(element, ids);
        break;
      }
    }
    if (!valid) {
      return false;
    }
  }
  return ids.size <= MAX_VIEW_REFERENCES;
};

// Cards stored before the household unit system carried props.display. The
// unit is now household-wide, so reads drop it; strict writes reject it.
const withoutLegacyDisplay = (raw: unknown): unknown => {
  if (typeof raw !== "object" || raw === null) {
    return raw;
  }
  const { spec } = raw as { spec?: { elements?: unknown } };
  if (
    typeof spec !== "object" ||
    spec === null ||
    typeof spec.elements !== "object" ||
    spec.elements === null
  ) {
    return raw;
  }
  const elements = Object.fromEntries(
    Object.entries(spec.elements as Record<string, unknown>).map(
      ([key, node]) => {
        if (typeof node !== "object" || node === null) {
          return [key, node];
        }
        const { type, props } = node as { type?: unknown; props?: unknown };
        if (
          (type !== "Metric" && type !== "TimeSeries") ||
          typeof props !== "object" ||
          props === null ||
          !("display" in props)
        ) {
          return [key, node];
        }
        const { display: _legacy, ...rest } = props as Record<string, unknown>;
        return [key, { ...node, props: rest }];
      }
    )
  );
  return { ...raw, spec: { ...spec, elements } };
};
export const parseDefinition = (raw: unknown): ViewDefinition | null => {
  if (!bounded(raw)) {
    return null;
  }
  const result = definitionSchema.safeParse(raw);
  if (
    !result.success ||
    !validGraph(result.data.spec) ||
    !validReferences(Object.values(result.data.spec.elements))
  ) {
    return null;
  }
  return result.data;
};

export const readDefinition = (raw: unknown): ReadView => {
  const unavailable = (reason: string): ReadView => ({
    status: "unavailable",
    raw,
    reason,
  });
  if (!bounded(raw)) {
    return unavailable("View exceeds the supported size.");
  }
  const current = withoutLegacyDisplay(raw);
  const envelope = storedEnvelope.safeParse(current);
  if (!envelope.success) {
    return unavailable("Invalid view definition.");
  }
  const value = envelope.data;
  if (
    value.schema_version !== 1 ||
    value.catalog_version !== "hearth.dashboard/v1"
  ) {
    return unavailable("This view requires a newer Hearth version.");
  }
  if (!validGraph(value.spec)) {
    return unavailable("Invalid view layout.");
  }
  const elements: Record<string, ViewElement | null> = {};
  const children: Record<string, string[]> = {};
  let unsupported = false;
  for (const [key, node] of Object.entries(value.spec.elements)) {
    const result = elementSchema.safeParse(node);
    const supported = result.success && validReferences([result.data]);
    elements[key] = supported ? result.data : null;
    children[key] = node.children ?? [];
    unsupported ||= !supported;
  }
  if (
    !validReferences(
      Object.values(elements).filter(
        (node): node is ViewElement => node !== null
      )
    )
  ) {
    return unavailable("Too many Entity references.");
  }
  if (!unsupported) {
    const definition = parseDefinition(current);
    if (definition) {
      return { status: "supported", raw, definition };
    }
    return unavailable("Invalid view definition.");
  }
  return {
    status: "partial",
    raw,
    title: value.title,
    root: value.spec.root,
    elements,
    children,
  };
};
