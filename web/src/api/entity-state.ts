import type { Entity } from "./types";

/** Display a returned State with its own presentation metadata, without conversion. */
export function formatEntityState(entity: Pick<Entity, "state" | "quantities">): string {
  if (!entity.state) return "—";
  function format(value: unknown, path: string): string {
    const quantity = entity.quantities?.[path];
    if (typeof value === "number" && quantity) {
      return `${value.toFixed(quantity.fraction_digits)}${quantity.label ? ` ${quantity.label}` : ""}`;
    }
    if (value !== null && typeof value === "object" && !Array.isArray(value)) {
      return `{${Object.entries(value).map(([key, child]) => `${key}: ${format(child, `${path}/${key.replace(/~/g, "~0").replace(/\//g, "~1")}`)}`).join(", ")}}`;
    }
    return JSON.stringify(value) ?? String(value);
  }
  return format(entity.state.value, "");
}
