import type { FieldEntity } from "../../bindings/entity";

export type ThermostatMode =
  | "off"
  | "heat"
  | "cool"
  | "heat_cool"
  | "auto"
  | "dry"
  | "fan_only";
export type ThermostatActivity =
  | "off"
  | "idle"
  | "heating"
  | "cooling"
  | "drying"
  | "fan"
  | "preheating"
  | "defrosting";

/** One projection, household or canonical, of hearth.thermostat/v1 State. */
export interface ThermostatValues {
  mode: ThermostatMode;
  activity?: ThermostatActivity;
  current_temperature?: number;
  target_temperature?: number;
  target_temperature_low?: number;
  target_temperature_high?: number;
  fan_mode?: string;
  current_humidity?: number;
}

/** The support facts the card uses. Temperatures are canonical milli-Celsius. */
export interface ThermostatSupport {
  modes: ThermostatMode[];
  fan_modes: string[];
  temperature_minimum: number;
  temperature_maximum: number;
}

/** The parsed State value; the shared Entity read already validated each field. */
export const thermostatValues = (
  entity: FieldEntity | undefined
): ThermostatValues | undefined =>
  entity?.state?.value as ThermostatValues | undefined;

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === "object" && value !== null;

/** support.state, or undefined when the Entity doesn't advertise it. */
export const thermostatSupport = (
  entity: FieldEntity | undefined
): ThermostatSupport | undefined => {
  const support = entity?.support;
  if (!isRecord(support) || !isRecord(support.state)) {
    return undefined;
  }
  const { modes, fan_modes, temperature_minimum, temperature_maximum } =
    support.state;
  if (
    !Array.isArray(modes) ||
    typeof temperature_minimum !== "number" ||
    typeof temperature_maximum !== "number"
  ) {
    return undefined;
  }
  return {
    modes: modes as ThermostatMode[],
    fan_modes: Array.isArray(fan_modes) ? (fan_modes as string[]) : [],
    temperature_minimum,
    temperature_maximum,
  };
};
