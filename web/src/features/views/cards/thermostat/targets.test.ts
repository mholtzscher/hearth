import { describe, expect, it } from "vitest";

import type { ThermostatSupport, ThermostatValues } from "./state";
import { stepTarget, targetsFor } from "./targets";
import type { Target } from "./targets";

const paired = { range: true, target: false };
const single = { range: false, target: true };
const readOnly = { range: false, target: false };

const shownPaired: ThermostatValues = {
  mode: "heat_cool",
  target_temperature_low: 69.8,
  target_temperature_high: 76,
};
const canonicalPaired: ThermostatValues = {
  mode: "heat_cool",
  target_temperature_low: 21_000,
  target_temperature_high: 24_444,
};
const support: ThermostatSupport = {
  modes: ["off", "heat", "cool", "heat_cool"],
  fan_modes: [],
  temperature_minimum: 10_000,
  temperature_maximum: 32_222,
};

const labels = (targets: Target[]) => targets.map((target) => target.label);

describe(targetsFor, () => {
  it.each([
    ["heat", ["Heat to"]],
    ["cool", ["Cool to"]],
    ["heat_cool", ["Heat to", "Cool to"]],
    ["auto", ["Heat to", "Cool to"]],
    ["dry", []],
    ["off", []],
    ["fan_only", []],
  ] as const)("shows a paired device's %s targets", (mode, want) => {
    const targets = targetsFor({
      mode,
      operations: paired,
      shown: { ...shownPaired, mode },
      canonical: { ...canonicalPaired, mode },
    });
    expect(labels(targets)).toStrictEqual(want);
    expect(
      targets.filter((target) => target.operation !== "set_range")
    ).toStrictEqual([]);
  });

  it.each([
    ["heat", ["Heat to"]],
    ["cool", ["Cool to"]],
    ["heat_cool", ["Target"]],
    ["dry", ["Target"]],
    ["off", []],
    ["fan_only", []],
  ] as const)("shows a single-target device's %s target", (mode, want) => {
    const targets = targetsFor({
      mode,
      operations: single,
      shown: { mode, target_temperature: 68 },
      canonical: { mode, target_temperature: 20_000 },
    });
    expect(labels(targets)).toStrictEqual(want);
  });

  it("shows a read-only device's reported targets without an Operation", () => {
    const targets = targetsFor({
      mode: "heat_cool",
      operations: readOnly,
      shown: shownPaired,
      canonical: canonicalPaired,
    });
    expect(labels(targets)).toStrictEqual(["Heat to", "Cool to"]);
    expect(targets.map((target) => target.operation)).toStrictEqual([
      undefined,
      undefined,
    ]);
  });
});

const [heat, cool] = targetsFor({
  mode: "heat_cool",
  operations: paired,
  shown: shownPaired,
  canonical: canonicalPaired,
});

describe(stepTarget, () => {
  it("steps an off-grid °F target onto the grid and sends both range values", () => {
    expect(
      stepTarget({
        target: heat,
        direction: 1,
        unit: "[degF]",
        support,
        canonical: canonicalPaired,
      })
    ).toStrictEqual({
      request: {
        operation: "set_range",
        parameters: { low: 21_111, high: 24_444 },
      },
      shown: 70,
    });
  });

  it("sends a single target in half degrees for a Celsius household", () => {
    const [target] = targetsFor({
      mode: "heat",
      operations: single,
      shown: { mode: "heat", target_temperature: 21 },
      canonical: { mode: "heat", target_temperature: 21_000 },
    });
    expect(
      stepTarget({
        target,
        direction: 1,
        unit: "Cel",
        support,
        canonical: { mode: "heat", target_temperature: 21_000 },
      })
    ).toStrictEqual({
      request: { operation: "set_target", parameters: { temperature: 21_500 } },
      shown: 21.5,
    });
  });

  it("refuses steps past the advertised bounds", () => {
    const top = { ...cool, value: 90, canonical: 32_222 };
    expect(
      stepTarget({
        target: top,
        direction: 1,
        unit: "[degF]",
        support,
        canonical: { ...canonicalPaired, target_temperature_high: 32_222 },
      })
    ).toBeUndefined();
  });

  it("refuses a range with the heat target above the cool target", () => {
    const close: ThermostatValues = {
      mode: "heat_cool",
      target_temperature_low: 24_444,
      target_temperature_high: 24_444,
    };
    const [low, high] = targetsFor({
      mode: "heat_cool",
      operations: paired,
      shown: {
        mode: "heat_cool",
        target_temperature_low: 76,
        target_temperature_high: 76,
      },
      canonical: close,
    });
    const step = (target: Target, direction: 1 | -1) =>
      stepTarget({
        target,
        direction,
        unit: "[degF]",
        support,
        canonical: close,
      });
    expect(step(low, 1)).toBeUndefined();
    expect(step(high, -1)).toBeUndefined();
    expect(step(low, -1)?.request.parameters).toStrictEqual({
      low: 23_889,
      high: 24_444,
    });
  });

  it("refuses unknown values, unconvertible units, and read-only targets", () => {
    const base = { direction: 1 as const, support, canonical: canonicalPaired };
    expect(
      stepTarget({
        ...base,
        target: { ...heat, value: undefined },
        unit: "[degF]",
      })
    ).toBeUndefined();
    expect(
      stepTarget({ ...base, target: heat, unit: undefined })
    ).toBeUndefined();
    expect(
      stepTarget({
        ...base,
        target: { ...heat, operation: undefined },
        unit: "[degF]",
      })
    ).toBeUndefined();
  });
});
