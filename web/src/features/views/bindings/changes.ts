import { z } from "zod";

import { apiFetch } from "../../../api/client";
import type { ReadRequest } from "../data/cache";
import type {
  EntitySource,
  SeriesWindow,
  TimelineData,
  TimelineInterval,
  WeekPatternData,
  Words,
} from "./types";
import { stateWords } from "./words";

const timestamp = z.string().datetime({ offset: true });
const range = z.object({ from: timestamp, to: timestamp });
const common = {
  entity_id: z.string(),
  select_field: z.string(),
  range,
  time_axis: z.literal("observed_at"),
  coverage: z.object({
    basis: z.literal("retained_changes"),
    seeded: z.boolean(),
  }),
};
const timeline = z.object({
  ...common,
  kind: z.literal("timeline"),
  intervals: z.array(
    z.discriminatedUnion("kind", [
      z.object({ kind: z.literal("unknown"), from: timestamp, to: timestamp }),
      z.object({
        kind: z.literal("value"),
        from: timestamp,
        to: timestamp,
        value: z.union([z.boolean(), z.string(), z.null()]),
      }),
    ])
  ),
});
const summary = z.object({
  ...common,
  kind: z.literal("summary"),
  bucket: z.object({
    duration: z.literal("1h0m0s"),
    alignment: z.literal("range_start"),
  }),
  buckets: z.array(
    z.object({
      range,
      seconds_true: z.number().nonnegative(),
      transitions_into_true: z.number().int().nonnegative(),
      known_seconds: z.number().nonnegative(),
    })
  ),
});
const durations: Record<SeriesWindow, number> = {
  "1h": 3_600_000,
  "6h": 21_600_000,
  "24h": 86_400_000,
  "7d": 604_800_000,
};
const millis = (time: string) => Date.parse(time);
const same = (a: string, b: Date) => millis(a) === b.getTime();

const validateTimeline = (
  raw: unknown,
  source: EntitySource,
  from: Date,
  to: Date
) => {
  const parsed = timeline.safeParse(raw);
  if (!parsed.success) {
    throw new Error("Invalid timeline");
  }
  const { data } = parsed;
  let cursor = from.getTime();
  if (
    data.entity_id !== source.entity_id ||
    data.select_field !== (source.field ?? "") ||
    !same(data.range.from, from) ||
    !same(data.range.to, to)
  ) {
    throw new Error("Invalid timeline");
  }
  for (const interval of data.intervals) {
    // Core times have nanosecond precision, so two changes in one millisecond
    // leave a valid interval that is empty here; readTimeline drops it.
    if (millis(interval.from) !== cursor || millis(interval.to) < cursor) {
      throw new Error("Invalid timeline");
    }
    cursor = millis(interval.to);
  }
  if (cursor !== to.getTime()) {
    throw new Error("Invalid timeline");
  }
  return data;
};

export const timelineRequest = (
  source: EntitySource,
  window: SeriesWindow
): ReadRequest => ({
  key: `entity-changes:${source.entity_id}:${source.field ?? ""}:${window}`,
  kind: "series",
  load: (signal, to) => {
    const from = new Date(to.getTime() - durations[window]);
    const body = JSON.stringify({
      range: { from: from.toISOString(), to: to.toISOString() },
      select_field: source.field ?? "",
      result: { kind: "timeline" },
    });
    return apiFetch(
      `/v1/entities/${encodeURIComponent(source.entity_id)}/state/changes`,
      { method: "POST", body, signal },
      (text) => validateTimeline(JSON.parse(text), source, from, to)
    );
  },
});

/** Midnight six local calendar days before the supplied time. */
export const weekStart = (now: Date): Date => {
  const start = new Date(now);
  start.setHours(0, 0, 0, 0);
  start.setDate(start.getDate() - 6);
  return start;
};

