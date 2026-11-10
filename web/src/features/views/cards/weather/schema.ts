import { z } from "zod";
import { entityID } from "../../catalog/primitives";

/** One weather reading from the root of a fixed Entity type. */
export function typedReading<T extends string>(type: T) {
  return z.strictObject({ kind: z.literal("entity-state"), entity_id: entityID, entity_type: z.literal(type), field: z.literal("").optional() });
}

/**
 * The stored units a weather slot accepts from a generic numeric sensor. The
 * Entity type alone can't tell rain from wind direction, so Core checks the
 * Entity's unit when a view is presented or saved.
 */
export const sensorUnits = { rainRate: "mm/h", rainDepth: "mm", direction: "deg" } as const;
export type SensorUnit = typeof sensorUnits[keyof typeof sensorUnits];

/** A hearth.numericsensor/v1 root reading whose stored unit is fixed by the slot. */
export function sensorReading(unit: SensorUnit) {
  return typedReading("hearth.numericsensor/v1").describe(`A hearth.numericsensor/v1 Entity whose unit is ${unit}.`);
}
