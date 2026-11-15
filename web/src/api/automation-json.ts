import { apiFetch } from "./client.ts";
import type { Automation } from "./types.ts";

// Parsed Automation objects are read-only here. Keep source out of their public
// JSON shape and let it expire with the response. In particular, never serialize
// parsed literal/evidence numbers: JavaScript may already have rounded them.
const sources = new WeakMap<object, string>();
const fieldSources = new WeakMap<object, Map<string, string>>();
interface Span {
  start: number;
  end: number;
}
interface FailureTarget {
  code: string;
  field: unknown;
  id: unknown;
}

const object = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === "object" && !Array.isArray(value);

const invalidFailure = () => new Error("Invalid Automation value failure.");

const FAILURE_KEYS = new Set([
  "field",
  "condition_id",
  "failure_code",
  "state",
]);
const FAILURE_CODES = new Set([
  "expression_missing",
  "expression_type",
  "expression_evaluation",
  "expression_value_invalid",
  "expression_limit",
  "resolved_input_invalid",
  "read_state_unusable",
  "read_state_read_failed",
  "read_state_snapshot_incomplete",
  "read_state_corrupt",
  "run_data_limit",
]);
const FAILURE_FIELD_PATTERN =
  /^(?<field>parameters|arguments|title|message|trigger|variables|assignments|(?<collection>variables|assignments)\.[A-Za-z_][A-Za-z0-9_]{0,62})$/u;
const HIDDEN_FAILURE_FIELD_PATTERN =
  /^(?<collection>variables|assignments)\.__hearth_/u;
const FAILURE_ID_PATTERN = /^[a-z0-9][a-z0-9_-]{0,62}$/u;
const INITIALIZATION_FIELD_PATTERN =
  /^(?<field>trigger|variables(?<path>\.[A-Za-z_][A-Za-z0-9_]{0,62})?)$/u;
const ASSIGNMENT_FIELD_PATTERN =
  /^assignments(?<path>\.[A-Za-z_][A-Za-z0-9_]{0,62})?$/u;
const SPACE_PATTERN = /\s/u;

// History failures are a closed wire contract; definition-time diagnostic paths
// and arbitrary retained values are deliberately not inspected here.
const failureTarget = (value: Record<string, unknown>): FailureTarget => {
  if (Object.keys(value).some((key) => !FAILURE_KEYS.has(key))) {
    throw invalidFailure();
  }
  const { failure_code: code, field, condition_id: id } = value;
  if (typeof code !== "string" || !FAILURE_CODES.has(code)) {
    throw invalidFailure();
  }
  return { code, field, id };
};

const checkFailureField = (field: unknown) => {
  if (field === undefined) {
    return;
  }
  if (
    typeof field !== "string" ||
    !FAILURE_FIELD_PATTERN.test(field) ||
    HIDDEN_FAILURE_FIELD_PATTERN.test(field)
  ) {
    throw invalidFailure();
  }
};

const checkFailureId = (id: unknown) => {
  if (id === undefined) {
    return;
  }
  if (typeof id !== "string" || !FAILURE_ID_PATTERN.test(id)) {
    throw invalidFailure();
  }
};

const checkFailureState = (
  value: Record<string, unknown>,
  target: FailureTarget
) => {
  const present = value.state !== undefined;
  if ((target.code === "read_state_unusable") !== present) {
    throw invalidFailure();
  }
  if (present && (target.field !== undefined || target.id !== undefined)) {
    throw invalidFailure();
  }
};

const checkStepFailureField = (context: string, field: string) => {
  switch (context) {
    case "command": {
      if (field !== "parameters") {
        throw invalidFailure();
      }
      break;
    }
    case "function": {
      if (field !== "arguments") {
        throw invalidFailure();
      }
      break;
    }
    case "notification": {
      if (field !== "title" && field !== "message") {
        throw invalidFailure();
      }
      break;
    }
    case "set_variables": {
      if (!ASSIGNMENT_FIELD_PATTERN.test(field)) {
        throw invalidFailure();
      }
      break;
    }
    default: {
      break;
    }
  }
};

