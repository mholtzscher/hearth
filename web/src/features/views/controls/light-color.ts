import { z } from "zod";
import type { FieldEntity } from "../bindings/entity";
import { supportsOperation } from "./support";

export type RGB = { red: number; green: number; blue: number };
export type HS = { hue: number; saturation: number };
export type ColorValue = RGB | HS;
export type TemperatureControl = { entity: FieldEntity; minimum: number; maximum: number; step: number };
export type ColorControl = { entity: FieldEntity; kind: "rgb" | "hs" };
export type LightColorControls = { temperature?: TemperatureControl; color?: ColorControl };

const channel = z.number().int().min(0).max(255);
const rgbSchema = z.object({ red: channel, green: channel, blue: channel });
const hsSchema = z.object({ hue: z.number().int().min(0).max(359), saturation: z.number().int().min(0).max(100) });
const temperatureSupport = z.object({
  state: z.object({ minimum: z.number().int().min(100).max(1000), maximum: z.number().int().min(100).max(1000) }),
  operations: z.object({ set: z.object({ step: z.number().int().min(1).max(100) }) }),
});

/** Never guess which endpoint to control when the same light has several of one type. */
export function lightColorControls(entities: FieldEntity[]): LightColorControls {
  const unique = (type: string) => {
    const matches = entities.filter(entity => entity.type === type);
    return matches.length === 1 && supportsOperation(matches[0], "set") ? matches[0] : undefined;
  };
  const controls: LightColorControls = {};
  const temperature = unique("hearth.colortemp/v1");
  if (temperature) {
    const parsed = temperatureSupport.safeParse(temperature.support);
    if (parsed.success) {
      const { minimum, maximum } = parsed.data.state, { step } = parsed.data.operations.set;
      // Core's step is a multiple from zero, not an offset from minimum.
      const first = Math.ceil(minimum / step) * step, last = Math.floor(maximum / step) * step;
      if (first <= last) controls.temperature = { entity: temperature, minimum: first, maximum: last, step };
    }
  }
  const rgb = unique("hearth.colorrgb/v1"), hs = unique("hearth.colorhs/v1");
  if (rgb) controls.color = { entity: rgb, kind: "rgb" };
  else if (hs) controls.color = { entity: hs, kind: "hs" };
  return controls;
}

export function readColor(value: unknown, kind: ColorControl["kind"]): ColorValue | undefined {
  const parsed = kind === "rgb" ? rgbSchema.safeParse(value) : hsSchema.safeParse(value);
  return parsed.success ? parsed.data : undefined;
}

export function colorRGB(value: ColorValue): RGB {
  if ("red" in value) return value;
  return hsRGB(value);
}

/** HSV with an explicit value channel; light brightness is a separate Entity. */
export function hsRGB({ hue, saturation }: HS, value = 255): RGB {
  const s = saturation / 100;
  const f = (n: number) => { const k = (n + hue / 60) % 6; return Math.round(value * (1 - s * Math.max(0, Math.min(k, 4 - k, 1)))); };
  return { red: f(5), green: f(3), blue: f(1) };
}

export function rgbHS({ red, green, blue }: RGB): HS {
  const max = Math.max(red, green, blue), min = Math.min(red, green, blue), delta = max - min;
  const raw = !delta ? 0 : max === red ? 60 * (((green - blue) / delta) % 6) : max === green ? 60 * ((blue - red) / delta + 2) : 60 * ((red - green) / delta + 4);
  return { hue: Math.round((raw + 360) % 360) % 360, saturation: max ? Math.round(delta / max * 100) : 0 };
}

export const colorHS = (value: ColorValue): HS => "hue" in value ? value : rgbHS(value);
export const colorHex = (value: ColorValue): string => "#" + Object.values(colorRGB(value)).map(channel => channel.toString(16).padStart(2, "0")).join("").toUpperCase();
export function parseHex(text: string): RGB | undefined {
  const value = text.replace(/^#/, "");
  if (!/^[0-9a-f]{6}$/i.test(value)) return undefined;
  return { red: parseInt(value.slice(0, 2), 16), green: parseInt(value.slice(2, 4), 16), blue: parseInt(value.slice(4, 6), 16) };
}
