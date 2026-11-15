import { describe, expect, it } from "vitest";

import type { FieldEntity } from "../bindings/entity";
import {
  colorHex,
  hsRGB,
  lightColorControls,
  parseHex,
  readColor,
  rgbHS,
} from "./light-color";

const defaultSupport = { state: {}, operations: { set: {} } };
const entity = (
  type: string,
  support: unknown = defaultSupport
): FieldEntity => ({
  id: type,
  type: `hearth.${type}/v1`,
  name: type,
  enabled: true,
  availability: { status: "available" },
  support,
  state: null,
});

describe("light color", () => {
  it("chooses supported targets even when inactive, and prefers native RGB precision", () => {
    const hs = entity("colorhs");
    const rgb = entity("colorrgb");
    expect(lightColorControls([hs, rgb]).color).toStrictEqual({
      entity: rgb,
      kind: "rgb",
    });
    expect(lightColorControls([hs]).color).toStrictEqual({
      entity: hs,
      kind: "hs",
    });
    expect(
      lightColorControls([
        entity("colorxy"),
        entity("colorrgb", { operations: {} }),
      ])
    ).toStrictEqual({});
  });

  it("does not guess an endpoint when color Entities are ambiguous", () => {
    expect(
      lightColorControls([
        entity("colorrgb"),
        { ...entity("colorrgb"), id: "another" },
      ])
    ).toStrictEqual({});
  });

  it("aligns temperature bounds to absolute step multiples and rejects unusable support", () => {
    const control = entity("colortemp", {
      state: { minimum: 153, maximum: 499 },
      operations: { set: { step: 5 } },
    });
    expect(lightColorControls([control]).temperature).toStrictEqual({
      entity: control,
      minimum: 155,
      maximum: 495,
      step: 5,
    });
    for (const support of [
      {
        state: { minimum: 155, maximum: 154 },
        operations: { set: { step: 1 } },
      },
      {
        state: { minimum: 153, maximum: 154 },
        operations: { set: { step: 5 } },
      },
      {
        state: { minimum: 153, maximum: 500 },
        operations: { set: { step: 0 } },
      },
      { state: {}, operations: { set: {} } },
    ]) {
      expect(lightColorControls([entity("colortemp", support)])).toStrictEqual(
        {}
      );
    }
  });

  it.each([
    [
      { hue: 0, saturation: 100 },
      { red: 255, green: 0, blue: 0 },
    ],
    [
      { hue: 120, saturation: 100 },
      { red: 0, green: 255, blue: 0 },
    ],
    [
      { hue: 240, saturation: 100 },
      { red: 0, green: 0, blue: 255 },
    ],
    [
      { hue: 205, saturation: 80 },
      { red: 51, green: 170, blue: 255 },
    ],
    [
      { hue: 359, saturation: 0 },
      { red: 255, green: 255, blue: 255 },
    ],
  ])(
    "converts whole-degree hue/saturation without changing light brightness: %j",
    (hs, rgb) => {
      expect(hsRGB(hs)).toStrictEqual(rgb);
    }
  );

  it("converts exact RGB to hex and parses valid or malformed hex", () => {
    expect(colorHex({ red: 128, green: 32, blue: 16 })).toBe("#802010");
    expect(parseHex("#802010")).toStrictEqual({
      red: 128,
      green: 32,
      blue: 16,
    });
    expect(parseHex("000000")).toStrictEqual({ red: 0, green: 0, blue: 0 });
    for (const invalid of ["bad", "#f00", "#zz0000", "#1234567"]) {
      expect(parseHex(invalid)).toBeUndefined();
    }
  });

  it("maps RGB black and red to hue/saturation and applies explicit brightness", () => {
    expect(rgbHS({ red: 0, green: 0, blue: 0 })).toStrictEqual({
      hue: 0,
      saturation: 0,
    });
    expect(rgbHS({ red: 255, green: 0, blue: 1 }).hue).toBe(0);
    expect(hsRGB({ hue: 0, saturation: 100 }, 128)).toStrictEqual({
      red: 128,
      green: 0,
      blue: 0,
    });
  });

  it("reads exact RGB color only when values are in range", () => {
    expect(
      readColor({ active: false, red: 128, green: 32, blue: 16 }, "rgb")
    ).toStrictEqual({ red: 128, green: 32, blue: 16 });
    expect(readColor({ red: 256, green: 0, blue: 0 }, "rgb")).toBeUndefined();
  });
});
