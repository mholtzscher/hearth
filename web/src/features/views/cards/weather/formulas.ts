// Weather words from measurements. Inputs use canonical units: metres per
// second, degrees, millimetres per hour, and hectopascals.

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
