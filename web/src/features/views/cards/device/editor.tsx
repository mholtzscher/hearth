import type { z } from "zod";

import { TextField } from "../../editor/text-field";
import { deviceSchema } from "./schema";

export const DeviceEditor = ({
  value,
  onChange,
}: {
  value: z.infer<typeof deviceSchema>;
  onChange: (value: z.infer<typeof deviceSchema>) => void;
}) => (
  <>
    <TextField
      label="Label"
      value={value.label ?? ""}
      optional
      schema={deviceSchema.shape.label}
      errorMessage="Use at most 120 characters for the label."
      onChange={(label) => onChange({ ...value, label: label || undefined })}
    />
    <p>
      Readings follow this Device&apos;s Entities automatically. Ask Hearth to
      choose a different Device.
    </p>
  </>
);
