import { useEffect, useState } from "react";
import { apiFetch, useBaseUrlVersion } from "../../api/client";
import { editJsonSource, type JsonPath } from "../../api/automation-json";
import type { Automation, AutomationBranchCondition, AutomationStep, Entity } from "../../api/types";
import type { NotificationDestination } from "../../api/notifications";
import { automationDocumentIssues, documentValue, editAutomationDocument, newDocumentID, readAutomationDocument, sequenceDocument, type AutomationDocument } from "./document";
import "./automations.css";

type Resources = { entities: Entity[]; automations: Automation[]; destinations: NotificationDestination[] };
type EditorProps = { document: AutomationDocument; onChange: (document: AutomationDocument) => void; onValidityChange?: (valid: boolean) => void };
type Context = { document: AutomationDocument; resources: Resources; apply: (path: JsonPath, raw: string | undefined) => void; sequence: (path: JsonPath, update: (children: string[]) => string[]) => void; invalid: Record<string, string>; setInvalid: (key: string, value: string | null) => void };
const keyOf = (path: JsonPath) => JSON.stringify(path);
const q = JSON.stringify;
async function collection<T>(path: string): Promise<T[]> {
  const all: T[] = [], seen = new Set<string>(); let cursor = "";
  for (let page = 0; page < 100; page++) {
    const result = await apiFetch<{ items: T[]; next_cursor?: string | null }>(`${path}?limit=200${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`);
    all.push(...result.items);
    if (!result.next_cursor) return all;
    if (seen.has(result.next_cursor)) throw new Error("Resource pagination repeated a cursor.");
    seen.add(result.next_cursor); cursor = result.next_cursor;
  }
  throw new Error("Resource pagination exceeded its limit.");
}
function Field({ ctx, path, label, raw = false, multiline = false }: { ctx: Context; path: JsonPath; label: string; raw?: boolean; multiline?: boolean }) {
  const key = keyOf(path); let source = "";
  try { source = documentValue(ctx.document, path); } catch { /* optional value */ }
  const shown = ctx.invalid[key] ?? (raw ? source : source ? String(JSON.parse(source)) : "");
  const update = (value: string) => {
    try { const next = editAutomationDocument(ctx.document, path, raw ? value : q(value)); ctx.setInvalid(key, null); ctx.apply([], next.source); }
    catch { ctx.setInvalid(key, value); }
  };
  const props = { value: shown, "aria-invalid": ctx.invalid[key] !== undefined, onChange: (event: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => update(event.target.value) };
  return <label className="automation-field"><span>{label}</span>{multiline ? <textarea {...props} rows={4}/> : <input {...props}/>} {ctx.invalid[key] !== undefined && <small role="alert">Enter a valid {raw ? "JSON value for this field" : "value"}.</small>}</label>;
}
function Select({ ctx, path, label, options }: { ctx: Context; path: JsonPath; label: string; options: { value: string; label: string }[] }) {
  let current = ""; try { current = JSON.parse(documentValue(ctx.document, path)); } catch { /* absent */ }
  return <label className="automation-field"><span>{label}</span><select value={current} onChange={event => ctx.apply(path, q(event.target.value))}>{!options.some(option => option.value === current) && <option value={current}>{current || "Select…"}</option>}{options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select></label>;
}
const entityOptions = (ctx: Context) => ctx.resources.entities.map(entity => ({ value: entity.id, label: `${entity.name} · ${entity.id}` }));
const firstEntity = (ctx: Context) => ctx.resources.entities[0]?.id;
function conditionSource(ctx: Context, kind: string, id: string): string | undefined {
  const entity = firstEntity(ctx); const childID = newDocumentID(ctx.document, "condition_child");
  if (kind === "entity_state") return entity ? q({ id, kind, entity_id: entity, value_pointer: "", operator: "eq", operand: true }) : undefined;
  if (kind === "expression") return q({ id, kind, expr: "true" });
  if (kind === "trigger") return ctx.document.definition.triggers[0] ? q({ id, kind, trigger_ids: [ctx.document.definition.triggers[0].id] }) : undefined;
  const child = conditionSource(ctx, "entity_state", childID);
  if (!child) return undefined;
  if (kind === "not") return `{"id":${q(id)},"kind":"not","child":${child}}`;
  if (kind === "none") return `{"id":${q(id)},"kind":"not","child":{"id":${q(childID)},"kind":"any","children":[${conditionSource(ctx, "entity_state", newDocumentID(ctx.document, "condition_leaf"))}]}}`;
  return `{"id":${q(id)},"kind":${q(kind)},"children":[${child}]}`;
}
function Condition({ ctx, value, path, reached }: { ctx: Context; value: AutomationBranchCondition; path: JsonPath; reached: boolean }) {
  const none = value.kind === "not" && value.child.kind === "any";
  const kinds = ["entity_state", "all", "any", "none", "not", ...(reached ? ["expression", "trigger"] : [])];
  const changeKind = (kind: string) => {
    if ((value.kind === "all" || value.kind === "any") && (kind === "all" || kind === "any")) { ctx.apply([...path, "kind"], q(kind)); return; }
    const source = conditionSource(ctx, kind, value.id); if (source) ctx.apply(path, source);
  };
  return <div className="automation-condition"><label className="automation-field"><span>Condition type</span><select value={none ? "none" : value.kind} onChange={event => changeKind(event.target.value)}>{kinds.map(kind => <option key={kind} value={kind}>{({ entity_state: "State", all: "All", any: "Any", none: "None (not any)", not: "Not", expression: "Expression", trigger: "Trigger" } as Record<string, string>)[kind]}</option>)}</select></label>
    {value.kind === "entity_state" && <><Select ctx={ctx} path={[...path, "entity_id"]} label="Entity" options={entityOptions(ctx)}/><Field ctx={ctx} path={[...path, "value_pointer"]} label="State value pointer"/><Select ctx={ctx} path={[...path, "operator"]} label="Comparison" options={["eq", "ne", "lt", "lte", "gt", "gte"].map(value => ({ value, label: value }))}/><Field ctx={ctx} path={[...path, "operand"]} label="Operand · exact JSON, canonical units" raw/></>}
    {value.kind === "expression" && <Field ctx={ctx} path={[...path, "expr"]} label="Expression"/>}
    {value.kind === "trigger" && <Field ctx={ctx} path={[...path, "trigger_ids"]} label="Trigger IDs · exact JSON array" raw/>}
    {(value.kind === "all" || value.kind === "any") && <><div className="automation-condition-list">{value.children.map((child, index) => <div key={child.id}><Condition ctx={ctx} value={child} path={[...path, "children", index]} reached={reached}/><button disabled={value.children.length === 1} onClick={() => ctx.sequence([...path, "children"], children => children.filter((_, i) => i !== index))}>Remove Condition</button></div>)}</div><button onClick={() => { const source = conditionSource(ctx, reached ? "expression" : "entity_state", newDocumentID(ctx.document, "condition")); if (source) ctx.sequence([...path, "children"], children => [...children, source]); }}>Add Condition</button></>}
    {value.kind === "not" && <Condition ctx={ctx} value={value.child} path={[...path, "child"]} reached={reached}/>}
  </div>;
}
function Binding({ ctx, path, name, label }: { ctx: Context; path: JsonPath; name: string; label: string }) {
  let obj: Record<string, unknown>; try { obj = JSON.parse(documentValue(ctx.document, path)); } catch { return null; }
  const expr = `${name}_expr`, isExpr = expr in obj;
  const toggle = (next: boolean) => {
    const oldKey = next ? name : expr, newKey = next ? expr : name;
    // Replacing this object preserves every unrelated token; the mutually exclusive pair changes atomically.
    const source = documentValue(ctx.document, path);
    try {
      // Operate on the object slice without passing through an invalid intermediate document.
      const removed = editJsonSource(source, [oldKey], undefined);
      ctx.apply(path, editJsonSource(removed, [newKey], next ? q("true") : name === "parameters" ? "{}" : q("")));
    } catch { /* retain existing binding */ }
  };
  return <><label className="automation-field"><span>{label} binding</span><select value={isExpr ? "expression" : "literal"} onChange={event => toggle(event.target.value === "expression")}><option value="literal">Literal</option><option value="expression">Expression</option></select></label><Field ctx={ctx} path={[...path, isExpr ? expr : name]} label={`${label}${!isExpr && name === "parameters" ? " · exact JSON" : ""}`} raw={!isExpr && name === "parameters"} multiline={!isExpr && name === "parameters"}/></>;
}
function newStep(ctx: Context, kind: string): string | undefined {
  const id = newDocumentID(ctx.document, "step"), entity = firstEntity(ctx);
  if (kind === "delay") return q({ id, kind, duration_ms: 1000 });
  if (kind === "command") { const target = ctx.resources.entities.find(e => Object.keys((e.support.operations ?? {}) as object).length); const operation = target && Object.keys((target.support.operations ?? {}) as object)[0]; return target && operation ? q({ id, kind, entity_id: target.id, operation, parameters: {} }) : undefined; }
  if (kind === "notification") { const destination = ctx.resources.destinations[0]; return destination ? q({ id, kind, destination_id: destination.id, title: "", message: "" }) : undefined; }
  if (kind === "sub_automation") { const target = ctx.resources.automations[0]; return target ? q({ id, kind, automation_id: target.id }) : undefined; }
  const condition = q({ id: newDocumentID(ctx.document, "condition"), kind: "expression", expr: "true" });
  const child = q({ id: newDocumentID(ctx.document, "wait"), kind: "delay", duration_ms: 1000 });
  if (kind === "if") return `{"id":${q(id)},"kind":"if","conditions":${condition},"then":[${child}]}`;
  if (kind === "choose") return `{"id":${q(id)},"kind":"choose","branches":[{"id":${q(newDocumentID(ctx.document, "branch"))},"conditions":${condition},"steps":[${child}]}]}`;
  if (kind === "read_state" && entity) return q({ id, kind, entity_id: entity, value_pointer: "" });
  if (kind === "set_variables") return q({ id, kind, assignments: { value: null } });
  return undefined;
}
function SequenceEditor({ ctx, path, steps, title }: { ctx: Context; path: JsonPath; steps: AutomationStep[]; title: string }) {
  const [kind, setKind] = useState("delay");
  return <section className="automation-sequence-editor"><h4>{title}</h4><ol className="automation-sequence">{steps.map((step, index) => <li key={step.id}><details className="automation-step-editor" open><summary>{step.kind.replace(/_/g, " ")} · {step.id}</summary><StepEditor ctx={ctx} path={[...path, index]} step={step}/><div className="automation-actions"><button disabled={index === 0} onClick={() => ctx.sequence(path, children => { [children[index - 1], children[index]] = [children[index], children[index - 1]]; return children; })}>Move up</button><button disabled={index === steps.length - 1} onClick={() => ctx.sequence(path, children => { [children[index + 1], children[index]] = [children[index], children[index + 1]]; return children; })}>Move down</button><button disabled={steps.length === 1} onClick={() => ctx.sequence(path, children => children.filter((_, i) => i !== index))}>Remove Step</button></div></details></li>)}</ol><div className="automation-actions"><label>Add Step <select value={kind} onChange={event => setKind(event.target.value)}>{["delay", "command", "notification", "sub_automation", "if", "choose", "read_state", "set_variables"].map(k => <option key={k} value={k}>{k.replace(/_/g, " ")}</option>)}</select></label><button disabled={!newStep(ctx, kind)} onClick={() => { const source = newStep(ctx, kind); if (source) ctx.sequence(path, children => [...children, source]); }}>Add to {title}</button></div></section>;
}
function StepEditor({ ctx, path, step }: { ctx: Context; path: JsonPath; step: AutomationStep }) {
  const p = (field: string): JsonPath => [...path, field];
  const options = entityOptions(ctx);
  const delay = () => q({ id: newDocumentID(ctx.document, "wait"), kind: "delay", duration_ms: 1000 });
  return <div className="automation-fields">
    {step.kind === "delay" && <Field ctx={ctx} path={p("duration_ms")} label="Wait · milliseconds" raw/>}
    {step.kind === "command" && <><Select ctx={ctx} path={p("entity_id")} label="Entity" options={options}/><Select ctx={ctx} path={p("operation")} label="Supported Operation" options={Object.keys((ctx.resources.entities.find(e => e.id === step.entity_id)?.support.operations ?? {}) as object).map(value => ({ value, label: value }))}/><Binding ctx={ctx} path={path} name="parameters" label="Parameters"/></>}
    {step.kind === "notification" && <><Select ctx={ctx} path={p("destination_id")} label="Destination" options={ctx.resources.destinations.map(d => ({ value: d.id, label: `${d.name} · ${d.id}` }))}/><Binding ctx={ctx} path={path} name="title" label="Title"/><Binding ctx={ctx} path={path} name="message" label="Message"/><p>Provider acceptance does not prove delivery.</p></>}
    {step.kind === "sub_automation" && <Select ctx={ctx} path={p("automation_id")} label="Automation" options={ctx.resources.automations.map(a => ({ value: a.id, label: `${a.definition.name} · ${a.id}` }))}/>}
    {step.kind === "if" && <><Condition ctx={ctx} value={step.conditions} path={p("conditions")} reached/><SequenceEditor ctx={ctx} path={p("then")} steps={step.then} title="Then"/>{step.else ? <><SequenceEditor ctx={ctx} path={p("else")} steps={step.else} title="Else"/><button onClick={() => ctx.apply(p("else"), undefined)}>Remove Else</button></> : <button onClick={() => ctx.apply(p("else"), `[${delay()}]`)}>Add Else</button>}</>}
    {step.kind === "choose" && <>{step.branches.map((branch, index) => <details className="automation-arm" open key={branch.id}><summary>Path {index + 1} · {branch.id}</summary><Condition ctx={ctx} value={branch.conditions} path={[...p("branches"), index, "conditions"]} reached/><SequenceEditor ctx={ctx} path={[...p("branches"), index, "steps"]} steps={branch.steps} title="Path Steps"/><div className="automation-actions"><button disabled={index === 0} onClick={() => ctx.sequence(p("branches"), children => { [children[index - 1], children[index]] = [children[index], children[index - 1]]; return children; })}>Move path up</button><button disabled={index === step.branches.length - 1} onClick={() => ctx.sequence(p("branches"), children => { [children[index + 1], children[index]] = [children[index], children[index + 1]]; return children; })}>Move path down</button><button disabled={step.branches.length === 1} onClick={() => ctx.sequence(p("branches"), children => children.filter((_, i) => i !== index))}>Remove path</button></div></details>)}<button onClick={() => { const branch = `{"id":${q(newDocumentID(ctx.document, "branch"))},"conditions":{"id":${q(newDocumentID(ctx.document, "condition"))},"kind":"expression","expr":"true"},"steps":[${delay()}]}`; ctx.sequence(p("branches"), children => [...children, branch]); }}>Add path</button>{step.default ? <><SequenceEditor ctx={ctx} path={p("default")} steps={step.default} title="Default"/><button onClick={() => ctx.apply(p("default"), undefined)}>Remove Default</button></> : <button onClick={() => ctx.apply(p("default"), `[${delay()}]`)}>Add Default</button>}</>}
    {(step.kind === "function" || step.kind === "read_state" || step.kind === "set_variables") && <Field ctx={ctx} path={path} label="Exact JSON Step inspector" raw multiline/>}
  </div>;
}
function Comparisons({ ctx, path, values, label, required = false }: { ctx: Context; path: JsonPath; values: { value_pointer: string; operator: string; operand: unknown }[] | undefined; label: string; required?: boolean }) {
  const comparison = '{"value_pointer":"","operator":"eq","operand":true}';
  return <section className="automation-condition"><h4>{label}</h4>{values?.map((_, index) => <div className="automation-fields" key={index}>
    <Field ctx={ctx} path={[...path, index, "value_pointer"]} label="Value pointer"/>
    <Select ctx={ctx} path={[...path, index, "operator"]} label="Comparison" options={["eq", "ne", "lt", "lte", "gt", "gte"].map(value => ({ value, label: value }))}/>
    <Field ctx={ctx} path={[...path, index, "operand"]} label="Operand · exact JSON, canonical units" raw/>
    <button disabled={required && values.length === 1} onClick={() => values.length === 1 ? ctx.apply(path, undefined) : ctx.sequence(path, children => children.filter((_, i) => i !== index))}>Remove comparison</button>
  </div>)}<button onClick={() => values ? ctx.sequence(path, children => [...children, comparison]) : ctx.apply(path, `[${comparison}]`)}>Add comparison</button></section>;
}
function TriggerEditor({ ctx }: { ctx: Context }) {
  const [kind, setKind] = useState("cron");
  const triggers = ctx.document.definition.triggers;
  const add = () => {
    const id = newDocumentID(ctx.document, "trigger"), entity = firstEntity(ctx);
    let source: string | undefined;
    if (kind === "cron") source = q({ id, kind, expression: "0 7 * * *" });
    if (entity && kind === "entity_event") source = q({ id, kind, entity_id: entity, event_name: "event" });
    if (entity && kind === "observation") source = q({ id, kind, entity_id: entity, dispositions: ["applied"] });
    if (entity && kind === "held_state") source = q({ id, kind, entity_id: entity, comparisons: [{ value_pointer: "", operator: "eq", operand: true }], for_seconds: 1 });
    if (source) ctx.sequence(["triggers"], children => [...children, source]);
  };
  return <section><h4>Triggers</h4>{triggers.length === 0 && <p>Manual only. Add a Trigger for automatic admission.</p>}{triggers.map((trigger, index) => {
    const path: JsonPath = ["triggers", index];
    return <details className="automation-step-editor" key={trigger.id} open><summary>{trigger.kind.replace(/_/g, " ")} · {trigger.id}</summary><div className="automation-fields">
      {trigger.kind !== "cron" && <Select ctx={ctx} path={[...path, "entity_id"]} label="Entity" options={entityOptions(ctx)}/>}
      {trigger.kind === "cron" && <Field ctx={ctx} path={[...path, "expression"]} label="Cron · household timezone"/>}
      {trigger.kind === "entity_event" && <Field ctx={ctx} path={[...path, "event_name"]} label="Event name"/>}
      {trigger.kind === "held_state" && <Field ctx={ctx} path={[...path, "for_seconds"]} label="Held for · seconds" raw/>}
      {trigger.kind === "observation" && <Field ctx={ctx} path={[...path, "dispositions"]} label="Dispositions · exact JSON array" raw/>}
      {(trigger.kind === "held_state" || trigger.kind === "observation") && <><Comparisons ctx={ctx} path={[...path, "comparisons"]} values={trigger.comparisons} label="Current value comparisons" required={trigger.kind === "held_state"}/>{trigger.kind === "observation" && <Comparisons ctx={ctx} path={[...path, "previous_comparisons"]} values={trigger.previous_comparisons} label="Previous value comparisons"/>}</>}
      <button onClick={() => ctx.sequence(["triggers"], children => children.filter((_, i) => i !== index))}>Remove Trigger</button></div></details>;
  })}<div className="automation-actions"><label>Trigger type <select value={kind} onChange={event => setKind(event.target.value)}>{["cron", "observation", "entity_event", "held_state"].map(value => <option key={value} value={value}>{value.replace(/_/g, " ")}</option>)}</select></label><button disabled={kind !== "cron" && !firstEntity(ctx)} onClick={add}>Add Trigger</button></div></section>;
}
export function AutomationEditor({ document, onChange, onValidityChange }: EditorProps) {
  const [resources, setResources] = useState<Resources>({ entities: [], automations: [], destinations: [] });
  const [loadError, setLoadError] = useState("");
  const [invalid, setInvalidState] = useState<Record<string, string>>({});
  const [operationError, setOperationError] = useState("");
  const backend = useBaseUrlVersion();
  useEffect(() => {
    let active = true;
    Promise.allSettled([collection<Entity>("/v1/entities"), collection<Automation>("/v1/automations"), collection<NotificationDestination>("/v1/notification-destinations")]).then(([entities, automations, destinations]) => {
      if (!active) return;
      setResources({ entities: entities.status === "fulfilled" ? entities.value : [], automations: automations.status === "fulfilled" ? automations.value : [], destinations: destinations.status === "fulfilled" ? destinations.value : [] });
      const errors = [entities, automations, destinations].filter(result => result.status === "rejected").map(result => result.status === "rejected" ? String(result.reason) : "");
      setLoadError(errors.join("; "));
    });
    return () => { active = false; };
  }, [backend]);
  const issues = automationDocumentIssues(document);
  useEffect(() => { onValidityChange?.(Object.keys(invalid).length === 0 && issues.length === 0 && !operationError); }, [invalid, document, onValidityChange, operationError]);
  const setInvalid = (key: string, value: string | null) => setInvalidState(previous => { const next = { ...previous }; if (value === null) delete next[key]; else next[key] = value; return next; });
  const apply = (path: JsonPath, raw: string | undefined) => { if (path.length && Object.keys(invalid).some(key => { const other = JSON.parse(key) as JsonPath; return path.length <= other.length && path.every((part, i) => part === other[i]); })) { setOperationError("Fix invalid fields before replacing their containing structure."); return; } try { onChange(editAutomationDocument(document, path, raw)); setOperationError(""); } catch (error) { setOperationError(error instanceof Error ? error.message : "Invalid change."); } };
  const sequence = (path: JsonPath, update: (children: string[]) => string[]) => { if (Object.keys(invalid).length) { setOperationError("Fix invalid fields before changing a sequence."); return; } try { onChange(sequenceDocument(document, path, update)); setOperationError(""); } catch (error) { setOperationError(error instanceof Error ? error.message : "Invalid sequence."); } };
  const ctx: Context = { document, resources, apply, sequence, invalid, setInvalid };
  let valid: AutomationDocument;
  try { valid = readAutomationDocument(document.source); } catch (error) { return <section className="automation-editor" role="alert">This Automation cannot be edited: {error instanceof Error ? error.message : "Invalid definition"}</section>; }
  return <section className="automation-editor" aria-label="Automation editor"><p>Changes remain a draft until you save. Edit each path in order.</p>
    {loadError && <p role="alert">Could not load resources: {loadError}. Existing references remain unchanged.</p>}
    {operationError && <p role="alert">Change not applied: {operationError}</p>}
    {issues.map(issue => <p role="alert" key={`${issue.path}-${issue.message}`}>{issue.path}: {issue.message}</p>)}
    <Field ctx={ctx} path={["name"]} label="Name"/><label className="automation-check"><input type="checkbox" checked={valid.definition.enabled} onChange={event => apply(["enabled"], String(event.target.checked))}/> Enable automatic Runs</label>
    <TriggerEditor ctx={ctx}/><section><h4>Admission Conditions</h4><p>These Conditions are evaluated before an automatic Run starts.</p>{valid.definition.conditions ? <><Condition ctx={ctx} value={valid.definition.conditions} path={["conditions"]} reached={false}/><button onClick={() => apply(["conditions"], undefined)}>Remove admission Condition</button></> : <button disabled={!firstEntity(ctx)} onClick={() => { const source = conditionSource(ctx, "entity_state", newDocumentID(document, "condition")); if (source) apply(["conditions"], source); }}>Add admission Condition</button>}</section>
    <SequenceEditor ctx={ctx} path={["steps"]} steps={valid.definition.steps} title="Steps"/>
    <details className="automation-advanced"><summary>Advanced · exact JSON definition</summary><p>Changes are validated against the canonical definition. Exact values are retained.</p><Field ctx={ctx} path={[]} label="Exact JSON definition" raw multiline/></details>
  </section>;
}
