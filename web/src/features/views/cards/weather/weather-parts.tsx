import type { ReactNode } from "react";

import { cn } from "../../../../lib/utils";
import { useNumericReads } from "../../bindings/registry";
import type {
  MetricData,
  NumericSource,
  ReadUnits,
  Resolved,
  SeriesData,
  SeriesWindow,
} from "../../bindings/types";
import { StatusList } from "../data-card";
import { Reading, ReadingUnit } from "../reading";
import type { ReadingRow } from "../reading-list/reading-rows";
import { cardStatus } from "../status";
import type { CardStatus } from "../status";

/** A slot selects one representation of a shared, paired read. */
export interface WeatherRead {
  source: NumericSource;
  window?: SeriesWindow;
  units?: ReadUnits;
  canonicalUnit?: string;
}
export interface WeatherReads<K extends string> {
  current: (slot: K) => Resolved<MetricData> | undefined;
  history: (slot: K) => Resolved<SeriesData> | undefined;
}

/** A trend in the wide weather aside uses a taller plot. */
export const weatherTrendParts = {
  plot: "@min-[460px]:h-[72px]",
  drawing: "@min-[460px]:h-[72px]",
};
export const weatherConditions =
  "text-label leading-normal text-gray-9 text-balance data-[value=dimmed]:text-gray-8";
export const weatherDetail =
  "text-label leading-normal text-muted-foreground tabular-nums text-pretty data-[value=dimmed]:text-gray-7";

/**
 * Current readings, plus history where a window is given, for a card's named
 * slots. An unbound slot reads nothing and resolves to undefined.
 */
export const useWeatherReads = <K extends string>(
  slots: [K, WeatherRead | undefined][]
): WeatherReads<K> => {
  const bound = slots.filter(
    (slot): slot is [K, WeatherRead] => slot[1] !== undefined
  );
  const results = useNumericReads(
    bound.flatMap(([, read]) => {
      const binding = {
        source: read.source,
        units: read.units,
        canonicalUnit: read.canonicalUnit,
      };
      return read.window
        ? [binding, { ...binding, window: read.window }]
        : [binding];
    })
  );
  const current = new Map<K, Resolved<MetricData>>();
  const history = new Map<K, Resolved<SeriesData>>();
  let index = 0;
  for (const [slot, read] of bound) {
    const reading = results[index];
    index += 1;
    current.set(slot, {
      ...reading,
      data: reading.data && "value" in reading.data ? reading.data : undefined,
    });
    if (read.window) {
      const series = results[index];
      index += 1;
      history.set(slot, {
        ...series,
        data: series.data && "buckets" in series.data ? series.data : undefined,
      });
    }
  }
  return {
    current: (slot) => current.get(slot),
    history: (slot) => history.get(slot),
  };
};

/** The fixed 40px reading, or words such as Dry at the same scale. Dimmed or empty by the card state table. */
export const WeatherReading = ({
  data,
  status,
  words,
}: {
  data?: MetricData;
  status: CardStatus;
  words?: string;
}) => (
  <Reading
    value={status.value}
    className={words ? "text-balance [overflow-wrap:anywhere]" : undefined}
  >
    {words ?? (data ? data.value.toFixed(data.format.fraction_digits) : "—")}
    {!words && data?.format.unit_label && (
      <ReadingUnit>{data.format.unit_label}</ReadingUnit>
    )}
  </Reading>
);

/**
 * The lead reading and its words, with the card's other readings beside them
 * in a wide card and below them otherwise. Without details there is one zone.
 */
export const WeatherZones = ({
  aside,
  children,
  wind,
}: {
  aside?: ReactNode;
  children: ReactNode;
  wind?: boolean;
}) => (
  <div
    className={cn(
      "flex min-w-0 flex-col gap-1.5",
      aside &&
        "@min-[460px]:grid @min-[460px]:grid-cols-2 @min-[460px]:items-start @min-[460px]:gap-6",
      aside &&
        wind &&
        "@min-[460px]:grid-cols-[minmax(max-content,1fr)_minmax(0,1fr)] @min-[460px]:items-center"
    )}
  >
    <div data-slot="weather-main" className="flex min-w-0 flex-col gap-1.5">
      {children}
    </div>
    {aside ? (
      <div data-slot="weather-aside" className="flex min-w-0 flex-col gap-1.5">
        {aside}
      </div>
    ) : null}
  </div>
);

/** Statuses of a reading that has no row of its own, named so readers know which value they describe. */
export const SlotStatus = ({
  name,
  resource,
}: {
  name: string;
  resource?: Resolved<unknown>;
}) => {
  const entries = resource ? cardStatus(resource).entries : [];
  if (entries.length === 0) {
    return null;
  }
  return (
    <div
      data-slot="weather-slot-status"
      className="text-label text-muted-foreground flex flex-wrap items-baseline gap-x-2 gap-y-0.5 leading-normal"
    >
      <span>{name}</span>
      <StatusList className="basis-full" entries={entries} />
    </div>
  );
};

/** A row for each bound slot, in order, so the footer, afterglow, and Source cover every reading the card uses. */
export const slotRows = (
  slots: [string, string, Resolved<MetricData> | undefined][]
): ReadingRow[] =>
  slots.flatMap(([key, name, resource]) =>
    resource ? [{ key, name, resource }] : []
  );
