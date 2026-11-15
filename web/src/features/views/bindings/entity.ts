import { z } from "zod";

import { apiFetch } from "../../../api/client";
import { decodeEntityDetail } from "../../../api/entity-detail";
import type { ReadRequest } from "../data/cache";
import { quantitySchema } from "./quantity";
import {
  isQuantityField,
  pointer,
  resolveQuantity,
  stateField,
  stateFields,
} from "./state-fields";
import type { EntitySource, ResourceMetadata } from "./types";

const timestamp = z.string().datetime({ offset: true });
export const entityBody = z.object({
  id: z.string(),
  device_id: z.string().optional(),
  type: z.string(),
  name: z.string(),
  enabled: z.boolean(),
  availability: z.object({ status: z.string() }),
  support: z.unknown().optional(),
  quantities: z.record(z.string(), quantitySchema).optional(),
  state: z
    .object({
      value: z.unknown(),
      observed_at: timestamp,
      observation_id: z.string().min(1).optional(),
    })
    .nullable(),
});
export type FieldEntity = z.infer<typeof entityBody>;

/**
 * Validates one Entity of a catalogued type: its support resolves every unit,
 * and its State matches every field. Every card reads through this check, so a
 * shared Entity response is accepted or rejected the same way whichever card
 * requested it first.
 */
export const parseFieldEntity = (raw: unknown): FieldEntity => {
  const parsed = entityBody.parse(raw);
  const fields = Object.hasOwn(stateFields, parsed.type)
    ? stateFields[parsed.type]
    : undefined;
  if (!fields) {
    throw new Error("Unsupported Entity type");
  }
  for (const [path, field] of Object.entries(fields)) {
    if (isQuantityField(field)) {
      resolveQuantity(field, parsed.support);
      if (
        parsed.state &&
        pointer(parsed.state.value, path) !== undefined &&
        !parsed.quantities?.[path]
      ) {
        throw new Error("Missing quantity metadata");
      }
    }
    if (!parsed.state) {
      continue;
    }
    const value = pointer(parsed.state.value, path);
    if (value === undefined && field.presence === "sometimes") {
      continue;
    }
    switch (field.value_type) {
      // A household projection of an integer canonical field can be fractional.
      case "integer": {
        z.number().parse(value);
        break;
      }
      case "number": {
        z.number().parse(value);
        break;
      }
      case "boolean": {
        z.boolean().parse(value);
        break;
      }
      case "string": {
        z.string().parse(value);
        if (field.enum && !field.enum.includes(value as string)) {
          throw new Error("Invalid State choice");
        }
        break;
      }
      default: {
        break;
      }
    }
  }
  return parsed;
};

/** Parses an Entity and checks it is the one, and the type, the source names. */
export const readEntity = (
  raw: unknown,
  source: EntitySource,
  units: "household" | "canonical" = "household"
): FieldEntity => {
  const selected =
    units === "canonical" ? (raw as { canonical?: unknown }).canonical : raw;
  const parsed = parseFieldEntity(selected);
  if (parsed.id !== source.entity_id || parsed.type !== source.entity_type) {
    throw new Error("Entity identity/type mismatch");
  }
  if (!stateField(source)) {
    throw new Error("Unsupported State field");
  }
  return parsed;
};

export const entityMetadata = (
  data: FieldEntity,
  source: EntitySource
): ResourceMetadata => ({
  id: data.id,
  label: data.name + (source.field ? ` · ${source.field}` : ""),
  enabled: data.enabled,
  availability: data.availability.status,
});

/** One shared current-State read per Entity, whichever field a card selects. */
export const entityStateRequest = (source: EntitySource): ReadRequest => ({
  key: `entity-state:${source.entity_id}:paired`,
  kind: "state",
  load: (signal) =>
    apiFetch(
      `/v1/entities/${encodeURIComponent(source.entity_id)}`,
      { signal },
      (text) => {
        const raw = decodeEntityDetail(JSON.parse(text));
        if (raw.id !== source.entity_id || raw.type !== source.entity_type) {
          throw new Error("The resource returned incompatible data.");
        }
        if (
          source.entity_type === "hearth.hourlyforecast/v1" ||
          source.entity_type === "hearth.dailyforecast/v1"
        ) {
          return raw;
        }
        readEntity(raw, source);
        const working = readEntity(raw, source, "canonical");
        for (const [path, field] of Object.entries(stateFields[working.type])) {
          if (
            !isQuantityField(field) ||
            !working.state ||
            pointer(working.state.value, path) === undefined
          ) {
            continue;
          }
          if (
            working.quantities?.[path].unit !==
            resolveQuantity(field, working.support).unit
          ) {
            throw new Error("Canonical quantity unit mismatch");
          }
        }
        return raw;
      }
    ),
});
