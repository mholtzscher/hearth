import type {
  ThermostatMode,
  ThermostatSupport,
  ThermostatValues,
} from "./state";
import { stepFrom, toMilliCelsius } from "./units";
import type { TemperatureUnit } from "./units";

export type TargetKey = "low" | "high" | "single";
/** The Operation that writes a target; undefined when the Entity can't. */
export type TargetOperation = "set_range" | "set_target";

export interface Target {
  key: TargetKey;
  /** "Heat to", "Cool to", or "Target". */
  label: string;
  /** Household value shown, the requested one while a Command is pending. */
  value: number | undefined;
  /** Canonical milli-Celsius, the basis for bound and order checks. */
  canonical: number | undefined;
  operation: TargetOperation | undefined;
}

export const targetFields = {
  low: "target_temperature_low",
  high: "target_temperature_high",
  single: "target_temperature",
} as const;

const usesHeat = (mode: ThermostatMode) =>
  mode === "heat" || mode === "heat_cool" || mode === "auto";
const usesCool = (mode: ThermostatMode) =>
  mode === "cool" || mode === "heat_cool" || mode === "auto";

const singleLabel = (mode: ThermostatMode) => {
  if (mode === "heat") {
    return "Heat to";
  }
  if (mode === "cool") {
    return "Cool to";
  }
  return "Target";
};

/**
 * The targets the mode uses. A paired-target device reports both targets in
 * every mode, but only heat uses the low one and only cool the high one; Off
 * and Fan only use none.
 */
export const targetsFor = ({
  mode,
  operations,
  shown,
  canonical,
}: {
  mode: ThermostatMode;
  operations: { range: boolean; target: boolean };
  shown: ThermostatValues;
  canonical: ThermostatValues;
}): Target[] => {
  if (mode === "off" || mode === "fan_only") {
    return [];
  }
  const target = (key: TargetKey, label: string): Target => ({
    key,
    label,
    value: shown[targetFields[key]],
    canonical: canonical[targetFields[key]],
    operation: undefined,
  });
  // Without a setter, a read-only Entity shows whichever targets it reports.
  const paired =
    operations.range ||
    (!operations.target &&
      (shown.target_temperature_low !== undefined ||
        shown.target_temperature_high !== undefined));
  if (paired) {
    const operation = operations.range ? "set_range" : undefined;
    const found: Target[] = [];
    if (usesHeat(mode)) {
      found.push({ ...target("low", "Heat to"), operation });
    }
    if (usesCool(mode)) {
      found.push({ ...target("high", "Cool to"), operation });
    }
    return found;
  }
  if (!operations.target && shown.target_temperature === undefined) {
    return [];
  }
  return [
    {
      ...target("single", singleLabel(mode)),
      operation: operations.target ? "set_target" : undefined,
    },
  ];
};

export type TargetRequest =
  | { operation: "set_target"; parameters: { temperature: number } }
  | { operation: "set_range"; parameters: { low: number; high: number } };

export interface StepResult {
  request: TargetRequest;
  /** The household value the control shows while the Command is pending. */
  shown: number;
}

/**
 * One −/+ press. Steps on the household grid, converts to milli-Celsius, and
 * refuses a value outside the advertised bounds or a range with low above
 * high. A range write carries both targets: the stepped one and the other's
 * shown canonical value, which is the pending request while one is in flight.
 */
export const stepTarget = ({
  target,
  direction,
  unit,
  support,
  canonical,
}: {
  target: Target;
  direction: 1 | -1;
  unit: TemperatureUnit | undefined;
  support: ThermostatSupport | undefined;
  canonical: ThermostatValues;
}): StepResult | undefined => {
  if (!target.operation || target.value === undefined || !unit || !support) {
    return undefined;
  }
  const shown = stepFrom(target.value, unit, direction);
  const next = toMilliCelsius(shown, unit);
  const inBounds = (value: number) =>
    value >= support.temperature_minimum &&
    value <= support.temperature_maximum;
  if (!inBounds(next)) {
    return undefined;
  }
  if (target.operation === "set_target") {
    return {
      request: { operation: "set_target", parameters: { temperature: next } },
      shown,
    };
  }
  const low = target.key === "low" ? next : canonical.target_temperature_low;
  const high = target.key === "high" ? next : canonical.target_temperature_high;
  // Core checks both range values, so an out-of-bounds partner refuses too.
  if (
    low === undefined ||
    high === undefined ||
    low > high ||
    !inBounds(low) ||
    !inBounds(high)
  ) {
    return undefined;
  }
  return {
    request: { operation: "set_range", parameters: { low, high } },
    shown,
  };
};
