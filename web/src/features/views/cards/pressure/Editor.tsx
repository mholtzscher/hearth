import type { z } from "zod";
import { seriesWindows } from "../../bindings/schema";
import type { SeriesWindow } from "../../bindings/types";
import { TextField } from "../../editor/TextField";
import type { SensorChoice } from "../time-series/Editor";
import { SlotSelect } from "../weather/SlotSelect";
import { pressureSchema } from "./schema";

type Value = z.infer<typeof pressureSchema>;

export function PressureEditor({ value, choices, onChange }: { value: Value; choices: SensorChoice[]; onChange: (value: Value) => void }) {
  return <>
    <TextField label="Label" value={value.label ?? ""} optional schema={pressureSchema.shape.label} errorMessage="Use at most 120 characters for the label." onChange={label => onChange({ ...value, label: label || undefined })} />
    <SlotSelect label="Pressure" value={value.source} choices={choices} type="hearth.pressure/v1" onChange={source => source && onChange({ ...value, source })} />
    <label>Trend<select value={value.trend ?? ""} onChange={event => onChange({ ...value, trend: event.target.value ? event.target.value as SeriesWindow : undefined })}>
      <option value="">No trend</option>{seriesWindows.map(window => <option key={window} value={window}>{window}</option>)}
    </select></label>
  </>;
}
