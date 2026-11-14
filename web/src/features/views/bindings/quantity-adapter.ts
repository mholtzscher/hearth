import { z } from "zod";
import { historySeriesRequest } from "./history";
import { entityMetadata, entityStateRequest, readEntity, type FieldEntity } from "./entity";
import { pointer, quantityField } from "./state-fields";
import type { NumericAdapter, NumericSource, SeriesData } from "./types";
import { quantityFormat } from "./quantity";

function readQuantityEntity(raw: unknown, source: NumericSource): FieldEntity {
  const data = readEntity(raw, source);
  if (!quantityField(source)) throw new Error("Unsupported quantity field");
  return data;
}

export const quantityAdapter: NumericAdapter = {
  stateRequest: source => entityStateRequest(source),
  seriesRequest: (source, window) => historySeriesRequest(source, window),
  metadata: (raw, source) => entityMetadata(readQuantityEntity(raw, source), source),
  metric(raw, source) {
    const data = readQuantityEntity(raw, source);
    if (!data.state) return null;
    const value = pointer(data.state.value, source.field ?? "");
    if (value === undefined) return { absent: true, observed_at: data.state.observed_at };
    const quantity = data.quantities?.[source.field ?? ""];
    if (!quantity) throw new Error("Missing quantity metadata");
    return { value: z.number().parse(value), observed_at: data.state.observed_at, format: quantityFormat(quantity), ...(quantity.range ? { range: quantity.range } : {}) };
  },
  series(data) {
    return { from: data.from, to: data.to, unit: data.quantity.unit, format: quantityFormat(data.quantity), buckets: data.buckets } satisfies SeriesData;
  },
};
