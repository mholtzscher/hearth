import { createContext, useContext } from "react";

import { ApiError } from "../../../api/client";
import { useMinuteClock } from "../cards/observation";
import { emptySnapshot } from "../data/cache";
import type { ReadRequest, ReadSnapshot } from "../data/cache";
import { useReads } from "../data/provider";
import {
  hourTotalsRequest,
  hourTotalsStart,
  readHourTotals,
  readTimeline,
  readWeekPattern,
  timelineRequest,
  weekPatternRequest,
  weekStart,
} from "./changes";
import { entityMetadata, entityStateRequest, readEntity } from "./entity";
import { historySpanRequest, readErrorMessage } from "./history";
import type { HistorySeries, HistorySpan } from "./history";
import { pairedQuantityAdapter } from "./paired";
import { quantityAdapter } from "./quantity-adapter";
import { quantityField, quantityTypes, textField } from "./state-fields";
import type {
  EntitySource,
  MetricData,
  NumericAdapter,
  NumericSource,
  PeriodTotalsData,
  ReadUnits,
  Resolved,
  SeriesData,
  SeriesWindow,
  TextData,
  TimelineData,
  WeekPatternData,
  Words,
} from "./types";
import { textReading } from "./words";

export const numericAdapters: Readonly<Record<string, NumericAdapter>> =
  Object.fromEntries(
    quantityTypes.map((type) => [type, quantityAdapter] as const)
  );

export const NumericAdapters = createContext(numericAdapters);

/** One card reading: a numeric field with its unit, or a yes/no or text field in words. */
export interface ReadingBinding {
  source: EntitySource;
  words?: Words;
}
export type Reading = MetricData | TextData;

type HistoricalText<T> = { current?: TextData } & T;

const loadStatus = (error: unknown): "missing" | "error" | "loading" => {
  if (error instanceof ApiError && error.status === 404) {
    return "missing";
  }
  if (error) {
    return "error";
  }
  return "loading";
};

const valueStatus = (
  missing: boolean,
  data: unknown,
  absent: boolean
): "missing" | "ready" | "absent" | "unobserved" => {
  if (missing) {
    return "missing";
  }
  if (data) {
    return "ready";
  }
  if (absent) {
    return "absent";
  }
  return "unobserved";
};

const prefixOffsets = (counts: number[]): number[] => {
  const offsets: number[] = [];
  let total = 0;
  for (const count of counts) {
    offsets.push(total);
    total += count;
  }
  return offsets;
};

const resolveText = (
  source: EntitySource,
  state: ReadSnapshot,
  words?: Words
): Resolved<TextData> => {
  const { error } = state;
  const missing = error instanceof ApiError && error.status === 404;
  if (state.data === undefined) {
    return {
      status: loadStatus(error),
      error: error?.message,
    };
  }
  try {
    const entity = readEntity(state.data, source);
    const reading = textReading(entity, source, words);
    const absent = reading !== null && "absent" in reading;
    const data = reading === null || "absent" in reading ? undefined : reading;
    return {
      status: valueStatus(missing, data, absent),
      unobserved: reading === null,
      metadata: entityMetadata(entity, source),
      data,
      error: error?.message,
      refreshFailed: !!error,
    };
  } catch {
    return {
      status: "error",
      error: "The resource returned incompatible data.",
    };
  }
};

const resolve = (
  adapter: NumericAdapter,
  source: NumericSource,
  state: ReadSnapshot,
  history: ReadSnapshot,
  window?: SeriesWindow
): Resolved<MetricData | SeriesData> => {
  const error = state.error ?? (window ? history.error : undefined);
  const missing = error instanceof ApiError && error.status === 404;
  if (state.data === undefined || (window && history.data === undefined)) {
    return {
      status: loadStatus(error),
      error: readErrorMessage(error),
    };
  }
  try {
    const metadata = adapter.metadata(state.data, source);
    const current = adapter.metric(state.data, source);
    const absent = current !== null && "absent" in current;
    let data: MetricData | SeriesData | null;
    if (window) {
      data = adapter.series(history.data as HistorySeries, source, state.data);
    } else {
      data = absent ? null : current;
    }
    return {
      status: valueStatus(missing, data, absent),
      unobserved: current === null,
      metadata,
      data: data ?? undefined,
      error: readErrorMessage(error),
      refreshFailed: !!error,
    };
  } catch {
    return {
      status: "error",
      error: "The resource returned incompatible data.",
    };
  }
};

const resolveHistorical = <T,>(
  binding: ReadingBinding,
  state: ReadSnapshot,
  history: ReadSnapshot,
  read: (raw: unknown) => T
): Resolved<HistoricalText<T>> => {
  const current = resolveText(binding.source, state, binding.words);
  const error = state.error ?? history.error;
  if (state.data === undefined || history.data === undefined) {
    return {
      ...current,
      data: undefined,
      status: loadStatus(error),
      error: readErrorMessage(error),
    };
  }
  try {
    return {
      ...current,
      data: { current: current.data, ...read(history.data) },
      error: readErrorMessage(error) ?? current.error,
      refreshFailed: !!error,
    };
  } catch {
    return {
      status: "error",
      metadata: current.metadata,
      error: "The resource returned incompatible data.",
    };
  }
};

