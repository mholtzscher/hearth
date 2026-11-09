import { useNumericReads } from "../../bindings/registry";
import type { MetricData, NumericSource, Resolved, SeriesData, SeriesWindow } from "../../bindings/types";
import { StatusList } from "../CardFrame";
import { cardStatus, type CardStatus } from "../status";
import type { ReadingRow } from "../reading-list/ReadingRows";

export type WeatherRead = { source: NumericSource; window?: SeriesWindow };
export type WeatherReads<K extends string> = {
  current: (slot: K) => Resolved<MetricData> | undefined;
  history: (slot: K) => Resolved<SeriesData> | undefined;
};

/**
 * Current readings, plus history where a window is given, for a card's named
 * slots. An unbound slot reads nothing and resolves to undefined.
 */
export function useWeatherReads<K extends string>(slots: Array<[K, WeatherRead | undefined]>): WeatherReads<K> {
  const bound = slots.filter((slot): slot is [K, WeatherRead] => slot[1] !== undefined);
  const results = useNumericReads(bound.flatMap(([, read]) => read.window ? [{ source: read.source }, { source: read.source, window: read.window }] : [{ source: read.source }]));
  const current = new Map<K, Resolved<MetricData>>(), history = new Map<K, Resolved<SeriesData>>();
  let index = 0;
  for (const [slot, read] of bound) {
    const reading = results[index++];
    current.set(slot, { ...reading, data: reading.data && "value" in reading.data ? reading.data : undefined });
    if (read.window) {
      const series = results[index++];
      history.set(slot, { ...series, data: series.data && "buckets" in series.data ? series.data : undefined });
    }
  }
  return { current: slot => current.get(slot), history: slot => history.get(slot) };
}

/** The fixed 40px reading, or words such as Dry at the same scale. Dimmed or empty by the card state table. */
export function WeatherReading({ data, status, words }: { data?: MetricData; status: CardStatus; words?: string }) {
  return <p className={`view-metric-value${words ? " view-state-value" : ""}`} data-value={status.value}>
    {words ?? (data ? data.value.toFixed(data.format.fraction_digits) : "—")}
    {!words && data?.format.unit_label && <small>{data.format.unit_label}</small>}
  </p>;
}

/** Statuses of a reading that has no row of its own, named so readers know which value they describe. */
export function SlotStatus({ name, resource }: { name: string; resource?: Resolved<unknown> }) {
  const entries = resource ? cardStatus(resource).entries : [];
  if (entries.length === 0) return null;
  return <div className="view-weather-slot-status"><span>{name}</span><StatusList entries={entries} /></div>;
}

/** A row for each bound slot, in order, so the footer, afterglow, and Source cover every reading the card uses. */
export function slotRows(slots: Array<[string, string, Resolved<MetricData> | undefined]>): ReadingRow[] {
  return slots.flatMap(([key, name, resource]) => resource ? [{ key, name, resource }] : []);
}