const checkFailureContext = (
  target: FailureTarget,
  context: string,
  postSuccess: boolean
) => {
  if (context === "initialization") {
    if (
      typeof target.field !== "string" ||
      !INITIALIZATION_FIELD_PATTERN.test(target.field)
    ) {
      throw invalidFailure();
    }
    return;
  }
  if (context === "if" || context === "choose") {
    if (
      target.field !== undefined ||
      (target.id === undefined && target.code !== "run_data_limit")
    ) {
      throw invalidFailure();
    }
    return;
  }
  if (target.id !== undefined) {
    throw invalidFailure();
  }
  if ((context === "read_state" || postSuccess) && target.field !== undefined) {
    throw invalidFailure();
  }
  if (typeof target.field === "string") {
    checkStepFailureField(context, target.field);
  }
  if (context !== "read_state" && target.code.startsWith("read_state_")) {
    throw invalidFailure();
  }
};

const checkFailure = (value: unknown, context: string, postSuccess = false) => {
  if (!object(value)) {
    throw invalidFailure();
  }
  const target = failureTarget(value);
  checkFailureField(target.field);
  checkFailureId(target.id);
  if (target.field !== undefined && target.id !== undefined) {
    throw invalidFailure();
  }
  checkFailureState(value, target);
  checkFailureContext(target, context, postSuccess);
};

const skipSpace = (text: string, start: number): number => {
  let index = start;
  while (index < text.length && SPACE_PATTERN.test(text[index])) {
    index += 1;
  }
  return index;
};

/** Only scan JSON.parse-validated source. Iterative, bounded by the source span;
 * strings (including escaped quotes/backslashes) hide all structural characters. */
const valueEnd = (text: string, start: number, limit: number): number => {
  let depth = 0;
  let quoted = false;
  for (let index = start; index < limit; index += 1) {
    const char = text[index];
    if (quoted) {
      if (char === "\\") {
        index += 1;
      } else if (char === '"') {
        quoted = false;
        if (depth === 0) {
          return index + 1;
        }
      }
    } else if (char === '"') {
      quoted = true;
    } else if (char === "{" || char === "[") {
      depth += 1;
    } else if (char === "}" || char === "]") {
      if (depth === 0) {
        return index;
      }
      depth -= 1;
      if (depth === 0) {
        return index + 1;
      }
    } else if (depth === 0 && (char === "," || SPACE_PATTERN.test(char))) {
      return index;
    }
  }
  return limit;
};

const members = (text: string, span: Span): Map<string, Span> => {
  const result = new Map<string, Span>();
  if (text[span.start] !== "{") {
    throw new Error("Expected Automation JSON object.");
  }
  let cursor = skipSpace(text, span.start + 1);
  while (cursor < span.end && text[cursor] !== "}") {
    const keyEnd = valueEnd(text, cursor, span.end);
    const key: string = JSON.parse(text.slice(cursor, keyEnd));
    // Skip the colon.
    const start = skipSpace(text, skipSpace(text, keyEnd) + 1);
    const end = valueEnd(text, start, span.end);
    // Match JSON.parse's last-member semantics.
    result.set(key, { start, end });
    cursor = skipSpace(text, end);
    if (text[cursor] === ",") {
      cursor = skipSpace(text, cursor + 1);
    }
  }
  return result;
};

const elements = (text: string, span: Span): Span[] => {
  if (text[span.start] !== "[") {
    throw new Error("Expected Automation JSON array.");
  }
  const result: Span[] = [];
  let cursor = skipSpace(text, span.start + 1);
  while (cursor < span.end && text[cursor] !== "]") {
    const end = valueEnd(text, cursor, span.end);
    result.push({ start: cursor, end });
    cursor = skipSpace(text, end);
    if (text[cursor] === ",") {
      cursor = skipSpace(text, cursor + 1);
    }
  }
  return result;
};

/** Native parsing still owns JSON validity and ordinary numeric semantics.
 * Source navigation follows only Automation envelopes and every Step arm, never
 * treating vendor argument fields as Steps or Entity references. */
