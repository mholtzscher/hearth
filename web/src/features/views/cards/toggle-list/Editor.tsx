import type { z } from "zod";
import { toggleListSchema, MAX_TOGGLE_ROWS } from "./schema";
import { powerBindingSchema } from "../../bindings/schema";
import { TextField } from "../../editor/TextField";
import type { SensorChoice } from "../time-series/Editor";

type Value = z.infer<typeof toggleListSchema>;

export function ToggleListEditor({ value, choices, onChange }: { value: Value; choices: SensorChoice[]; onChange: (value: Value) => void }) {
  const powers = choices.filter(choice => choice.type === "hearth.power/v1");
  const baseLabel = (choice: SensorChoice) => choice.device_name
    ? `${choice.device_name}${choice.device_id !== undefined && powers.filter(other => other.device_id === choice.device_id).length > 1 ? ` · ${choice.name}` : ""}`
    : choice.name;
  const choiceLabel = (choice: SensorChoice) => {
    const label = baseLabel(choice);
    return label.length > 120 || powers.some(other => other.id !== choice.id && baseLabel(other) === label)
      ? `${label.slice(0, 120 - choice.id.length - 3)} · ${choice.id}` : label;
  };
  const available = powers.filter(choice => !value.rows.some(row => row.source.entity_id === choice.id));
  function updateRow(index: number, row: Value["rows"][number]) { onChange({ ...value, rows: value.rows.map((entry, i) => i === index ? row : entry) }); }
  const source = (choice: SensorChoice) => powerBindingSchema.parse({ kind: "entity-state", entity_id: choice.id, entity_type: choice.type });
  return <>
    <TextField label="List title" value={value.title} schema={toggleListSchema.shape.title} errorMessage="Use 1–120 characters for the list title." onChange={title => onChange({ ...value, title })} />
    <label className="view-checkbox"><input type="checkbox" checked={value.all_off ?? false} onChange={event => onChange({ ...value, all_off: event.target.checked ? true : undefined })} />Offer Turn all off</label>
    {value.rows.map((row, index) => <fieldset key={row.source.entity_id}><legend>Row {index + 1}</legend>
      <TextField label={`Row ${index + 1} label`} value={row.label ?? ""} optional schema={toggleListSchema.shape.rows.element.shape.label} errorMessage="Use at most 120 characters for the row label." onChange={label => updateRow(index, { ...row, label: label || undefined })} />
      <label>Power source<select value={row.source.entity_id} onChange={event => { const choice = powers.find(choice => choice.id === event.target.value); if (choice) updateRow(index, { ...row, source: source(choice), label: choiceLabel(choice) }); }}>
        {!powers.some(choice => choice.id === row.source.entity_id) && <option value={row.source.entity_id}>Existing source · {row.source.entity_id}</option>}
        {powers.filter(choice => choice.id === row.source.entity_id || available.includes(choice)).map(choice => <option key={choice.id} value={choice.id}>{choiceLabel(choice)}</option>)}
      </select></label>
      <button type="button" disabled={index === 0} onClick={() => { const rows = [...value.rows]; [rows[index - 1], rows[index]] = [rows[index], rows[index - 1]]; onChange({ ...value, rows }); }}>Move row {index + 1} up</button>
      <button type="button" disabled={value.rows.length === 1} onClick={() => onChange({ ...value, rows: value.rows.filter((_, i) => i !== index) })}>Remove row {index + 1}</button>
    </fieldset>)}
    <label>Add switch<select value="" disabled={value.rows.length >= MAX_TOGGLE_ROWS} onChange={event => {
      const choice = available.find(choice => choice.id === event.target.value);
      if (choice) onChange({ ...value, rows: [...value.rows, { source: source(choice), label: choiceLabel(choice) }] });
    }}><option value="">Choose a power Entity…</option>{available.map(choice => <option key={choice.id} value={choice.id}>{choiceLabel(choice)}</option>)}</select></label>
  </>;
}
