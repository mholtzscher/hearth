import { createContext, useContext } from "react";
import { ApiError } from "../../../api/client";
import { useReads } from "../data/provider";
import { emptySnapshot, type ReadSnapshot } from "../data/cache";
import { quantityAdapter } from "./quantity-adapter";
import { readErrorMessage, type HistorySeries } from "./history";
import { entityMetadata, entityStateRequest, readEntity } from "./entity";
import { quantityField, quantityTypes, textField } from "./state-fields";
import { textReading } from "./words";
import { readTimeline, readWeekPattern, timelineRequest, weekPatternRequest, weekStart } from "./changes";
import { useUnitSystem } from "../../units/household";
import type { UnitSystem } from "../../units/units";
import type { ReadRequest } from "../data/cache";
import { type EntitySource, type MetricData, type NumericAdapter, type NumericSource, type Resolved, type SeriesData, type SeriesWindow, type TextData, type TimelineData, type WeekPatternData, type Words } from "./types";

export const numericAdapters: Readonly<Record<string, NumericAdapter>> =
  Object.fromEntries(quantityTypes.map(type => [type, quantityAdapter] as const));

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
/** One card reading: a numeric field with its unit, or a yes/no or text field in words. */
export type ReadingBinding = { source: EntitySource; words?: Words };
export type Reading = MetricData | TextData;

type HistoricalText<T> = { current?: TextData } & T;

function resolveHistorical<T>(binding: ReadingBinding, state: ReadSnapshot, history: ReadSnapshot, read: (raw: unknown) => T): Resolved<HistoricalText<T>> {
  const current = resolveText(binding.source, state, binding.words);
  const error = state.error ?? history.error;
  if (state.data === undefined || history.data === undefined) {
    return { ...current, data: undefined, status: error instanceof ApiError && error.status === 404 ? "missing" : error ? "error" : "loading", error: readErrorMessage(error) };
  }
  try {
    return { ...current, data: { current: current.data, ...read(history.data) }, error: readErrorMessage(error) ?? current.error, refreshFailed: !!error };
  } catch {
    return { status: "error", metadata: current.metadata, error: "The resource returned incompatible data." };
  }
}

export function useTimelines(rows: ReadingBinding[], window: SeriesWindow): Resolved<{ current?: TextData; timeline?: TimelineData }>[] {
  const requests = rows.flatMap(({ source }) => [entityStateRequest(source), timelineRequest(source, window)]);
  const snapshots = useReads(requests);
  return rows.map((binding, index) => resolveHistorical(binding, snapshots[index * 2], snapshots[index * 2 + 1], raw => ({ timeline: readTimeline(raw, binding.source, window, binding.words) })));
}

export function useWeekPattern(binding: ReadingBinding): Resolved<{ current?: TextData; week?: WeekPatternData }> {
  const start = weekStart(new Date());
  const snapshots = useReads([entityStateRequest(binding.source), weekPatternRequest(binding.source, start)]);
  return resolveHistorical(binding, snapshots[0], snapshots[1], raw => {
    const data = raw as { range: { to: string } };
    return { week: readWeekPattern(raw, start, new Date(data.range.to)) };
  });
}

/** Current readings for State, numeric or text, sharing one Entity read per Entity. */
export function useReadings(bindings: ReadingBinding[]): Resolved<Reading>[] {
  const adapters = useContext(NumericAdapters);
  const system = useUnitSystem();
  const requests = bindings.map(({ source }): ReadRequest | undefined => {
    if (quantityField(source)) return adapters[source.entity_type]?.stateRequest(source);
    return textField(source) ? entityStateRequest(source) : undefined;
  });
  const snapshots = useReads(requests.filter((request): request is ReadRequest => request !== undefined));
  let index = 0;
  return bindings.map(({ source, words }, i): Resolved<Reading> => {
    if (!requests[i]) return { status: "error", error: "Unsupported resource binding" };
    const state = snapshots[index++];
    if (quantityField(source)) {
      if (system === undefined) return { status: "loading" };
      const result = resolve(adapters[source.entity_type], source, system, state, emptySnapshot);
      return { ...result, data: result.data && "value" in result.data ? result.data : undefined };
    }
    return resolveText(source, state, words);
  });
}
function resolveText(source: EntitySource, state: ReadSnapshot, words?: Words): Resolved<TextData> {
  const error = state.error;
  const missing = error instanceof ApiError && error.status === 404;
  if (state.data === undefined) return { status: missing ? "missing" : error ? "error" : "loading", error: error?.message };
  try {
    const entity = readEntity(state.data, source);
    const reading = textReading(entity, source, words);
    const absent = reading !== null && "absent" in reading;
    const data = reading === null || "absent" in reading ? undefined : reading;
    return { status: missing ? "missing" : data ? "ready" : absent ? "absent" : "unobserved", unobserved: reading === null, metadata: entityMetadata(entity, source), data, error: error?.message, refreshFailed: !!error };
  } catch {
    return { status: "error", error: "The resource returned incompatible data." };
  }
}
function resolve(adapter: NumericAdapter, source: NumericSource, system: UnitSystem, state: ReadSnapshot, history: ReadSnapshot, window?: SeriesWindow): Resolved<MetricData | SeriesData> {
  const error = state.error ?? (window ? history.error : undefined);
  const missing = error instanceof ApiError && error.status === 404;
  if (state.data === undefined || (window && history.data === undefined)) {
    return { status: missing ? "missing" : error ? "error" : "loading", error: readErrorMessage(error) };
  }
  try {
    const metadata = adapter.metadata(state.data, source);
    const current = adapter.metric(state.data, source, system);
    const absent = current !== null && "absent" in current;
    const data = window ? adapter.series(history.data as HistorySeries, source, system, state.data) : absent ? null : current;
    return { status: missing ? "missing" : data ? "ready" : absent ? "absent" : "unobserved", unobserved: current === null, metadata, data: data ?? undefined, error: readErrorMessage(error), refreshFailed: !!error };
  } catch {
    return { status: "error", error: "The resource returned incompatible data." };
  }
}