export const parseAutomationResponse = <T>(text: string): T => {
  const body: T = JSON.parse(text);
  const save = (value: object, span: Span) => {
    sources.set(value, text.slice(span.start, span.end));
  };
  const saveField = (
    value: Record<string, unknown>,
    fields: Map<string, Span>,
    key: string
  ) => {
    const location = fields.get(key);
    if (object(value[key]) && location) {
      save(value[key], location);
    }
  };
  const saveValueField = (
    value: Record<string, unknown>,
    fields: Map<string, Span>,
    key: string
  ) => {
    const location = fields.get(key);
    if (!location) {
      return;
    }
    let saved = fieldSources.get(value);
    if (!saved) {
      saved = new Map();
      fieldSources.set(value, saved);
    }
    saved.set(key, text.slice(location.start, location.end));
  };
  const evaluation = (value: unknown, span: Span | undefined) => {
    if (!object(value) || !span || !Array.isArray(value.nodes)) {
      return;
    }
    const nodes = members(text, span).get("nodes");
    if (!nodes) {
      return;
    }
    for (const [index, location] of elements(text, nodes).entries()) {
      const node = Array.isArray(value.nodes) ? value.nodes[index] : undefined;
      if (object(node)) {
        saveValueField(node, members(text, location), "selected_value");
      }
    }
  };
  const executionEvidence = (value: Record<string, unknown>, span: Span) => {
    const location = members(text, span).get("evidence");
    if (object(value.evidence) && value.evidence.value_failure !== undefined) {
      checkFailure(
        value.evidence.value_failure,
        String(value.kind),
        value.kind === "function" &&
          value.evidence.invocation_outcome === "succeeded"
      );
      if (
        (value.kind === "if" || value.kind === "choose") &&
        object(value.evidence.value_failure) &&
        value.evidence.failure_code !==
          value.evidence.value_failure.failure_code
      ) {
        throw new Error("Invalid Automation value failure.");
      }
    }
    if (
      !location ||
      !object(value.evidence) ||
      !Array.isArray(value.evidence.evaluations)
    ) {
      return;
    }
    const evaluations = members(text, location).get("evaluations");
    if (!evaluations) {
      return;
    }
    for (const [index, itemSpan] of elements(text, evaluations).entries()) {
      const item = (value.evidence as Record<string, unknown>).evaluations;
      const entry = Array.isArray(item) ? item[index] : undefined;
      if (object(entry)) {
        evaluation(entry.evaluation, members(text, itemSpan).get("evaluation"));
      }
    }
  };
  const bindings = (
    value: Record<string, unknown>,
    fields: Map<string, Span>,
    key: string
  ) => {
    const location = fields.get(key);
    if (!object(value[key]) || !location) {
      return;
    }
    save(value[key], location);
    const entries = members(text, location);
    for (const name of Object.keys(value[key])) {
      saveField(value[key], entries, name);
      saveValueField(value[key], entries, name);
    }
  };
  const condition = (value: unknown, location: Span | undefined, depth = 1) => {
    if (!object(value) || !location) {
      return;
    }
    if (depth > 8) {
      throw new Error("Automation exceeds source preservation bounds.");
    }
    const fields = members(text, location);
    if (value.kind === "not") {
      condition(value.child, fields.get("child"), depth + 1);
    }
    if (
      (value.kind === "all" || value.kind === "any") &&
      Array.isArray(value.children)
    ) {
      const children = fields.get("children");
      if (children) {
        for (const [index, span] of elements(text, children).entries()) {
          condition((value.children as unknown[])[index], span, depth + 1);
        }
      }
    }
  };
  const definition = (value: Record<string, unknown>, span: Span) => {
    save(value, span);
    const definitionFields = members(text, span);
    bindings(value, definitionFields, "variables");
    condition(value.conditions, definitionFields.get("conditions"));
    let count = 0;
    const sequence = (
      steps: unknown,
      location: Span | undefined,
      depth: number
    ) => {
      if (!Array.isArray(steps) || !location) {
        throw new Error("Missing Automation Step source.");
      }
      if (depth > 8 || steps.length > 32) {
        throw new Error("Automation exceeds source preservation bounds.");
      }
      const spans = elements(text, location);
      for (const [index, step] of steps.entries()) {
        count += 1;
        if (count > 64) {
          throw new Error("Automation exceeds source preservation bounds.");
        }
        if (!object(step)) {
          throw new Error("Expected Automation Step object.");
        }
        const fields = members(text, spans[index]);
        for (const key of [
          "parameters",
          "arguments",
          "title",
          "message",
          "duration_ms",
        ]) {
          saveField(step, fields, key);
          saveValueField(step, fields, key);
        }
        bindings(step, fields, "assignments");
        switch (step.kind) {
          case "if": {
            condition(step.conditions, fields.get("conditions"));
            sequence(step.then, fields.get("then"), depth + 1);
            if (step.else !== undefined) {
              sequence(step.else, fields.get("else"), depth + 1);
            }
            break;
          }
          case "choose": {
            const branches = fields.get("branches");
            if (
              !Array.isArray(step.branches) ||
              !branches ||
              step.branches.length > 32
            ) {
              throw new Error("Missing Automation branch source.");
            }
            const branchSpans = elements(text, branches);
            for (const [branchIndex, branch] of step.branches.entries()) {
              if (!object(branch)) {
                throw new Error("Expected Automation branch object.");
              }
              const branchFields = members(text, branchSpans[branchIndex]);
              condition(branch.conditions, branchFields.get("conditions"));
              sequence(branch.steps, branchFields.get("steps"), depth + 1);
            }
            if (step.default !== undefined) {
              sequence(step.default, fields.get("default"), depth + 1);
            }
            break;
          }
          default: {
            break;
          }
        }
      }
    };
    sequence(value.steps, definitionFields.get("steps"), 1);
  };
  const saveCauseValues = (
    value: Record<string, unknown>,
    fields: Map<string, Span>
  ) => {
    const causeSpan = fields.get("cause");
    if (!object(value.cause) || !causeSpan || !object(value.cause.fact)) {
      return;
    }
    const factSpan = members(text, causeSpan).get("fact");
    if (!factSpan) {
      return;
    }
    for (const key of ["value", "previous_value"]) {
      saveValueField(value.cause.fact, members(text, factSpan), key);
    }
  };
  const saveDecisionValues = (
    value: Record<string, unknown>,
    fields: Map<string, Span>
  ) => {
    const decisionSpan = fields.get("condition_decision");
    if (object(value.condition_decision) && decisionSpan) {
      evaluation(
        value.condition_decision.evaluation,
        members(text, decisionSpan).get("evaluation")
      );
    }
  };
  const saveExecutions = (executions: unknown[], span: Span) => {
    const spans = elements(text, span);
    for (const [index, execution] of executions.entries()) {
      if (object(execution)) {
        save(execution, spans[index]);
        executionEvidence(execution, spans[index]);
      }
    }
  };
  const response = (value: unknown, span: Span) => {
    if (!object(value)) {
      return;
    }
    save(value, span);
    const fields = members(text, span);
    saveCauseValues(value, fields);
    saveDecisionValues(value, fields);
    for (const key of ["definition", "snapshot"] as const) {
      const location = fields.get(key);
      if (object(value[key]) && location) {
        definition(value[key], location);
      }
    }
    const run = fields.get("run");
    if (run) {
      response(value.run, run);
    }
    const skip = fields.get("skip");
    if (skip) {
      response(value.skip, skip);
    }
    saveField(value, fields, "value_initialization");
    if (
      object(value.value_initialization) &&
      value.value_initialization.kind === "failed"
    ) {
      checkFailure(value.value_initialization.failure, "initialization");
    }
    const executions = fields.get("executions");
    if (Array.isArray(value.executions) && executions) {
      saveExecutions(value.executions, executions);
    }
    const items = fields.get("items");
    if (Array.isArray(value.items) && items) {
      const spans = elements(text, items);
      for (const [index, item] of value.items.entries()) {
        response(item, spans[index]);
      }
    }
  };
  response(body, { start: skipSpace(text, 0), end: text.trimEnd().length });
  return body;
};

