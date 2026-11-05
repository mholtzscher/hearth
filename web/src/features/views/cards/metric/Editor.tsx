import type { z } from "zod";
import { metricSchema } from "./schema";
import { TextField } from "../../editor/TextField";
import { FieldSelect } from "../../editor/FieldSelect";

export function MetricEditor({ value, onChange }: { value: z.infer<typeof metricSchema>; onChange: (value: z.infer<typeof metricSchema>) => void }) {
  return <>
    <FieldSelect source={value.source} onChange={field => onChange({ ...value, source: { ...value.source, field } })} />
    <TextField label="Label" value={value.label ?? ""} optional schema={metricSchema.shape.label} errorMessage="Use at most 120 characters for the label." onChange={label => onChange({ ...value, label: label || undefined })} />
  </>;
}
