import type { z } from "zod";
import { deviceSchema } from "./schema";
import { TextField } from "../../editor/TextField";

export function DeviceEditor({ value, onChange }: { value: z.infer<typeof deviceSchema>; onChange: (value: z.infer<typeof deviceSchema>) => void }) {
  return <>
    <TextField label="Label" value={value.label ?? ""} optional schema={deviceSchema.shape.label} errorMessage="Use at most 120 characters for the label." onChange={label => onChange({ ...value, label: label || undefined })} />
    <p>Readings follow this Device's Entities automatically. Ask Hearth to choose a different Device.</p>
  </>;
}
