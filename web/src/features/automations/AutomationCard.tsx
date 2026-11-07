import { useEffect, useRef, useState, type ReactNode } from "react";
import { ApiError } from "../../api/client";
import { automationJsonSource } from "../../api/automation-json";
import { getAutomation } from "../../api/automations";
import type { AutomationBranchCondition, AutomationStep, AutomationTrigger } from "../../api/types";
import { documentValue, readAutomationDocument, type AutomationDocument, type AutomationReadStyle } from "./document";
import "./automations.css";

type Props = { document: AutomationDocument; onEdit?: () => void; children?: ReactNode; automationId?: string; compact?: boolean };

function conditionText(condition: AutomationBranchCondition): string {
  switch (condition.kind) {
    case "entity_state": return `${condition.entity_id} ${condition.value_pointer || "value"} ${condition.operator} operand (see exact definition)`;
    case "expression": return `Expression: ${condition.expr}`;
    case "trigger": return `Trigger: ${condition.trigger_ids.join(", ")}`;
    case "not": return `Not (${conditionText(condition.child)})`;
    case "all": case "any": return `${condition.kind === "all" ? "All" : "Any"}: ${condition.children.map(conditionText).join("; ")}`;
  }
}
function triggerText(trigger: AutomationTrigger): string {
  switch (trigger.kind) {
    case "cron": return `Schedule ${trigger.expression}`;
    case "entity_event": return `${trigger.entity_id}: ${trigger.event_name}`;
    case "held_state": return `${trigger.entity_id} held for ${trigger.for_seconds} seconds`;
    case "observation": return `${trigger.entity_id} observation (${trigger.dispositions.join(", ")})`;
  }
}
function stepText(step: AutomationStep): string {
  switch (step.kind) {
    case "command": return `${step.operation} · ${step.entity_id}`;
    case "notification": return `Notify ${step.destination_id}`;
    case "delay": return `Wait ${step.duration_ms} ms`;
    case "sub_automation": return `Run ${step.automation_id}`;
    case "if": return `If ${conditionText(step.conditions)}`;
    case "choose": return "Choose the first matching path";
    case "function": return `Function ${step.adapter_id} / ${step.function_key}`;
    case "read_state": return `Read State ${step.entity_id} ${step.value_pointer}`;
    case "set_variables": return "Set variables";
  }
}

function Sequence({ steps, style, inspect }: { steps: AutomationStep[]; style: AutomationReadStyle; inspect: (id: string, button: HTMLButtonElement) => void }) {
  return <ol className={`automation-sequence automation-${style}`}>{steps.map(step => <li key={step.id}>
    <div className="automation-step"><span className="automation-kind">{step.kind.replace(/_/g, " ")}</span>
      {step.kind === "sub_automation" ? <button className="automation-link" onClick={event => inspect(step.automation_id, event.currentTarget)}>{stepText(step)}</button> : <span>{stepText(step)}</span>}
      <small>{step.id}</small></div>
    {step.kind === "if" && <><Arm title="Then" steps={step.then} style={style} inspect={inspect}/>{step.else && <Arm title="Else" steps={step.else} style={style} inspect={inspect}/>}</>}
    {step.kind === "choose" && <>{step.branches.map((branch, index) => <Arm key={branch.id} title={`${index + 1}. ${conditionText(branch.conditions)}`} steps={branch.steps} style={style} inspect={inspect}/>)}{step.default && <Arm title="Default" steps={step.default} style={style} inspect={inspect}/>}</>}
  </li>)}</ol>;
}
function Arm({ title, steps, style, inspect }: { title: string; steps: AutomationStep[]; style: AutomationReadStyle; inspect: (id: string, button: HTMLButtonElement) => void }) {
  return <details className="automation-arm" open><summary>{title} <small>{steps.length} {steps.length === 1 ? "Step" : "Steps"}</small></summary><Sequence steps={steps} style={style} inspect={inspect}/></details>;
}

