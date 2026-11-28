import type { z } from "zod";

import { NativeSelect } from "../../../../components/native-select";
import { SegmentedControl } from "../../../../components/segmented-control";
import { stateFields } from "../../bindings/state-fields";
import { EditorLabel } from "../../editor/editor-label";
import { TextField } from "../../editor/text-field";
import { WordsEditor } from "../state/editor";
import { periodTotalsSchema } from "./schema";
import type { PeriodTotalsPeriod, PeriodTotalsStyle } from "./schema";

const periodNames: Record<PeriodTotalsPeriod, string> = {
  hour: "Last 24 hours, by hour",
  day: "Last 7 days, by day",
};
const styleNames: Record<PeriodTotalsStyle, string> = {
  columns: "Columns",
  ledger: "Ledger",
};

export const PeriodTotalsEditor = ({
  value,
  onChange,
}: {
  value: z.infer<typeof periodTotalsSchema>;
  onChange: (value: z.infer<typeof periodTotalsSchema>) => void;
}) => {
  const fields = Object.entries(
    stateFields[value.source.entity_type] ?? {}
  ).filter(([, field]) => field.value_type === "boolean");
  return (
    <>
      {fields.length > 1 && (
        <EditorLabel>
          State field
          <NativeSelect
            size="auto"
            value={value.source.field ?? ""}
            onChange={(event) =>
              onChange({
                ...value,
                source: { ...value.source, field: event.target.value },
                words: undefined,
              })
            }
          >
            {fields.map(([path]) => (
              <option key={path} value={path}>
                {path || "State root"}
              </option>
            ))}
          </NativeSelect>
        </EditorLabel>
      )}
      <TextField
        label="Label"
        value={value.label ?? ""}
        optional
        schema={periodTotalsSchema.shape.label}
        errorMessage="Use at most 120 characters for the label."
        onChange={(label) => onChange({ ...value, label: label || undefined })}
      />
      <WordsEditor
        source={value.source}
        words={value.words}
        onChange={(words) => onChange({ ...value, words })}
      />
      <EditorLabel>
        Period
        <NativeSelect
          size="auto"
          value={value.period}
          onChange={(event) =>
            onChange({
              ...value,
              period: event.target.value as PeriodTotalsPeriod,
            })
          }
        >
          {(Object.keys(periodNames) as PeriodTotalsPeriod[]).map((period) => (
            <option key={period} value={period}>
              {periodNames[period]}
            </option>
          ))}
        </NativeSelect>
      </EditorLabel>
      <SegmentedControl
        label="Style"
        value={value.style}
        onValueChange={(style) => onChange({ ...value, style })}
        options={(Object.keys(styleNames) as PeriodTotalsStyle[]).map(
          (style) => ({ value: style, label: styleNames[style] })
        )}
      />
    </>
  );
};
