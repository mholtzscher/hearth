// The household display policy: preferred UCUM units per unit system, in
// priority order. A canonical unit converts to the first preferred unit with
// the same physical dimension; otherwise it is shown as stored. UCUM supplies
// every conversion factor and display symbol.
export type UnitSystem = "metric" | "us";

export const unitSystems: Readonly<Record<UnitSystem, readonly string[]>> = {
  metric: ["Cel"],
  us: ["[degF]", "[mi_i]/h", "[in_i'Hg]"],
};

// Decimal places for converted values; other converted units use one.
export const precisionOverrides: Readonly<Record<string, number>> = {
  "[in_i'Hg]": 2,
};

// Presentation choices, not conversion factors. Scaling still comes from UCUM.
export const presentationOverrides: Readonly<Record<string, { label: string; fraction_digits?: number; to?: string }>> = {
  "1": { label: "" },
  "/MK": { label: "mired", fraction_digits: 0 },
  "10*-4": { to: "1", label: "", fraction_digits: 4 },
};