export const weekPatternRequest = (
  source: EntitySource,
  start: Date
): ReadRequest => ({
  key: `entity-week:${source.entity_id}:${source.field ?? ""}:${start.toISOString()}`,
  kind: "series",
  load: (signal, to) => {
    const body = JSON.stringify({
      range: { from: start.toISOString(), to: to.toISOString() },
      select_field: source.field ?? "",
      result: {
        kind: "summary",
        bucket: { duration: "1h", alignment: "range_start" },
      },
    });
    return apiFetch(
      `/v1/entities/${encodeURIComponent(source.entity_id)}/state/changes`,
      { method: "POST", body, signal },
      (text) => {
        const parsed = summary.safeParse(JSON.parse(text));
        if (!parsed.success) {
          throw new Error("Invalid week pattern");
        }
        const { data } = parsed;
        if (
          data.entity_id !== source.entity_id ||
          data.select_field !== (source.field ?? "") ||
          !same(data.range.from, start) ||
          !same(data.range.to, to)
        ) {
          throw new Error("Invalid week pattern");
        }
        let cursor = start.getTime();
        for (const bucket of data.buckets) {
          const end = Math.min(cursor + 3_600_000, to.getTime());
          if (
            cursor >= to.getTime() ||
            millis(bucket.range.from) !== cursor ||
            millis(bucket.range.to) !== end ||
            bucket.known_seconds > (end - cursor) / 1000 ||
            bucket.seconds_true > bucket.known_seconds
          ) {
            throw new Error("Invalid week pattern");
          }
          cursor = end;
        }
        if (cursor !== to.getTime()) {
          throw new Error("Invalid week pattern");
        }
        return data;
      }
    );
  },
});

const valueKey = (interval: Extract<TimelineInterval, { kind: "value" }>) =>
  interval.truthy === null
    ? `text:${interval.text}`
    : `bool:${interval.truthy}`;

export const readTimeline = (
  raw: unknown,
  source: EntitySource,
  window: SeriesWindow,
  words?: Words
): TimelineData => {
  const parsed = timeline.parse(raw);
  const from = millis(parsed.range.from);
  const to = millis(parsed.range.to);
  const data = validateTimeline(raw, source, new Date(from), new Date(to));
  const intervals: TimelineInterval[] = data.intervals
    .filter((interval) => millis(interval.to) > millis(interval.from))
    .map((interval) => {
      const bounds = { from: millis(interval.from), to: millis(interval.to) };
      if (interval.kind === "unknown") {
        return { kind: "unknown", ...bounds };
      }
      if (interval.value === null) {
        return { kind: "absent", ...bounds };
      }
      const text = stateWords(interval.value, source, words);
      if (text === undefined) {
        throw new Error("Invalid timeline");
      }
      return {
        kind: "value",
        ...bounds,
        text,
        truthy: typeof interval.value === "boolean" ? interval.value : null,
      };
    });
  const totals: TimelineData["totals"] = [];
  const totalsByText = new Map<string, TimelineData["totals"][number]>();
  // A yes/no value totals by truth, not by its words, which a card may set equal.
  for (const [index, interval] of intervals.entries()) {
    if (interval.kind !== "value") {
      continue;
    }
    const key = valueKey(interval);
    let total = totalsByText.get(key);
    if (!total) {
      total = {
        text: interval.text,
        truthy: interval.truthy,
        seconds: 0,
        entries: 0,
      };
      totals.push(total);
      totalsByText.set(key, total);
    }
    total.seconds += (interval.to - interval.from) / 1000;
    const previous = intervals[index - 1];
    if (
      previous &&
      previous.kind !== "unknown" &&
      (previous.kind === "absent" || valueKey(previous) !== key)
    ) {
      total.entries += 1;
    }
  }
  return { from, to, window, intervals, totals };
};

export const readWeekPattern = (
  raw: unknown,
  start: Date,
  now: Date
): WeekPatternData => {
  const data = summary.parse(raw);
  const days = Array.from({ length: 7 }, (_, index) => {
    const day = new Date(start);
    day.setDate(day.getDate() + index);
    return {
      start: day.getTime(),
      hours: Array.from({ length: 24 }, (_hour, hour) => {
        const cell = new Date(day);
        cell.setHours(hour, 0, 0, 0);
        return {
          start: cell.getTime(),
          end: cell.getTime() + 3_600_000,
          seconds: null as number | null,
          entries: 0,
          future: cell.getTime() >= now.getTime(),
        };
      }),
    };
  });
  for (const bucket of data.buckets) {
    const time = new Date(bucket.range.from);
    const day = days.find((item) => {
      const date = new Date(item.start);
      return (
        date.getFullYear() === time.getFullYear() &&
        date.getMonth() === time.getMonth() &&
        date.getDate() === time.getDate()
      );
    });
    const cell = day?.hours[time.getHours()];
    if (cell && bucket.known_seconds > 0) {
      cell.seconds = (cell.seconds ?? 0) + bucket.seconds_true;
      cell.entries += bucket.transitions_into_true;
      cell.future = false;
    }
  }
  return { from: start.getTime(), to: now.getTime(), days };
};

