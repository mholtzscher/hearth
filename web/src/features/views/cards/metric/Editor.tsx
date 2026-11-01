import type { z } from "zod";
import type { metricSchema } from "./schema";

export function MetricEditor({ value, onChange }: { value: z.infer<typeof metricSchema>; onChange: (value: z.infer<typeof metricSchema>) => void }) {
  return <>
    <label>Label<input value={value.label ?? ""} maxLength={120} onChange={event => onChange({ ...value, label: event.target.value || undefined })} /></label>
    <label>Units<select value={value.display.unit} onChange={event => onChange({ ...value, display: { unit: event.target.value === "fahrenheit" ? "fahrenheit" : "celsius" } })}>
      <option value="celsius">Celsius</option><option value="fahrenheit">Fahrenheit</option>
    </select></label>
  </>;
}
