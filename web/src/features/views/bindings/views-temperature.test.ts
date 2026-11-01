import { describe, expect, it } from "vitest";
import { temperatureAdapter } from "./temperature";

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
});
