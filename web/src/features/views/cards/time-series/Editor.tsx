import type { z } from "zod";
import type { timeSeriesSchema } from "./schema";

export type TemperatureChoice = { id: string; name: string };
export function TimeSeriesEditor({ value, choices, onChange }: { value: z.infer<typeof timeSeriesSchema>; choices: TemperatureChoice[]; onChange: (value: z.infer<typeof timeSeriesSchema>) => void }) {
  const window = value.series[0].source.window;
  return <>
    <label>Chart title<input value={value.title} maxLength={120} onChange={event => onChange({ ...value, title: event.target.value })} /></label>
    <label>Units<select value={value.display.unit} onChange={event => onChange({ ...value, display: { unit: event.target.value === "fahrenheit" ? "fahrenheit" : "celsius" } })}>
      <option value="celsius">Celsius</option><option value="fahrenheit">Fahrenheit</option>
    </select></label>
    <label>Time window<select value={window} onChange={event => onChange({ ...value, series: value.series.map(item => ({ ...item, source: { ...item.source, window: event.target.value === "6h" ? "6h" : "24h" } })) })}>
      <option value="6h">Last 6 hours</option><option value="24h">Last 24 hours</option>
    </select></label>
    <fieldset><legend>Series</legend>{value.series.map((item, index) => <div key={item.source.entity_id} className="view-series-editor">
      <label>Series {index + 1} label<input value={item.label ?? ""} placeholder={choices.find(choice => choice.id === item.source.entity_id)?.name ?? item.source.entity_id} onChange={event => onChange({ ...value, series: value.series.map((entry, i) => i === index ? { ...entry, label: event.target.value || undefined } : entry) })} /></label>
      <button type="button" disabled={value.series.length === 1} onClick={() => onChange({ ...value, series: value.series.filter((_, i) => i !== index) })}>Remove series {index + 1}</button>
    </div>)}
      <label>Add sensor<select value="" disabled={value.series.length >= 8} onChange={event => {
        if (event.target.value) onChange({ ...value, series: [...value.series, { source: { kind: "entity-series", entity_type: "hearth.temperature/v1", entity_id: event.target.value, window } }] });
      }}><option value="">Choose a temperature sensor…</option>{choices.filter(choice => !value.series.some(item => item.source.entity_id === choice.id)).map(choice => <option key={choice.id} value={choice.id}>{choice.name}</option>)}</select></label>
    </fieldset>
  </>;
}
