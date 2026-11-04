import { z } from "zod";
import { apiFetch } from "../../../api/client";
import { historySeriesRequest, historySeriesSchema } from "./history";
import { nativeNumericTypes } from "./types";
import type { NumericAdapter, NumericSource, SeriesData } from "./types";

// Brightness State is an integer scalar; the other native types carry fractions.
const stateValue = (type: string) => type === "hearth.brightness/v1" ? z.number().int() : z.number();
const timestamp = z.string().datetime({ offset: true });
const entity = z.object({
  id: z.string(), type: z.enum(nativeNumericTypes), name: z.string(), enabled: z.boolean(),
  availability: z.object({ status: z.string() }),
  state: z.object({ value: z.number(), observed_at: timestamp }).nullable(),
});
function readEntity(raw: unknown, source: NumericSource) {
  const parsed = entity.parse(raw);
  if (parsed.id !== source.entity_id || parsed.type !== source.entity_type) throw new Error("Entity identity/type mismatch");
  if (parsed.state) stateValue(parsed.type).parse(parsed.state.value);
  return parsed;
}
function format(unit: string) {
  if (unit !== "native") throw new Error("Unsupported native display unit");
  return { unit_label: "", fraction_digits: 1 };
}
export const nativeNumericAdapter: NumericAdapter = {
  stateRequest: source => ({ key: `entity-state:${source.entity_id}`, kind: "state",
    load: signal => apiFetch(`/v1/entities/${encodeURIComponent(source.entity_id)}`, { signal }),
  }),
  seriesRequest: (source, window) => historySeriesRequest(source, window),
  metadata(raw, source) {
    const data = readEntity(raw, source);
    return { id: data.id, label: data.name, enabled: data.enabled, availability: data.availability.status };
  },
  metric(raw, source, unit) {
    const data = readEntity(raw, source);
    const display = format(unit);
    return data.state ? { value: data.state.value, observed_at: data.state.observed_at, format: display } : null;
  },
  series(raw, _source, unit) {
    const data = historySeriesSchema.parse(raw);
    const display = format(unit);
    return { from: data.from, to: data.to, format: display, buckets: data.buckets.map(
      bucket => ({ start: bucket.start, end: bucket.end, min: bucket.min, max: bucket.max, mean: bucket.mean }),
    ) } satisfies SeriesData;
  },
};
