import type { z } from "zod";
import { stateFields } from "../../bindings/state-fields";
import { TextField } from "../../editor/TextField";
import { WordsEditor } from "../state/Editor";
import { weekPatternSchema } from "./schema";

export function WeekPatternEditor({ value, onChange }: { value: z.infer<typeof weekPatternSchema>; onChange: (value: z.infer<typeof weekPatternSchema>) => void }) {
  const fields = Object.entries(stateFields[value.source.entity_type] ?? {}).filter(([, field]) => field.value_type === "boolean");
  return <>
    {fields.length > 1 && <label>State field<select value={value.source.field ?? ""} onChange={event => onChange({ ...value, source: { ...value.source, field: event.target.value }, words: undefined })}>{fields.map(([path]) => <option key={path} value={path}>{path || "State root"}</option>)}</select></label>}
    <TextField label="Label" value={value.label ?? ""} optional schema={weekPatternSchema.shape.label} errorMessage="Use at most 120 characters for the label." onChange={label => onChange({ ...value, label: label || undefined })} />
    <WordsEditor source={value.source} words={value.words} onChange={words => onChange({ ...value, words })} />
  </>;
}
