import type { SeriesData } from "../../bindings/types";

/** The usual range is kept per half hour of the local day. */
export const SLOT_MINUTES = 30;
export const SLOTS = (24 * 60) / SLOT_MINUTES;

export const localMidnight = (at: Date | number, daysBefore = 0): Date => {
  const date = new Date(at);
  date.setHours(0, 0, 0, 0);
  date.setDate(date.getDate() - daysBefore);
  return date;
};

/** Minutes since local midnight, so a daylight-saving change cannot shift a bucket's slot. */
export const minuteOfDay = (ms: number): number => {
  const date = new Date(ms);
  return date.getHours() * 60 + date.getMinutes();
};

export interface UsualSlot {
  low: number;
  high: number;
  mean: number;
}

/**
 * The last days' bucket means folded by time of day: for each half hour, the
 * lowest and highest of those means and their average. A slot seen on fewer
 * than two days has no usual range.
 */
export const usualRange = (
  buckets: SeriesData["buckets"]
): (UsualSlot | null)[] => {
  const slots: number[][] = Array.from({ length: SLOTS }, () => []);
  for (const bucket of buckets) {
    if (bucket.mean !== null) {
      const slot = Math.floor(
        minuteOfDay(Date.parse(bucket.start)) / SLOT_MINUTES
      );
      slots[slot]?.push(bucket.mean);
    }
  }
  return slots.map((means) =>
    means.length < 2
      ? null
      : {
          low: Math.min(...means),
          high: Math.max(...means),
          mean: means.reduce((sum, value) => sum + value, 0) / means.length,
        }
  );
};

export interface TodayPoint {
  /** Minutes since local midnight. */
  minute: number;
  time: number;
  value: number;
}

export const todayPoints = (buckets: SeriesData["buckets"]): TodayPoint[] =>
  buckets.flatMap((bucket) => {
    const time = Date.parse(bucket.start);
    return bucket.mean === null
      ? []
      : [{ minute: minuteOfDay(time), time, value: bucket.mean }];
  });

export type Departure =
  | { kind: "none" }
  | { kind: "within"; latest: TodayPoint }
  | {
      kind: "outside";
      latest: TodayPoint;
      above: boolean;
      /** How far past the usual range's edge the latest value is. */
      gap: number;
      /** When the run of out-of-range values that reaches now began. */
      since: number;
    };

const side = (point: TodayPoint, usual: (UsualSlot | null)[]): number => {
  const slot = usual[Math.floor(point.minute / SLOT_MINUTES)];
  if (!slot) {
    return 0;
  }
  if (point.value > slot.high) {
    return 1;
  }
  return point.value < slot.low ? -1 : 0;
};

/** Whether today's latest value is outside the usual range, and since when. */
export const departure = (
  today: TodayPoint[],
  usual: (UsualSlot | null)[]
): Departure => {
  const latest = today.at(-1);
  const slot = latest && usual[Math.floor(latest.minute / SLOT_MINUTES)];
  if (!latest || !slot) {
    return { kind: "none" };
  }
  const now = side(latest, usual);
  if (now === 0) {
    return { kind: "within", latest };
  }
  let start = today.length - 1;
  while (start > 0 && side(today[start - 1], usual) === now) {
    start -= 1;
  }
  return {
    kind: "outside",
    latest,
    above: now > 0,
    gap: now > 0 ? latest.value - slot.high : slot.low - latest.value,
    since: today[start].time,
  };
};

export interface DayRow {
  minute: number;
  today: number | null;
  usual: [number, number] | null;
  typical: number | null;
}

/**
 * One row per five minutes of the day for the plot. The usual range sits on
 * its half-hour marks, with a last row at midnight so the band reaches the
 * end of the axis.
 */
export const dayRows = (
  today: TodayPoint[],
  usual: (UsualSlot | null)[]
): DayRow[] => {
  const values = new Map(today.map((point) => [point.minute, point.value]));
  const rows: DayRow[] = [];
  for (let at = 0; at <= 24 * 60; at += 5) {
    const onSlot = at % SLOT_MINUTES === 0;
    const slot = onSlot
      ? usual[Math.min(at / SLOT_MINUTES, SLOTS - 1)]
      : undefined;
    rows.push({
      minute: at,
      today: values.get(at) ?? null,
      usual: slot ? [slot.low, slot.high] : null,
      typical: slot ? slot.mean : null,
    });
  }
  return rows;
};
