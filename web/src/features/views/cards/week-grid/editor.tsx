import type { z } from "zod";

import { FieldSelect } from "../../editor/field-select";
import { TextField } from "../../editor/text-field";
import { weekGridSchema } from "./schema";

export const WeekGridEditor = ({
  value,
  onChange,
}: {
  value: z.infer<typeof weekGridSchema>;
  onChange: (value: z.infer<typeof weekGridSchema>) => void;
}) => (
  <>
    <FieldSelect
      source={value.source}
      onChange={(field) =>
        onChange({ ...value, source: { ...value.source, field } })
      }
    />
    <TextField
      label="Label"
      value={value.label ?? ""}
      optional
      schema={weekGridSchema.shape.label}
      errorMessage="Use at most 120 characters for the label."
      onChange={(label) => onChange({ ...value, label: label || undefined })}
    />
  </>
);
