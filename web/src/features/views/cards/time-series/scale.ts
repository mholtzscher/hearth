import type { SeriesData } from "../../bindings/types";

const minute = 60_000;
const hour = 60 * minute;
const day = 24 * hour;

/**
 * Round domain and ticks covering [min, max] in about `count` steps of 1, 2, or
 * 5 × 10^n, so axis labels read as 20, 20.5, 21 rather than 19.8, 20.3, 20.7.
 * A flat series gets a small band around its value instead of a zero span.
 */
export function niceTicks(min: number, max: number, count: number): { domain: [number, number]; ticks: number[] } {
  if (!Number.isFinite(min) || !Number.isFinite(max) || max < min) return { domain: [0, 1], ticks: [0, 1] };
  if (max === min) {
    const pad = Math.abs(min) * 0.05 || 1;
    max += pad;
    min = min >= 0 && min - pad < 0 ? 0 : min - pad;
  }
  const rough = (max - min) / Math.max(1, count);
  const magnitude = 10 ** Math.floor(Math.log10(rough));
  const step = [1, 2, 5, 10].map(n => n * magnitude).find(n => n >= rough) ?? 10 * magnitude;
  const lo = Math.floor(min / step) * step;
  const hi = Math.ceil(max / step) * step;
  const ticks: number[] = [];
  for (let value = lo; value <= hi + step / 2; value += step) ticks.push(Number(value.toFixed(10)));
  return { domain: [ticks[0], ticks[ticks.length - 1]], ticks };
}

export function sameDay(a: number, b: number): boolean {
  return new Date(a).toDateString() === new Date(b).toDateString();
}

/**
 * Axis and tooltip time: "6:45 PM", or "Wed 6:45 PM" when the weekday is needed
 * to place it. Axis ticks pass `short`, so a time on the hour reads "8 PM".
 */
export function timeLabel(ms: number, opts: { weekday: boolean; time?: boolean; short?: boolean }): string {
  const date = new Date(ms);
  const onHour = opts.short && date.getMinutes() === 0;
  const time = opts.time === false ? "" : date.toLocaleTimeString([], onHour ? { hour: "numeric" } : { hour: "numeric", minute: "2-digit" });
  const weekday = opts.weekday ? date.toLocaleDateString([], { weekday: "short" }) : "";
  return [weekday, time].filter(Boolean).join(" ");
}

/**
 * Local-time ticks for a window, at most about six. Each label carries the
 * weekday on the first tick of a new day, so a 24-hour window that crosses
 * midnight never shows the same clock time at both ends without a day.
 */
export function timeTicks(from: number, to: number): Array<{ value: number; label: string }> {
  const span = to - from;
  const step = [5, 15, 30, 60, 120, 180, 240, 360, 720].map(n => n * minute).find(s => span / s <= 6) ?? day;
  const cursor = new Date(from);
  if (step < hour) {
    cursor.setSeconds(0, 0);
    const minutes = step / minute;
    cursor.setMinutes(Math.ceil(cursor.getMinutes() / minutes) * minutes);
  } else if (step < day) {
    cursor.setMinutes(0, 0, 0);
    if (cursor.getTime() < from) cursor.setHours(cursor.getHours() + 1);
    while (cursor.getHours() % (step / hour) !== 0) cursor.setHours(cursor.getHours() + 1);
  } else {
    cursor.setHours(0, 0, 0, 0);
    if (cursor.getTime() < from) cursor.setDate(cursor.getDate() + 1);
  }
  const values: number[] = [];
  while (cursor.getTime() <= to) {
    values.push(cursor.getTime());
    if (step < day) cursor.setTime(cursor.getTime() + step);
    else cursor.setDate(cursor.getDate() + 1);
  }
  const crossesMidnight = !sameDay(from, to);
  return values.map((value, i) => ({
    value,
    label: step >= day
      ? timeLabel(value, { weekday: true, time: false })
      : timeLabel(value, { short: true, weekday: crossesMidnight && (i === 0 ? !sameDay(value, to) : !sameDay(value, values[i - 1])) }),
  }));
}

/**
 * The first sample's start when history begins more than 10% of the window
 * after its start, so a short history is named rather than left as an empty plot.
 */
export function coverageStart(series: SeriesData[]): string | undefined {
  const window = series[0];
  if (!window) return undefined;
  const from = Date.parse(window.from);
  const to = Date.parse(window.to);
  const starts = series.flatMap(item => {
    const bucket = item.buckets.find(b => b.mean !== null);
    return bucket ? [Date.parse(bucket.start)] : [];
  });
  if (starts.length === 0) return undefined;
  const first = Math.min(...starts);
  return first - from > 0.1 * (to - from) ? new Date(first).toISOString() : undefined;
}
