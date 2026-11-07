import type { JsonPath } from "../../api/automation-json";
import type { AutomationBranchCondition, AutomationComparison, AutomationStep, AutomationTrigger } from "../../api/types";
import { documentValue, type AutomationDocument } from "./document";

export type ReadContext = {
  document: AutomationDocument;
  entityName: (id: string) => string;
  destinationName: (id: string) => string;
  childName: (id: string) => string;
  inspect: (id: string, button: HTMLButtonElement) => void;
};
const operator: Record<string, string> = { eq: "equals", ne: "is not", lt: "is below", lte: "is at most", gt: "is above", gte: "is at least" };
const exact = (ctx: ReadContext, path: JsonPath) => documentValue(ctx.document, path);
const entity = (ctx: ReadContext, id: string) => `${ctx.entityName(id)} (${id})`;

function comparisonText(ctx: ReadContext, value: AutomationComparison, path: JsonPath): string {
  return `${value.value_pointer || "value"} ${operator[value.operator]} ${exact(ctx, [...path, "operand"])}`;
}
export function conditionText(ctx: ReadContext, condition: AutomationBranchCondition, path: JsonPath): string {
  switch (condition.kind) {
    case "entity_state": return `${entity(ctx, condition.entity_id)} ${comparisonText(ctx, condition, path)}${condition.max_age_seconds !== undefined ? ` (max age ${condition.max_age_seconds} seconds)` : ""}`;
    case "expression": return `expression ${condition.expr}`;
    case "trigger": return `Trigger ${condition.trigger_ids.join(", ")}`;
    case "not": return condition.child.kind === "any" ? `None: ${condition.child.children.map((child, index) => conditionText(ctx, child, [...path, "child", "children", index])).join("; ")}` : `Not (${conditionText(ctx, condition.child, [...path, "child"])})`;
    case "all": case "any": return `${condition.kind === "all" ? "All" : "Any"}: ${condition.children.map((child, index) => conditionText(ctx, child, [...path, "children", index])).join("; ")}`;
  }
}
export function triggerText(ctx: ReadContext, trigger: AutomationTrigger, path: JsonPath): string {
  switch (trigger.kind) {
    case "cron": return `schedule ${trigger.expression} (household timezone)`;
    case "entity_event": return `${entity(ctx, trigger.entity_id)} emits ${trigger.event_name}`;
    case "held_state": return `${entity(ctx, trigger.entity_id)} ${trigger.comparisons.map((value, index) => comparisonText(ctx, value, [...path, "comparisons", index])).join(" and ")} for ${trigger.for_seconds} seconds`;
    case "observation": {
      const current = trigger.comparisons?.map((value, index) => comparisonText(ctx, value, [...path, "comparisons", index])) ?? [];
      const previous = trigger.previous_comparisons?.map((value, index) => comparisonText(ctx, value, [...path, "previous_comparisons", index])) ?? [];
      return `${entity(ctx, trigger.entity_id)} has an ${trigger.dispositions.join(" or ")} observation${current.length ? ` where ${current.join(" and ")}` : ""}${previous.length ? `; previous ${previous.join(" and ")}` : ""}`;
    }
  }
}
function binding(ctx: ReadContext, path: JsonPath, literal: string, expression?: string): string {
  return expression === undefined ? exact(ctx, [...path, literal]) : `expression ${expression}`;
}
function assignments(ctx: ReadContext, path: JsonPath, literals?: Record<string, unknown>, expressions?: Record<string, string>): string {
  return [...Object.keys(literals ?? {}).map(name => `${name} = ${exact(ctx, [...path, "assignments", name])}`), ...Object.entries(expressions ?? {}).map(([name, value]) => `${name} = expression ${value}`)].join("; ");
}
function duration(ms: number): string {
  if (ms % 60000 === 0) return `${ms / 60000} ${ms === 60000 ? "minute" : "minutes"}`;
  if (ms % 1000 === 0) return `${ms / 1000} ${ms === 1000 ? "second" : "seconds"}`;
  return `${ms} milliseconds`;
}
function StepCopy({ ctx, step, path }: { ctx: ReadContext; step: AutomationStep; path: JsonPath }) {
  switch (step.kind) {
    case "command": return <>{step.operation} {entity(ctx, step.entity_id)} · parameters {binding(ctx, path, "parameters", step.parameters_expr)}</>;
    case "notification": return <>Notify {ctx.destinationName(step.destination_id)} ({step.destination_id}) · title {binding(ctx, path, "title", step.title_expr)} · message {binding(ctx, path, "message", step.message_expr)}</>;
    case "delay": return <>Wait {duration(step.duration_ms)}</>;
    case "sub_automation": return <>Run {ctx.childName(step.automation_id)} ({step.automation_id})</>;
    case "if": return <>If {conditionText(ctx, step.conditions, [...path, "conditions"])}</>;
    case "choose": return <>Choose the first matching path</>;
    case "function": return <>Call Function {step.adapter_id} / {step.function_key} · arguments {binding(ctx, path, "arguments", step.arguments_expr)} · contract {step.contract_fingerprint}</>;
    case "read_state": return <>Read State {entity(ctx, step.entity_id)} · {step.value_pointer || "root value"}{step.max_age_seconds !== undefined ? ` · max age ${step.max_age_seconds} seconds` : ""} · on unusable: {step.on_unusable ?? "fail"}</>;
    case "set_variables": return <>Set variables: {assignments(ctx, path, step.assignments, step.assignments_expr)}</>;
  }
}
/** Qualifying notes and actions follow the Step's own sentence. */
function StepAside({ ctx, step }: { ctx: ReadContext; step: AutomationStep }) {
  if (step.kind === "notification") return <small className="automation-step-note">Provider acceptance is not proof of delivery.</small>;
  if (step.kind === "sub_automation") return <><small className="automation-step-note">Waits for child success.</small><button className="automation-link" aria-label={`Inspect ${ctx.childName(step.automation_id)} (${step.automation_id})`} onClick={event => ctx.inspect(step.automation_id, event.currentTarget)}>Inspect child</button></>;
  return null;
}
function Branches({ ctx, step, path, mode }: { ctx: ReadContext; step: AutomationStep; path: JsonPath; mode: "sentence" | "steps" }) {
  if (step.kind === "if") return <><Arm ctx={ctx} title="Then · true" path={[...path, "then"]} steps={step.then} mode={mode}/>{step.else ? <Arm ctx={ctx} title="Else · false" path={[...path, "else"]} steps={step.else} mode={mode}/> : <p className="automation-hint">No Else; continue when false.</p>}</>;
  if (step.kind === "choose") return <>{step.branches.map((branch, index) => <Arm key={branch.id} ctx={ctx} title={`Path ${index + 1} · ${conditionText(ctx, branch.conditions, [...path, "branches", index, "conditions"])}`} path={[...path, "branches", index, "steps"]} steps={branch.steps} mode={mode}/>)}{step.default && <Arm ctx={ctx} title="Default" path={[...path, "default"]} steps={step.default} mode={mode}/>}</>;
  return null;
}
function Arm({ ctx, title, steps, path, mode }: { ctx: ReadContext; title: string; steps: AutomationStep[]; path: JsonPath; mode: "sentence" | "steps" }) {
  return <details className="automation-arm" open><summary>{title}<small> · {steps.length} {steps.length === 1 ? "Step" : "Steps"}</small></summary>{mode === "sentence" ? <SentenceSequence ctx={ctx} steps={steps} path={path}/> : <StackSequence ctx={ctx} steps={steps} path={path}/>}</details>;
}
/** Prose is an outline of sentences; branches remain explicitly owned paths. */
export function SentenceSequence({ ctx, steps, path }: { ctx: ReadContext; steps: AutomationStep[]; path: JsonPath }) {
  return <div className="automation-prose" aria-label="Sentence sequence">{steps.map((step, index) => <div className="automation-prose-step" key={step.id}><p><StepCopy ctx={ctx} step={step} path={[...path, index]}/>{step.kind !== "sub_automation" && step.kind !== "if" && step.kind !== "choose" ? "." : ""}</p><StepAside ctx={ctx} step={step}/><Branches ctx={ctx} step={step} path={[...path, index]} mode="sentence"/></div>)}</div>;
}
/** Numbering restarts inside each owned sequence. */
export function StackSequence({ ctx, steps, path }: { ctx: ReadContext; steps: AutomationStep[]; path: JsonPath }) {
  return <ol className="automation-stack" aria-label="Step stack sequence">{steps.map((step, index) => <li key={step.id}><div className="automation-stack-row"><span className="automation-step-number" aria-hidden="true">{index + 1}</span><div><span className="automation-step-meta"><span className="automation-step-kind">{step.kind.replace(/_/g, " ")}</span> · <code>{step.id}</code></span><div className="automation-step-copy"><StepCopy ctx={ctx} step={step} path={[...path, index]}/></div><StepAside ctx={ctx} step={step}/></div></div><Branches ctx={ctx} step={step} path={[...path, index]} mode="steps"/></li>)}</ol>;
}
export function InitialVariables({ ctx }: { ctx: ReadContext }) {
  const definition = ctx.document.definition;
  if (!definition.variables && !definition.variables_expr) return null;
  return <p className="automation-variables">Initial variables: {[...Object.keys(definition.variables ?? {}).map(name => `${name} = ${exact(ctx, ["variables", name])}`), ...Object.entries(definition.variables_expr ?? {}).map(([name, source]) => `${name} = expression ${source}`)].join("; ")}</p>;
}
