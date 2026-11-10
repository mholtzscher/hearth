import { quantityField, resolveQuantity } from "../../bindings/state-fields";
import type { SensorChoice } from "../time-series/Editor";

/**
 * Picks the Entity for one weather slot: Entities of the slot's type, and for
 * generic numeric sensors only those whose stored unit matches. An existing
 * selection stays listed when discovery doesn't return it.
 */
type Source<T extends string> = { kind: "entity-state"; entity_id: string; entity_type: T; field?: "" };

export function SlotSelect<T extends string>({ label, value, choices, type, unit, optional = false, onChange }: {
  label: string; value?: Source<T>; choices: SensorChoice[]; type: T; unit?: string; optional?: boolean;
  onChange: (source: Source<T> | undefined) => void;
}) {
  const candidates = choices.filter(choice => {
    if (choice.type !== type) return false;
    if (!unit) return true;
    const field = quantityField({ entity_id: choice.id, entity_type: type });
    try { return !!field && resolveQuantity(field, choice.support).unit === unit; }
    catch { return false; }
  });
  const missing = value && !candidates.some(choice => choice.id === value.entity_id);
  return <label>{label}<select value={value?.entity_id ?? ""} onChange={event => {
    const id = event.target.value;
    onChange(id ? { kind: "entity-state", entity_id: id, entity_type: type } : undefined);
  }}>
    {(optional || !value) && <option value="">{optional ? "None" : "Choose a sensor…"}</option>}
    {missing && <option value={value.entity_id}>Existing sensor · {value.entity_id}</option>}
    {candidates.map(choice => <option key={choice.id} value={choice.id}>{choice.name}{choice.device_name ? ` · ${choice.device_name}` : ""}{unit ? ` (${unit})` : ""}</option>)}
  </select></label>;
}
