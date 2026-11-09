import type { z } from "zod";
import { seriesWindows, textBindingSchema } from "../../bindings/schema";
import { isTextField, stateFields } from "../../bindings/state-fields";
import { FieldSelect } from "../../editor/FieldSelect";
import { TextField } from "../../editor/TextField";
import { WordsEditor } from "../state/Editor";
import type { SensorChoice } from "../time-series/Editor";
import { MAX_TIMELINE_ROWS, stateTimelineSchema, timelineStyles } from "./schema";

export function StateTimelineEditor({ value, choices, onChange }: { value: z.infer<typeof stateTimelineSchema>; choices: SensorChoice[]; onChange: (value: z.infer<typeof stateTimelineSchema>) => void }) {
  const rowSchema = stateTimelineSchema.shape.rows.element;
  function row(index: number, next: z.infer<typeof rowSchema>) { onChange({ ...value, rows: value.rows.map((entry, i) => i === index ? next : entry) }); }
  return <>
    <TextField label="Timeline title" value={value.title} schema={stateTimelineSchema.shape.title} errorMessage="Use 1–120 characters for the timeline title." onChange={title => onChange({ ...value, title })} />
    <label>Style<select value={value.style} onChange={event => onChange({ ...value, style: stateTimelineSchema.shape.style.parse(event.target.value) })}>{timelineStyles.map(style => <option key={style} value={style}>{style}</option>)}</select></label>
    <label>Window<select value={value.window} onChange={event => onChange({ ...value, window: stateTimelineSchema.shape.window.parse(event.target.value) })}>{seriesWindows.map(window => <option key={window} value={window}>{window}</option>)}</select></label>
    {value.rows.map((item, index) => <fieldset key={index}><legend>Row {index + 1}</legend>
      <TextField label={`Row ${index + 1} label`} value={item.label ?? ""} optional schema={rowSchema.shape.label} errorMessage="Use at most 120 characters for the label." onChange={label => row(index, { ...item, label: label || undefined })} />
      <FieldSelect source={item.source} kind="text" onChange={field => row(index, { ...item, source: { ...item.source, field }, words: undefined })} />
      <WordsEditor source={item.source} words={item.words} onChange={words => row(index, { ...item, words })} />
      <button type="button" disabled={index === 0} onClick={() => { const rows = [...value.rows]; [rows[index - 1], rows[index]] = [rows[index], rows[index - 1]]; onChange({ ...value, rows }); }}>Move row {index + 1} up</button>
      <button type="button" disabled={value.rows.length === 1} onClick={() => onChange({ ...value, rows: value.rows.filter((_, i) => i !== index) })}>Remove row {index + 1}</button>
    </fieldset>)}
    <label>Add reading<select value="" disabled={value.rows.length >= MAX_TIMELINE_ROWS} onChange={event => {
      const choice = choices.find(item => item.id === event.target.value);
      if (!choice) return;
      const field = Object.entries(stateFields[choice.type] ?? {}).find(([, candidate]) => isTextField(candidate))?.[0];
      if (field === undefined) return;
      const source = textBindingSchema.parse({ kind: "entity-state", entity_id: choice.id, entity_type: choice.type, field });
      onChange({ ...value, rows: [...value.rows, { source }] });
    }}><option value="">Choose an Entity…</option>{choices.filter(choice => Object.values(stateFields[choice.type] ?? {}).some(isTextField)).map(choice => <option key={choice.id} value={choice.id}>{choice.name}</option>)}</select></label>
  </>;
}
