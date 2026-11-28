import { describe, expect, it } from "vitest";

import { entityId } from "../../../../../.storybook/fixtures";
import { bucketExtent, quantityKind, sortRows, usesRangePlot } from "./rank";
import type { RankedRow } from "./rank";

const row = (
  label: string,
  value: number | null,
  dimmed = false
): RankedRow => ({
  label,
  value,
  dimmed,
});

describe(sortRows, () => {
  it("sorts highest first and keeps rows without a value last", () => {
    const sorted = sortRows([
      row("Empty", null),
      row("Low", 2),
      row("High", 40, true),
      row("Mid", 10),
    ]);
    expect(sorted.map((entry) => entry.label)).toStrictEqual([
      "High",
      "Mid",
      "Low",
      "Empty",
    ]);
  });
});

describe(usesRangePlot, () => {
  const power = [row("A", 5), row("B", 0)];

  it("draws bars for a quantity where zero means none", () => {
    expect(usesRangePlot(power, [undefined, "power"], undefined)).toBeFalsy();
  });

  it("draws the range plot when a window is set", () => {
    expect(usesRangePlot(power, [], "24h")).toBeTruthy();
  });

  it("draws the range plot for kinds with no meaningful zero", () => {
    for (const kind of [
      "temperature",
      "atmospheric_pressure",
      "color_temperature",
      "color_hue",
      "chromaticity",
    ]) {
      expect(usesRangePlot(power, [kind], undefined)).toBeTruthy();
    }
  });

  it("draws the range plot when any value is negative", () => {
    expect(
      usesRangePlot([row("A", 5), row("B", -1)], [], undefined)
    ).toBeTruthy();
  });
});

describe(quantityKind, () => {
  it("reads a kind the catalog names", () => {
    expect(
      quantityKind(
        {
          entity_id: entityId(15),
          entity_type: "hearth.temperature/v1",
        },
        {}
      )
    ).toBe("temperature");
  });

  it("reads a kind the Entity's support declares", () => {
    const source = {
      entity_id: entityId(6),
      entity_type: "hearth.numericsensor/v1",
    };
    expect(quantityKind(source, { state: { quantity_kind: "power" } })).toBe(
      "power"
    );
    expect(quantityKind(source, { state: {} })).toBeUndefined();
  });
});

describe(bucketExtent, () => {
  it("spans every bucket with samples", () => {
    expect(
      bucketExtent([
        { min: null, max: null },
        { min: 3, max: 8 },
        { min: 1, max: 5 },
      ])
    ).toStrictEqual({ low: 1, high: 8 });
  });

  it("is undefined without samples", () => {
    expect(bucketExtent([{ min: null, max: null }])).toBeUndefined();
  });
});
