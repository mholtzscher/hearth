import { useEffect, useState } from "react";
import { useBaseUrlVersion } from "../../api/client";
import { editJsonSource, type JsonPath } from "../../api/automation-json";
import type { Automation, AutomationStep, Entity } from "../../api/types";
import type { NotificationDestination } from "../../api/notifications";
import { automationDocumentIssues, documentValue, editAutomationDocument, newDocumentID, readAutomationDocument, sequenceDocument, type AutomationDocument } from "./document";
import { collection, discoverFunctions, type AdapterFunction } from "./resources";
import { getOperationParametersSchema } from "./operation-parameters";
import { Condition, Field, Select, StateGuide, conditionSource, entityOptions, firstEntity, q, type Context, type Resources } from "./ConditionEditor";
import "./automations.css";

type EditorProps = { document: AutomationDocument; onChange: (document: AutomationDocument) => void; onValidityChange?: (valid: boolean) => void };

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
function supportedOperations(entity: Entity): string[] {
  const operations = entity.support.operations;
  return operations && typeof operations === "object" && !Array.isArray(operations) ? Object.keys(operations) : [];
}
function SchemaGuide({ schema, title }: { schema?: Record<string, unknown>; title: string }) {
  return <details className="automation-guide"><summary>{title}</summary>{schema ? <pre>{JSON.stringify(schema, null, 2)}</pre> : <p>No schema is available for this selection. Core validates the current support at Save.</p>}</details>;
}
function CommandEditor({ ctx, path, step }: { ctx: Context; path: JsonPath; step: Extract<AutomationStep, { kind: "command" }> }) {
  const eligible = ctx.resources.entities.filter(entity => supportedOperations(entity).length > 0);
  const selected = ctx.resources.entities.find(entity => entity.id === step.entity_id);
  const operations = selected ? supportedOperations(selected) : [];
  const changeEntity = (id: string) => {
    const entity = eligible.find(item => item.id === id);
    if (!entity) return;
    const available = supportedOperations(entity);
    const source = editJsonSource(documentValue(ctx.document, path), ["entity_id"], q(id));
    ctx.apply(path, available.includes(step.operation) ? source : editJsonSource(source, ["operation"], q(available[0])));
  };
  return <><label className="automation-field"><span>Entity</span><select value={step.entity_id} onChange={event => changeEntity(event.target.value)}>{!eligible.some(entity => entity.id === step.entity_id) && <option value={step.entity_id} disabled>{step.entity_id} · unavailable or no supported Operations</option>}{eligible.map(entity => <option key={entity.id} value={entity.id}>{entity.name} · {entity.id}</option>)}</select></label>
    <label className="automation-field"><span>Supported Operation</span><select value={step.operation} onChange={event => ctx.apply([...path, "operation"], q(event.target.value))}>{!operations.includes(step.operation) && <option value={step.operation} disabled>{step.operation} · not currently supported</option>}{operations.map(operation => <option key={operation} value={operation}>{operation}</option>)}</select></label>
    <SchemaGuide title="Operation parameter schema · canonical units" schema={selected && getOperationParametersSchema(selected.type, step.operation)}/>
    <Binding ctx={ctx} path={path} name="parameters" label="Parameters"/>
  </>;
}
function newStep(ctx: Context, kind: string, selectedFunction?: AdapterFunction): string | undefined {
  const id = newDocumentID(ctx.document, "step"), entity = firstEntity(ctx);
  if (kind === "delay") return q({ id, kind, duration_ms: 1000 });
  if (kind === "command") { const target = ctx.resources.entities.find(e => supportedOperations(e).length); const operation = target && supportedOperations(target)[0]; return target && operation ? q({ id, kind, entity_id: target.id, operation, parameters: {} }) : undefined; }
  if (kind === "function") return selectedFunction ? q({ id, kind, adapter_id: selectedFunction.adapter_id, function_key: selectedFunction.function_key, contract_fingerprint: selectedFunction.contract_fingerprint, arguments: {} }) : undefined;
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
  const [functionKey, setFunctionKey] = useState("");
  const selectedFunction = ctx.resources.functions.find(item => JSON.stringify([item.adapter_id, item.function_key]) === functionKey) ?? ctx.resources.functions[0];
  return <section className="automation-sequence-editor"><h4>{title}</h4><ol className="automation-sequence">{steps.map((step, index) => <li key={step.id}><details className="automation-step-editor" data-automation-step-id={step.id} open><summary>{step.kind.replace(/_/g, " ")} · {step.id}</summary><StepEditor ctx={ctx} path={[...path, index]} step={step}/><div className="automation-actions"><button disabled={index === 0} onClick={() => ctx.sequence(path, children => { [children[index - 1], children[index]] = [children[index], children[index - 1]]; return children; })}>Move up</button><button disabled={index === steps.length - 1} onClick={() => ctx.sequence(path, children => { [children[index + 1], children[index]] = [children[index], children[index + 1]]; return children; })}>Move down</button><button disabled={steps.length === 1} onClick={() => ctx.sequence(path, children => children.filter((_, i) => i !== index))}>Remove Step</button></div></details></li>)}</ol><div className="automation-actions"><label>Add Step <select value={kind} onChange={event => setKind(event.target.value)}>{["delay", "command", "notification", "sub_automation", "if", "choose", "function", "read_state", "set_variables"].map(k => <option key={k} value={k}>{k.replace(/_/g, " ")}</option>)}</select></label>{kind === "function" && <label>Function <select value={selectedFunction ? JSON.stringify([selectedFunction.adapter_id, selectedFunction.function_key]) : ""} onChange={event => setFunctionKey(event.target.value)}>{!selectedFunction && <option value="">No Functions available</option>}{ctx.resources.functions.map(item => <option key={JSON.stringify([item.adapter_id, item.function_key])} value={JSON.stringify([item.adapter_id, item.function_key])}>{item.adapter_id} / {item.function_key}{item.callable ? "" : " · offline"}</option>)}</select></label>}<button disabled={!newStep(ctx, kind, selectedFunction)} onClick={() => { const source = newStep(ctx, kind, selectedFunction); if (source) ctx.sequence(path, children => [...children, source]); }}>Add to {title}</button></div></section>;
}
function StepEditor({ ctx, path, step }: { ctx: Context; path: JsonPath; step: AutomationStep }) {
  const p = (field: string): JsonPath => [...path, field];
  const delay = () => q({ id: newDocumentID(ctx.document, "wait"), kind: "delay", duration_ms: 1000 });
  return <div className="automation-fields">
    {step.kind === "delay" && <Field ctx={ctx} path={p("duration_ms")} label="Wait · milliseconds" raw/>}
    {step.kind === "command" && <CommandEditor ctx={ctx} path={path} step={step}/>}
    {step.kind === "notification" && <><Select ctx={ctx} path={p("destination_id")} label="Destination" options={ctx.resources.destinations.map(d => ({ value: d.id, label: `${d.name} · ${d.id}` }))}/><Binding ctx={ctx} path={path} name="title" label="Title"/><Binding ctx={ctx} path={path} name="message" label="Message"/><p>Provider acceptance does not prove delivery.</p></>}
    {step.kind === "sub_automation" && <Select ctx={ctx} path={p("automation_id")} label="Automation" options={ctx.resources.automations.map(a => ({ value: a.id, label: `${a.definition.name} · ${a.id}` }))}/>}
    {step.kind === "if" && <><Condition ctx={ctx} value={step.conditions} path={p("conditions")} reached/><SequenceEditor ctx={ctx} path={p("then")} steps={step.then} title="Then"/>{step.else ? <><SequenceEditor ctx={ctx} path={p("else")} steps={step.else} title="Else"/><button onClick={() => ctx.apply(p("else"), undefined)}>Remove Else</button></> : <button onClick={() => ctx.apply(p("else"), `[${delay()}]`)}>Add Else</button>}</>}
    {step.kind === "choose" && <>{step.branches.map((branch, index) => <details className="automation-arm" open key={branch.id}><summary>Path {index + 1} · {branch.id}</summary><Condition ctx={ctx} value={branch.conditions} path={[...p("branches"), index, "conditions"]} reached/><SequenceEditor ctx={ctx} path={[...p("branches"), index, "steps"]} steps={branch.steps} title="Path Steps"/><div className="automation-actions"><button disabled={index === 0} onClick={() => ctx.sequence(p("branches"), children => { [children[index - 1], children[index]] = [children[index], children[index - 1]]; return children; })}>Move path up</button><button disabled={index === step.branches.length - 1} onClick={() => ctx.sequence(p("branches"), children => { [children[index + 1], children[index]] = [children[index], children[index + 1]]; return children; })}>Move path down</button><button disabled={step.branches.length === 1} onClick={() => ctx.sequence(p("branches"), children => children.filter((_, i) => i !== index))}>Remove path</button></div></details>)}<button onClick={() => { const branch = `{"id":${q(newDocumentID(ctx.document, "branch"))},"conditions":{"id":${q(newDocumentID(ctx.document, "condition"))},"kind":"expression","expr":"true"},"steps":[${delay()}]}`; ctx.sequence(p("branches"), children => [...children, branch]); }}>Add path</button>{step.default ? <><SequenceEditor ctx={ctx} path={p("default")} steps={step.default} title="Default"/><button onClick={() => ctx.apply(p("default"), undefined)}>Remove Default</button></> : <button onClick={() => ctx.apply(p("default"), `[${delay()}]`)}>Add Default</button>}</>}
    {step.kind === "function" && <SchemaGuide title="Function argument schema" schema={ctx.resources.functions.find(item => item.adapter_id === step.adapter_id && item.function_key === step.function_key && item.contract_fingerprint === step.contract_fingerprint)?.input_schema}/>}
    {(step.kind === "function" || step.kind === "read_state" || step.kind === "set_variables") && <Field ctx={ctx} path={path} label="Exact JSON Step inspector" raw multiline/>}
  </div>;
}
function Comparisons({ ctx, path, values, label, entityId, required = false }: { ctx: Context; path: JsonPath; values: { value_pointer: string; operator: string; operand: unknown }[] | undefined; label: string; entityId: string; required?: boolean }) {
  const comparison = '{"value_pointer":"","operator":"eq","operand":true}';
  return <section className="automation-condition"><h4>{label}</h4><StateGuide ctx={ctx} entityId={entityId}/>{values?.map((_, index) => <div className="automation-fields" key={index}>
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
      {(trigger.kind === "held_state" || trigger.kind === "observation") && <><Comparisons ctx={ctx} path={[...path, "comparisons"]} values={trigger.comparisons} label="Current value comparisons" entityId={trigger.entity_id} required={trigger.kind === "held_state"}/>{trigger.kind === "observation" && <Comparisons ctx={ctx} path={[...path, "previous_comparisons"]} values={trigger.previous_comparisons} label="Previous value comparisons" entityId={trigger.entity_id}/>}</>}
      <button onClick={() => ctx.sequence(["triggers"], children => children.filter((_, i) => i !== index))}>Remove Trigger</button></div></details>;
  })}<div className="automation-actions"><label>Trigger type <select value={kind} onChange={event => setKind(event.target.value)}>{["cron", "observation", "entity_event", "held_state"].map(value => <option key={value} value={value}>{value.replace(/_/g, " ")}</option>)}</select></label><button disabled={kind !== "cron" && !firstEntity(ctx)} onClick={add}>Add Trigger</button></div></section>;
}
export function AutomationEditor({ document, onChange, onValidityChange }: EditorProps) {
  const [resources, setResources] = useState<Resources>({ entities: [], automations: [], destinations: [], functions: [] });
  const [loadError, setLoadError] = useState("");
  const [invalid, setInvalidState] = useState<Record<string, string>>({});
  const [operationError, setOperationError] = useState("");
  const backend = useBaseUrlVersion();
  useEffect(() => {
    let active = true;
    setResources({ entities: [], automations: [], destinations: [], functions: [] });
    Promise.allSettled([collection<Entity>("/v1/entities"), collection<Automation>("/v1/automations"), collection<NotificationDestination>("/v1/notification-destinations"), discoverFunctions()]).then(([entities, automations, destinations, functions]) => {
      if (!active) return;
      setResources({ entities: entities.status === "fulfilled" ? entities.value : [], automations: automations.status === "fulfilled" ? automations.value : [], destinations: destinations.status === "fulfilled" ? destinations.value : [], functions: functions.status === "fulfilled" ? functions.value.functions : [] });
      const errors = [entities, automations, destinations, functions].filter(result => result.status === "rejected").map(result => result.status === "rejected" ? String(result.reason) : "");
      if (functions.status === "fulfilled") errors.push(...functions.value.errors);
      setLoadError(errors.join("; "));
    });
    return () => { active = false; };
  }, [backend]);
  let valid: AutomationDocument | undefined;
  let validationError = "Invalid definition";
  try { valid = readAutomationDocument(document.source); } catch (error) { validationError = error instanceof Error ? error.message : validationError; }
  const issues = valid ? automationDocumentIssues(valid) : [];
  useEffect(() => { onValidityChange?.(!!valid && Object.keys(invalid).length === 0 && issues.length === 0 && !operationError); }, [invalid, document, onValidityChange, operationError]);
  const setInvalid = (key: string, value: string | null) => setInvalidState(previous => { const next = { ...previous }; if (value === null) delete next[key]; else next[key] = value; return next; });
  const apply = (path: JsonPath, raw: string | undefined) => { if (path.length && Object.keys(invalid).some(key => { const other = JSON.parse(key) as JsonPath; return path.length <= other.length && path.every((part, i) => part === other[i]); })) { setOperationError("Fix invalid fields before replacing their containing structure."); return; } try { onChange(editAutomationDocument(document, path, raw)); setOperationError(""); } catch (error) { setOperationError(error instanceof Error ? error.message : "Invalid change."); } };
  const sequence = (path: JsonPath, update: (children: string[]) => string[]) => { if (Object.keys(invalid).length) { setOperationError("Fix invalid fields before changing a sequence."); return; } try { onChange(sequenceDocument(document, path, update)); setOperationError(""); } catch (error) { setOperationError(error instanceof Error ? error.message : "Invalid sequence."); } };
  const ctx: Context = { document: valid ?? document, resources, apply, sequence, invalid, setInvalid };
  if (!valid) return <section className="automation-editor" role="alert">This Automation cannot be edited: {validationError}</section>;
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
