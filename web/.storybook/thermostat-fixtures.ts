import type { FieldEntity } from "../src/features/views/bindings/entity";
import type { Quantity } from "../src/features/views/bindings/quantity";
import type {
  ThermostatActivity,
  ThermostatMode,
} from "../src/features/views/cards/thermostat/state";

/** Synthetic hearth.thermostat/v1 State in canonical units: milli-Celsius and %. */
export interface ThermostatState {
  mode: ThermostatMode;
  activity?: ThermostatActivity;
  current_temperature?: number;
  target_temperature?: number;
  target_temperature_low?: number;
  target_temperature_high?: number;
  fan_mode?: string;
  current_humidity?: number;
}

export interface ThermostatFixture {
  id: string;
  name?: string;
  state: ThermostatState | null;
  /** The household projection: Fahrenheit (us) or Celsius (metric). */
  units?: "us" | "metric";
  /** A display unit the card can't convert, to exercise read-only targets. */
  displayUnit?: { unit: string; label: string };
  operations?: string[];
  modes?: ThermostatMode[];
  fanModes?: string[];
  enabled?: boolean;
  availability?: string;
  observedAt?: string;
}

const temperatureFields = [
  "current_temperature",
  "target_temperature",
  "target_temperature_low",
  "target_temperature_high",
] as const;

const roundTenth = (value: number) => Math.round(value * 10) / 10;

/**
 * The paired projections Core returns for one thermostat: household values
 * converted from canonical milli-Celsius, with quantities for present fields.
 */
export const thermostatEntities = ({
  id,
  name = "Hallway thermostat",
  state,
  units = "us",
  displayUnit,
  operations = ["set_mode", "set_range", "set_fan_mode"],
  modes = ["off", "heat", "cool", "heat_cool", "fan_only"],
  fanModes = ["Automatic", "Speed 1", "Speed 2", "Continuous"],
  enabled = true,
  availability = "available",
  observedAt = "2026-11-09T09:58:00Z",
}: ThermostatFixture): { display: FieldEntity; canonical: FieldEntity } => {
  const shownUnit = displayUnit ?? {
    unit: units === "us" ? "[degF]" : "Cel",
    label: units === "us" ? "°F" : "°C",
  };
  const toShown = (mCel: number) =>
    roundTenth(units === "us" ? (mCel / 1000) * 1.8 + 32 : mCel / 1000);
  const displayQuantities: Record<string, Quantity> = {};
  const canonicalQuantities: Record<string, Quantity> = {};
  const displayState: Record<string, unknown> = { ...state };
  for (const field of temperatureFields) {
    const value = state?.[field];
    if (value !== undefined) {
      displayState[field] = toShown(value);
      displayQuantities[`/${field}`] = { ...shownUnit, fraction_digits: 1 };
      canonicalQuantities[`/${field}`] = {
        unit: "mCel",
        label: "mCel",
        fraction_digits: 1,
      };
    }
  }
  if (state?.current_humidity !== undefined) {
    const percent = { unit: "%", label: "%", fraction_digits: 0 };
    displayQuantities["/current_humidity"] = percent;
    canonicalQuantities["/current_humidity"] = percent;
  }
  const base = {
    id,
    name,
    type: "hearth.thermostat/v1",
    enabled,
    availability: { status: availability },
    support: {
      state: {
        modes,
        fan_modes: fanModes,
        temperature_minimum: 10_000,
        temperature_maximum: 32_222,
        humidity_minimum: 0,
        humidity_maximum: 100,
      },
      operations: Object.fromEntries(
        operations.map((operation) => [operation, {}])
      ),
    },
  };
  const observation = (value: unknown) =>
    state ? { value, observed_at: observedAt } : null;
  return {
    display: {
      ...base,
      quantities: displayQuantities,
      state: observation(displayState),
    },
    canonical: {
      ...base,
      quantities: canonicalQuantities,
      state: observation(state),
    },
  };
};

const fromFahrenheit = (value: number) =>
  Math.round(((value - 32) * 1000) / 1.8);

/** A household °F State as canonical milli-Celsius, for the shared Core double. */
export const thermostatCanonicalState = (
  shown: Record<string, unknown>
): Record<string, unknown> => {
  const copy = { ...shown };
  for (const field of temperatureFields) {
    const value = copy[field];
    if (typeof value === "number") {
      copy[field] = fromFahrenheit(value);
    }
  }
  return copy;
};

/** A canonical State shown in household °F. */
export const thermostatShownState = (
  state: Record<string, unknown>
): Record<string, unknown> => {
  const copy = { ...state };
  for (const field of temperatureFields) {
    const value = copy[field];
    if (typeof value === "number") {
      copy[field] = roundTenth((value / 1000) * 1.8 + 32);
    }
  }
  return copy;
};

const runsHeat = (mode: ThermostatMode) =>
  mode === "heat" || mode === "heat_cool" || mode === "auto";
const runsCool = (mode: ThermostatMode) =>
  mode === "cool" || mode === "heat_cool" || mode === "auto";

/** Synthetic: what equipment would report doing under these settings. */
const activityFor = (state: ThermostatState): ThermostatActivity => {
  if (state.mode === "off") {
    return "off";
  }
  if (state.mode === "fan_only") {
    return "fan";
  }
  const room = state.current_temperature;
  const heatTo = state.target_temperature_low ?? state.target_temperature;
  const coolTo = state.target_temperature_high ?? state.target_temperature;
  if (
    room !== undefined &&
    heatTo !== undefined &&
    runsHeat(state.mode) &&
    room < heatTo
  ) {
    return "heating";
  }
  if (
    room !== undefined &&
    coolTo !== undefined &&
    runsCool(state.mode) &&
    room > coolTo
  ) {
    return "cooling";
  }
  return "idle";
};

/**
 * Applies a thermostat Command to canonical State as a satisfied device
 * would report it. Returns undefined for an Operation the type lacks.
 */
export const applyThermostatCommand = (
  state: ThermostatState,
  operation: string,
  parameters: Record<string, unknown>
): ThermostatState | undefined => {
  const patches: Record<string, Partial<ThermostatState>> = {
    set_mode: { mode: parameters.mode as ThermostatMode },
    set_target: { target_temperature: parameters.temperature as number },
    set_range: {
      target_temperature_low: parameters.low as number,
      target_temperature_high: parameters.high as number,
    },
    set_fan_mode: { fan_mode: parameters.fan_mode as string },
  };
  const patch = patches[operation];
  if (!patch) {
    return undefined;
  }
  const next = { ...state, ...patch };
  return next.activity === undefined
    ? next
    : { ...next, activity: activityFor(next) };
};
