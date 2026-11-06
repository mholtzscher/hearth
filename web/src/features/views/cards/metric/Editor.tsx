import type { z } from "zod";
import { metricSchema } from "./schema";
import { TextField } from "../../editor/TextField";
import { FieldSelect } from "../../editor/FieldSelect";
import { quantityField } from "../../bindings/state-fields";
import { seriesWindows } from "../../bindings/schema";
import type { SeriesWindow } from "../../bindings/types";

export function MetricEditor({ value, onChange }: { value: z.infer<typeof metricSchema>; onChange: (value: z.infer<typeof metricSchema>) => void }) {
  return <>
    <FieldSelect source={value.source} onChange={field => {
      const source = { ...value.source, field };
      onChange({ ...value, source, range: quantityField(source)?.quantity.range ? value.range : undefined });
    }} />
    <TextField label="Label" value={value.label ?? ""} optional schema={metricSchema.shape.label} errorMessage="Use at most 120 characters for the label." onChange={label => onChange({ ...value, label: label || undefined })} />
    <label>Trend<select value={value.trend ?? ""} onChange={event => onChange({ ...value, trend: event.target.value ? event.target.value as SeriesWindow : undefined })}>
      <option value="">No trend</option>{seriesWindows.map(window => <option key={window} value={window}>{window}</option>)}
    </select></label>
    {quantityField(value.source)?.quantity.range && <label className="view-checkbox"><input type="checkbox" checked={!!value.range} onChange={event => onChange({ ...value, range: event.target.checked ? true : undefined })} />Show range</label>}
  </>;
}
