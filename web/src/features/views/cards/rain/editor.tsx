import type { z } from "zod";

import { NativeSelect } from "../../../../components/native-select";
import { seriesWindows } from "../../bindings/schema";
import type { SeriesWindow } from "../../bindings/types";
import { EditorLabel } from "../../editor/editor-label";
import { TextField } from "../../editor/text-field";
import type { SensorChoice } from "../time-series/editor";
import { sensorUnits } from "../weather/schema";
import { SlotSelect } from "../weather/slot-select";
import { rainPeriods, rainSchema } from "./schema";

type Value = z.infer<typeof rainSchema>;
const totalLabels = {
  day: "Today's total",
  week: "This week's total",
  month: "This month's total",
  year: "This year's total",
} as const;

export const RainEditor = ({
  value,
  choices,
  onChange,
}: {
  value: Value;
  choices: SensorChoice[];
  onChange: (value: Value) => void;
}) => {
  const sensor = { choices, type: "hearth.numericsensor/v1" as const };
  return (
    <>
      <TextField
        label="Label"
        value={value.label ?? ""}
        optional
        schema={rainSchema.shape.label}
        errorMessage="Use at most 120 characters for the label."
        onChange={(label) => onChange({ ...value, label: label || undefined })}
      />
      <SlotSelect
        label="Rain rate"
        value={value.rate}
        {...sensor}
        unit={sensorUnits.rainRate}
        onChange={(rate) => rate && onChange({ ...value, rate })}
      />
      <SlotSelect
        label="Storm total"
        optional
        value={value.storm}
        {...sensor}
        unit={sensorUnits.rainDepth}
        onChange={(storm) => onChange({ ...value, storm })}
      />
      {rainPeriods.map((period) => (
        <SlotSelect
          key={period}
          label={totalLabels[period]}
          optional
          value={value.totals?.[period]}
          {...sensor}
          unit={sensorUnits.rainDepth}
          onChange={(source) => {
            const totals = { ...value.totals, [period]: source };
            onChange({
              ...value,
              totals: rainPeriods.some((key) => totals[key])
                ? totals
                : undefined,
            });
          }}
        />
      ))}
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
    </>
  );
};
