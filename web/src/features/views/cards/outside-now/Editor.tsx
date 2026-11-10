import type { z } from "zod";
import { TextField } from "../../editor/TextField";
import type { SensorChoice } from "../time-series/Editor";
import { sensorUnits } from "../weather/schema";
import { SlotSelect } from "../weather/SlotSelect";
import { outsideNowSchema } from "./schema";

type Value = z.infer<typeof outsideNowSchema>;

export function OutsideNowEditor({ value, choices, onChange }: { value: Value; choices: SensorChoice[]; onChange: (value: Value) => void }) {
  return <>
    <TextField label="Label" value={value.label ?? ""} optional schema={outsideNowSchema.shape.label} errorMessage="Use at most 120 characters for the label." onChange={label => onChange({ ...value, label: label || undefined })} />
    <SlotSelect label="Temperature" value={value.temperature} choices={choices} type="hearth.temperature/v1" onChange={temperature => temperature && onChange({ ...value, temperature })} />
    <SlotSelect label="Humidity" optional value={value.humidity} choices={choices} type="hearth.relativehumidity/v1" onChange={humidity => onChange({ ...value, humidity })} />
    <SlotSelect label="Wind speed" optional value={value.wind} choices={choices} type="hearth.speed/v1" onChange={wind => onChange({ ...value, wind })} />
    <SlotSelect label="Wind direction" optional value={value.wind_direction} choices={choices} type="hearth.numericsensor/v1" unit={sensorUnits.direction} onChange={wind_direction => onChange({ ...value, wind_direction })} />
    <SlotSelect label="Rain rate" optional value={value.rain_rate} choices={choices} type="hearth.numericsensor/v1" unit={sensorUnits.rainRate} onChange={rain_rate => onChange({ ...value, rain_rate })} />
    <SlotSelect label="Rain today" optional value={value.rain_today} choices={choices} type="hearth.numericsensor/v1" unit={sensorUnits.rainDepth} onChange={rain_today => onChange({ ...value, rain_today })} />
  </>;
}
