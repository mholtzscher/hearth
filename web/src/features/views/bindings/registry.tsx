import { createContext, useContext } from "react";
import { ApiError } from "../../../api/client";
import { useReads } from "../data/provider";
import { emptySnapshot, type ReadSnapshot } from "../data/cache";
import { readingAdapter } from "./reading-adapter";
import { readingTypes } from "./readings";
import { type MetricData, type NumericAdapter, type NumericSource, type Resolved, type SeriesData, type SeriesWindow } from "./types";

export const numericAdapters: Readonly<Record<string, NumericAdapter>> =
  Object.fromEntries(readingTypes.map(type => [type, readingAdapter] as const));

export const NumericAdapters = createContext(numericAdapters);

export function useNumericRead(source: NumericSource, unit: string, window?: SeriesWindow): Resolved<MetricData | SeriesData> {
  return useNumericReads([{ source, unit, window }])[0];
}
export function useNumericReads(bindings: Array<{ source: NumericSource; unit: string; window?: SeriesWindow }>): Resolved<MetricData | SeriesData>[] {
  const adapters = useContext(NumericAdapters);
  const requests = bindings.flatMap(({ source, window }) => {
    const adapter = adapters[source.entity_type];
    return adapter ? [adapter.stateRequest(source), ...(window ? [adapter.seriesRequest(source, window)] : [])] : [];
  });
  const snapshots = useReads(requests);
  let index = 0;
  return bindings.map(({ source, unit, window }) => {
    const adapter = adapters[source.entity_type];
    if (!adapter) return { status: "error", error: "Unsupported resource binding" };
    const state = snapshots[index++];
    const history = window ? snapshots[index++] : emptySnapshot;
    return resolve(adapter, source, unit, state, history, window);
  });
}
function resolve(adapter: NumericAdapter, source: NumericSource, unit: string, state: ReadSnapshot, history: ReadSnapshot, window?: SeriesWindow): Resolved<MetricData | SeriesData> {
  const error = state.error ?? (window ? history.error : undefined);
  const missing = error instanceof ApiError && error.status === 404;
  if (state.data === undefined || (window && history.data === undefined)) {
    return { status: missing ? "missing" : error ? "error" : "loading", error: error?.message };
  }
  try {
    const metadata = adapter.metadata(state.data, source);
    const current = adapter.metric(state.data, source, unit);
    const data = window ? adapter.series(history.data, source, unit, state.data) : current;
    return { status: missing ? "missing" : data ? "ready" : "unobserved", unobserved: current === null, metadata, data: data ?? undefined, error: error?.message, refreshFailed: !!error };
  } catch {
    return { status: "error", error: "The resource returned incompatible data." };
  }
}
