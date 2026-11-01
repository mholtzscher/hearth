import { z } from "zod";
import { definitionSchema, elementSchema, type ViewDefinition, type ViewElement } from "./index";
import { childrenSchema, elementKey, plainText } from "./primitives";

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

function validReferences(elements: ViewElement[]): boolean {
  const ids = new Set<string>();
  for (const element of elements) {
    if (element.type === "Metric") ids.add(element.props.source.entity_id);
    if (element.type === "TimeSeries") {
      const { series } = element.props;
      if (new Set(series.map(item => item.source.entity_id)).size !== series.length ||
          new Set(series.map(item => item.source.window)).size !== 1) return false;
      series.forEach(item => ids.add(item.source.entity_id));
    }
  }
  return ids.size <= 16;
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
  const envelope = storedEnvelope.safeParse(raw);
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
    const definition = parseDefinition(raw);
    if (definition) return { status: "supported", raw, definition };
    return unavailable("Invalid view definition.");
  }
  return { status: "partial", raw, title: value.title, root: value.spec.root, elements, children };
}