export const automationFetch = <T>(
  path: string,
  init?: RequestInit
): Promise<T> => apiFetch<T>(path, init, parseAutomationResponse<T>);

/** Exact response provenance for authoring. Parsed JS numbers are never a fallback. */
export const automationJsonSource = (value: object): string => {
  const source = sources.get(value);
  if (source === undefined) {
    throw new Error(
      "Exact Automation source unavailable. Refresh before editing."
    );
  }
  return source;
};

export type JsonPath = readonly (string | number)[];

/** Navigate already JSON.parse-validated source, including escaped property names. */
export const jsonSourceAt = (source: string, path: JsonPath): string => {
  let span = { start: skipSpace(source, 0), end: source.trimEnd().length };
  for (const key of path) {
    const next =
      typeof key === "number"
        ? elements(source, span)[key]
        : members(source, span).get(key);
    if (!next) {
      throw new Error("The selected Automation field no longer exists.");
    }
    span = next;
  }
  return source.slice(span.start, span.end);
};

/** Replace one value, or add/remove an object member, keeping every sibling token. */
export const editJsonSource = (
  source: string,
  path: JsonPath,
  replacement: string | undefined
): string => {
  if (replacement !== undefined) {
    JSON.parse(replacement);
  }
  if (!path.length) {
    if (replacement === undefined) {
      throw new Error("Cannot remove the Automation document.");
    }
    return replacement;
  }
  const parentPath = path.slice(0, -1);
  const [key] = path.slice(-1);
  const parent = jsonSourceAt(source, parentPath);
  const span = { start: 0, end: parent.length };
  let updated: string;
  if (typeof key === "number") {
    const children = elements(parent, span).map((item) =>
      parent.slice(item.start, item.end)
    );
    if (key < 0 || key >= children.length) {
      throw new Error("The selected Step no longer exists.");
    }
    if (replacement === undefined) {
      children.splice(key, 1);
    } else {
      children[key] = replacement;
    }
    updated = `[${children.join(",")}]`;
  } else {
    const fields = members(parent, span);
    const field = fields.get(key);
    if (field && replacement !== undefined) {
      updated =
        parent.slice(0, field.start) + replacement + parent.slice(field.end);
    } else if (!field && replacement !== undefined) {
      updated = `${parent.slice(0, -1)}${fields.size ? "," : ""}${JSON.stringify(key)}:${replacement}}`;
    } else {
      // Removing all copies also prevents an earlier duplicate from becoming authoritative.
      const retained: string[] = [];
      let cursor = skipSpace(parent, 1);
      while (cursor < parent.length && parent[cursor] !== "}") {
        const keyEnd = valueEnd(parent, cursor, parent.length);
        const memberKey: string = JSON.parse(parent.slice(cursor, keyEnd));
        const start = skipSpace(parent, skipSpace(parent, keyEnd) + 1);
        const end = valueEnd(parent, start, parent.length);
        if (memberKey !== key) {
          retained.push(parent.slice(cursor, end));
        }
        cursor = skipSpace(parent, end);
        if (parent[cursor] === ",") {
          cursor = skipSpace(parent, cursor + 1);
        }
      }
      updated = `{${retained.join(",")}}`;
    }
  }
  return editJsonSource(source, parentPath, updated);
};

