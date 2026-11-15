import { Fragment, type ReactNode } from "react";
import { Link } from "react-router-dom";
import type { JsonPath } from "../../api/automation-json";
import type { AutomationBranchCondition, AutomationComparison, AutomationStep, AutomationTrigger } from "../../api/types";
import { ExpressionCode, JsonCode } from "./AutomationCode";
import { documentValue, type AutomationDocument } from "./document";
import { Button } from "../../components/ui/button";

const note = "block text-caption leading-normal text-muted-foreground";
const paragraph = "my-[5px] text-[13px] text-muted-foreground";

/** Same-named Entities, such as several "Power" Entities, are told apart by their Device. */
export const entityLabelText = (name: string, device?: string) => device ? `${name} (${device})` : name;

/** Display names for referenced resources; undefined while unknown, so the reference shows its ID. */
export type ReadContext = {
  document: AutomationDocument;
  entityLabel: (id: string) => string | undefined;
  destinationName: (id: string) => string | undefined;
  childName: (id: string) => string | undefined;
  childMissing: (id: string) => boolean;
  inspect: (id: string, button: HTMLButtonElement) => void;
};
const operator: Record<string, string> = { eq: "equals", ne: "is not", lt: "is below", lte: "is at most", gt: "is above", gte: "is at least" };
const exact = (ctx: ReadContext, path: JsonPath) => <JsonCode source={documentValue(ctx.document, path)}/>;
const join = (nodes: ReactNode[], separator: string) => nodes.map((node, index) => <Fragment key={index}>{index > 0 && separator}{node}</Fragment>);

