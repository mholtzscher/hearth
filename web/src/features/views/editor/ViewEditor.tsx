import { useEffect, useState } from "react";
import { z } from "zod";
import { apiFetch, useBaseUrlVersion } from "../../../api/client";
import { definitionSchema, type ViewDefinition, type ViewElement } from "../catalog";
import { parseDefinition } from "../catalog/compatibility";
import { MetricEditor } from "../cards/metric/Editor";
import { StateEditor } from "../cards/state/Editor";
import { ReadingListEditor } from "../cards/reading-list/Editor";
import { DeviceEditor } from "../cards/device/Editor";
import { TimeSeriesEditor, type SensorChoice } from "../cards/time-series/Editor";
import { fieldTypes } from "../bindings/state-fields";
import { headingSchema, noteSchema } from "../cards/text/schema";
import { TextField } from "./TextField";

const entityPage = z.object({ items: z.array(z.object({ id: z.string(), name: z.string(), type: z.string(), support: z.unknown().optional() })), next_cursor: z.string().nullish() });
export function ViewEditor({ definition, onChange, onValidityChange }: { definition: ViewDefinition; onChange: (definition: ViewDefinition) => void; onValidityChange?: (valid: boolean) => void }) {
  const [working, setWorking] = useState(definition);
  useEffect(() => { setWorking(definition); onValidityChange?.(true); }, [definition, onValidityChange]);
  const [choices, setChoices] = useState<SensorChoice[]>([]);
  const [error, setError] = useState("");
  const version = useBaseUrlVersion();
  const readable = new Set<string>(fieldTypes);
  useEffect(() => {
    const controller = new AbortController();
    setChoices([]);
    void (async () => {
      const all: SensorChoice[] = [];
      let cursor: string | undefined;
      do {
        const query = new URLSearchParams({ limit: "200", ...(cursor ? { cursor } : {}) });
        const page = await apiFetch(`/v1/entities?${query}`, { signal: controller.signal }, text => entityPage.parse(JSON.parse(text)));
        all.push(...page.items.filter(item => readable.has(item.type)));
        cursor = page.next_cursor ?? undefined;
      } while (cursor && !controller.signal.aborted);
      if (!controller.signal.aborted) setChoices(all);
    })().catch(() => { if (!controller.signal.aborted) setError("Sensor list unavailable; existing selections are preserved."); });
    return () => controller.abort();
  }, [version]);
  function update(candidate: ViewDefinition) {
    setWorking(candidate);
    const parsed = parseDefinition(candidate);
    onValidityChange?.(parsed !== null);
    if (parsed) { setError(""); onChange(parsed); }
    else setError("This edit exceeds the view limits or leaves required text empty.");
  }
  function element(key: string, value: ViewElement) { update({ ...working, spec: { ...working.spec, elements: { ...working.spec.elements, [key]: value } } }); }
  const validation = definitionSchema.safeParse(working);
  const invalidNodes = new Set(validation.success ? [] : validation.error.issues.flatMap(issue =>
    issue.path[0] === "spec" && issue.path[1] === "elements" ? [String(issue.path[2])] : []));
  return <div className="view-editor">
    <p>Changes stay in this draft until you save.</p>
    {error && <p role="status">{error}</p>}
    <TextField label="Dashboard title" value={working.title} onChange={title => update({ ...working, title })} schema={definitionSchema.shape.title} errorMessage="Use 1–80 characters for the dashboard title." />
    {Object.entries(working.spec.elements).map(([key, node]) => <details key={key}><summary>{node.type} · {key}{invalidNodes.has(key) && <span className="invalid-fields"> · Fix invalid fields</span>}</summary>
      {node.type === "Metric" && <MetricEditor value={node.props} onChange={props => element(key, { ...node, props })} />}
      {node.type === "State" && <StateEditor value={node.props} onChange={props => element(key, { ...node, props })} />}
      {node.type === "ReadingList" && <ReadingListEditor value={node.props} choices={choices} onChange={props => element(key, { ...node, props })} />}
      {node.type === "Device" && <DeviceEditor value={node.props} onChange={props => element(key, { ...node, props })} />}
      {node.type === "TimeSeries" && <TimeSeriesEditor value={node.props} choices={choices} onChange={props => element(key, { ...node, props })} />}
      {(node.type === "Heading" || node.type === "Note") && <TextField label="Text" multiline value={node.props.text} onChange={text => element(key, { ...node, props: { text } })} schema={node.type === "Heading" ? headingSchema.shape.text : noteSchema.shape.text} errorMessage={`Use 1–${node.type === "Heading" ? 120 : 500} characters for this ${node.type.toLowerCase()}.`} />}
      {node.type === "Grid" && <label>Columns<select value={node.props.columns} onChange={event => element(key, { ...node, props: { columns: event.target.value === "1" ? 1 : event.target.value === "2" ? 2 : 3 } })}><option>1</option><option>2</option><option>3</option></select></label>}
      {(node.type === "Grid" || node.type === "Stack") && <ol>{node.children.map((child, index) => <li key={child}>{child} <button type="button" disabled={index === 0} aria-label={`Move ${child} up`} onClick={() => {
        const children = [...node.children]; [children[index - 1], children[index]] = [children[index], children[index - 1]]; element(key, { ...node, children });
      }}>↑</button><button type="button" disabled={index === node.children.length - 1} aria-label={`Move ${child} down`} onClick={() => {
        const children = [...node.children]; [children[index], children[index + 1]] = [children[index + 1], children[index]]; element(key, { ...node, children });
      }}>↓</button></li>)}</ol>}
    </details>)}
  </div>;
}
