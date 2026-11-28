/** `display = scale × canonical + offset`, with a positive scale. */
export interface Affine {
  scale: number;
  offset: number;
}

// Core's display conversions, mirrored from TableConverter in
// internal/modules/devices/quantity_conversion.go. Core converts every reading
// it returns; the browser needs the same line only for a number Core never
// sees, such as a card's saved threshold.
const table: Record<string, Affine> = {
  "mCel>Cel": { scale: 1 / 1000, offset: 0 },
  "mCel>[degF]": { scale: 9 / 5000, offset: 32 },
  "Cel>[degF]": { scale: 9 / 5, offset: 32 },
  "m/s>[mi_i]/h": { scale: 3600 / 1609.344, offset: 0 },
  "hPa>[in_i'Hg]": { scale: 1 / 33.863788, offset: 0 },
  "mm>[in_i]": { scale: 1 / 25.4, offset: 0 },
  "mm/h>[in_i]/h": { scale: 1 / 25.4, offset: 0 },
  "10*-4>1": { scale: 1 / 10_000, offset: 0 },
};

/** The conversion between two unit codes, or undefined when Core defines none. */
export const unitConversion = (from: string, to: string): Affine | undefined =>
  from === to ? { scale: 1, offset: 0 } : table[`${from}>${to}`];
