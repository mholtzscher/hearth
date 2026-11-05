import { describe, expect, it, vi } from "vitest";
import { quantityAdapter as readingAdapter } from "./quantity-adapter";
import { isQuantityField, stateFields, quantityTypes } from "./state-fields";
import { seriesBuckets } from "./history";
import { UcumLhcUtils } from "@lhncbc/ucum-lhc";
import { ReadCache } from "../data/cache";

const id = "ent_019a3e6a-4111-7000-8000-000000000001";
const observed_at = "2026-11-01T10:00:00Z";
function entityOf(type: string, value: unknown, support: unknown = { state: {}, operations: {} }) {
  return { id, type, name: "Hallway", enabled: true, availability: { status: "available" }, support, state: { value, observed_at } };
}
const source = (type: string, field?: string) => ({ entity_id: id, entity_type: type, field });

describe("State field catalog", () => {
  it("derives numeric eligibility without enabling nonnumeric cards", () => {
    expect(quantityTypes).toEqual([
      "hearth.brightness/v1", "hearth.colorhs/v1", "hearth.colortemp/v1", "hearth.colorxy/v1",
      "hearth.numericsensor/v1", "hearth.numericsetting/v1", "hearth.pressure/v1",
      "hearth.relativehumidity/v1", "hearth.speed/v1", "hearth.temperature/v1",
    ]);
  });
  it("declares UCUM-valid fixed units", () => {
    const ucum = UcumLhcUtils.getInstance();
    const fields = Object.values(stateFields).flatMap(fields => Object.values(fields));
    expect(fields).toHaveLength(21);
    expect(fields.filter(isQuantityField)).toHaveLength(12);
    for (const field of fields) {
      if (!isQuantityField(field) || typeof field.quantity.unit !== "string") continue;
      expect(ucum.validateUnitString(field.quantity.unit).status).toBe("valid");
    }
  });
});

