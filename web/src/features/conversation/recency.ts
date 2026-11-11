import { useEffect, useState } from "react";

/** A run of conversations under one date heading in the Recent list. */
export type RecencyGroup<T> = { label: string; items: T[] };

type Dated = { created_at: string; last_message_at?: string | null };

/**
 * Groups newest-first conversations under local calendar headings: Today,
 * Yesterday, Previous 7 days, Previous 30 days, then one heading per month,
 * with the year when it isn't the current one. Groups keep the input order.
 * A conversation with no readable time goes under Earlier, at the end.
 */
export function groupByRecency<T extends Dated>(items: readonly T[], now: Date): RecencyGroup<T>[] {
  const day = (offset: number) => new Date(now.getFullYear(), now.getMonth(), now.getDate() - offset).getTime();
  const bounds: [string, number][] = [["Today", day(0)], ["Yesterday", day(1)], ["Previous 7 days", day(7)], ["Previous 30 days", day(30)]];
  const groups = new Map<string, T[]>();
  const undated: T[] = [];
  for (const item of items) {
    const time = Date.parse(item.last_message_at || item.created_at);
    if (Number.isNaN(time)) { undated.push(item); continue; }
    const label = bounds.find(([, start]) => time >= start)?.[0] ?? monthLabel(new Date(time), now);
    const group = groups.get(label);
    if (group) group.push(item);
    else groups.set(label, [item]);
  }
  const result = [...groups].map(([label, items]) => ({ label, items }));
  return undated.length ? [...result, { label: "Earlier", items: undated }] : result;
}

function monthLabel(date: Date, now: Date): string {
  return date.toLocaleDateString(undefined, date.getFullYear() === now.getFullYear() ? { month: "long" } : { month: "long", year: "numeric" });
}

const startOfDay = (offset = 0) => { const now = new Date(); return new Date(now.getFullYear(), now.getMonth(), now.getDate() + offset).getTime(); };

/**
 * The start of the local calendar day, in milliseconds. It advances at local
 * midnight and when a hidden tab becomes visible again, since a suspended tab's
 * timer may not fire on time, so date headings never keep yesterday's labels.
 */
export function useLocalDay(): number {
  const [day, setDay] = useState(() => startOfDay());
  useEffect(() => {
    const update = () => setDay(startOfDay());
    const timer = setTimeout(update, startOfDay(1) - Date.now());
    document.addEventListener("visibilitychange", update);
    return () => {
      clearTimeout(timer);
      document.removeEventListener("visibilitychange", update);
    };
  }, [day]);
  return day;
}