/** An Entity reads as its name and Device; the ID stays in the link's tooltip and target. */
function EntityRef({ ctx, id }: { ctx: ReadContext; id: string }) {
  return <Link className="text-link underline underline-offset-[3px]" to={`/entities/${encodeURIComponent(id)}`} title={id}>{ctx.entityLabel(id) ?? id}</Link>;
}
function Named({ id, name }: { id: string; name: string | undefined }) {
  return <span title={id}>{name ?? id}</span>;
}
function comparisonText(ctx: ReadContext, value: AutomationComparison, path: JsonPath): ReactNode {
  return <>{value.value_pointer || "value"} {operator[value.operator]} {exact(ctx, [...path, "operand"])}</>;
}
export function ConditionText({ ctx, condition, path }: { ctx: ReadContext; condition: AutomationBranchCondition; path: JsonPath }): ReactNode {
  switch (condition.kind) {
    case "entity_state": return <><EntityRef ctx={ctx} id={condition.entity_id}/> {comparisonText(ctx, condition, path)}{condition.max_age_seconds !== undefined ? ` (max age ${condition.max_age_seconds} seconds)` : ""}</>;
    case "expression": return <ExpressionCode source={condition.expr}/>;
    case "trigger": return <>Trigger {condition.trigger_ids.join(", ")}</>;
    case "not": return condition.child.kind === "any" ? <>None: {join(condition.child.children.map((child, index) => <ConditionText ctx={ctx} condition={child} path={[...path, "child", "children", index]}/>), "; ")}</> : <>Not (<ConditionText ctx={ctx} condition={condition.child} path={[...path, "child"]}/>)</>;
    case "all": case "any": return <>{condition.kind === "all" ? "All" : "Any"}: {join(condition.children.map((child, index) => <ConditionText ctx={ctx} condition={child} path={[...path, "children", index]}/>), "; ")}</>;
  }
}
export function TriggerText({ ctx, trigger, path }: { ctx: ReadContext; trigger: AutomationTrigger; path: JsonPath }): ReactNode {
  switch (trigger.kind) {
    case "cron": return <>schedule {trigger.expression} (household timezone)</>;
    case "entity_event": return <><EntityRef ctx={ctx} id={trigger.entity_id}/> emits {trigger.event_name}</>;
    case "held_state": return <><EntityRef ctx={ctx} id={trigger.entity_id}/> {join(trigger.comparisons.map((value, index) => comparisonText(ctx, value, [...path, "comparisons", index])), " and ")} for {trigger.for_seconds} seconds</>;
    case "observation": {
      const current = trigger.comparisons?.map((value, index) => comparisonText(ctx, value, [...path, "comparisons", index])) ?? [];
      const previous = trigger.previous_comparisons?.map((value, index) => comparisonText(ctx, value, [...path, "previous_comparisons", index])) ?? [];
      return <><EntityRef ctx={ctx} id={trigger.entity_id}/> has an {trigger.dispositions.join(" or ")} observation{current.length > 0 && <> where {join(current, " and ")}</>}{previous.length > 0 && <>; previous {join(previous, " and ")}</>}</>;
    }
  }
}
function binding(ctx: ReadContext, path: JsonPath, literal: string, expression?: string): ReactNode {
  return expression === undefined ? exact(ctx, [...path, literal]) : <ExpressionCode source={expression}/>;
}
function assignments(ctx: ReadContext, path: JsonPath, literals?: Record<string, unknown>, expressions?: Record<string, string>): ReactNode {
  return join([...Object.keys(literals ?? {}).map(name => <>{name} = {exact(ctx, [...path, "assignments", name])}</>), ...Object.entries(expressions ?? {}).map(([name, value]) => <>{name} = <ExpressionCode source={value}/></>)], "; ");
}
function duration(ms: number): string {
  if (ms % 60000 === 0) return `${ms / 60000} ${ms === 60000 ? "minute" : "minutes"}`;
  if (ms % 1000 === 0) return `${ms / 1000} ${ms === 1000 ? "second" : "seconds"}`;
  return `${ms} milliseconds`;
}
function StepCopy({ ctx, step, path }: { ctx: ReadContext; step: AutomationStep; path: JsonPath }) {
  switch (step.kind) {
    case "command": return <>{step.operation} <EntityRef ctx={ctx} id={step.entity_id}/> · parameters {binding(ctx, path, "parameters", step.parameters_expr)}</>;
    case "notification": return <>Notify <Named id={step.destination_id} name={ctx.destinationName(step.destination_id)}/> · title {binding(ctx, path, "title", step.title_expr)} · message {binding(ctx, path, "message", step.message_expr)}</>;
    case "delay": return <>Wait {duration(step.duration_ms)}</>;
    case "sub_automation": return <>Run <Named id={step.automation_id} name={ctx.childName(step.automation_id)}/></>;
    case "if": return <>If <ConditionText ctx={ctx} condition={step.conditions} path={[...path, "conditions"]}/></>;
    case "choose": return <>Choose the first matching path</>;
    case "function": return <>Call Function {step.adapter_id} / {step.function_key} · arguments {binding(ctx, path, "arguments", step.arguments_expr)}</>;
    case "read_state": return <>Read State <EntityRef ctx={ctx} id={step.entity_id}/> · {step.value_pointer || "root value"}{step.max_age_seconds !== undefined ? ` · max age ${step.max_age_seconds} seconds` : ""} · on unusable: {step.on_unusable ?? "fail"}</>;
    case "set_variables": return <>Set variables: {assignments(ctx, path, step.assignments, step.assignments_expr)}</>;
  }
}
/** Qualifying notes and actions follow the Step's own sentence. */
function StepAside({ ctx, step }: { ctx: ReadContext; step: AutomationStep }) {
  if (step.kind === "notification") return <small className={`${note} mt-0.5`}>Provider acceptance is not proof of delivery.</small>;
  if (step.kind === "sub_automation") return <><small className={`${note} mt-0.5`}>{ctx.childMissing(step.automation_id) ? "Automation not found. It may have been removed." : "Waits for child success."}</small><Button variant="link" size="default" className="h-auto min-h-8 p-0 text-left text-violet-text max-tablet:min-h-11" aria-label={`Inspect child ${ctx.childName(step.automation_id) ?? step.automation_id}`} onClick={event => ctx.inspect(step.automation_id, event.currentTarget)}>Inspect child</Button></>;
  return null;
}
function Branches({ ctx, step, path, mode }: { ctx: ReadContext; step: AutomationStep; path: JsonPath; mode: "sentence" | "steps" }) {
  if (step.kind === "if") return <><Arm ctx={ctx} title="Then · true" path={[...path, "then"]} steps={step.then} mode={mode}/>{step.else ? <Arm ctx={ctx} title="Else · false" path={[...path, "else"]} steps={step.else} mode={mode}/> : <p className={mode === "sentence" ? "m-0 text-body leading-[1.55] text-foreground" : `${paragraph} ml-7 max-tablet:ml-6`}>No Else; continue when false.</p>}</>;
  if (step.kind === "choose") return <>{step.branches.map((branch, index) => <Arm key={branch.id} ctx={ctx} title={<>Path {index + 1} · <ConditionText ctx={ctx} condition={branch.conditions} path={[...path, "branches", index, "conditions"]}/></>} path={[...path, "branches", index, "steps"]} steps={branch.steps} mode={mode}/>)}{step.default && <Arm ctx={ctx} title="Default" path={[...path, "default"]} steps={step.default} mode={mode}/>}</>;
  return null;
}
function Arm({ ctx, title, steps, path, mode }: { ctx: ReadContext; title: ReactNode; steps: AutomationStep[]; path: JsonPath; mode: "sentence" | "steps" }) {
  return <details className="my-2 min-w-0 border-l border-gray-5 pl-3 max-tablet:pl-[9px] group-data-[stack=true]/sequence:ml-7 max-tablet:group-data-[stack=true]/sequence:ml-6" open><summary className="min-h-9 cursor-pointer py-2 marker:text-gray-7">{title}<small className="text-caption text-muted-foreground"> · {steps.length} {steps.length === 1 ? "Step" : "Steps"}</small></summary>{mode === "sentence" ? <SentenceSequence ctx={ctx} steps={steps} path={path}/> : <StackSequence ctx={ctx} steps={steps} path={path}/>}</details>;
}
/** Prose is an outline of sentences; branches remain explicitly owned paths. */
export function SentenceSequence({ ctx, steps, path }: { ctx: ReadContext; steps: AutomationStep[]; path: JsonPath }) {
  return <div aria-label="Sentence sequence">{steps.map((step, index) => <div className="py-2" key={step.id}><p className="m-0 text-body leading-[1.55] text-foreground"><StepCopy ctx={ctx} step={step} path={[...path, index]}/>{step.kind !== "sub_automation" && step.kind !== "if" && step.kind !== "choose" ? "." : ""}</p><StepAside ctx={ctx} step={step}/><Branches ctx={ctx} step={step} path={[...path, index]} mode="sentence"/></div>)}</div>;
}
/** Numbering restarts inside each owned sequence. */
export function StackSequence({ ctx, steps, path }: { ctx: ReadContext; steps: AutomationStep[]; path: JsonPath }) {
  return <ol className="m-0 list-none p-0" aria-label="Step stack sequence">{steps.map((step, index) => <li key={step.id} data-stack="true" className="group/sequence min-w-0 py-2.5 not-first:border-t not-first:border-border"><div className="grid grid-cols-[18px_minmax(0,1fr)] items-start gap-x-2.5 max-tablet:grid-cols-[14px_minmax(0,1fr)]"><span className="text-right text-caption leading-normal text-muted-foreground tabular-nums" aria-hidden="true">{index + 1}</span><div><span className={note}><span className="capitalize">{step.kind.replace(/_/g, " ")}</span> · <code className="text-caption">{step.id}</code></span><div className="mt-0.5 text-label leading-[1.55]"><StepCopy ctx={ctx} step={step} path={[...path, index]}/></div><StepAside ctx={ctx} step={step}/></div></div><Branches ctx={ctx} step={step} path={[...path, index]} mode="steps"/></li>)}</ol>;
}
export function InitialVariables({ ctx }: { ctx: ReadContext }) {
  const definition = ctx.document.definition;
  if (!definition.variables && !definition.variables_expr) return null;
  return <p className={paragraph}>Initial variables: {join([...Object.keys(definition.variables ?? {}).map(name => <>{name} = {exact(ctx, ["variables", name])}</>), ...Object.entries(definition.variables_expr ?? {}).map(([name, source]) => <>{name} = <ExpressionCode source={source}/></>)], "; ")}</p>;
}
