import { apiFetch } from "./client.ts";
import type { Automation } from "./types.ts";

// Parsed Automation objects are read-only here. Keep source out of their public
// JSON shape and let it expire with the response. In particular, never serialize
// parsed binding/evidence numbers: JavaScript may already have rounded them.
const sources = new WeakMap<object, string>();
type Span = { start: number; end: number };

function object(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function skipSpace(text: string, start: number): number {
  while (start < text.length && /\s/.test(text[start])) start++;
  return start;
}

/** Only scan JSON.parse-validated source. Iterative, bounded by the source span;
 * strings (including escaped quotes/backslashes) hide all structural characters. */
function valueEnd(text: string, start: number, limit: number): number {
  let depth = 0;
  let quoted = false;
  for (let i = start; i < limit; i++) {
    const char = text[i];
    if (quoted) {
      if (char === "\\") i++;
      else if (char === '"') {
        quoted = false;
        if (depth === 0) return i + 1;
      }
    } else if (char === '"') quoted = true;
    else if (char === "{" || char === "[") depth++;
    else if (char === "}" || char === "]") {
      if (depth === 0) return i;
      if (--depth === 0) return i + 1;
    } else if (depth === 0 && (char === "," || /\s/.test(char))) return i;
  }
  return limit;
}

function members(text: string, span: Span): Map<string, Span> {
  const result = new Map<string, Span>();
  if (text[span.start] !== "{") throw new Error("Expected Automation JSON object.");
  let cursor = skipSpace(text, span.start + 1);
  while (cursor < span.end && text[cursor] !== "}") {
    const keyEnd = valueEnd(text, cursor, span.end);
    const key: string = JSON.parse(text.slice(cursor, keyEnd));
    const start = skipSpace(text, skipSpace(text, keyEnd) + 1); // colon
    const end = valueEnd(text, start, span.end);
    result.set(key, { start, end }); // Match JSON.parse's last-member semantics.
    cursor = skipSpace(text, end);
    if (text[cursor] === ",") cursor = skipSpace(text, cursor + 1);
  }
  return result;
}

function elements(text: string, span: Span): Span[] {
  if (text[span.start] !== "[") throw new Error("Expected Automation JSON array.");
  const result: Span[] = [];
  let cursor = skipSpace(text, span.start + 1);
  while (cursor < span.end && text[cursor] !== "]") {
    const end = valueEnd(text, cursor, span.end);
    result.push({ start: cursor, end });
    cursor = skipSpace(text, end);
    if (text[cursor] === ",") cursor = skipSpace(text, cursor + 1);
  }
  return result;
}

/** Native parsing still owns JSON validity and ordinary numeric semantics.
 * Source navigation follows only Automation envelopes and every Step arm, never
 * treating literal/provider fields as Steps, bindings or Entity references. */
export function parseAutomationResponse<T>(text: string): T {
  const body: T = JSON.parse(text);
  const save = (value: object, span: Span) => sources.set(value, text.slice(span.start, span.end));
  function saveField(value: Record<string, unknown>, fields: Map<string, Span>, key: string) {
    const location = fields.get(key);
    if (object(value[key]) && location) save(value[key], location);
  }
  function bindings(value: Record<string, unknown>, fields: Map<string, Span>, key: string) {
    const location = fields.get(key);
    if (!object(value[key]) || !location) return;
    save(value[key], location);
    const entries = members(text, location);
    for (const name of Object.keys(value[key])) saveField(value[key], entries, name);
  }
  function condition(value: unknown, location: Span | undefined, depth = 1) {
    if (!object(value) || !location) return;
    if (depth > 8) throw new Error("Automation exceeds source preservation bounds.");
    const fields = members(text, location);
    if (value.kind === "expression") saveField(value, fields, "value");
    if (value.kind === "not") condition(value.child, fields.get("child"), depth + 1);
    if ((value.kind === "all" || value.kind === "any") && Array.isArray(value.children)) {
      const children = fields.get("children");
      if (children) elements(text, children).forEach((span, index) => condition((value.children as unknown[])[index], span, depth + 1));
    }
  }
  function definition(value: Record<string, unknown>, span: Span) {
    save(value, span);
    const definitionFields = members(text, span);
    bindings(value, definitionFields, "variables");
    condition(value.conditions, definitionFields.get("conditions"));
    let count = 0;
    function sequence(steps: unknown, location: Span | undefined, depth: number) {
      if (!Array.isArray(steps) || !location) throw new Error("Missing Automation Step source.");
      if (depth > 8 || steps.length > 32) throw new Error("Automation exceeds source preservation bounds.");
      const spans = elements(text, location);
      steps.forEach((step: unknown, index) => {
        if (++count > 64) throw new Error("Automation exceeds source preservation bounds.");
        if (!object(step)) throw new Error("Expected Automation Step object.");
        const fields = members(text, spans[index]);
        for (const key of ["parameters", "arguments", "title", "message", "duration_ms"]) saveField(step, fields, key);
        bindings(step, fields, "assignments");
        switch (step.kind) {
          case "if":
            condition(step.conditions, fields.get("conditions"));
            sequence(step.then, fields.get("then"), depth + 1);
            if (step.else !== undefined) sequence(step.else, fields.get("else"), depth + 1);
            break;
          case "choose": {
            const branches = fields.get("branches");
            if (!Array.isArray(step.branches) || !branches || step.branches.length > 32) throw new Error("Missing Automation branch source.");
            const branchSpans = elements(text, branches);
            step.branches.forEach((branch: unknown, branchIndex) => {
              if (!object(branch)) throw new Error("Expected Automation branch object.");
              const branchFields = members(text, branchSpans[branchIndex]);
              condition(branch.conditions, branchFields.get("conditions"));
              sequence(branch.steps, branchFields.get("steps"), depth + 1);
            });
            if (step.default !== undefined) sequence(step.default, fields.get("default"), depth + 1);
            break;
          }
        }
      });
    }
    sequence(value.steps, definitionFields.get("steps"), 1);
  }
  function response(value: unknown, span: Span) {
    if (!object(value)) return;
    save(value, span);
    const fields = members(text, span);
    for (const key of ["definition", "snapshot"] as const) {
      const location = fields.get(key);
      if (object(value[key]) && location) definition(value[key], location);
    }
    const run = fields.get("run");
    if (run) response(value.run, run);
    const data = fields.get("data");
    if (Array.isArray(value.data) && data) {
      const spans = elements(text, data);
      value.data.forEach((record: unknown, index) => {
        if (object(record)) save(record, spans[index]);
      });
    }
    const items = fields.get("items");
    if (Array.isArray(value.items) && items) {
      const spans = elements(text, items);
      value.items.forEach((item: unknown, index) => response(item, spans[index]));
    }
  }
  response(body, { start: skipSpace(text, 0), end: text.trimEnd().length });
  return body;
}

export function automationFetch<T>(path: string, init?: RequestInit): Promise<T> {
  return apiFetch<T>(path, init, parseAutomationResponse<T>);
}

/** Enablement is the browser's only definition edit. Copy the retained definition
 * verbatim except this boolean; never reconstruct bindings from parsed numbers.
 * Missing provenance fails closed before issuing a PUT. */
export function serializeAutomationEnablement(automation: Automation, enabled: boolean): string {
  const source = sources.get(automation.definition);
  if (!source) throw new Error("Exact Automation source unavailable. Refresh before changing enablement.");
  const field = members(source, { start: 0, end: source.length }).get("enabled");
  if (!field) throw new Error("Missing Automation enablement source.");
  const definition = source.slice(0, field.start) + String(enabled) + source.slice(field.end);
  return `{"expected_revision":${JSON.stringify(automation.revision)},"definition":${definition}}`;
}

/** Whitespace-only formatting of retained, validated JSON: number and string
 * tokens are copied, not parsed or re-encoded. Works without JSON.rawJSON or
 * reviver-context support, including older browsers supported by the dashboard. */
export function automationJsonText(value: object): string {
  const source = sources.get(value);
  if (!source) return "Exact Automation JSON unavailable. Refresh to reload it.";
  const tokens = source.match(/"(?:\\.|[^"\\])*"|[^\s{}\[\],:]+|[{}\[\],:]/g) ?? [];
  let depth = 0;
  let result = "";
  const newline = () => { result += "\n" + "  ".repeat(depth); };
  tokens.forEach((token, index) => {
    switch (token) {
      case "{":
      case "[":
        result += token;
        if (tokens[index + 1] !== (token === "{" ? "}" : "]")) { depth++; newline(); }
        break;
      case "}":
      case "]":
        if (tokens[index - 1] !== (token === "}" ? "{" : "[")) { depth--; newline(); }
        result += token;
        break;
      case ",": result += token; newline(); break;
      case ":": result += ": "; break;
      default: result += token;
    }
  });
  return result;
}
