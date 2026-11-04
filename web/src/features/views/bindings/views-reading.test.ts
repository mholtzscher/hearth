import { describe, expect, it } from "vitest";
import { readingAdapter } from "./reading-adapter";
import { acceptsDisplay, displayKeys, displayOptions, readingTypes } from "./readings";
import { seriesBuckets } from "./history";

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
    expect(displayKeys).toEqual(["celsius", "fahrenheit", "hpa", "mps", "native", "percent"]);
  });
  it("pairs each type with only its own displays", () => {
    expect(acceptsDisplay("hearth.temperature/v1", "fahrenheit")).toBe(true);
    expect(acceptsDisplay("hearth.temperature/v1", "percent")).toBe(false);
    expect(acceptsDisplay("hearth.relativehumidity/v1", "native")).toBe(false);
    expect(acceptsDisplay("hearth.numericsensor/v1", "native")).toBe(true);
    expect(acceptsDisplay("hearth.power/v1", "native")).toBe(false);
    expect(acceptsDisplay("hearth.temperature/v1", "toString")).toBe(false);
    expect(displayOptions("hearth.temperature/v1")).toEqual([{ key: "celsius", name: "Celsius" }, { key: "fahrenheit", name: "Fahrenheit" }]);
  });
});

describe("reading adapter", () => {
  it.each([
    ["hearth.relativehumidity/v1", "percent", 45, 45, "%", 1],
    ["hearth.pressure/v1", "hpa", 1013.2, 1013.2, "hPa", 1],
    ["hearth.speed/v1", "mps", 3.4, 3.4, "m/s", 1],
    ["hearth.brightness/v1", "percent", 80, 80, "%", 0],
    ["hearth.temperature/v1", "celsius", 21500, 21.5, "°C", 1],
  ])("formats %s with %s", (type, display, raw, value, label, digits) => {
    const metric = readingAdapter.metric(entityOf(type, raw), source(type), display);
    expect(metric?.value).toBeCloseTo(value, 9);
    expect(metric?.format).toEqual({ unit_label: label, fraction_digits: digits });
  });
  it("converts temperature to fahrenheit only for presentation", () => {
    const entity = entityOf("hearth.temperature/v1", 21500);
    expect(readingAdapter.metric(entity, source(entity.type), "fahrenheit")?.value).toBeCloseTo(70.7, 9);
    expect(entity.state.value).toBe(21500);
  });
  it("labels generic readings with the Entity's support unit", () => {
    const type = "hearth.numericsensor/v1";
    const entity = entityOf(type, 120, { state: { minimum: 0, maximum: 255, unit: "lqi" }, operations: {} });
    expect(readingAdapter.metric(entity, source(type), "native")?.format).toEqual({ unit_label: "lqi", fraction_digits: 1 });
    expect(() => readingAdapter.metric(entityOf(type, 120), source(type), "native")).toThrow();
  });
  it("preserves resource condition and unobserved State", () => {
    const type = "hearth.pressure/v1";
    const entity = { ...entityOf(type, 1000), enabled: false, availability: { status: "unavailable" } };
    expect(readingAdapter.metadata(entity, source(type))).toMatchObject({ enabled: false, availability: "unavailable" });
    expect(readingAdapter.metric({ ...entity, state: null }, source(type), "hpa")).toBeNull();
  });
  it("rejects mismatched identity, unreadable types and unpaired displays", () => {
    const type = "hearth.relativehumidity/v1";
    const entity = entityOf(type, 45);
    expect(() => readingAdapter.metric({ ...entity, id: "other" }, source(type), "percent")).toThrow();
    expect(() => readingAdapter.metric({ ...entity, type: "hearth.temperature/v1" }, source(type), "percent")).toThrow();
    expect(() => readingAdapter.metric({ ...entity, type: "hearth.power/v1" }, source("hearth.power/v1"), "native")).toThrow();
    expect(() => readingAdapter.metric(entity, source(type), "native")).toThrow();
    expect(() => readingAdapter.metric(entity, source(type), "celsius")).toThrow();
  });
  it("converts series aggregates in the presentation unit without a sample count", () => {
    const type = "hearth.temperature/v1";
    const from = "2026-11-01T04:00:00Z";
    const buckets: Array<{ start: string; end: string; min: number | null; max: number | null; mean: number | null }> =
      Array.from({ length: seriesBuckets }, () => ({ start: from, end: "2026-11-01T04:01:15Z", min: null, max: null, mean: null }));
    buckets[0] = { start: from, end: "2026-11-01T04:01:15Z", min: 21000, max: 22000, mean: 21500 };
    const raw = { from, to: observed_at, bucket: { duration: "1m15s", alignment: "range_start" }, buckets };
    const series = readingAdapter.series(raw as never, source(type), "fahrenheit", entityOf(type, 21500));
    expect(series.from).toBe(from);
    expect(series.buckets).toHaveLength(seriesBuckets);
    expect(series.buckets[0].min).toBeCloseTo(69.8, 9);
    expect(series.buckets[0].max).toBeCloseTo(71.6, 9);
    expect(series.buckets[0].mean).toBeCloseTo(70.7, 9);
    expect(series.buckets[1]).toEqual({ start: from, end: "2026-11-01T04:01:15Z", min: null, max: null, mean: null });
    expect(series.format).toEqual({ unit_label: "°F", fraction_digits: 1 });
    expect("count" in series.buckets[0]).toBe(false);
    expect(() => readingAdapter.series({ to: observed_at, buckets }, source(type), "celsius", entityOf(type, 21500))).toThrow();
  });
});