/** Reorder a sequence using its original child slices, never rounded projections. */
export const editJsonSequence = (
  source: string,
  path: JsonPath,
  update: (children: string[]) => string[]
): string => {
  const sequence = jsonSourceAt(source, path);
  const children = elements(sequence, { start: 0, end: sequence.length }).map(
    (span) => sequence.slice(span.start, span.end)
  );
  return editJsonSource(source, path, `[${update(children).join(",")}]`);
};

/** Copy the retained definition
 * verbatim except this boolean; never reconstruct opaque arguments from numbers.
 * Missing provenance fails closed before issuing a PUT. */
export const serializeAutomationEnablement = (
  automation: Automation,
  enabled: boolean
): string => {
  const source = sources.get(automation.definition);
  if (!source) {
    throw new Error(
      "Exact Automation source unavailable. Refresh before changing enablement."
    );
  }
  const field = members(source, { start: 0, end: source.length }).get(
    "enabled"
  );
  if (!field) {
    throw new Error("Missing Automation enablement source.");
  }
  const definition =
    source.slice(0, field.start) + String(enabled) + source.slice(field.end);
  return `{"expected_revision":${JSON.stringify(automation.revision)},"definition":${definition}}`;
};

/** Indent validated JSON source. Only whitespace changes; every token is copied. */
export const formatJsonSource = (source: string): string => {
  const tokens =
    source.match(/"(?:\\.|[^"\\])*"|[^\s{}[\],:]+|[{}[\],:]/gu) ?? [];
  let depth = 0;
  let result = "";
  const newline = () => {
    result += `\n${"  ".repeat(depth)}`;
  };
  for (const [index, token] of tokens.entries()) {
    switch (token) {
      case "{":
      case "[": {
        result += token;
        if (tokens[index + 1] !== (token === "{" ? "}" : "]")) {
          depth += 1;
          newline();
        }
        break;
      }
      case "}":
      case "]": {
        if (tokens[index - 1] !== (token === "}" ? "{" : "[")) {
          depth -= 1;
          newline();
        }
        result += token;
        break;
      }
      case ",": {
        result += token;
        newline();
        break;
      }
      case ":": {
        result += ": ";
        break;
      }
      default: {
        result += token;
      }
    }
  }
  return result;
};

/** Whitespace-only formatting of retained, validated JSON: number and string
 * tokens are copied, not parsed or re-encoded. Works without JSON.rawJSON or
 * reviver-context support, including older browsers supported by the dashboard. */
export const automationJsonText = (value: object): string => {
  const source = sources.get(value);
  if (!source) {
    return "Exact Automation JSON unavailable. Refresh to reload it.";
  }
  return formatJsonSource(source);
};

/** Preserve scalar and nested evidence tokens without round-tripping through JS numbers. */
export const automationJsonFieldText = (value: object, key: string): string => {
  const source = fieldSources.get(value)?.get(key);
  return source === undefined
    ? "Exact value unavailable"
    : formatJsonSource(source);
};
