// Weather words and derived values. Inputs use fixed working units: Celsius,
// percent, metres per second, hectopascals, and millimetres per hour.

/** NWS heat index: the Rothfusz regression with its published adjustments, in °F. */
function heatIndexF(t: number, rh: number): number {
  const simple = 0.5 * (t + 61 + (t - 68) * 1.2 + rh * 0.094);
  if ((simple + t) / 2 < 80) return simple;
  let hi = -42.379 + 2.04901523 * t + 10.14333127 * rh - 0.22475541 * t * rh - 0.00683783 * t * t
    - 0.05481717 * rh * rh + 0.00122874 * t * t * rh + 0.00085282 * t * rh * rh - 0.00000199 * t * t * rh * rh;
  if (rh < 13 && t >= 80 && t <= 112) hi -= ((13 - rh) / 4) * Math.sqrt((17 - Math.abs(t - 95)) / 17);
  else if (rh > 85 && t >= 80 && t <= 87) hi += ((rh - 85) / 10) * ((87 - t) / 5);
  return hi;
}

/** NWS wind chill in °F, defined at or below 50 °F with wind above 3 mph. */
function windChillF(t: number, mph: number): number {
  const v = Math.pow(mph, 0.16);
  return 35.74 + 0.6215 * t - 35.75 * v + 0.4275 * t * v;
}

export type FeelsLike = { celsius: number; method: "heat index" | "wind chill" };

/** Undefined when neither NWS formula applies, so a card shows only the air temperature. */
export function feelsLike(celsius: number, humidity?: number, windMs?: number): FeelsLike | undefined {
  const f = celsius * 9 / 5 + 32;
  if (humidity !== undefined && f >= 80 && humidity >= 40) return { celsius: (heatIndexF(f, humidity) - 32) * 5 / 9, method: "heat index" };
  const mph = windMs === undefined ? undefined : windMs * 3600 / 1609.344;
  if (mph !== undefined && f <= 50 && mph > 3) return { celsius: (windChillF(f, mph) - 32) * 5 / 9, method: "wind chill" };
  return undefined;
}

/** Magnus formula with the Alduchov and Eskridge coefficients. */
export function dewPoint(celsius: number, humidity: number): number {
  const a = 17.625, b = 243.04;
  const gamma = Math.log(Math.max(humidity, 1) / 100) + a * celsius / (b + celsius);
  return b * gamma / (a - gamma);
}

// Upper speed bound of Beaufort forces 0–11 in m/s; force 12 has no upper bound.
const beaufortUpper = [0.5, 1.5, 3.3, 5.5, 7.9, 10.7, 13.8, 17.1, 20.7, 24.4, 28.4, 32.6];
export const beaufortNames = ["Calm", "Light air", "Light breeze", "Gentle breeze", "Moderate breeze", "Fresh breeze",
  "Strong breeze", "Near gale", "Gale", "Strong gale", "Storm", "Violent storm", "Hurricane force"] as const;

export function beaufort(ms: number): number {
  const force = beaufortUpper.findIndex(upper => ms < upper);
  return force === -1 ? 12 : force;
}

/** Everyday words for a summary line, less formal than the Beaufort names. */
export function windWord(ms: number): string {
  const force = beaufort(ms);
  return force === 0 ? "Calm" : force <= 2 ? "Light wind" : force <= 4 ? "Breezy" : force <= 6 ? "Windy" : force <= 9 ? "Very windy" : "Storm-force wind";
}

const points = ["N", "NNE", "NE", "ENE", "E", "ESE", "SE", "SSE", "S", "SSW", "SW", "WSW", "W", "WNW", "NW", "NNW"];
const pointWords = ["north", "north-northeast", "northeast", "east-northeast", "east", "east-southeast", "southeast", "south-southeast",
  "south", "south-southwest", "southwest", "west-southwest", "west", "west-northwest", "northwest", "north-northwest"];

/** The nearest of 16 compass points for a bearing in degrees. */
export function compassPoint(degrees: number): { short: string; words: string } {
  const index = Math.round(((degrees % 360) + 360) % 360 / 22.5) % 16;
  return { short: points[index], words: pointWords[index] };
}

/** AMS rain intensity: light below 2.5 mm/h, heavy above 7.6 mm/h. Undefined when dry. */
export function rainWord(mmPerHour: number): string | undefined {
  if (mmPerHour <= 0) return undefined;
  return mmPerHour < 2.5 ? "Light rain" : mmPerHour <= 7.6 ? "Rain" : "Heavy rain";
}

export type Tendency = { direction: "rising" | "falling" | "steady"; words: string };

/** Met Office barometric tendency for a three-hour change in hPa. */
export function pressureTendency(change: number): Tendency {
  const size = Math.abs(change);
  if (size < 0.1) return { direction: "steady", words: "Steady" };
  const pace = size <= 1.5 ? " slowly" : size <= 3.5 ? "" : size <= 6 ? " quickly" : " very rapidly";
  return change > 0 ? { direction: "rising", words: `Rising${pace}` } : { direction: "falling", words: `Falling${pace}` };
}
