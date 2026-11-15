import type { z } from "zod";
import { durationWords } from "../../bindings/changes";
import { useWeekPattern } from "../../bindings/registry";
import type { Resolved, TextData, WeekPatternData } from "../../bindings/types";
import { DataCard } from "../DataCard";
import { cardStatus } from "../status";
import { intervalMethod } from "../state-timeline/StateTimeline";
import { weekPatternSchema } from "./schema";

const cellClass = "block rounded-[2px] bg-gray-3 data-[level='1']:bg-[color-mix(in_oklch,var(--gray-10)_22%,var(--gray-2))] data-[level='2']:bg-[color-mix(in_oklch,var(--gray-10)_45%,var(--gray-2))] data-[level='3']:bg-[color-mix(in_oklch,var(--gray-10)_70%,var(--gray-2))] data-[level='4']:bg-gray-10 data-[future]:bg-transparent data-[future]:shadow-[inset_0_0_0_1px_var(--gray-3)] data-[unknown]:bg-transparent data-[unknown]:shadow-[inset_0_0_0_1px_var(--gray-3)] data-[unknown]:outline data-[unknown]:outline-dashed data-[unknown]:outline-gray-5 data-[unknown]:outline-offset-[-1px]";
const totalClass = "whitespace-nowrap text-right text-muted-foreground @max-[420px]:hidden";

function level(seconds: number | null): number | null {
  if (seconds === null) return null;
  return seconds === 0 ? 0 : seconds <= 300 ? 1 : seconds <= 900 ? 2 : seconds <= 1800 ? 3 : 4;
}
function dayTotals(day: WeekPatternData["days"][number]) {
  return { count: day.hours.reduce((sum, cell) => sum + cell.entries, 0), seconds: day.hours.reduce((sum, cell) => sum + (cell.seconds ?? 0), 0), known: day.hours.some(cell => cell.seconds !== null), unknown: day.hours.some(cell => cell.seconds === null && !cell.future) };
}

export function WeekPattern({ label, entityId, resource, trueWord = "On" }: { label: string; entityId: string; resource: Resolved<{ current?: TextData; week?: WeekPatternData }>; trueWord?: string }) {
  const status = cardStatus(resource), week = resource.data?.week, metadata = resource.metadata;
  const signature = week && JSON.stringify(week.days.map(day => { const total = dayTotals(day); return { cells: day.hours.map(cell => cell.future ? "future" : level(cell.seconds)), count: total.known ? total.count : null, total: total.known ? durationWords(total.seconds) : "No history" }; }));
  return <DataCard className="@container" title={label} unit="Last 7 days" status={status.entries} sources={[{ entityId, label: metadata?.label ?? label, enabled: metadata?.enabled, availability: metadata?.availability }]}
    arrivals={week && signature ? [{ key: entityId, updatedAt: new Date(week.to).toISOString(), shown: signature, eligible: status.glows }] : []}
    sourceExtra={<><p>{intervalMethod}</p>{week && <p>{new Date(week.from).toLocaleString()} to {new Date(week.to).toLocaleString()}</p>}</>}>
    {week ? <>
      <div role="region" tabIndex={0} aria-label={`${label}, last 7 days`}>
        <ul className="sr-only">{week.days.map(day => { const total = dayTotals(day); return <li key={day.start}>{new Date(day.start).toLocaleDateString([], { weekday: "long", month: "short", day: "numeric" })}: {total.known ? `${total.count} times, ${durationWords(total.seconds)} ${trueWord.toLowerCase()}.${total.unknown ? " Some hours have no history." : ""}` : "No history."}</li>; })}</ul>
        <div data-slot="week-grid" className="grid grid-cols-[2.4rem_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-[3px] tabular-nums @max-[420px]:grid-cols-[2.2rem_minmax(0,1fr)]" aria-hidden="true"><span /><div className="grid grid-cols-4 text-label text-muted-foreground"><span>12 AM</span><span>6 AM</span><span>12 PM</span><span>6 PM</span></div><span className={totalClass}>{trueWord}</span>
          {week.days.map((day, index) => { const total = dayTotals(day); return <div className="contents" key={day.start}>
            <span className="text-muted-foreground">{index === 6 ? "Today" : new Date(day.start).toLocaleDateString([], { weekday: "short" })}</span>
            <div data-slot="week-hours" className="grid grid-cols-[repeat(24,minmax(0,1fr))] gap-0.5 @max-[420px]:gap-px">{day.hours.map((cell, hour) => <span key={hour} data-slot="week-cell" className={`${cellClass} aspect-[1/1.15]`} data-level={level(cell.seconds) ?? undefined} data-unknown={cell.seconds === null && !cell.future || undefined} data-future={cell.future || undefined} />)}</div>
            <span className={totalClass}>{total.known ? <><b className="font-medium text-foreground">{total.count}×</b> {durationWords(total.seconds)}</> : "No history"}</span>
          </div>; })}
        </div>
      </div>
      <div className="mt-3.5 flex flex-wrap items-center gap-x-3.5 gap-y-1.5 text-label text-muted-foreground"><span>Minutes {trueWord.toLowerCase()} per hour</span><ol className="flex list-none flex-wrap gap-x-[9px] gap-y-[3px]">{["0", "1–5", "6–15", "16–30", "30+"].map((name, index) => <li className="flex items-center gap-[5px]" key={name}><span className={`${cellClass} aspect-square w-3`} data-level={index} aria-hidden="true" />{name}</li>)}</ol></div>
    </> : <p className="py-6 text-label leading-normal text-muted-foreground">Historical readings will appear here.</p>}
  </DataCard>;
}

export function BoundWeekPattern({ source, label, words }: z.infer<typeof weekPatternSchema>) {
  const resource = useWeekPattern({ source, words });
  const trueWord = words?.true ?? (source.field === "/active" ? "Active" : "On");
  return <WeekPattern label={label ?? resource.metadata?.label ?? "State"} entityId={source.entity_id} resource={resource} trueWord={trueWord} />;
}
