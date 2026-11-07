import Ajv2020 from "ajv/dist/2020";
import schema from "../../../../internal/modules/automations/automation-definition.schema.json";
import { automationJsonSource, editJsonSequence, editJsonSource, jsonSourceAt, parseAutomationResponse, type JsonPath } from "../../api/automation-json";
import type { Automation, AutomationDefinition, AutomationStep } from "../../api/types";

export type AutomationDocument = Readonly<{ source: string; definition: AutomationDefinition }>;
export type AutomationBase = Readonly<{ automation_id: string; revision: number }>;
export type AutomationReadStyle = "sentence" | "steps";
export type AutomationDraft = { kind: "automation"; document: AutomationDocument; original: AutomationDocument; origin_artifact_id: string | null; pendingProposal: boolean; dirty: boolean } & (
  | { mode: "create"; base: null }
  | { mode: "replace"; base: AutomationBase }
);
export type DocumentIssue = { path: string; message: string };
const validate = new Ajv2020({ allErrors: true, strict: false }).compile<AutomationDefinition>(schema);

/** Source is authoritative; schema validation supplies the typed read projection. */
export function readAutomationDocument(source: string): AutomationDocument {
  if (new TextEncoder().encode(source).length > 128 * 1024) throw new Error("Automation definitions must fit within 128 KiB.");
  const value: unknown = JSON.parse(source);
  // Bound recursive schema evaluation before letting a recursive validator walk input.
  const pending: { value: unknown; depth: number }[] = [{ value, depth: 0 }];
  while (pending.length) {
    const item = pending.pop()!;
    if (item.depth > 64) throw new Error("Automation JSON nesting exceeds the supported bound.");
    if (item.value && typeof item.value === "object") for (const child of Object.values(item.value)) pending.push({ value: child, depth: item.depth + 1 });
  }
  if (!validate(value)) {
    const error = validate.errors?.filter(issue => !["oneOf", "const", "required", "additionalProperties"].includes(issue.keyword)).sort((a, b) => b.instancePath.length - a.instancePath.length)[0] ?? validate.errors?.find(issue => issue.keyword !== "oneOf") ?? validate.errors?.[0];
    throw new Error(`${error?.instancePath || "/"}: ${error?.message || "Invalid Automation definition"}`);
  }
  // Attach exact sources for the existing read-only definition/history renderers too.
  const parsed = parseAutomationResponse<{ definition: AutomationDefinition }>(`{"definition":${source}}`);
  const document = { source, definition: parsed.definition };
  const issues = automationDocumentIssues(document);
  if (issues.length) throw new Error(issues.map(issue => `${issue.path}: ${issue.message}`).join("\n"));
  return document;
}

export function fromSavedAutomation(automation: Automation): AutomationDraft {
  const document = readAutomationDocument(automationJsonSource(automation.definition));
  return { kind: "automation", mode: "replace", base: { automation_id: automation.id, revision: automation.revision }, document, original: document, origin_artifact_id: null, pendingProposal: false, dirty: false };
}

export function newAutomationDraft(): AutomationDraft {
  const document = readAutomationDocument('{"name":"New Automation","enabled":false,"triggers":[],"steps":[{"id":"wait","kind":"delay","duration_ms":1000}]}');
  return { kind: "automation", mode: "create", base: null, document, original: document, origin_artifact_id: null, pendingProposal: false, dirty: true };
}

export function editAutomationDocument(document: AutomationDocument, path: JsonPath, source: string | undefined): AutomationDocument {
  return readAutomationDocument(editJsonSource(document.source, path, source));
}
export function sequenceDocument(document: AutomationDocument, path: JsonPath, update: (children: string[]) => string[]): AutomationDocument {
  return readAutomationDocument(editJsonSequence(document.source, path, update));
}
export function documentValue(document: AutomationDocument, path: JsonPath): string { return jsonSourceAt(document.source, path); }

export function serializeAutomationDocument(document: AutomationDocument, revision?: number): string {
  if (revision !== undefined && (!Number.isSafeInteger(revision) || revision < 1)) throw new Error("Invalid saved revision.");
  const validated = readAutomationDocument(document.source);
  return revision === undefined ? validated.source : `{"expected_revision":${revision},"definition":${validated.source}}`;
}

