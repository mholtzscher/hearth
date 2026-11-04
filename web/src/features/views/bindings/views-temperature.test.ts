import { describe, expect, it } from "vitest";
import { temperatureAdapter } from "./temperature";
import { seriesBuckets } from "./history";

const source = { entity_id: "ent_019a3e6a-4111-7000-8000-000000000001", entity_type: "hearth.temperature/v1" };
const entity = { id: source.entity_id, type: source.entity_type, name: "Living room", enabled: false, availability: { status: "unavailable" }, state: { value: 21500, observed_at: "2026-11-01T10:00:00Z" } };
describe("temperature adapter", () => {
  it("converts only presentation values and preserves resource condition", () => {
    expect(temperatureAdapter.metric(entity, source, "fahrenheit")?.value).toBe(70.7);
    expect(temperatureAdapter.metric(entity, source, "celsius")?.value).toBe(21.5);
    expect(entity.state.value).toBe(21500);
    expect(temperatureAdapter.metadata(entity, source)).toMatchObject({ enabled: false, availability: "unavailable" });
    expect(temperatureAdapter.metric({ ...entity, state: null }, source, "celsius")).toBeNull();
  });
  it("rejects mismatched identity, type, units and invalid canonical readings", () => {
    for (const invalid of [{ ...entity, id: "other" }, { ...entity, type: "other" }, { ...entity, state: { ...entity.state, value: 1.5 } }]) {
      expect(() => temperatureAdapter.metric(invalid, source, "celsius")).toThrow();
    }
    expect(() => temperatureAdapter.metric(entity, source, "percent")).toThrow();
  });
  it("converts admitted series aggregates in the presentation unit without a sample count", () => {
    const from = "2026-11-01T04:00:00Z";
    const buckets: Array<{ start: string; end: string; min: number | null; max: number | null; mean: number | null }> =
      Array.from({ length: seriesBuckets }, () => ({ start: from, end: "2026-11-01T04:01:15Z", min: null, max: null, mean: null }));
    buckets[0] = { start: from, end: "2026-11-01T04:01:15Z", min: 21000, max: 22000, mean: 21500 };
    const series = temperatureAdapter.series({ from, to: "2026-11-01T10:00:00Z", bucket: { duration: "1m15s", alignment: "range_start" }, buckets } as never, source, "fahrenheit");
    expect(series.from).toBe(from);
    expect(series.buckets).toHaveLength(seriesBuckets);
    expect(series.buckets[0]).toEqual({ start: from, end: "2026-11-01T04:01:15Z", min: 69.8, max: 71.6, mean: 70.7 });
    expect(series.format).toEqual({ unit_label: "°F", fraction_digits: 1 });
    expect("count" in series.buckets[0]).toBe(false);
    expect(() => temperatureAdapter.series({ to: "2026-11-01T10:00:00Z", buckets }, source, "celsius")).toThrow();
  });
});
