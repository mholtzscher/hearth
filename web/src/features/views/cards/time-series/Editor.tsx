import type { z } from "zod";
import { timeSeriesSchema } from "./schema";
import { TextField } from "../../editor/TextField";

export type TemperatureChoice = { id: string; name: string };
export function TimeSeriesEditor({ value, choices, onChange }: { value: z.infer<typeof timeSeriesSchema>; choices: TemperatureChoice[]; onChange: (value: z.infer<typeof timeSeriesSchema>) => void }) {
  const window = value.series[0].source.window;
  return <>
    <TextField label="Chart title" value={value.title} schema={timeSeriesSchema.shape.title} errorMessage="Use 1–120 characters for the chart title." onChange={title => onChange({ ...value, title })} />
    <label>Units<select value={value.display.unit} onChange={event => onChange({ ...value, display: { unit: event.target.value === "fahrenheit" ? "fahrenheit" : "celsius" } })}>
      <option value="celsius">Celsius</option><option value="fahrenheit">Fahrenheit</option>
    </select></label>
    <label>Time window<select value={window} onChange={event => onChange({ ...value, series: value.series.map(item => ({ ...item, source: { ...item.source, window: event.target.value === "6h" ? "6h" : "24h" } })) })}>
      <option value="6h">Last 6 hours</option><option value="24h">Last 24 hours</option>
    </select></label>
    <fieldset><legend>Series</legend>{value.series.map((item, index) => <div key={item.source.entity_id} className="view-series-editor">
      <TextField label={`Series ${index + 1} label`} value={item.label ?? ""} placeholder={choices.find(choice => choice.id === item.source.entity_id)?.name ?? item.source.entity_id} optional schema={timeSeriesSchema.shape.series.element.shape.label} errorMessage="Use at most 120 characters for the series label." onChange={label => onChange({ ...value, series: value.series.map((entry, i) => i === index ? { ...entry, label: label || undefined } : entry) })} />
      <button type="button" disabled={value.series.length === 1} onClick={() => onChange({ ...value, series: value.series.filter((_, i) => i !== index) })}>Remove series {index + 1}</button>
    </div>)}
      <label>Add sensor<select value="" disabled={value.series.length >= 8} onChange={event => {
        if (event.target.value) onChange({ ...value, series: [...value.series, { source: { kind: "entity-series", entity_type: "hearth.temperature/v1", entity_id: event.target.value, window } }] });
      }}><option value="">Choose a temperature sensor…</option>{choices.filter(choice => !value.series.some(item => item.source.entity_id === choice.id)).map(choice => <option key={choice.id} value={choice.id}>{choice.name}</option>)}</select></label>
    </fieldset>
  </>;
}
