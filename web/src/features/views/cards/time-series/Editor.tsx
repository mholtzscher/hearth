import type { z } from "zod";
import { timeSeriesSchema } from "./schema";
import { TextField } from "../../editor/TextField";
import type { SeriesWindow } from "../../bindings/types";
import { quantityAxis } from "../../bindings/state-fields";
import type { Quantity } from "../../bindings/quantity";
import { FieldSelect } from "../../editor/FieldSelect";
import { Button } from "../../../../components/ui/button";
import { NativeSelect } from "../../../../components/ui/native-select";
import { EditorLabel } from "../../editor/EditorLabel";

const windows: Array<[SeriesWindow, string]> = [["1h", "Last hour"], ["6h", "Last 6 hours"], ["24h", "Last 24 hours"], ["7d", "Last 7 days"]];
export type SensorChoice = { id: string; name: string; type: string; support?: unknown; quantities?: Record<string, Quantity>; device_id?: string; device_name?: string };
export function TimeSeriesEditor({ value, choices, onChange }: { value: z.infer<typeof timeSeriesSchema>; choices: SensorChoice[]; onChange: (value: z.infer<typeof timeSeriesSchema>) => void }) {
  const window = value.series[0].source.window;
  const chartType = value.series[0].source.entity_type;
  const chartSource = value.series[0].source;
  const selected = choices.find(choice => choice.id === chartSource.entity_id);
  const path = chartSource.field ?? "";
  const deliveredUnit = selected?.quantities?.[path]?.unit;
  const candidates = choices.filter(choice => {
    if (choice.type !== chartType || value.series.some(item => item.source.entity_id === choice.id) || !deliveredUnit || choice.quantities?.[path]?.unit !== deliveredUnit) return false;
    try { return quantityAxis(chartSource, selected?.support) === quantityAxis(chartSource, choice.support); }
    catch { return false; }
  });
  return <>
    <FieldSelect source={chartSource} onChange={field => onChange({ ...value, series: value.series.map(item => ({ ...item, source: { ...item.source, field } })) })} />
    <TextField label="Chart title" value={value.title} schema={timeSeriesSchema.shape.title} errorMessage="Use 1–120 characters for the chart title." onChange={title => onChange({ ...value, title })} />
    <EditorLabel>Time window<NativeSelect size="auto" value={window} onChange={event => onChange({ ...value, series: value.series.map(item => ({ ...item, source: { ...item.source, window: event.target.value as SeriesWindow } })) })}>
      {windows.map(([window, label]) => <option key={window} value={window}>{label}</option>)}
    </NativeSelect></EditorLabel>
    <fieldset><legend>Series</legend>{value.series.map((item, index) => <div key={`${item.source.entity_id}:${item.source.field ?? ""}`}>
      <TextField label={`Series ${index + 1} label`} value={item.label ?? ""} placeholder={choices.find(choice => choice.id === item.source.entity_id)?.name ?? item.source.entity_id} optional schema={timeSeriesSchema.shape.series.element.shape.label} errorMessage="Use at most 120 characters for the series label." onChange={label => onChange({ ...value, series: value.series.map((entry, i) => i === index ? { ...entry, label: label || undefined } : entry) })} />
      <Button variant="outline" size="touch" type="button" disabled={value.series.length === 1} onClick={() => onChange({ ...value, series: value.series.filter((_, i) => i !== index) })}>Remove series {index + 1}</Button>
    </div>)}
      <EditorLabel>Add sensor<NativeSelect size="auto" value="" disabled={value.series.length >= 8} onChange={event => {
        if (event.target.value) onChange({ ...value, series: [...value.series, { source: { ...chartSource, entity_id: event.target.value, window } }] });
      }}><option value="">Choose a compatible sensor…</option>{candidates.map(choice => <option key={choice.id} value={choice.id}>{choice.name}</option>)}</NativeSelect></EditorLabel>
    </fieldset>
  </>;
}
