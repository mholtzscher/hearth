import { describe, expect, it } from "vitest";
import { nativeNumericAdapter } from "./native";
import { nativeNumericTypes } from "./types";

const state = { value: 41.5, observed_at: "2026-11-01T10:00:00Z" };
const display = { unit_label: "", fraction_digits: 1 };
for (const type of nativeNumericTypes) {
  const source = { entity_id: "ent_019a3e6a-4111-7000-8000-000000000001", entity_type: type };
  // Brightness is a canonical integer scalar; the other types carry fractions.
  const value = type === "hearth.brightness/v1" ? 42 : state.value;
  const entity = { id: source.entity_id, type, name: "Hallway", enabled: true, availability: { status: "available" }, state: { ...state, value } };
  describe(`native ${type} adapter`, () => {
    it("keeps canonical readings with a unitless display", () => {
      expect(nativeNumericAdapter.metric(entity, source, "native")).toEqual({ value, observed_at: state.observed_at, format: display });
      expect(nativeNumericAdapter.metadata(entity, source)).toMatchObject({ enabled: true, availability: "available" });
      expect(nativeNumericAdapter.metric({ ...entity, state: null }, source, "native")).toBeNull();
    });
    it("rejects mismatched identity, unknown types and unsupported units", () => {
      expect(() => nativeNumericAdapter.metric({ ...entity, id: "other" }, source, "native")).toThrow();
      expect(() => nativeNumericAdapter.metric({ ...entity, type: "hearth.temperature/v1" }, source, "native")).toThrow();
      expect(() => nativeNumericAdapter.metric(entity, source, "celsius")).toThrow();
      if (type === "hearth.brightness/v1") expect(() => nativeNumericAdapter.metric({ ...entity, state: { ...state, value: 0.5 } }, source, "native")).toThrow();
    });
  });
}
