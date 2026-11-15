// Weather words from measurements. Inputs use canonical units: metres per
// second, degrees, millimetres per hour, and hectopascals.

// Upper speed bound of Beaufort forces 0–11 in m/s; force 12 has no upper bound.
const beaufortUpper = [
  0.5, 1.5, 3.3, 5.5, 7.9, 10.7, 13.8, 17.1, 20.7, 24.4, 28.4, 32.6,
];
export const beaufortNames = [
  "Calm",
  "Light air",
  "Light breeze",
  "Gentle breeze",
  "Moderate breeze",
  "Fresh breeze",
  "Strong breeze",
  "Near gale",
  "Gale",
  "Strong gale",
  "Storm",
  "Violent storm",
  "Hurricane force",
] as const;

export const beaufort = (ms: number): number => {
  const force = beaufortUpper.findIndex((upper) => ms < upper);
  return force === -1 ? 12 : force;
};

/** Everyday words for a summary line, less formal than the Beaufort names. */
export const windWord = (ms: number): string => {
  const force = beaufort(ms);
  if (force === 0) {
    return "Calm";
  }
  if (force <= 2) {
    return "Light wind";
  }
  if (force <= 4) {
    return "Breezy";
  }
  if (force <= 6) {
    return "Windy";
  }
  if (force <= 9) {
    return "Very windy";
  }
  return "Storm-force wind";
};

const points = [
  "N",
  "NNE",
  "NE",
  "ENE",
  "E",
  "ESE",
  "SE",
  "SSE",
  "S",
  "SSW",
  "SW",
  "WSW",
  "W",
  "WNW",
  "NW",
  "NNW",
];
const pointWords = [
  "north",
  "north-northeast",
  "northeast",
  "east-northeast",
  "east",
  "east-southeast",
  "southeast",
  "south-southeast",
  "south",
  "south-southwest",
  "southwest",
  "west-southwest",
  "west",
  "west-northwest",
  "northwest",
  "north-northwest",
];

/** The nearest of 16 compass points for a bearing in degrees. */
export const compassPoint = (
  degrees: number
): { short: string; words: string } => {
  const index = Math.round((((degrees % 360) + 360) % 360) / 22.5) % 16;
  return { short: points[index], words: pointWords[index] };
};

/** AMS rain intensity: light below 2.5 mm/h, heavy above 7.6 mm/h. Undefined when dry. */
export const rainWord = (mmPerHour: number): string | undefined => {
  if (mmPerHour <= 0) {
    return undefined;
  }
  if (mmPerHour < 2.5) {
    return "Light rain";
  }
  if (mmPerHour <= 7.6) {
    return "Rain";
  }
  return "Heavy rain";
};

export interface Tendency {
  direction: "rising" | "falling" | "steady";
  words: string;
}

/** Met Office barometric tendency for a three-hour change in hPa. */
export const pressureTendency = (change: number): Tendency => {
  const size = Math.abs(change);
  // Subtracting means near 1000 hPa can put an exact threshold a few ulps away.
  const tolerance = 1e-9;
  if (size < 0.1 - tolerance) {
    return { direction: "steady", words: "Steady" };
  }
  let pace = " very rapidly";
  if (size <= 1.5 + tolerance) {
    pace = " slowly";
  } else if (size <= 3.5 + tolerance) {
    pace = "";
  } else if (size <= 6 + tolerance) {
    pace = " quickly";
  }
  return change > 0
    ? { direction: "rising", words: `Rising${pace}` }
    : { direction: "falling", words: `Falling${pace}` };
};
