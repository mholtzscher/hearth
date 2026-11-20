import type { Quantity } from "../../bindings/quantity";

/** The household temperature units a thermostat target can be written from. */
export type TemperatureUnit = "[degF]" | "Cel";

/**
 * The display quantity's unit, or undefined when the card can't convert it
 * back to milli-Celsius; such targets stay read-only.
 */
export const temperatureUnit = (
  quantity: Quantity | undefined
): TemperatureUnit | undefined => {
  if (quantity?.unit === "[degF]" || quantity?.unit === "Cel") {
    return quantity.unit;
  }
  return undefined;
};

/** 1 °F in Fahrenheit households, 0.5 °C in Celsius households. */
export const stepOf = (unit: TemperatureUnit) => (unit === "Cel" ? 0.5 : 1);

/**
 * A household value as the canonical integer milli-Celsius every thermostat
 * write uses (docs/guides/esphome-thermostat.md).
 */
export const toMilliCelsius = (value: number, unit: TemperatureUnit) =>
  unit === "Cel"
    ? Math.round(value * 1000)
    : Math.round(((value - 32) * 1000) / 1.8);

/** A value rounded to a quantity's fraction digits. */
export const roundTo = (value: number, fractionDigits: number) => {
  const scale = 10 ** fractionDigits;
  return Math.round(value * scale) / scale;
};

/** A canonical value in household units, rounded to the quantity's digits. */
export const fromMilliCelsius = (
  mCel: number,
  unit: TemperatureUnit,
  fractionDigits: number
) => {
  const celsius = mCel / 1000;
  return roundTo(unit === "Cel" ? celsius : celsius * 1.8 + 32, fractionDigits);
};

// Absorbs float error so a value already on the grid counts as on it.
const EPSILON = 1e-6;

/**
 * The next step-aligned value strictly above (1) or below (-1) the current
 * one, so an off-grid 69.8 °F steps up to 70 and down to 69.
 */
export const stepFrom = (
  value: number,
  unit: TemperatureUnit,
  direction: 1 | -1
) => {
  const step = stepOf(unit);
  const index =
    direction === 1
      ? Math.floor(value / step + EPSILON) + 1
      : Math.ceil(value / step - EPSILON) - 1;
  return index * step;
};

/** A target with its digits, dropping trailing zeros: 70.0 reads 70, 69.8 stays. */
export const formatTarget = (value: number, fractionDigits: number) => {
  const text = value.toFixed(fractionDigits);
  return text.includes(".") ? text.replace(/\.?0+$/u, "") : text;
};
