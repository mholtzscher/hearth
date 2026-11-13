import { expect, it } from "vitest";
import { formatEntityState } from "./entity-state";

const state = (value: unknown) => ({ value, observation_id: "obs", adapter_received_at: "", observed_at: "" });
it("formats projected State and nested fields with supplied metadata", () => {
  expect(formatEntityState({ state: state(70.7), quantities: { "": { unit: "[degF]", label: "°F", fraction_digits: 1 } } })).toBe("70.7 °F");
  expect(formatEntityState({ state: state({ active: true, x: 0.3127, y: 0.329 }), quantities: {
    "/x": { unit: "1", label: "", fraction_digits: 4 }, "/y": { unit: "1", label: "", fraction_digits: 4 },
  } })).toBe("{active: true, x: 0.3127, y: 0.3290}");
  expect(formatEntityState({ state: null, quantities: { "": { unit: "Cel", label: "°C", fraction_digits: 1 } } })).toBe("—");
});

it("shows root text as reported, including an empty string", () => {
  expect(formatEntityState({ state: state("Good"), quantities: {} })).toBe("Good");
  expect(formatEntityState({ state: state(""), quantities: {} })).toBe("");
  expect(formatEntityState({ state: state({ choice: "Auto" }), quantities: {} })).toBe('{choice: "Auto"}');
});
