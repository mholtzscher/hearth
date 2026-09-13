// Semantic numeric measurements (hearth.measurement/v1).
//
// The web client renders measurements from the Entity's own descriptor support
// and never infers meaning from an Entity name or from a bare unit. Canonical
// UCUM units are the wire/storage units; the labels here are presentation only
// and own no conversion. A descriptor that is not a known kind with its
// canonical unit and sound finite bounds is malformed: callers must fall back
// to structured/raw rendering instead of showing a partial semantic reading.

/** Canonical unit admitted by measurement/v1 for each known measurement kind,
    mirroring the contract's kind/unit pairs. Used only to reject a descriptor
    that pairs a kind with the wrong unit. */
const CANONICAL_UNITS: Readonly<Record<string, string>> = {
  temperature: "Cel",
  relative_humidity: "%",
  illuminance: "lx",
  battery_level: "%",
};

/** Human-readable labels for canonical UCUM units. Cel is presented as the
    degree-Celsius sign; the other canonical units already read as themselves. */
const UNIT_LABELS: Readonly<Record<string, string>> = {
  Cel: "°C",
  "%": "%",
  lx: "lx",
};

export interface MeasurementDescriptor {
  kind: string;
  unit: string;
  unitLabel: string;
  minimum: number;
  maximum: number;
}

export function measurementUnitLabel(unit: string): string {
  return UNIT_LABELS[unit] ?? unit;
}

function isFiniteNumber(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

/** Structurally validated measurement descriptor from Entity support, or null
    when support is not an object, is missing a field, pairs a known kind with
    the wrong canonical unit, carries unknown kinds or non-finite bounds, or
    inverts its bounds. */
export function readMeasurementDescriptor(support: unknown): MeasurementDescriptor | null {
  if (typeof support !== "object" || support === null) return null;
  const state = (support as { state?: unknown }).state;
  if (typeof state !== "object" || state === null) return null;
  const { measurement_kind: kind, unit, minimum, maximum } = state as Record<string, unknown>;
  if (typeof kind !== "string" || typeof unit !== "string") return null;
  if (CANONICAL_UNITS[kind] !== unit) return null;
  if (!isFiniteNumber(minimum) || !isFiniteNumber(maximum) || minimum > maximum) return null;
  return { kind, unit, unitLabel: measurementUnitLabel(unit), minimum, maximum };
}

export interface MeasurementReading {
  descriptor: MeasurementDescriptor;
  numeric: number;
  /** Canonical value with its human unit label, for example "21.5 °C". */
  label: string;
}

/** Finite numeric State read under one measurement descriptor, or null for a
    non-number/non-finite value or a malformed descriptor. The value is never
    clamped to the descriptor bounds: a historical reading outside current
    support must still render. */
export function readMeasurementReading(
  value: unknown,
  support: unknown,
): MeasurementReading | null {
  const descriptor = readMeasurementDescriptor(support);
  if (!descriptor || !isFiniteNumber(value)) return null;
  return { descriptor, numeric: value, label: `${value} ${descriptor.unitLabel}` };
}