/** Cross-node bounds and identities not expressible by the canonical JSON schema. */
export function automationDocumentIssues(document: AutomationDocument): DocumentIssue[] {
  const issues: DocumentIssue[] = [];
  const ids = new Set<string>(); let nodes = 0, effects = 0, conditionNodes = 0;
  const triggerIDs = new Set<string>();
  const report = (path: string, message: string) => issues.push({ path, message });
  function disjoint(literals: Record<string, unknown> | undefined, expressions: Record<string, string> | undefined, path: string) {
    if (literals && expressions && Object.keys(literals).some(name => Object.prototype.hasOwnProperty.call(expressions, name))) report(path, "Literal and expression names must not overlap.");
  }
  disjoint(document.definition.variables, document.definition.variables_expr, "/variables_expr");
  for (const [index, trigger] of document.definition.triggers.entries()) {
    if (triggerIDs.has(trigger.id)) report(`/triggers/${index}/id`, "Trigger IDs must be unique.");
    triggerIDs.add(trigger.id);
  }
  function conditions(value: unknown, path: string, depth = 1, seen = new Set<string>()) {
    if (!value || typeof value !== "object") return;
    const node = value as Record<string, unknown>;
    conditionNodes++;
    if (depth > 8 || seen.size >= 64) { report(path, "Condition tree exceeds its depth or node limit."); return; }
    if (seen.has(String(node.id))) report(path, "Condition IDs must be unique within their tree.");
    seen.add(String(node.id));
    if (node.kind === "trigger" && Array.isArray(node.trigger_ids) && node.trigger_ids.some(id => !triggerIDs.has(id))) report(path, "Choose an existing Trigger ID.");
    if (node.child) conditions(node.child, `${path}/child`, depth + 1, seen);
    if (Array.isArray(node.children)) node.children.forEach((child, index) => conditions(child, `${path}/children/${index}`, depth + 1, seen));
  }
  function sequence(steps: AutomationStep[], path: string, depth = 1) {
    if (depth > 8) { report(path, "Step tree exceeds depth 8."); return; }
    steps.forEach((step, index) => {
      const location = `${path}/${index}`;
      nodes++;
      if (ids.has(step.id)) report(`${location}/id`, "Step IDs must be unique across all paths.");
      ids.add(step.id);
      if (step.kind === "set_variables") disjoint(step.assignments, step.assignments_expr, `${location}/assignments_expr`);
      if (!["if", "choose", "delay"].includes(step.kind)) effects++;
      if (step.kind === "if") {
        conditions(step.conditions, `${location}/conditions`);
        sequence(step.then, `${location}/then`, depth + 1);
        if (step.else) sequence(step.else, `${location}/else`, depth + 1);
      } else if (step.kind === "choose") {
        const branchIDs = new Set<string>();
        step.branches.forEach((branch, branchIndex) => {
          if (branchIDs.has(branch.id)) report(location, "Choose branch IDs must be unique.");
          branchIDs.add(branch.id);
          conditions(branch.conditions, `${location}/branches/${branchIndex}/conditions`);
          sequence(branch.steps, `${location}/branches/${branchIndex}/steps`, depth + 1);
        });
        if (step.default) sequence(step.default, `${location}/default`, depth + 1);
      }
    });
  }
  if (!document.definition.name.trim()) report("/name", "Enter a name.");
  if (document.definition.conditions) conditions(document.definition.conditions, "/conditions");
  sequence(document.definition.steps, "/steps");
  if (nodes > 64) report("/steps", "Use at most 64 Step nodes.");
  if (effects > 32) report("/steps", "Use at most 32 effect/data Steps.");
  if (conditionNodes > 256) report("/conditions", "Use at most 256 Condition nodes.");
  return issues;
}

export function newDocumentID(document: AutomationDocument, prefix = "step"): string {
  const names = new Set(document.source.match(/"(?:[^"\\]|\\.)*"/g)?.map(token => { try { return JSON.parse(token); } catch { return token; } }));
  for (let index = 1; ; index++) if (!names.has(`${prefix}_${index}`)) return `${prefix}_${index}`;
}
