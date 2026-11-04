import type { z } from "zod";
import { metricSchema } from "./schema";
import { TextField } from "../../editor/TextField";
import { displayOptions } from "../../bindings/readings";

export function MetricEditor({ value, onChange }: { value: z.infer<typeof metricSchema>; onChange: (value: z.infer<typeof metricSchema>) => void }) {
  return <>
    <TextField label="Label" value={value.label ?? ""} optional schema={metricSchema.shape.label} errorMessage="Use at most 120 characters for the label." onChange={label => onChange({ ...value, label: label || undefined })} />
    <label>Units<select value={value.display.unit} onChange={event => onChange({ ...value, display: { unit: event.target.value } })}>
      {displayOptions(value.source.entity_type).map(option => <option key={option.key} value={option.key}>{option.name}</option>)}
    </select></label>
  </>;
}
