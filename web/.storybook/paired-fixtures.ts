import type { FieldEntity } from "../src/features/views/bindings/entity";
import type { Quantity } from "../src/features/views/bindings/quantity";

/** Synthetic read projections; fixture inputs already contain their unit conversions. */
function quantityTree(value: unknown, quantities: Record<string, Quantity>, canonical: boolean, path = ""): unknown {
  const quantity = quantities[path];
  if (quantity) return canonical ? { value, unit: quantity.unit } : { value, ...quantity };
  if (Array.isArray(value)) return value.map((entry, index) => quantityTree(entry, quantities, canonical, `${path}/${index}`));
  if (value !== null && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).map(([name, entry]) =>
      [name, quantityTree(entry, quantities, canonical, `${path}/${name}`)]));
  }
  return value;
}

export function pairedEntityResponse(display: FieldEntity, canonical: FieldEntity) {
  const { quantities, state, ...metadata } = display;
  return { ...metadata, state: state && canonical.state ? {
    ...state, value: undefined,
    canonical: quantityTree(canonical.state.value, canonical.quantities ?? {}, true),
    display: quantityTree(state.value, quantities ?? {}, false),
  } : null };
}

type Summary = {
  quantity: Quantity;
  buckets: Array<{ range: { from: string; to: string }; metrics: Record<string, number | null> }>;
};

export function pairedHistoryResponse<T extends Summary>(display: T, canonical: Summary) {
  const { quantity, buckets, ...envelope } = display;
  return { ...envelope, buckets: buckets.map((bucket, index) => ({
    range: bucket.range,
    canonical: { unit: canonical.quantity.unit, metrics: canonical.buckets[index].metrics },
    display: { ...quantity, metrics: bucket.metrics },
  })) };
}
