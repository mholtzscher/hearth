import { z } from "zod";
import { apiFetch } from "../../../api/client";
import type { ReadRequest } from "../data/cache";
import { entityMetadata, parseFieldEntity, type FieldEntity } from "./entity";
import { isQuantityField, isTextField, pointer, stateFields } from "./state-fields";
import { stateWords } from "./words";
import type { EntitySource, MetricData, ResourceMetadata, TextData } from "./types";
import type { UnitSystem } from "../../units/systems";
import { quantityFormat } from "./quantity";

/** A Device card shows at most this many rows; Source still lists every Entity. */
export const MAX_DEVICE_ROWS = 12;

const deviceBody = z.object({ id: z.string(), name: z.string(), entities: z.array(z.object({ type: z.string() }).passthrough()), next_entity_cursor: z.string().nullish() });
export type FieldDevice = { id: string; name: string; entities: FieldEntity[] };

/** A Device and its readable Entities. Event sources and unknown types carry no State a card can show. */
export function parseDevice(raw: unknown, deviceId: string): FieldDevice {
  const data = deviceBody.parse(raw);
  if (data.id !== deviceId) throw new Error("Device identity mismatch");
  return { id: data.id, name: data.name, entities: data.entities.filter(entity => Object.prototype.hasOwnProperty.call(stateFields, entity.type)).map(parseFieldEntity) };
}

export function deviceRequest(deviceId: string, units: "household" | "canonical" = "household"): ReadRequest {
  return { key: `device:${deviceId}:${units}`, kind: "state",
    load: async signal => {
      const entities: FieldEntity[] = [];
      let cursor: string | undefined;
      let name = "Device";
      do {
        const query = new URLSearchParams({ entity_limit: "200", ...(units === "canonical" ? { units } : {}), ...(cursor ? { entity_cursor: cursor } : {}) });
        const page = await apiFetch(`/v1/devices/${encodeURIComponent(deviceId)}?${query}`, { signal }, text => deviceBody.parse(JSON.parse(text)));
        const device = parseDevice(page, deviceId);
        name = device.name;
        entities.push(...device.entities);
        cursor = page.next_entity_cursor ?? undefined;
      } while (cursor);
      return { id: deviceId, name, entities } satisfies FieldDevice;
    },
  };
}

/** One Entity's primary reading on a Device card. data is undefined when never observed or absent. */
export type DeviceReading = {
  metadata: ResourceMetadata;
  status: "ready" | "unobserved" | "absent";
  data?: MetricData | TextData;
};

// Flags and discriminators qualify a reading rather than being one.
const qualifiers = new Set(["/active", "/mode"]);

function title(path: string): string {
  const name = path.split("/").pop() ?? path;
  return name.length <= 1 ? name.toUpperCase() : name[0].toUpperCase() + name.slice(1);
}

function numeric(entity: FieldEntity, path: string): MetricData | undefined {
  const field = stateFields[entity.type][path];
  if (!field || !isQuantityField(field) || !entity.state) return undefined;
  const value = pointer(entity.state.value, path);
  if (typeof value !== "number") return undefined;
  const quantity = entity.quantities?.[path];
  if (!quantity) throw new Error("Missing quantity metadata");
  return { value, observed_at: entity.state.observed_at, format: quantityFormat(quantity), ...(quantity.range ? { range: quantity.range } : {}) };
}
function formatted(reading: MetricData): string {
  return reading.value.toFixed(reading.format.fraction_digits) + (reading.format.unit_label ? ` ${reading.format.unit_label}` : "");
}

/**
 * The fields that make up an Entity's primary reading: the State root when it is
 * a field, otherwise its named fields without qualifiers. A numeric setting
 * shows its value or its choice, following its mode.
 */
function primaryPaths(entity: FieldEntity): string[] {
  const fields = stateFields[entity.type];
  if (Object.prototype.hasOwnProperty.call(fields, "")) return [""];
  if (fields["/mode"] && entity.state) {
    const mode = pointer(entity.state.value, "/mode");
    if (typeof mode === "string" && fields[`/${mode}`]) return [`/${mode}`];
  }
  return Object.keys(fields).filter(path => !qualifiers.has(path));
}

/** An Entity's primary reading in the household's units. */
export function deviceReading(entity: FieldEntity, _system: UnitSystem): DeviceReading {
  const source: EntitySource = { entity_id: entity.id, entity_type: entity.type };
  const metadata = { ...entityMetadata(entity, source), label: entity.name };
  if (!entity.state) return { metadata, status: "unobserved" };
  const observed_at = entity.state.observed_at;
  const parts = primaryPaths(entity).flatMap<{ path: string; reading: MetricData | TextData }>(path => {
    const field = stateFields[entity.type][path];
    if (isQuantityField(field)) {
      const reading = numeric(entity, path);
      return reading ? [{ path, reading }] : [];
    }
    if (!isTextField(field)) return [];
    const text = stateWords(pointer(entity.state!.value, path), { ...source, field: path });
    return text === undefined ? [] : [{ path, reading: { text, observed_at } satisfies TextData }];
  });
  if (parts.length === 0) return { metadata, status: "absent" };
  if (parts.length === 1) return { metadata, status: "ready", data: parts[0].reading };
  const text = parts.map(({ path, reading }) => `${title(path)} ${"text" in reading ? reading.text : formatted(reading)}`).join(" · ");
  return { metadata, status: "ready", data: { text, observed_at } };
}

/** A light's power, brightness and color in one line, built from its Entities. */
export type LightReading = {
  power: FieldEntity; brightness: FieldEntity; color?: FieldEntity; mode?: FieldEntity;
  on?: boolean; level?: MetricData; colorText?: string; swatch?: string;
};