describe("reading adapter", () => {
  it("rejects a malformed sibling field before replacing the shared cached response", async () => {
    vi.useFakeTimers();
    const cache = new ReadCache();
    try {
      const type = "hearth.colorhs/v1";
      const good = entityOf(type, { active: true, hue: 210, saturation: 64 });
      const fetch = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(new Response(JSON.stringify(good)));
      const hue = readingAdapter.stateRequest(source(type, "/hue"));
      cache.subscribe(hue, vi.fn());
      cache.subscribe(readingAdapter.stateRequest(source(type, "/saturation")), vi.fn());
      await vi.advanceTimersByTimeAsync(0);
      expect(cache.snapshot(hue.key).data).toEqual(good);
      expect(fetch).toHaveBeenCalledTimes(1);
      fetch.mockResolvedValueOnce(new Response(JSON.stringify(entityOf(type, { active: true, hue: 220, saturation: "bad" }))));
      await vi.advanceTimersByTimeAsync(10000);
      expect(fetch).toHaveBeenCalledTimes(2);
      expect(cache.snapshot(hue.key).data).toEqual(good);
      expect(cache.snapshot(hue.key).error).toBeInstanceOf(Error);
    } finally {
      cache.dispose();
      vi.restoreAllMocks();
      vi.useRealTimers();
    }
  });
  it.each([
    ["hearth.relativehumidity/v1", "us", 45, 45, "%", 1],
    ["hearth.pressure/v1", "metric", 1013.2, 1013.2, "hPa", 1],
    ["hearth.pressure/v1", "us", 1013.2, 29.919866023257644, "in Hg", 2],
    ["hearth.speed/v1", "us", 3.4, 7.605583392984967, "mi/h", 1],
    ["hearth.temperature/v1", "metric", 21500, 21.5, "°C", 1],
    ["hearth.temperature/v1", "us", 21500, 70.7, "°F", 1],
  ] as const)("formats %s for a %s household", (type, system, raw, value, label, digits) => {
    const entity = entityOf(type, raw);
    const metric = readingAdapter.metric(entity, source(type), system);
    if (!metric || "absent" in metric) throw new Error("Expected numeric metric");
    expect(metric.value).toBeCloseTo(value, 6);
    expect(metric.format).toEqual({ unit_label: label, fraction_digits: digits });
    expect(entity.state.value).toBe(raw);
  });
  it("labels generic readings with the Entity's support unit", () => {
    const type = "hearth.numericsensor/v1";
    const entity = entityOf(type, 120, { state: { minimum: 0, maximum: 255, unit: "lqi" }, operations: {} });
    expect(readingAdapter.metric(entity, source(type), "us")).toMatchObject({ value: 120, format: { unit_label: "lqi", fraction_digits: 1 } });
    expect(() => readingAdapter.metric(entityOf(type, 120), source(type), "us")).toThrow();
  });
  it("preserves resource condition and unobserved State", () => {
    const type = "hearth.pressure/v1";
    const entity = { ...entityOf(type, 1000), enabled: false, availability: { status: "unavailable" } };
    expect(readingAdapter.metadata(entity, source(type))).toMatchObject({ enabled: false, availability: "unavailable" });
    expect(readingAdapter.metric({ ...entity, state: null }, source(type), "metric")).toBeNull();
  });
  it.each(["metric", "us"] as const)("presents structured and canonical percentage fields in %s households", system => {
    const examples = [
      ["brightness", "", 51, { state: {}, operations: { set: {} } }, 51, "%", 0],
      ["brightness", "", 99, { state: {}, operations: { set: {} } }, 99, "%", 0],
      ["brightness", "", 100, { state: {}, operations: { set: {} } }, 100, "%", 0],
      ["colorhs", "/hue", { active: false, hue: 210, saturation: 64 }, {}, 210, "°", 0],
      ["colorhs", "/saturation", { active: true, hue: 210, saturation: 64 }, {}, 64, "%", 0],
      ["colortemp", "/value", { active: true, value: 370 }, {}, 370, "mired", 0],
      ["colorxy", "/x", { active: true, x: 3127, y: 3290 }, {}, 0.3127, "", 4],
      ["colorxy", "/y", { active: true, x: 3127, y: 3290 }, {}, 0.329, "", 4],
      ["numericsetting", "/value", { mode: "value", value: 40 }, { state: { unit: "%" } }, 40, "%", 1],
      ["numericsetting", "/value", { mode: "value", value: 0 }, {}, 0, "", 1],
    ] as const;
    for (const [name, field, raw, support, value, label, digits] of examples) {
      const type = `hearth.${name}/v1`;
      const metric = readingAdapter.metric(entityOf(type, raw, support), source(type, field), system);
      if (!metric || "absent" in metric) throw new Error("Expected numeric metric");
      expect(metric.value).toBeCloseTo(value, 6);
      expect(metric).toMatchObject({ observed_at, format: { unit_label: label, fraction_digits: digits } });
    }
  });
  it("distinguishes absent optional values from null State and malformed required data", () => {
    const type = "hearth.numericsetting/v1";
    const selected = source(type, "/value");
    const choice = entityOf(type, { mode: "choice", choice: "auto" });
    expect(readingAdapter.metric(choice, selected, "metric")).toEqual({ absent: true, observed_at });
    expect(readingAdapter.metric({ ...choice, state: null }, selected, "metric")).toBeNull();
    for (const value of [null, "40", true]) {
      expect(() => readingAdapter.metric(entityOf(type, { mode: "value", value }), selected, "metric")).toThrow();
    }
    expect(() => readingAdapter.metric(entityOf("hearth.colorhs/v1", { active: true, hue: 20 }), source("hearth.colorhs/v1", "/hue"), "metric")).toThrow();
    expect(() => readingAdapter.metric(choice, source(type, "/mode"), "metric")).toThrow();
  });
  it.each([null, 42, [], { state: null }, { state: 42 }, { state: [] }].map(support => ({ support })))("rejects malformed optional-unit support $support", ({ support }) => {
    const type = "hearth.numericsetting/v1";
    expect(() => readingAdapter.metric(entityOf(type, { mode: "value", value: 40 }, support), source(type, "/value"), "metric")).toThrow();
  });
  it("rejects mismatched identity and unreadable types", () => {
    const type = "hearth.relativehumidity/v1";
    const entity = entityOf(type, 45);
    expect(() => readingAdapter.metric({ ...entity, id: "other" }, source(type), "metric")).toThrow();
    expect(() => readingAdapter.metric({ ...entity, type: "hearth.temperature/v1" }, source(type), "metric")).toThrow();
    expect(() => readingAdapter.metric({ ...entity, type: "hearth.power/v1" }, source("hearth.power/v1"), "metric")).toThrow();
  });
  it.each(["metric", "us"] as const)("preserves canonical brightness history and gaps in %s households", system => {
    const type = "hearth.brightness/v1";
    const from = "2026-11-01T04:00:00Z";
    const buckets = Array.from({ length: seriesBuckets }, (_, index) => ({
      start: from, end: observed_at,
      min: index === 0 ? 50 : null, max: index === 0 ? 51 : null, mean: index === 0 ? 50.5 : null,
    }));
    const raw = { from, to: observed_at, bucket: { duration: "1m15s", alignment: "range_start" }, buckets };
    const series = readingAdapter.series(raw, source(type), system, entityOf(type, 51, { state: {}, operations: { set: {} } }));
    expect(series.buckets).toEqual(buckets);
    expect(series.format).toEqual({ unit_label: "%", fraction_digits: 0 });
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
