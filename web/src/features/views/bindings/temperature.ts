import { z } from "zod";
import { apiFetch } from "../../../api/client";
import type { NumericAdapter, NumericSource } from "./types";

const value = z.number().int().min(-273150).max(1000000);
const timestamp = z.string().datetime({ offset: true });
const entity = z.object({
  id: z.string(), type: z.literal("hearth.temperature/v1"), name: z.string(), enabled: z.boolean(),
  availability: z.object({ status: z.string() }),
  state: z.object({ value, observed_at: timestamp }).nullable(),
});
const series = z.object({
  entity_id: z.string(), from: timestamp, to: timestamp,
  buckets: z.array(z.object({
    start: timestamp, end: timestamp, count: z.number().int().nonnegative(),
    min_milli_celsius: value.nullable(), max_milli_celsius: value.nullable(),
    mean_milli_celsius: z.number().min(-273150).max(1000000).nullable(),
  })).length(288),
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
  seriesRequest: (source, window) => ({ key: `entity-series:${source.entity_id}:${window}`, kind: "series",
    load: (signal, to) => {
      const from = new Date(to.getTime() - (window === "6h" ? 6 : 24) * 3600_000);
      const query = new URLSearchParams({ from: from.toISOString(), to: to.toISOString() });
      return apiFetch(`/v1/entities/${encodeURIComponent(source.entity_id)}/temperature-series?${query}`, { signal });
    },
  }),
  metadata(raw, source) {
    const data = readEntity(raw, source);
    return { id: data.id, label: data.name, enabled: data.enabled, availability: data.availability.status };
  },
  metric(raw, source, unit) {
    const data = readEntity(raw, source);
    const display = format(unit);
    return data.state ? { value: convert(data.state.value, unit), observed_at: data.state.observed_at, format: display } : null;
  },
  series(raw, source, unit) {
    const data = series.parse(raw);
    if (data.entity_id !== source.entity_id) throw new Error("Series Entity identity mismatch");
    const display = format(unit);
    const buckets = data.buckets.map(bucket => {
      const { min_milli_celsius: min, max_milli_celsius: max, mean_milli_celsius: mean } = bucket;
      if ((bucket.count === 0 && (min !== null || max !== null || mean !== null)) ||
          (bucket.count > 0 && (min === null || max === null || mean === null || min > mean || mean > max))) throw new Error("Invalid series aggregates");
      return { start: bucket.start, end: bucket.end, count: bucket.count,
        min: min === null ? null : convert(min, unit), max: max === null ? null : convert(max, unit), mean: mean === null ? null : convert(mean, unit) };
    });
    return { from: data.from, to: data.to, format: display, buckets };
  },
};
