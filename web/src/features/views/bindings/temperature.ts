import { z } from "zod";
import { apiFetch } from "../../../api/client";
import { historySeriesRequest, historySeriesSchema, type HistorySeries } from "./history";
import type { NumericAdapter, NumericSource, SeriesData } from "./types";

const value = z.number().int().min(-273150).max(1000000);
const timestamp = z.string().datetime({ offset: true });
const entity = z.object({
  id: z.string(), type: z.literal("hearth.temperature/v1"), name: z.string(), enabled: z.boolean(),
  availability: z.object({ status: z.string() }),
  state: z.object({ value, observed_at: timestamp }).nullable(),
});
function readEntity(raw: unknown, source: NumericSource) {
  const parsed = entity.parse(raw);
  if (parsed.id !== source.entity_id || parsed.type !== source.entity_type) throw new Error("Entity identity/type mismatch");
  return parsed;
}
function format(unit: string) {
  if (unit !== "celsius" && unit !== "fahrenheit") throw new Error("Unsupported temperature unit");
  return { unit_label: unit === "celsius" ? "°C" : "°F", fraction_digits: 1 };
}
function convert(value: number, unit: string) { return unit === "fahrenheit" ? value / 1000 * 9 / 5 + 32 : value / 1000; }
export const temperatureAdapter: NumericAdapter = {
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
    return data.state ? { value: convert(data.state.value, unit), observed_at: data.state.observed_at, format: display } : null;
  },
  series(raw, _source, unit) {
    const data: HistorySeries = historySeriesSchema.parse(raw);
    const display = format(unit);
    const buckets = data.buckets.map(bucket => ({
      start: bucket.start, end: bucket.end,
      min: bucket.min === null ? null : convert(bucket.min, unit),
      max: bucket.max === null ? null : convert(bucket.max, unit),
      mean: bucket.mean === null ? null : convert(bucket.mean, unit),
    }));
    return { from: data.from, to: data.to, format: display, buckets } satisfies SeriesData;
  },
};
