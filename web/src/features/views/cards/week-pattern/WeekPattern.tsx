import type { z } from "zod";
import { durationWords } from "../../bindings/changes";
import { useWeekPattern } from "../../bindings/registry";
import type { Resolved, TextData, WeekPatternData } from "../../bindings/types";
import { CardFrame } from "../CardFrame";
import { cardStatus } from "../status";
import { intervalMethod } from "../state-timeline/StateTimeline";
import { weekPatternSchema } from "./schema";

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
  return <CardFrame className="view-week-card" title={label} unit="Last 7 days" status={status.entries} sources={[{ entityId, label: metadata?.label ?? label, enabled: metadata?.enabled, availability: metadata?.availability }]}
    arrivals={week && signature ? [{ key: entityId, updatedAt: new Date(week.to).toISOString(), shown: signature, eligible: status.glows }] : []}
    sourceExtra={<><p>{intervalMethod}</p>{week && <p>{new Date(week.from).toLocaleString()} to {new Date(week.to).toLocaleString()}</p>}</>}>
    {week ? <>
      <div role="region" tabIndex={0} aria-label={`${label}, last 7 days`}>
        <ul className="sr-only">{week.days.map(day => { const total = dayTotals(day); return <li key={day.start}>{new Date(day.start).toLocaleDateString([], { weekday: "long", month: "short", day: "numeric" })}: {total.known ? `${total.count} times, ${durationWords(total.seconds)} ${trueWord.toLowerCase()}.${total.unknown ? " Some hours have no history." : ""}` : "No history."}</li>; })}</ul>
        <div className="view-week-grid" aria-hidden="true"><span /><div className="view-week-axis"><span>12 AM</span><span>6 AM</span><span>12 PM</span><span>6 PM</span></div><span className="view-week-total">{trueWord}</span>
          {week.days.map((day, index) => { const total = dayTotals(day); return <div className="view-week-day" key={day.start}>
            <span>{index === 6 ? "Today" : new Date(day.start).toLocaleDateString([], { weekday: "short" })}</span>
            <div className="view-week-hours">{day.hours.map((cell, hour) => <span key={hour} className="view-week-cell" data-level={level(cell.seconds) ?? undefined} data-unknown={cell.seconds === null && !cell.future || undefined} data-future={cell.future || undefined} />)}</div>
            <span className="view-week-total">{total.known ? <><b>{total.count}×</b> {durationWords(total.seconds)}</> : "No history"}</span>
          </div>; })}
        </div>
      </div>
      <div className="view-week-key"><span>Minutes {trueWord.toLowerCase()} per hour</span><ol>{["0", "1–5", "6–15", "16–30", "30+"].map((name, index) => <li key={name}><span className="view-week-cell" data-level={index} aria-hidden="true" />{name}</li>)}</ol></div>
    </> : <p className="view-empty">Historical readings will appear here.</p>}
  </CardFrame>;
}

export function BoundWeekPattern({ source, label, words }: z.infer<typeof weekPatternSchema>) {
  const resource = useWeekPattern({ source, words });
  const trueWord = words?.true ?? (source.field === "/active" ? "Active" : "On");
  return <WeekPattern label={label ?? resource.metadata?.label ?? "State"} entityId={source.entity_id} resource={resource} trueWord={trueWord} />;
}
