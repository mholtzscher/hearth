import { entityStateRequest, readEntity, entityMetadata } from "./entity";
import { historySeriesRequest } from "./history";
import { quantityFormat } from "./quantity";
import { quantityAdapter } from "./quantity-adapter";
import { pointer, quantityField } from "./state-fields";
import type { NumericAdapter, ReadUnits } from "./types";

/** Unit selection is local; all readers share the same paired HTTP/cache entry. */
export const pairedQuantityAdapter = (
  units: ReadUnits,
  canonicalUnit?: string
): NumericAdapter => ({
  stateRequest: (source) => entityStateRequest(source),
  seriesRequest: (source, window) => {
    const field = quantityField(source);
    const unit =
      canonicalUnit ??
      (typeof field?.quantity.unit === "string"
        ? field.quantity.unit
        : undefined);
    return historySeriesRequest(source, window, unit);
  },
  metadata: (raw, source) => entityMetadata(readEntity(raw, source), source),
  metric(raw, source) {
    const entity = readEntity(raw, source, units);
    if (!entity.state) {
      return null;
    }
    const value = pointer(entity.state.value, source.field ?? "");
    if (value === undefined) {
      return { absent: true, observed_at: entity.state.observed_at };
    }
    const quantity = entity.quantities?.[source.field ?? ""];
    if (typeof value !== "number" || !quantity) {
      throw new Error("Missing numeric reading");
    }
    if (
      canonicalUnit &&
      readEntity(raw, source, "canonical").quantities?.[source.field ?? ""]
        ?.unit !== canonicalUnit
    ) {
      throw new Error("Unexpected canonical quantity unit");
    }
    return {
      value,
      observed_at: entity.state.observed_at,
      format: quantityFormat(quantity),
      ...(quantity.range ? { range: quantity.range } : {}),
    };
  },
  series(data, source, raw) {
    if (!data.canonical) {
      throw new Error("Missing paired history");
    }
    return quantityAdapter.series(
      units === "canonical" ? data.canonical : data,
      source,
      raw
    );
  },
});
