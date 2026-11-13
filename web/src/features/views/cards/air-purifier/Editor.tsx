import { quantityField, resolveQuantity } from "../../bindings/state-fields";
import { TextField } from "../../editor/TextField";
import type { SensorChoice } from "../time-series/Editor";
import { choicesOf } from "./category";
import { airPurifierSchema, type AirPurifierProps, type AirPurifierRole } from "./schema";

const labels: Record<AirPurifierRole, string> = { power: "Power", pm25: "PM2.5", filter_life: "Filter life", mode: "Mode", fan_level: "Fan level", display: "Display", child_lock: "Child lock" };
const roles: AirPurifierRole[] = ["power", "pm25", "filter_life", "mode", "fan_level", "display", "child_lock"];
const levels = (choices: string[]) => choices.length >= 2 && choices.length <= 10 && choices.every((choice, i) => choice === String(i + 1));
function compatible(role: AirPurifierRole, choice: SensorChoice): boolean {
  if (role === "power" || role === "display" || role === "child_lock") return choice.type === "hearth.power/v1";
  if (role === "mode" || role === "fan_level") {
    const options = choicesOf(choice.support);
    return choice.type === "hearth.enumsetting/v1" && (role === "fan_level" ? levels(options) : options.length >= 2 && options.length <= 5 && !levels(options));
  }
  if (choice.type !== "hearth.numericsensor/v1") return false;
  const field = quantityField({ entity_id: choice.id, entity_type: choice.type });
  try { return !!field && resolveQuantity(field, choice.support).unit === (role === "pm25" ? "µg/m³" : "%"); } catch { return false; }
}

export function AirPurifierEditor({ value, choices, onChange }: { value: AirPurifierProps; choices: SensorChoice[]; onChange: (value: AirPurifierProps) => void }) {
  const deviceId = choices.find(choice => choice.id === value.power.entity_id)?.device_id;
  return <>
    <TextField label="Card label" value={value.label ?? ""} optional schema={airPurifierSchema.shape.label} errorMessage="Use at most 120 characters for the card label." onChange={label => onChange({ ...value, label: label || undefined })} />
    <label>Layout<select value={value.layout} onChange={event => onChange({ ...value, layout: event.target.value as AirPurifierProps["layout"] })}><option value="card">Card</option><option value="tile">Tile</option></select></label>
    {roles.map(role => {
      const current = value[role];
      const used = new Set(roles.filter(other => other !== role).map(other => value[other]?.entity_id));
      const candidates = choices.filter(choice => compatible(role, choice) && (role === "power" || (deviceId !== undefined && choice.device_id === deviceId)) && !used.has(choice.id));
      const missing = current && !candidates.some(choice => choice.id === current.entity_id);
      return <label key={role}>{labels[role]}<select value={current?.entity_id ?? ""} onChange={event => {
        const selected = candidates.find(choice => choice.id === event.target.value);
        if (!selected) { if (role !== "power" && role !== "pm25") onChange({ ...value, [role]: undefined }); return; }
        const source = { kind: "entity-state", entity_id: selected.id, entity_type: selected.type };
        if (role === "power") {
          const next = { ...value, power: source } as AirPurifierProps;
          if (deviceId !== undefined && selected.device_id !== deviceId) {
            for (const other of roles.filter(item => item !== "power")) {
              const old = value[other];
              if (old && choices.find(choice => choice.id === old.entity_id)?.device_id !== selected.device_id) {
                if (other === "pm25") next.pm25 = { ...next.pm25, entity_id: "" };
                else delete next[other];
              }
            }
          }
          onChange(next);
        } else onChange({ ...value, [role]: source });
      }}>
        {(role !== "power" && role !== "pm25") && <option value="">None</option>}
        {role === "pm25" && !current?.entity_id && <option value="">Choose a sensor…</option>}
        {missing && current.entity_id && <option value={current.entity_id}>Existing source · {current.entity_id}</option>}
        {candidates.map(choice => <option key={choice.id} value={choice.id}>{choice.device_name ? `${choice.device_name} · ` : ""}{choice.name}</option>)}
      </select></label>;
    })}
    {deviceId === undefined && <p>Device information is unavailable. Existing source selections are preserved.</p>}
  </>;
}
