import type {
  ThermostatActivity,
  ThermostatMode,
  ThermostatValues,
} from "./state";

export const modeWords: Record<ThermostatMode, string> = {
  off: "Off",
  heat: "Heat",
  cool: "Cool",
  heat_cool: "Heat & cool",
  auto: "Auto",
  dry: "Dry",
  fan_only: "Fan only",
};

/**
 * Activity words appear only beside a mode other than Off, so equipment that
 * reports "off" there reads as not running rather than repeating the mode.
 */
export const activityWords: Record<ThermostatActivity, string> = {
  off: "Not running",
  idle: "Idle",
  heating: "Heating",
  cooling: "Cooling",
  drying: "Drying",
  fan: "Fan running",
  preheating: "Preheating",
  defrosting: "Defrosting",
};

/**
 * The line under the targets: the mode and activity when targets lead, the
 * activity alone when the mode already leads, or nothing in Off.
 */
export const detailLine = (values: ThermostatValues, hasTargets: boolean) => {
  if (hasTargets) {
    return values.activity
      ? `${modeWords[values.mode]} · ${activityWords[values.activity]}`
      : modeWords[values.mode];
  }
  return values.mode !== "off" && values.activity
    ? activityWords[values.activity]
    : undefined;
};

/** Words for a pending range request, naming only the targets it changes. */
export const rangeWords = ({
  low,
  high,
  lowChanged,
  highChanged,
  unitLabel,
}: {
  low: string;
  high: string;
  lowChanged: boolean;
  highChanged: boolean;
  unitLabel: string;
}) => {
  if (lowChanged && !highChanged) {
    return `Setting heat to ${low} ${unitLabel}…`;
  }
  if (highChanged && !lowChanged) {
    return `Setting cool to ${high} ${unitLabel}…`;
  }
  return `Setting the range to ${low}–${high} ${unitLabel}…`;
};
