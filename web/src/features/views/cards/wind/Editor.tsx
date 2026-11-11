import type { z } from "zod";
import { seriesWindows } from "../../bindings/schema";
import type { SeriesWindow } from "../../bindings/types";
import { TextField } from "../../editor/TextField";
import type { SensorChoice } from "../time-series/Editor";
import { sensorUnits } from "../weather/schema";
import { SlotSelect } from "../weather/SlotSelect";
import { windSchema } from "./schema";

type Value = z.infer<typeof windSchema>;

export function WindEditor({ value, choices, onChange }: { value: Value; choices: SensorChoice[]; onChange: (value: Value) => void }) {
  const speed = { choices, type: "hearth.speed/v1" as const };
  return <>
    <TextField label="Label" value={value.label ?? ""} optional schema={windSchema.shape.label} errorMessage="Use at most 120 characters for the label." onChange={label => onChange({ ...value, label: label || undefined })} />
    <SlotSelect label="Wind speed" value={value.speed} {...speed} onChange={source => source && onChange({ ...value, speed: source })} />
    <SlotSelect label="Gusts" optional value={value.gust} {...speed} onChange={gust => onChange({ ...value, gust })} />
    <SlotSelect label="Wind direction" optional value={value.direction} choices={choices} type="hearth.numericsensor/v1" unit={sensorUnits.direction} onChange={direction => onChange({ ...value, direction })} />
    <SlotSelect label="Today's max gust" optional value={value.max_gust} {...speed} onChange={max_gust => onChange({ ...value, max_gust })} />
    <label>Trend<select value={value.trend ?? ""} onChange={event => onChange({ ...value, trend: event.target.value ? event.target.value as SeriesWindow : undefined })}>
      <option value="">No trend</option>{seriesWindows.map(window => <option key={window} value={window}>{window}</option>)}
    </select></label>
  </>;
}
