import { describe, expect, it } from "vitest";
import { readingAdapter } from "./reading-adapter";
import { readings, readingTypes } from "./readings";
import { seriesBuckets } from "./history";
import { displayUnit } from "../../units/units";

const id = "ent_019a3e6a-4111-7000-8000-000000000001";
const observed_at = "2026-11-01T10:00:00Z";
function entityOf(type: string, value: number, support: unknown = { state: {}, operations: {} }) {
  return { id, type, name: "Hallway", enabled: true, availability: { status: "available" }, support, state: { value, observed_at } };
}
const source = (type: string) => ({ entity_id: id, entity_type: type });

describe("reading catalog", () => {
  it("is generated from the six quantity Entity types", () => {
    expect(readingTypes).toEqual([
      "hearth.brightness/v1", "hearth.numericsensor/v1", "hearth.pressure/v1",
      "hearth.relativehumidity/v1", "hearth.speed/v1", "hearth.temperature/v1",
    ]);
  });
  it("names only canonical units that UCUM can display", () => {
    for (const reading of Object.values(readings)) {
      if (!("fixed" in reading.unit)) continue;
      // A valid UCUM unit gets a print symbol rather than echoing its code back.
      expect(displayUnit(reading.unit.fixed, reading.value_type, "metric").label).not.toBe("");
    }
  });
});

describe("reading adapter", () => {
  it.each([
    ["hearth.relativehumidity/v1", "us", 45, 45, "%", 1],
    ["hearth.pressure/v1", "metric", 1013.2, 1013.2, "hPa", 1],
    ["hearth.pressure/v1", "us", 1013.2, 29.919866023257644, "in Hg", 2],
    ["hearth.speed/v1", "us", 3.4, 7.605583392984967, "mi/h", 1],
    ["hearth.brightness/v1", "metric", 80, 80, "%", 0],
    ["hearth.temperature/v1", "metric", 21500, 21.5, "°C", 1],
    ["hearth.temperature/v1", "us", 21500, 70.7, "°F", 1],
  ] as const)("formats %s for a %s household", (type, system, raw, value, label, digits) => {
    const entity = entityOf(type, raw);
    const metric = readingAdapter.metric(entity, source(type), system);
    expect(metric?.value).toBeCloseTo(value, 6);
    expect(metric?.format).toEqual({ unit_label: label, fraction_digits: digits });
    expect(entity.state.value).toBe(raw);
  });
  it("labels generic readings with the Entity's support unit", () => {
    const type = "hearth.numericsensor/v1";
    const entity = entityOf(type, 120, { state: { minimum: 0, maximum: 255, unit: "lqi" }, operations: {} });
    expect(readingAdapter.metric(entity, source(type), "us")?.format).toEqual({ unit_label: "lqi", fraction_digits: 1 });
    expect(() => readingAdapter.metric(entityOf(type, 120), source(type), "us")).toThrow();
  });
  it("preserves resource condition and unobserved State", () => {
    const type = "hearth.pressure/v1";
    const entity = { ...entityOf(type, 1000), enabled: false, availability: { status: "unavailable" } };
    expect(readingAdapter.metadata(entity, source(type))).toMatchObject({ enabled: false, availability: "unavailable" });
    expect(readingAdapter.metric({ ...entity, state: null }, source(type), "metric")).toBeNull();
  });
  it("rejects mismatched identity and unreadable types", () => {
    const type = "hearth.relativehumidity/v1";
    const entity = entityOf(type, 45);
    expect(() => readingAdapter.metric({ ...entity, id: "other" }, source(type), "metric")).toThrow();
    expect(() => readingAdapter.metric({ ...entity, type: "hearth.temperature/v1" }, source(type), "metric")).toThrow();
    expect(() => readingAdapter.metric({ ...entity, type: "hearth.power/v1" }, source("hearth.power/v1"), "metric")).toThrow();
  });
  it("converts series aggregates for the household without a sample count", () => {
    const type = "hearth.temperature/v1";
    const from = "2026-11-01T04:00:00Z";
    const buckets: Array<{ start: string; end: string; min: number | null; max: number | null; mean: number | null }> =
      Array.from({ length: seriesBuckets }, () => ({ start: from, end: "2026-11-01T04:01:15Z", min: null, max: null, mean: null }));
    buckets[0] = { start: from, end: "2026-11-01T04:01:15Z", min: 21000, max: 22000, mean: 21500 };
    const raw = { from, to: observed_at, bucket: { duration: "1m15s", alignment: "range_start" }, buckets };
    const series = readingAdapter.series(raw as never, source(type), "us", entityOf(type, 21500));
    expect(series.from).toBe(from);
    expect(series.buckets).toHaveLength(seriesBuckets);
    expect(series.buckets[0].min).toBeCloseTo(69.8, 6);
    expect(series.buckets[0].max).toBeCloseTo(71.6, 6);
    expect(series.buckets[0].mean).toBeCloseTo(70.7, 6);
    expect(series.buckets[1]).toEqual({ start: from, end: "2026-11-01T04:01:15Z", min: null, max: null, mean: null });
    expect(series.format).toEqual({ unit_label: "°F", fraction_digits: 1 });
    expect("count" in series.buckets[0]).toBe(false);
    expect(() => readingAdapter.series({ to: observed_at, buckets }, source(type), "metric", entityOf(type, 21500))).toThrow();
  });
});
