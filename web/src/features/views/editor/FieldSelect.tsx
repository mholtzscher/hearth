import { isQuantityField, stateFields } from "../bindings/state-fields";
import type { NumericSource } from "../bindings/types";

export function FieldSelect({ source, onChange }: { source: NumericSource; onChange: (field: string) => void }) {
  const fields = Object.entries(stateFields[source.entity_type] ?? {}).filter(([, field]) => isQuantityField(field));
  if (fields.length < 2) return null;
  return <label>State field<select value={source.field ?? ""} onChange={event => onChange(event.target.value)}>
    {fields.map(([path]) => <option key={path} value={path}>{path || "State root"}</option>)}
  </select></label>;
}
