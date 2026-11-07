import { Fragment, useEffect, useRef, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { ApiError, useBaseUrlVersion } from "../../api/client";
import { automationJsonSource } from "../../api/automation-json";
import { getAutomation } from "../../api/automations";
import type { AutomationStep, Device, Entity } from "../../api/types";
import type { NotificationDestination } from "../../api/notifications";
import { readAutomationDocument, type AutomationDocument, type AutomationReadStyle } from "./document";
import { ConditionText, entityLabelText, InitialVariables, SentenceSequence, StackSequence, TriggerText, type ReadContext } from "./AutomationRead";
import { collection } from "./resources";
import { automationEntityIds, describeAutomationSteps } from "./definition-tree";
import "./automations.css";

type Props = { document: AutomationDocument; onEdit?: () => void; children?: ReactNode; automationId?: string; compact?: boolean; entityLabels?: ReadonlyMap<string, string>; headingLevel?: 2 | 3 | 4 };
type ChildStatus = { status: "loading" } | { status: "missing" } | { status: "error" } | { status: "ready"; name: string };
function referencedChildren(steps: AutomationStep[]): string[] {
  const ids = new Set<string>();
  const visit = (sequence: AutomationStep[]) => sequence.forEach(step => {
    if (step.kind === "sub_automation") ids.add(step.automation_id);
    if (step.kind === "if") { visit(step.then); if (step.else) visit(step.else); }
    if (step.kind === "choose") { step.branches.forEach(branch => visit(branch.steps)); if (step.default) visit(step.default); }
  });
  visit(steps);
  return [...ids];
}
function ChildPreview({ id, close, ancestry }: { id: string; close: () => void; ancestry: string[] }) {
  const [state, setState] = useState<{ status: "loading" } | { status: "error"; message: string } | { status: "ready"; document: AutomationDocument }>({ status: "loading" });
  const backend = useBaseUrlVersion();
  useEffect(() => {
    const controller = new AbortController();
    setState({ status: "loading" });
    getAutomation(id, controller.signal).then(value => {
      const document = readAutomationDocument(automationJsonSource(value.definition));
      if (!controller.signal.aborted) setState({ status: "ready", document });
    }).catch(error => {
      if (!controller.signal.aborted) setState({ status: "error", message: error instanceof ApiError && error.status === 404 ? "Automation not found. It may have been removed." : `Could not load Automation: ${error instanceof Error ? error.message : "Unknown error"}` });
    });
    return () => controller.abort();
  }, [id, backend]);
  return <section className="automation-child" aria-label="Referenced Automation" onKeyDown={event => { if (event.key === "Escape") { event.stopPropagation(); close(); } }}>
    <div className="automation-card-heading"><div><h3>Referenced Automation</h3><p>Current definition · read-only</p></div><button autoFocus onClick={close}>Close preview</button></div>
    <Link to={`/automations/${encodeURIComponent(id)}`}>Open Automation and history</Link>
    {state.status === "loading" && <p role="status">Loading {id}…</p>}
    {state.status === "error" && <p role="alert">{state.message}</p>}
    {state.status === "ready" && <><h4>{state.document.definition.name}</h4><p>{id}</p><CardContent document={state.document} ancestry={[...ancestry, id]}/></>}
  </section>;
}
function CardContent({ document, ancestry, entityLabels }: { document: AutomationDocument; ancestry: string[]; entityLabels?: ReadonlyMap<string, string> }) {
  const [style, setStyle] = useState<AutomationReadStyle>("sentence");
  const [child, setChild] = useState<string | null>(null);
  const [childNames, setChildNames] = useState<Record<string, ChildStatus>>({});
  const [entities, setEntities] = useState<Entity[]>([]);
  const [devices, setDevices] = useState<Device[]>([]);
  const [destinations, setDestinations] = useState<NotificationDestination[]>([]);
  const opener = useRef<HTMLButtonElement | null>(null);
  const backend = useBaseUrlVersion();
  const childrenKey = JSON.stringify(referencedChildren(document.definition.steps));
  const needsEntityNames = !entityLabels && automationEntityIds(document.definition).length > 0;
  const needsDestinationNames = describeAutomationSteps(document.definition.steps).notificationCount > 0;
  useEffect(() => {
    const controller = new AbortController();
    const ids: string[] = JSON.parse(childrenKey);
    setChildNames(Object.fromEntries(ids.map(id => [id, { status: "loading" }])));
    ids.forEach(id => {
      getAutomation(id, controller.signal).then(value => {
        const valid = readAutomationDocument(automationJsonSource(value.definition));
        if (!controller.signal.aborted) setChildNames(previous => ({ ...previous, [id]: { status: "ready", name: valid.definition.name } }));
      }).catch(error => {
        if (!controller.signal.aborted) setChildNames(previous => ({ ...previous, [id]: { status: error instanceof ApiError && error.status === 404 ? "missing" : "error" } }));
      });
    });
    return () => controller.abort();
  }, [childrenKey, backend]);
  useEffect(() => {
    let active = true;
    setEntities([]); setDevices([]); setDestinations([]);
    // Names are best effort: a failed read leaves each reference labeled by its ID.
    if (needsEntityNames) {
      collection<Entity>("/v1/entities").then(value => { if (active) setEntities(value); }).catch(() => {});
      collection<Device>("/v1/devices").then(value => { if (active) setDevices(value); }).catch(() => {});
    }
    if (needsDestinationNames) collection<NotificationDestination>("/v1/notification-destinations").then(value => { if (active) setDestinations(value); }).catch(() => {});
    return () => { active = false; };
  }, [backend, needsEntityNames, needsDestinationNames]);
  const inspect = (id: string, button: HTMLButtonElement) => { opener.current = button; setChild(id); };
  const close = () => { setChild(null); requestAnimationFrame(() => { const target = opener.current; if (!target) return; for (let parent = target.parentElement; parent; parent = parent.parentElement) if (parent instanceof HTMLDetailsElement) parent.open = true; target.focus(); }); };
  const ctx: ReadContext = {
    document, inspect,
    entityLabel: id => {
      if (entityLabels) return entityLabels.get(id);
      const found = entities.find(item => item.id === id);
      return found && entityLabelText(found.name, devices.find(item => item.id === found.device_id)?.name);
    },
    destinationName: id => destinations.find(item => item.id === id)?.name,
    childName: id => { const state = childNames[id]; return state?.status === "ready" ? state.name : undefined; },
    childMissing: id => childNames[id]?.status === "missing",
  };
  const definition = document.definition;
  return <><div className="automation-admission"><span className="automation-kind">When</span><p>{definition.triggers.length ? definition.triggers.map((trigger, index) => <Fragment key={trigger.id}>{index > 0 && "; "}<TriggerText ctx={ctx} trigger={trigger} path={["triggers", index]}/> · Trigger {trigger.id}</Fragment>) : "Manual only · no automatic Triggers"}</p>{definition.conditions && <><span className="automation-kind">Only if</span><p><ConditionText ctx={ctx} condition={definition.conditions} path={["conditions"]}/></p></>}<InitialVariables ctx={ctx}/></div>
    <div className="automation-view-options"><span>{definition.steps.length} root {definition.steps.length === 1 ? "Step" : "Steps"}</span><div aria-label="Read style" role="group"><button disabled={!!child} aria-pressed={style === "sentence"} onClick={() => setStyle("sentence")}>Sentence</button><button disabled={!!child} aria-pressed={style === "steps"} onClick={() => setStyle("steps")}>Steps</button></div></div>
    <div className="automation-read-heading">Then</div>
    {style === "sentence" ? <SentenceSequence ctx={ctx} steps={definition.steps} path={["steps"]}/> : <StackSequence ctx={ctx} steps={definition.steps} path={["steps"]}/>}
    <details className="automation-advanced"><summary>Exact definition</summary><pre>{document.source}</pre></details>
    {child && (ancestry.includes(child) || ancestry.length >= 8 ? <section className="automation-child"><p>Preview unavailable: recursive or deeply nested reference.</p><button onClick={close}>Close preview</button></section> : <ChildPreview key={child} id={child} close={close} ancestry={ancestry}/>)}
  </>;
}
export function AutomationCard({ document, onEdit, children, automationId, compact = false, entityLabels, headingLevel = 3 }: Props) {
  const Heading = `h${headingLevel}` as const;
  let valid: AutomationDocument | undefined;
  let message = "Invalid definition";
  try { valid = readAutomationDocument(document.source); } catch (error) { if (error instanceof Error) message = error.message; }
  return <article className={`automation-card${compact ? " automation-card-compact" : ""}`}>
    {valid ? <><header className="automation-card-heading"><div><Heading>{valid.definition.name}</Heading><p>{valid.definition.enabled ? "Automatic Runs enabled" : "Automatic Runs disabled"}</p></div>{onEdit && <button onClick={onEdit}>Edit Automation</button>}</header><CardContent document={valid} ancestry={automationId ? [automationId] : []} entityLabels={entityLabels}/></> : <p role="alert">This Automation cannot be shown: {message}</p>}
    {children}
  </article>;
}
