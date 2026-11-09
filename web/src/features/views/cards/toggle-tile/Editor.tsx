import type { z } from "zod";
import { toggleTileSchema } from "./schema";
import { stateBindingSchema } from "../../bindings/schema";
import { isQuantityField, resolveQuantity, stateFields } from "../../bindings/state-fields";
import type { EntitySource, SeriesWindow } from "../../bindings/types";
import { TextField } from "../../editor/TextField";
import type { SensorChoice } from "../time-series/Editor";

const sourceKey = (source: EntitySource) => `${source.entity_id}:${source.field ?? ""}`;
type Value = z.infer<typeof toggleTileSchema>;

/** A single source selection feeds both the displayed number and its history. */
export function ToggleTileEditor({ value, choices, onChange }: { value: Value; choices: SensorChoice[]; onChange: (value: Value) => void }) {
  const deviceId = choices.find(choice => choice.id === value.source.entity_id)?.device_id;
  const candidates = choices.filter(choice => deviceId !== undefined && choice.device_id === deviceId).flatMap(choice =>
    Object.entries(stateFields[choice.type] ?? {}).flatMap(([field, descriptor]) => {
      if (!isQuantityField(descriptor)) return [];
      try {
        const unit = resolveQuantity(descriptor, choice.support).unit;
        const source = stateBindingSchema.parse({ kind: "entity-state", entity_id: choice.id, entity_type: choice.type, field });
        return [{ source, label: `${choice.name}${field ? ` · ${field}` : ""} (${unit})` }];
      } catch { return []; }
    }));
  const metricKey = value.metric ? sourceKey(value.metric.source) : "";
  const detailKey = value.metric?.detail ? sourceKey(value.metric.detail.source) : "";
  const options = (existing?: EntitySource) => <>
    <option value="">None</option>
    {existing && !candidates.some(candidate => sourceKey(candidate.source) === sourceKey(existing)) && <option value={sourceKey(existing)}>Existing source · {existing.entity_id}{existing.field}</option>}
    {candidates.map(candidate => <option key={sourceKey(candidate.source)} value={sourceKey(candidate.source)}>{candidate.label}</option>)}
  </>;
  return <>
    <TextField label="Tile label" value={value.label ?? ""} optional schema={toggleTileSchema.shape.label} errorMessage="Use at most 120 characters for the tile label." onChange={label => onChange({ ...value, label: label || undefined })} />
    <p>The tile switches this power Entity. Its reading and surface show observed State.</p>
    <label>Sparkline source<select value={metricKey} onChange={event => {
      if (!event.target.value) { onChange({ ...value, metric: undefined }); return; }
      const selected = candidates.find(candidate => sourceKey(candidate.source) === event.target.value);
      if (selected) onChange({ ...value, metric: { source: selected.source, window: value.metric?.window ?? "24h", detail: value.metric?.detail } });
    }}>{options(value.metric?.source)}</select></label>
    <p>Choose a numeric field from the same Device. The displayed value and sparkline always share this source; nothing changes automatically on refresh.</p>
    {deviceId === undefined && <p>Device information is unavailable. Existing source selections are preserved.</p>}
    {value.metric && <>
      <label>Sparkline window<select value={value.metric.window} onChange={event => onChange({ ...value, metric: { ...value.metric!, window: event.target.value as SeriesWindow } })}>
        <option value="1h">Last hour</option><option value="6h">Last 6 hours</option><option value="24h">Last 24 hours</option><option value="7d">Last 7 days</option>
      </select></label>
      <label>Additional reading<select value={detailKey} onChange={event => {
        if (!event.target.value) { onChange({ ...value, metric: { ...value.metric!, detail: undefined } }); return; }
        const selected = candidates.find(candidate => sourceKey(candidate.source) === event.target.value);
        if (selected) onChange({ ...value, metric: { ...value.metric!, detail: { source: selected.source, label: value.metric?.detail?.label } } });
      }}>{options(value.metric.detail?.source)}</select></label>
      {value.metric.detail && <TextField label="Additional reading label" value={value.metric.detail.label ?? ""} optional schema={toggleTileSchema.shape.metric.unwrap().shape.detail.unwrap().shape.label} errorMessage="Use at most 120 characters for the additional reading label." onChange={label => onChange({ ...value, metric: { ...value.metric!, detail: { ...value.metric!.detail!, label: label || undefined } } })} />}
    </>}
  </>;
}
