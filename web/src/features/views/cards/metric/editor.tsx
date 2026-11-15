import type { z } from "zod";

import { Input } from "../../../../components/input";
import { NativeSelect } from "../../../../components/native-select";
import { seriesWindows } from "../../bindings/schema";
import { quantityField } from "../../bindings/state-fields";
import type { SeriesWindow } from "../../bindings/types";
import { EditorLabel } from "../../editor/editor-label";
import { FieldSelect } from "../../editor/field-select";
import { TextField } from "../../editor/text-field";
import { metricSchema } from "./schema";

export const MetricEditor = ({
  value,
  onChange,
}: {
  value: z.infer<typeof metricSchema>;
  onChange: (value: z.infer<typeof metricSchema>) => void;
}) => (
  <>
    <FieldSelect
      source={value.source}
      onChange={(field) => {
        const source = { ...value.source, field };
        onChange({
          ...value,
          source,
          range: quantityField(source)?.quantity.range
            ? value.range
            : undefined,
        });
      }}
    />
    <TextField
      label="Label"
      value={value.label ?? ""}
      optional
      schema={metricSchema.shape.label}
      errorMessage="Use at most 120 characters for the label."
      onChange={(label) => onChange({ ...value, label: label || undefined })}
    />
    <EditorLabel>
      Trend
      <NativeSelect
        size="auto"
        value={value.trend ?? ""}
        onChange={(event) =>
          onChange({
            ...value,
            trend: event.target.value
              ? (event.target.value as SeriesWindow)
              : undefined,
          })
        }
      >
        <option value="">No trend</option>
        {seriesWindows.map((window) => (
          <option key={window} value={window}>
            {window}
          </option>
        ))}
      </NativeSelect>
    </EditorLabel>
    {quantityField(value.source)?.quantity.range && (
      <EditorLabel className="flex items-center gap-2">
        <Input
          size="auto"
          className="w-auto"
          type="checkbox"
          checked={!!value.range}
          onChange={(event) =>
            onChange({
              ...value,
              range: event.target.checked ? true : undefined,
            })
          }
        />
        Show range
      </EditorLabel>
    )}
  </>
);
