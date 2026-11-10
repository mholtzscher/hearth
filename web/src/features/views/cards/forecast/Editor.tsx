import type { z } from "zod";
import { TextField } from "../../editor/TextField";
import type { SensorChoice } from "../time-series/Editor";
import { forecastSchema, forecastTypes } from "./schema";

type Value = z.infer<typeof forecastSchema>;
export function ForecastEditor({ value, choices, onChange }: { value: Value; choices: SensorChoice[]; onChange: (value: Value) => void }) {
  const candidates = choices.filter(choice => choice.type === forecastTypes.hourly || choice.type === forecastTypes.daily);
  const missing = !candidates.some(choice => choice.id === value.source.entity_id);
  const isHourly = value.source.entity_type === forecastTypes.hourly;
  const min = isHourly ? 6 : 3, max = isHourly ? 48 : 8;
  return <>
    <TextField label="Label" value={value.label ?? ""} optional schema={forecastSchema.options[0].shape.label} errorMessage="Use at most 120 characters for the label." onChange={label => onChange({ ...value, label: label || undefined })} />
    <label>Forecast Entity<select value={value.source.entity_id} onChange={event => {
      const selected = candidates.find(choice => choice.id === event.target.value);
      if (!selected) return;
      const entity_type = selected.type === forecastTypes.hourly ? forecastTypes.hourly : forecastTypes.daily;
      const periods = value.periods !== undefined && (value.periods < (entity_type === forecastTypes.hourly ? 6 : 3) || value.periods > (entity_type === forecastTypes.hourly ? 48 : 8)) ? undefined : value.periods;
      if (entity_type === forecastTypes.hourly) onChange({ label: value.label, periods, source: { kind: "entity-state", entity_id: selected.id, entity_type: forecastTypes.hourly } });
      else onChange({ label: value.label, periods, source: { kind: "entity-state", entity_id: selected.id, entity_type: forecastTypes.daily } });
    }}>{missing && <option value={value.source.entity_id}>Existing forecast · {value.source.entity_id}</option>}
      {candidates.map(choice => <option key={choice.id} value={choice.id}>{choice.name}{choice.device_name ? ` · ${choice.device_name}` : ""}</option>)}
    </select></label>
    <label>{isHourly ? "Hours" : "Days"}<input type="number" min={min} max={max} placeholder={isHourly ? "24" : "8"} value={value.periods ?? ""} onChange={event => onChange({ ...value, periods: event.target.value === "" ? undefined : Number(event.target.value) })} /></label>
  </>;
}