function ChildPreview({ id, close, ancestry }: { id: string; close: () => void; ancestry: string[] }) {
  const [state, setState] = useState<{ status: "loading" } | { status: "error"; message: string } | { status: "ready"; document: AutomationDocument }>({ status: "loading" });
  useEffect(() => {
    const controller = new AbortController();
    getAutomation(id, controller.signal).then(value => setState({ status: "ready", document: readAutomationDocument(automationJsonSource(value.definition)) })).catch(error => {
      if (!controller.signal.aborted) setState({ status: "error", message: error instanceof ApiError && error.status === 404 ? "Automation not found. It may have been removed." : `Could not load Automation: ${error instanceof Error ? error.message : "Unknown error"}` });
    });
    return () => controller.abort();
  }, [id]);
  return <section className="automation-child" aria-label="Referenced Automation"><div className="automation-card-heading"><div><h4>Referenced Automation</h4><p>Current definition · read-only</p></div><button autoFocus onClick={close}>Close preview</button></div>
    {state.status === "loading" && <p role="status">Loading {id}…</p>}
    {state.status === "error" && <p role="alert">{state.message}</p>}
    {state.status === "ready" && <><h4>{state.document.definition.name}</h4><p>{id}</p><CardContent document={state.document} ancestry={[...ancestry, id]}/></>}
  </section>;
}
function CardContent({ document, ancestry }: { document: AutomationDocument; ancestry: string[] }) {
  const [style, setStyle] = useState<AutomationReadStyle>("sentence");
  const [child, setChild] = useState<string | null>(null);
  const opener = useRef<HTMLButtonElement | null>(null);
  const inspect = (id: string, button: HTMLButtonElement) => { opener.current = button; setChild(id); };
  const close = () => { setChild(null); requestAnimationFrame(() => { const target = opener.current; if (!target) return; for (let parent = target.parentElement; parent; parent = parent.parentElement) if (parent instanceof HTMLDetailsElement) parent.open = true; target.focus(); }); };
  const definition = document.definition;
  return <><div className="automation-admission"><span className="automation-kind">When</span><p>{definition.triggers.length ? definition.triggers.map(triggerText).join(" · ") : "Manual only · no automatic Triggers"}</p>{definition.conditions && <><span className="automation-kind">Only if</span><p>{conditionText(definition.conditions)}</p></>}</div>
    <div className="automation-view-options"><span>{definition.steps.length} root {definition.steps.length === 1 ? "Step" : "Steps"}</span><div aria-label="Read style" role="group"><button aria-pressed={style === "sentence"} onClick={() => setStyle("sentence")}>Sentence</button><button aria-pressed={style === "steps"} onClick={() => setStyle("steps")}>Steps</button></div></div>
    <Sequence steps={definition.steps} style={style} inspect={inspect}/>
    <details className="automation-advanced"><summary>Exact definition</summary><pre>{document.source}</pre></details>
    {child && (ancestry.includes(child) || ancestry.length >= 8 ? <section className="automation-child"><p>Preview unavailable: recursive or deeply nested reference.</p><button onClick={close}>Close preview</button></section> : <ChildPreview key={child} id={child} close={close} ancestry={ancestry}/>)}
  </>;
}
export function AutomationCard({ document, onEdit, children, automationId, compact = false }: Props) {
  // Do not trust structurally constructed objects supplied by a caller.
  let valid: AutomationDocument;
  try { valid = readAutomationDocument(document.source); } catch (error) { return <section className="automation-card" role="alert">This Automation cannot be shown: {error instanceof Error ? error.message : "Invalid definition"}</section>; }
  return <article className={`automation-card${compact ? " automation-card-compact" : ""}`}><header className="automation-card-heading"><div><h3>{valid.definition.name}</h3><p>{valid.definition.enabled ? "Automatic Runs enabled" : "Automatic Runs disabled"}{automationId ? ` · ${automationId}` : ""}</p></div>{onEdit && <button onClick={onEdit}>Edit Automation</button>}</header><CardContent document={valid} ancestry={automationId ? [automationId] : []}/>{children}</article>;
}

// Used by the editor when showing literal operands without rounding.
export function exactValue(document: AutomationDocument, path: readonly (string | number)[]): string { return documentValue(document, path); }
