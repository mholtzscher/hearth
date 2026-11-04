import { createContext, useContext } from "react";
import { ApiError } from "../../../api/client";
import { useReads } from "../data/provider";
import { emptySnapshot, type ReadSnapshot } from "../data/cache";
import { readingAdapter } from "./reading-adapter";
import { readingTypes } from "./readings";
import { useUnitSystem } from "../../units/household";
import type { UnitSystem } from "../../units/units";
import { type MetricData, type NumericAdapter, type NumericSource, type Resolved, type SeriesData, type SeriesWindow } from "./types";

export const numericAdapters: Readonly<Record<string, NumericAdapter>> =
  Object.fromEntries(readingTypes.map(type => [type, readingAdapter] as const));

export const NumericAdapters = createContext(numericAdapters);

export function useNumericRead(source: NumericSource, window?: SeriesWindow): Resolved<MetricData | SeriesData> {
  return useNumericReads([{ source, window }])[0];
}
export function useNumericReads(bindings: Array<{ source: NumericSource; window?: SeriesWindow }>): Resolved<MetricData | SeriesData>[] {
  const adapters = useContext(NumericAdapters);
  const system = useUnitSystem();
  const requests = bindings.flatMap(({ source, window }) => {
    const adapter = adapters[source.entity_type];
    return adapter ? [adapter.stateRequest(source), ...(window ? [adapter.seriesRequest(source, window)] : [])] : [];
  });
  const snapshots = useReads(requests);
  let index = 0;
  return bindings.map(({ source, window }) => {
    const adapter = adapters[source.entity_type];
    if (!adapter) return { status: "error", error: "Unsupported resource binding" };
    const state = snapshots[index++];
    const history = window ? snapshots[index++] : emptySnapshot;
    // Hold readings until the household unit system is known, so cards never flash the wrong unit.
    if (system === undefined) return { status: "loading" };
    return resolve(adapter, source, system, state, history, window);
  });
}
function resolve(adapter: NumericAdapter, source: NumericSource, system: UnitSystem, state: ReadSnapshot, history: ReadSnapshot, window?: SeriesWindow): Resolved<MetricData | SeriesData> {
  const error = state.error ?? (window ? history.error : undefined);
  const missing = error instanceof ApiError && error.status === 404;
  if (state.data === undefined || (window && history.data === undefined)) {
    return { status: missing ? "missing" : error ? "error" : "loading", error: error?.message };
  }
  try {
    const metadata = adapter.metadata(state.data, source);
    const current = adapter.metric(state.data, source, system);
    const data = window ? adapter.series(history.data, source, system, state.data) : current;
    return { status: missing ? "missing" : data ? "ready" : "unobserved", unobserved: current === null, metadata, data: data ?? undefined, error: error?.message, refreshFailed: !!error };
  } catch {
    return { status: "error", error: "The resource returned incompatible data." };
  }
}
