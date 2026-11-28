import type { SeriesData } from "../../bindings/types";
import { niceTicks } from "../time-series/scale";

/** Blocks per day: three local hours each. */
export const BLOCKS_PER_DAY = 8;
const BLOCK_HOURS = 3;

export interface GridBlock {
  start: number;
  /** Mean of the readings placed in this block; null when none were. */
  value: number | null;
  /** The block has not started yet. */
  future: boolean;
}
export interface GridDay {
  start: number;
  blocks: GridBlock[];
}

const dayKey = (date: Date): string =>
  `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}`;
const mean = (values: number[]): number =>
  values.reduce((sum, value) => sum + value, 0) / values.length;
const known = (blocks: GridBlock[]): number[] =>
  blocks.flatMap((block) => (block.value === null ? [] : [block.value]));

/**
 * Seven local days from `from` (local midnight), eight three-hour blocks each.
 * A bucket is placed by its own local start time, never by its index, so a
 * daylight-saving change cannot shift a cell: a repeated hour lands in the
 * block it names and its mean joins that block's.
 */
export const foldBlocks = (
  buckets: SeriesData["buckets"],
  from: number,
  now: number
): GridDay[] => {
  const days = Array.from({ length: 7 }, (_, index) => {
    const day = new Date(from);
    day.setDate(day.getDate() + index);
    return day;
  });
  const slots = new Map<string, number[]>();
  for (const bucket of buckets) {
    if (bucket.mean === null) {
      continue;
    }
    const time = new Date(bucket.start);
    const key = `${dayKey(time)}:${Math.floor(time.getHours() / BLOCK_HOURS)}`;
    slots.set(key, [...(slots.get(key) ?? []), bucket.mean]);
  }
  return days.map((day) => ({
    start: day.getTime(),
    blocks: Array.from({ length: BLOCKS_PER_DAY }, (_, block) => {
      const start = new Date(day);
      start.setHours(block * BLOCK_HOURS, 0, 0, 0);
      const values = slots.get(`${dayKey(day)}:${block}`);
      return {
        start: start.getTime(),
        value: values ? mean(values) : null,
        future: !values && start.getTime() >= now,
      };
    }),
  }));
};

/** One day's mean over the blocks that have readings. */
export const dayMean = (day: GridDay): number | null => {
  const values = known(day.blocks);
  return values.length ? mean(values) : null;
};

/** The whole week's mean over the blocks that have readings. */
export const weekMean = (days: GridDay[]): number | null => {
  const values = days.flatMap((day) => known(day.blocks));
  return values.length ? mean(values) : null;
};

/** The block of the day, averaged over the week, where the field reads highest. */
export const peakBlock = (
  days: GridDay[]
): { block: number; mean: number } | null => {
  let best: { block: number; mean: number } | null = null;
  for (let block = 0; block < BLOCKS_PER_DAY; block += 1) {
    const values = days.flatMap((day) => known([day.blocks[block]]));
    if (values.length && (!best || mean(values) > best.mean)) {
      best = { block, mean: mean(values) };
    }
  }
  return best;
};

/** The local hour a block is named for: its middle, to the hour. */
export const blockMiddleHour = (block: number): number =>
  block * BLOCK_HOURS + 1;

/**
 * Five shades at most, from the scale's equal steps over the week's readings.
 * A longer scale shares its darkest and lightest shades.
 */
export const shadeScale = (days: GridDay[]): ((value: number) => number) => {
  const values = days.flatMap((day) => known(day.blocks));
  if (values.length === 0) {
    return () => 0;
  }
  const scale = niceTicks(Math.min(...values), Math.max(...values), 5);
  const step = scale.ticks[1] - scale.ticks[0];
  const levels = scale.ticks.length - 1;
  return (value) => {
    const level = Math.min(
      levels - 1,
      Math.max(0, Math.floor((value - scale.domain[0]) / step))
    );
    return Math.round((level / Math.max(1, levels - 1)) * 4);
  };
};
