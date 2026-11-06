import type { FieldEntity } from "../bindings/entity";

/**
 * True when the Entity's current support lists the Operation, so a card can
 * offer a control for it. Enablement disables the control rather than hiding
 * it, and availability doesn't gate control at all: Core still attempts a
 * Command for an unavailable Entity whose Adapter is healthy.
 */
export function supportsOperation(entity: FieldEntity, operation: string): boolean {
  const support = entity.support;
  if (typeof support !== "object" || support === null) return false;
  const operations = (support as { operations?: unknown }).operations;
  return typeof operations === "object" && operations !== null && Object.prototype.hasOwnProperty.call(operations, operation);
}