const colorTypes = ["hearth.colorhs/v1", "hearth.colorxy/v1", "hearth.colortemp/v1"] as const;
const colorModes: Record<string, (typeof colorTypes)[number]> = { hs: "hearth.colorhs/v1", xy: "hearth.colorxy/v1", color_temp: "hearth.colortemp/v1" };

/** The color Entity in use: the one the color mode names, else the active one, else the only one. */
function activeColor(entities: FieldEntity[]): FieldEntity | undefined {
  const colors = entities.filter(entity => (colorTypes as readonly string[]).includes(entity.type));
  const mode = entities.find(entity => entity.type === "hearth.colormode/v1")?.state?.value;
  const named = typeof mode === "string" ? colors.find(entity => entity.type === colorModes[mode]) : undefined;
  return named ?? colors.find(entity => entity.state && pointer(entity.state.value, "/active") === true) ?? (colors.length === 1 ? colors[0] : undefined);
}

/** The Entities a light composite consumes, so they aren't repeated as rows. */
export function lightEntities(light: LightReading, all: FieldEntity[]): Set<string> {
  const used = all.filter(entity => entity === light.power || entity === light.brightness || entity.type === "hearth.colormode/v1" ||
    (colorTypes as readonly string[]).includes(entity.type));
  return new Set(used.map(entity => entity.id));
}

export function lightReading(entities: FieldEntity[], system: UnitSystem, canonicalEntities: FieldEntity[] = []): LightReading | undefined {
  if (entities.filter(entity => entity.type === "hearth.power/v1").length !== 1 || entities.filter(entity => entity.type === "hearth.brightness/v1").length !== 1) return undefined;
  const power = entities.find(entity => entity.type === "hearth.power/v1");
  const brightness = entities.find(entity => entity.type === "hearth.brightness/v1");
  if (!power || !brightness) return undefined;
  const color = activeColor(entities);
  const mode = entities.find(entity => entity.type === "hearth.colormode/v1");
  const on = typeof power.state?.value === "boolean" ? power.state.value : undefined;
  const level = numeric(brightness, "");
  const reading = color ? deviceReading(color, system) : undefined;
  const colorText = reading?.data ? colorWords(color!, reading.data) : undefined;
  const canonicalColor = color && canonicalEntities.find(entity => entity.id === color.id);
  return { power, brightness, color, mode, on, level, colorText, swatch: canonicalColor ? swatchColor(canonicalColor) : undefined };
}

function colorWords(entity: FieldEntity, data: MetricData | TextData): string {
  if (entity.type === "hearth.colortemp/v1" && "value" in data) return `Color temperature ${formatted(data)}`;
  return "text" in data ? data.text : formatted(data);
}

/** An approximate on-screen color for the light's active color State. */
export function swatchColor(entity: FieldEntity): string | undefined {
  if (!entity.state) return undefined;
  const value = entity.state?.value;
  const get = (path: string) => { const v = pointer(value, path); return typeof v === "number" ? v : undefined; };
  switch (entity.type) {
    case "hearth.colorhs/v1": {
      const hue = get("/hue"), saturation = get("/saturation");
      return hue === undefined || saturation === undefined ? undefined : rgb(hsv(hue, saturation / 100));
    }
    case "hearth.colorxy/v1": {
      const x = get("/x"), y = get("/y");
      return x === undefined || y === undefined ? undefined : rgb(xyColor(x / 10_000, y / 10_000));
    }
    case "hearth.colortemp/v1": {
      const mireds = get("/value");
      return mireds === undefined || mireds <= 0 ? undefined : rgb(kelvin(1_000_000 / mireds));
    }
  }
  return undefined;
}

type RGB = [number, number, number];
const rgb = ([r, g, b]: RGB) => `rgb(${Math.round(r * 255)} ${Math.round(g * 255)} ${Math.round(b * 255)})`;
const clamp = (value: number) => Math.min(1, Math.max(0, value));

function hsv(hue: number, saturation: number): RGB {
  const f = (n: number) => { const k = (n + hue / 60) % 6; return 1 - saturation * Math.max(0, Math.min(k, 4 - k, 1)); };
  return [f(5), f(3), f(1)];
}
/** CIE xy at full luminance, normalized so the brightest channel is 1. */
function xyColor(x: number, y: number): RGB {
  if (y <= 0) return [1, 1, 1];
  const X = x / y, Z = (1 - x - y) / y;
  const linear: RGB = [3.2406 * X - 1.5372 - 0.4986 * Z, -0.9689 * X + 1.8758 + 0.0415 * Z, 0.0557 * X - 0.204 + 1.057 * Z];
  const peak = Math.max(...linear, 1e-6);
  return linear.map(c => { const v = clamp(c / peak); return v <= 0.0031308 ? 12.92 * v : 1.055 * v ** (1 / 2.4) - 0.055; }) as RGB;
}
/** A blackbody approximation (Tanner Helland) for 1000–40000 K. */
function kelvin(k: number): RGB {
  const t = Math.min(40_000, Math.max(1_000, k)) / 100;
  const r = t <= 66 ? 255 : 329.698727446 * (t - 60) ** -0.1332047592;
  const g = t <= 66 ? 99.4708025861 * Math.log(t) - 161.1195681661 : 288.1221695283 * (t - 60) ** -0.0755148492;
  const b = t >= 66 ? 255 : t <= 19 ? 0 : 138.5177312231 * Math.log(t - 10) - 305.0447927307;
  return [clamp(r / 255), clamp(g / 255), clamp(b / 255)];
}
