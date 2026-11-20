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
