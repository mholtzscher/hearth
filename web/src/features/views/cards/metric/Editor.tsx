import type { z } from "zod";
import { metricSchema } from "./schema";
import { TextField } from "../../editor/TextField";
import { TEMPERATURE_TYPE } from "../../bindings/types";

export function MetricEditor({ value, onChange }: { value: z.infer<typeof metricSchema>; onChange: (value: z.infer<typeof metricSchema>) => void }) {
  return <>
    <TextField label="Label" value={value.label ?? ""} optional schema={metricSchema.shape.label} errorMessage="Use at most 120 characters for the label." onChange={label => onChange({ ...value, label: label || undefined })} />
    <label>Units<select value={value.display.unit} onChange={event => onChange({ ...value, display: { unit: event.target.value as "celsius" | "fahrenheit" | "native" } })}>
      {value.source.entity_type === TEMPERATURE_TYPE ? <>
        <option value="celsius">Celsius</option><option value="fahrenheit">Fahrenheit</option>
      </> : <option value="native">Native</option>}
    </select></label>
  </>;
}
