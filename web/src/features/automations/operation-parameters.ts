import { z } from "zod";

import catalog from "../../../../contracts/operation-parameters/v1/operation-parameters.json";

// These are type-level schemas, not evidence that an Entity supports an Operation.
const entityTypes = z
  .strictObject({
    $comment: z.string(),
    schema_version: z.literal(1),
    entity_types: z.record(
      z.string(),
      z.strictObject({
        operations: z.record(
          z.string(),
          z.strictObject({
            parameters_schema: z.record(z.string(), z.unknown()),
          })
        ),
      })
    ),
  })
  .parse(catalog).entity_types;

export const getOperationParametersSchema = (
  entityType: string,
  operation: string
): Record<string, unknown> | undefined => {
  if (!Object.hasOwn(entityTypes, entityType)) {
    return undefined;
  }
  const { operations } = entityTypes[entityType];
  return Object.hasOwn(operations, operation)
    ? operations[operation].parameters_schema
    : undefined;
};
