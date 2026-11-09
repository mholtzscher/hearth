import { z } from "zod";
import { definitionSchema, elementSchema, type ViewDefinition, type ViewElement } from "./index";
import { childrenSchema, elementKey, plainText } from "./primitives";
import { quantityField, stateField, textField } from "../bindings/state-fields";
import type { Words } from "../bindings/types";

export const MAX_DEFINITION_BYTES = 16 * 1024;
const storedNode = z.object({ type: z.string().min(1).max(120), props: z.record(z.string(), z.unknown()), children: childrenSchema.optional() }).passthrough();
const storedEnvelope = z.strictObject({
  schema_version: z.number().int(), catalog_version: z.string().min(1).max(120), title: plainText(80),
  spec: z.strictObject({ root: elementKey, elements: z.record(elementKey, storedNode) }),
});
type Graph = z.infer<typeof storedEnvelope>["spec"];
export type ReadView =
  | { status: "unavailable"; raw: unknown; reason: string }
  | { status: "supported"; raw: unknown; definition: ViewDefinition }
  | { status: "partial"; raw: unknown; title: string; root: string; elements: Record<string, ViewElement | null>; children: Record<string, string[]> };

function bounded(value: unknown): boolean {
  try { return new TextEncoder().encode(JSON.stringify(value)).length <= MAX_DEFINITION_BYTES; }
  catch { return false; }
}

function validGraph(spec: Graph): boolean {
  const count = Object.keys(spec.elements).length;
  if (count < 1 || count > 32) return false;
  const visited = new Set<string>();
  function visit(key: string, depth: number): boolean {
    if (depth > 8 || visited.has(key) || !Object.prototype.hasOwnProperty.call(spec.elements, key)) return false;
    visited.add(key);
    return (spec.elements[key].children ?? []).every(child => visit(child, depth + 1));
  }
  return visit(spec.root, 1) && visited.size === count;
}

/** Words belong to yes/no fields only. */
function validWords(source: { entity_id: string; entity_type: string; field?: string }, words?: Words): boolean {
  return words === undefined || textField(source)?.value_type === "boolean";
}

/** Entity and Device references a view reads; one view reads at most 16. */
export const MAX_VIEW_REFERENCES = 16;

function validReferences(elements: ViewElement[]): boolean {
  const ids = new Set<string>();
  for (const element of elements) {
    if (element.type === "Metric") {
      const field = quantityField(element.props.source);
      if (!field || (element.props.range && !field.quantity.range)) return false;
      ids.add(element.props.source.entity_id);
    }
    if (element.type === "State") {
      if (!textField(element.props.source) || !validWords(element.props.source, element.props.words)) return false;
      ids.add(element.props.source.entity_id);
    }
    if (element.type === "ReadingList") {
      if (element.props.rows.some(row => !stateField(row.source) || !validWords(row.source, row.words))) return false;
      element.props.rows.forEach(row => ids.add(row.source.entity_id));
    }
    if (element.type === "Device") ids.add(element.props.device.device_id);
    if (element.type === "PowerTile") {
      ids.add(element.props.source.entity_id);
      const metric = element.props.metric;
      if (metric) {
        if (!quantityField(metric.source)) return false;
        ids.add(metric.source.entity_id);
        if (metric.detail) {
          if (!quantityField(metric.detail.source)) return false;
          ids.add(metric.detail.source.entity_id);
        }
      }
    }
    if (element.type === "SwitchList") {
      const rowIDs = element.props.rows.map(row => row.source.entity_id);
      if (new Set(rowIDs).size !== rowIDs.length) return false;
      rowIDs.forEach(id => ids.add(id));
    }
    if (element.type === "StateTimeline") {
      const { rows } = element.props;
      if (rows.some(row => !textField(row.source) || !validWords(row.source, row.words)) ||
          new Set(rows.map(row => `${row.source.entity_id}:${row.source.field ?? ""}`)).size !== rows.length) return false;
      rows.forEach(row => ids.add(row.source.entity_id));
    }
    if (element.type === "WeekPattern") {
      if (textField(element.props.source)?.value_type !== "boolean") return false;
      ids.add(element.props.source.entity_id);
    }
    if (element.type === "TimeSeries") {
      const { series } = element.props;
      const types = series.map(item => item.source.entity_type);
      if (series.some(item => !quantityField(item.source)) ||
          new Set(series.map(item => `${item.source.entity_id}:${item.source.field ?? ""}`)).size !== series.length ||
          new Set(series.map(item => item.source.field ?? "")).size !== 1 ||
          new Set(series.map(item => item.source.window)).size !== 1 ||
          new Set(types).size !== 1) return false;
      series.forEach(item => ids.add(item.source.entity_id));
    }
  }
  return ids.size <= MAX_VIEW_REFERENCES;
}

// Cards stored before the household unit system carried props.display. The
// unit is now household-wide, so reads drop it; strict writes reject it.
function withoutLegacyDisplay(raw: unknown): unknown {
  if (typeof raw !== "object" || raw === null) return raw;
  const spec = (raw as { spec?: { elements?: unknown } }).spec;
  if (typeof spec !== "object" || spec === null || typeof spec.elements !== "object" || spec.elements === null) return raw;
  const elements = Object.fromEntries(Object.entries(spec.elements as Record<string, unknown>).map(([key, node]) => {
    if (typeof node !== "object" || node === null) return [key, node];
    const { type, props } = node as { type?: unknown; props?: unknown };
    if ((type !== "Metric" && type !== "TimeSeries") || typeof props !== "object" || props === null || !("display" in props)) return [key, node];
    const { display: _legacy, ...rest } = props as Record<string, unknown>;
    return [key, { ...node, props: rest }];
  }));
  return { ...raw, spec: { ...spec, elements } };
}
export function parseDefinition(raw: unknown): ViewDefinition | null {
  if (!bounded(raw)) return null;
  const result = definitionSchema.safeParse(raw);
  if (!result.success || !validGraph(result.data.spec) || !validReferences(Object.values(result.data.spec.elements))) return null;
  return result.data;
}

export function readDefinition(raw: unknown): ReadView {
  const unavailable = (reason: string): ReadView => ({ status: "unavailable", raw, reason });
  if (!bounded(raw)) return unavailable("View exceeds the supported size.");
  const current = withoutLegacyDisplay(raw);
  const envelope = storedEnvelope.safeParse(current);
  if (!envelope.success) return unavailable("Invalid view definition.");
  const value = envelope.data;
  if (value.schema_version !== 1 || value.catalog_version !== "hearth.dashboard/v1") return unavailable("This view requires a newer Hearth version.");
  if (!validGraph(value.spec)) return unavailable("Invalid view layout.");
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
  if (!validReferences(Object.values(elements).filter((node): node is ViewElement => node !== null))) return unavailable("Too many Entity references.");
  if (!unsupported) {
    const definition = parseDefinition(current);
    if (definition) return { status: "supported", raw, definition };
    return unavailable("Invalid view definition.");
  }
  return { status: "partial", raw, title: value.title, root: value.spec.root, elements, children };
}