export interface FeedEntry {
  label: string;
  row: number;
  at: number;
  end: number;
  text: string;
  group: string;
  groupKey: string;
  pulses?: { from: number; to: number }[];
}
const dayPart = (at: number, now: number) => {
  const date = new Date(at);
  const hour = date.getHours();
  let part: "overnight" | "morning" | "afternoon" | "evening";
  if (hour < 5 || hour >= 22) {
    part = "overnight";
  } else if (hour < 12) {
    part = "morning";
  } else if (hour < 17) {
    part = "afternoon";
  } else {
    part = "evening";
  }
  const anchor = new Date(date);
  anchor.setHours(0, 0, 0, 0);
  if (hour < 5) {
    anchor.setDate(anchor.getDate() - 1);
  }
  const today = new Date(now);
  today.setHours(0, 0, 0, 0);
  const yesterday = new Date(today);
  yesterday.setDate(yesterday.getDate() - 1);
  let day: string;
  if (anchor.getTime() === today.getTime()) {
    day = "This";
  } else if (anchor.getTime() === yesterday.getTime()) {
    day = "Yesterday";
  } else {
    day = anchor.toLocaleDateString(undefined, { weekday: "short" });
  }
  return {
    group:
      part === "overnight" &&
      (anchor.getTime() === today.getTime() ||
        anchor.getTime() === yesterday.getTime())
        ? "Overnight"
        : `${day} ${part}`,
    groupKey: `${anchor.getTime()}:${part}`,
  };
};
export const durationWords = (seconds: number): string => {
  if (seconds < 30) {
    return "under 1 min";
  }
  const minutes = Math.round(seconds / 60);
  return minutes < 60
    ? `${minutes} min`
    : `${Math.floor(minutes / 60)} h${minutes % 60 ? ` ${minutes % 60} min` : ""}`;
};
export const feedEntries = (
  rows: { label: string; data: TimelineData }[],
  now: number
): FeedEntry[] => {
  const entries: FeedEntry[] = [];
  for (const [row, { label, data }] of rows.entries()) {
    // In a text row a known absence is a value worded "No value"; changes from
    // unknown time still record nothing.
    const textRow = !data.intervals.some(
      (interval) => interval.kind === "value" && interval.truthy !== null
    );
    const words = (interval?: TimelineInterval) => {
      if (interval?.kind === "value") {
        return interval.text;
      }
      if (interval?.kind === "absent" && textRow) {
        return "No value";
      }
    };
    for (const [index, interval] of data.intervals.entries()) {
      const previous = data.intervals[index - 1];
      let text: string;
      let end = interval.to;
      if (interval.kind === "value" && interval.truthy === true) {
        text = `${interval.text.toLowerCase()} for ${durationWords((interval.to - interval.from) / 1000)}${interval.to === data.to ? " so far" : ""}`;
      }
      // A text change is an instant, so its pulse is a tick rather than the held interval.
      else if (
        textRow &&
        words(previous) !== undefined &&
        words(interval) !== undefined &&
        words(previous) !== words(interval)
      ) {
        text = `${words(previous)} → ${words(interval)}`;
        end = interval.from;
      } else {
        continue;
      }
      entries.push({
        label,
        row,
        at: interval.from,
        end,
        text,
        ...dayPart(interval.from, now),
      });
    }
  }
  const grouped = new Map<string, FeedEntry[]>();
  for (const entry of entries) {
    const key = `${entry.row}:${entry.groupKey}`;
    const group = grouped.get(key);
    if (group) {
      group.push(entry);
    } else {
      grouped.set(key, [entry]);
    }
  }
  return [...grouped.values()]
    .flatMap((items) => {
      if (items.length <= 3) {
        return items;
      }
      const ordered = items.toSorted((a, b) => a.at - b.at);
      const [first] = ordered;
      const last = ordered.at(-1) ?? first;
      return [
        {
          ...last,
          text: `${items.length} times since ${new Date(first.at).toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" })}`,
          pulses: ordered.map((item) => ({ from: item.at, to: item.end })),
        },
      ];
    })
    .toSorted((a, b) => b.at - a.at);
};
