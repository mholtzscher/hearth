// Synthetic household data for the chart prototypes. Nothing here is read from
// Core: every value is authored, deterministic, and anchored to one local time.

const minute = 60_000;
const hour = 60 * minute;

/** The prototypes' wall clock: Monday 9 November 2026, 3:40 PM local time. */
export const NOW = new Date(2026, 10, 9, 15, 40).getTime();

/** Local midnight `offset` days before today. */
export const dayStart = (offset: number): number => {
  const date = new Date(NOW);
  date.setHours(0, 0, 0, 0);
  date.setDate(date.getDate() - offset);
  return date.getTime();
};

/** Deterministic noise in [-0.5, 0.5). */
const noise = (index: number, seed: number): number => {
  const raw = Math.sin(index * 12.9898 + seed * 78.233) * 43_758.5453;
  return raw - Math.floor(raw) - 0.5;
};

const round = (value: number, digits = 1): number =>
  Number(value.toFixed(digits));

export const weekdayLabel = (start: number): string =>
  start === dayStart(0)
    ? "Today"
    : new Date(start).toLocaleDateString([], { weekday: "short" });

export const clockLabel = (ms: number): string =>
  new Date(ms).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });

// Period totals: energy used per day. Friday has no history; today is partial.

export interface PeriodTotal {
  start: number;
  total: number | null;
  partial: boolean;
}

export const dailyEnergy: PeriodTotal[] = [
  11.8,
  9.6,
  12.4,
  null,
  14.2,
  13.1,
  6.3,
].map((total, index) => ({
  start: dayStart(6 - index),
  total,
  partial: index === 6,
}));

// Period comparison: outdoor temperature in 15-minute slots across one day.

export const SLOTS = 96;
export const slotMillis = 15 * minute;
/** The last slot with a sample today. */
export const nowSlot = Math.floor((NOW - dayStart(0)) / slotMillis);

const daily = (slot: number, mid: number, amplitude: number): number =>
  mid + amplitude * Math.cos(((slot / 4 - 15) / 24) * 2 * Math.PI);

const smooth = (value: number, from: number, to: number): number => {
  const t = Math.min(1, Math.max(0, (value - from) / (to - from)));
  return t * t * (3 - 2 * t);
};

export interface ComparisonSlot {
  time: number;
  today: number | null;
  yesterday: number;
  typical: number;
  usual: [number, number];
}

export const outdoorDay: ComparisonSlot[] = Array.from(
  { length: SLOTS },
  (_, slot) => {
    const week = [1, 2, 3, 4, 5, 6, 7].map(
      (day) =>
        daily(slot, 6.2 + noise(day, 3) * 2.2, 3.4 + noise(day, 5)) +
        noise(slot + day * 97, 7) * 0.2
    );
    const today =
      daily(slot, 7.4, 4.2) +
      smooth(slot / 4, 10.5, 13.5) * 2.4 +
      noise(slot, 11) * 0.4;
    return {
      time: dayStart(0) + slot * slotMillis,
      today: slot <= nowSlot ? round(today) : null,
      yesterday: round(daily(slot, 6.1, 3.5) + noise(slot, 13) * 0.4),
      typical: round(week.reduce((sum, value) => sum + value, 0) / week.length),
      usual: [round(Math.min(...week)), round(Math.max(...week))],
    };
  }
);

// Ranked readings: plug power right now, and room temperatures with their day's range.

export interface RankedReading {
  label: string;
  value: number;
  unavailable?: boolean;
  low?: number;
  high?: number;
}

export const plugPower: RankedReading[] = [
  { label: "Space heater", value: 1180 },
  { label: "Dryer", value: 640 },
  { label: "Fridge", value: 128 },
  { label: "Desk", value: 86, unavailable: true },
  { label: "TV", value: 42 },
  { label: "Reading lamp", value: 0 },
];

export const roomTemperatures: RankedReading[] = [
  { label: "Attic office", value: 24.6, low: 19.8, high: 25.1 },
  { label: "Kitchen", value: 22.8, low: 20.2, high: 23.4 },
  { label: "Living room", value: 21.9, low: 19.9, high: 22.3 },
  { label: "Bedroom", value: 20.4, low: 18.6, high: 21.2 },
  { label: "Nursery", value: 20.1, low: 19.6, high: 20.9 },
  { label: "Basement", value: 17.2, low: 16.8, high: 17.6 },
];

// Stepped line: a thermostat target that holds and jumps, with the room following.

export interface Hold {
  from: number;
  to: number;
  value: number;
}

const windowStart = NOW - 24 * hour;
const at = (dayOffset: number, hours: number, minutes = 0): number =>
  dayStart(dayOffset) + hours * hour + minutes * minute;

const changes: { at: number; value: number }[] = [
  { at: windowStart, value: 18.5 },
  { at: at(1, 17), value: 21 },
  { at: at(1, 22, 30), value: 17.5 },
  { at: at(0, 6, 30), value: 20.5 },
  { at: at(0, 9), value: 18.5 },
  { at: at(0, 14, 15), value: 21 },
];

export const targetHolds: Hold[] = changes.map((change, index) => ({
  from: change.at,
  to: changes[index + 1]?.at ?? NOW,
  value: change.value,
}));

export interface StepPoint {
  time: number;
  target: number;
  room: number;
  /** True where the target differs from the bucket before it. */
  changed: boolean;
}

export const thermostatDay = {
  from: windowStart,
  to: NOW,
  points: (() => {
    const points: StepPoint[] = [];
    let room = 19.4;
    for (let index = 0; index <= 288; index += 1) {
      const time = windowStart + index * 5 * minute;
      const target =
        targetHolds.findLast((hold) => hold.from <= time)?.value ?? 18.5;
      room += (target - room) * 0.035 + noise(index, 17) * 0.06;
      points.push({
        time,
        target,
        room: round(room),
        changed: index > 0 && points[index - 1].target !== target,
      });
    }
    return points;
  })(),
};

// Heat grid and distribution: one room's hourly mean temperature over 7 days.
// A null hour has no history; hours after NOW today are in the future.

export interface GridHour {
  value: number | null;
  future: boolean;
}

export interface GridDay {
  start: number;
  hours: GridHour[];
}

const currentHour = new Date(NOW).getHours();

export const livingRoomWeek: GridDay[] = Array.from({ length: 7 }, (_, row) => {
  const offset = 6 - row;
  const weekday = new Date(dayStart(offset)).getDay();
  const weekend = weekday === 0 || weekday === 6;
  return {
    start: dayStart(offset),
    hours: Array.from({ length: 24 }, (__, h) => {
      const future = offset === 0 && h > currentHour;
      const offline = row === 2 && h >= 2 && h <= 4;
      const value =
        20.6 +
        1.5 * Math.cos(((h - 16.5) / 24) * 2 * Math.PI) +
        (weekend ? smooth(h, 8, 12) * (1 - smooth(h, 15, 19)) * 0.9 : 0) +
        noise(row, 23) * 0.8 +
        noise(row * 24 + h, 29) * 0.3;
      return { value: future || offline ? null : round(value), future };
    }),
  };
});