export const useNumericReads = (
  bindings: {
    source: NumericSource;
    window?: SeriesWindow;
    units?: ReadUnits;
    canonicalUnit?: string;
  }[]
): Resolved<MetricData | SeriesData>[] => {
  const adapters = useContext(NumericAdapters);
  const selected = bindings.map(({ source, units, canonicalUnit }) => {
    const adapter = adapters[source.entity_type];
    return adapter === quantityAdapter
      ? pairedQuantityAdapter(units ?? "household", canonicalUnit)
      : adapter;
  });
  const requests = bindings.flatMap(({ source, window, units }, index) => {
    const adapter = selected[index];
    return adapter
      ? [
          adapter.stateRequest(source, units),
          ...(window ? [adapter.seriesRequest(source, window, units)] : []),
        ]
      : [];
  });
  const snapshots = useReads(requests);
  const offsets = prefixOffsets(
    bindings.map((binding, bindingIndex) => {
      if (!selected[bindingIndex]) {
        return 0;
      }
      return binding.window ? 2 : 1;
    })
  );
  return bindings.map(({ source, window }, bindingIndex) => {
    const adapter = selected[bindingIndex];
    if (!adapter) {
      return { status: "error", error: "Unsupported resource binding" };
    }
    const offset = offsets[bindingIndex];
    const state = snapshots[offset];
    const history = window ? snapshots[offset + 1] : emptySnapshot;
    return resolve(adapter, source, state, history, window);
  });
};

/**
 * History over card-chosen spans, in household units, each with its Entity's
 * current State for status. Bucket counts follow the span, not a window.
 */
export const useNumericSpans = (
  bindings: { source: NumericSource; span: HistorySpan }[]
): Resolved<SeriesData>[] => {
  const adapter = pairedQuantityAdapter("household");
  const supported = bindings.map(({ source }) => !!quantityField(source));
  const snapshots = useReads(
    bindings.flatMap(({ source, span }, index) =>
      supported[index]
        ? [entityStateRequest(source), historySpanRequest(source, span)]
        : []
    )
  );
  const offsets = prefixOffsets(supported.map((ok) => (ok ? 2 : 0)));
  return bindings.map(({ source }, index) => {
    if (!supported[index]) {
      return { status: "error", error: "Unsupported resource binding" };
    }
    const offset = offsets[index];
    const result = resolve(
      adapter,
      source,
      snapshots[offset],
      snapshots[offset + 1],
      "24h"
    );
    return {
      ...result,
      data: result.data && "buckets" in result.data ? result.data : undefined,
    };
  });
};

export const useNumericRead = (
  source: NumericSource,
  window?: SeriesWindow
): Resolved<MetricData | SeriesData> =>
  useNumericReads([{ source, window }])[0];

export const useTimelines = (
  rows: ReadingBinding[],
  window: SeriesWindow
): Resolved<{ current?: TextData; timeline?: TimelineData }>[] => {
  const requests = rows.flatMap(({ source }) => [
    entityStateRequest(source),
    timelineRequest(source, window),
  ]);
  const snapshots = useReads(requests);
  return rows.map((binding, index) =>
    resolveHistorical(
      binding,
      snapshots[index * 2],
      snapshots[index * 2 + 1],
      (raw) => ({
        timeline: readTimeline(raw, binding.source, window, binding.words),
      })
    )
  );
};

export const useWeekPattern = (
  binding: ReadingBinding
): Resolved<{ current?: TextData; week?: WeekPatternData }> => {
  const now = useMinuteClock();
  const start = weekStart(new Date(now));
  const snapshots = useReads([
    entityStateRequest(binding.source),
    weekPatternRequest(binding.source, start),
  ]);
  return resolveHistorical(binding, snapshots[0], snapshots[1], (raw) => {
    const data = raw as { range: { to: string } };
    return { week: readWeekPattern(raw, start, new Date(data.range.to)) };
  });
};

/** Seconds true per hour over the last 24 hours, in one-hour periods. */
export const useHourTotals = (
  binding: ReadingBinding
): Resolved<{ current?: TextData; hours?: PeriodTotalsData }> => {
  const now = useMinuteClock();
  const start = hourTotalsStart(new Date(now));
  const snapshots = useReads([
    entityStateRequest(binding.source),
    hourTotalsRequest(binding.source, start),
  ]);
  return resolveHistorical(binding, snapshots[0], snapshots[1], (raw) => {
    const data = raw as { range: { to: string } };
    return { hours: readHourTotals(raw, start, new Date(data.range.to)) };
  });
};

/** Current readings for State, numeric or text, sharing one Entity read per Entity. */
export const useReadings = (
  bindings: ReadingBinding[]
): Resolved<Reading>[] => {
  const adapters = useContext(NumericAdapters);
  const requests = bindings.map(({ source }): ReadRequest | undefined => {
    if (quantityField(source)) {
      return adapters[source.entity_type]?.stateRequest(source);
    }
    return textField(source) ? entityStateRequest(source) : undefined;
  });
  const snapshots = useReads(
    requests.filter((request): request is ReadRequest => request !== undefined)
  );
  const snapshotIndexes = requests.map((request, requestIndex) => {
    if (request === undefined) {
      return -1;
    }
    return requests
      .slice(0, requestIndex)
      .filter((candidate) => candidate !== undefined).length;
  });
  return bindings.map(({ source, words }, bindingIndex): Resolved<Reading> => {
    const snapshotIndex = snapshotIndexes[bindingIndex];
    if (snapshotIndex === -1) {
      return { status: "error", error: "Unsupported resource binding" };
    }
    const state = snapshots[snapshotIndex];
    if (quantityField(source)) {
      const result = resolve(
        adapters[source.entity_type],
        source,
        state,
        emptySnapshot
      );
      return {
        ...result,
        data: result.data && "value" in result.data ? result.data : undefined,
      };
    }
    return resolveText(source, state, words);
  });
};
