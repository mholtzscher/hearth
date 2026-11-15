import { useState, type CSSProperties } from "react";
import { feedEntries } from "../../bindings/changes";
import { Button } from "../../../../components/ui/button";
import type { TimelineRow } from "./Lanes";

export function TimelineFeed({ rows, windowWords }: { rows: TimelineRow[]; windowWords: string }) {
  const [shown, setShown] = useState(12);
  const entries = feedEntries(rows.flatMap((row, index) => row.resource.data?.timeline ? [{ label: row.label, data: row.resource.data.timeline, index }] : []), Date.now());
  // Preserve series colors when preceding rows have no history.
  const indices = rows.flatMap((row, index) => row.resource.data?.timeline ? [index] : []);
  const visible = entries.slice(0, shown);
  const groups: Array<{ key: string; label: string; entries: typeof visible }> = [];
  for (const entry of visible) {
    let group = groups[groups.length - 1];
    if (!group || group.key !== entry.groupKey) { group = { key: entry.groupKey, label: entry.group, entries: [] }; groups.push(group); }
    group.entries.push(entry);
  }
  const hasHistory = rows.some(row => row.resource.data?.timeline);
  return <div className="grid gap-1">{visible.length === 0 ? <p className="py-6 text-label leading-normal text-muted-foreground">{hasHistory ? `No changes in the last ${windowWords}` : "Historical readings will appear here."}</p> : groups.map((group, groupIndex) => <section key={group.key}>
    <h5 className={`mb-1 text-label font-medium text-muted-foreground ${groupIndex === 0 ? "mt-0" : "mt-3.5"}`}>{group.label}</h5><ol className="list-none">{group.entries.map(entry => <li key={`${entry.row}:${entry.at}`} data-slot="timeline-feed-entry" className="grid min-h-[30px] grid-cols-[4.6rem_14px_minmax(0,1fr)] gap-x-2.5 gap-y-0" style={{ "--timeline-color": `var(--chart-${(indices[entry.row] ?? 0) % 8 + 1})` } as CSSProperties}>
      <time className="whitespace-nowrap text-right text-muted-foreground tabular-nums" dateTime={new Date(entry.at).toISOString()}>{new Date(entry.at).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}</time><span className="relative before:absolute before:inset-y-0 before:left-1.5 before:w-px before:bg-gray-4 before:content-[''] after:absolute after:top-1.5 after:left-0.5 after:size-[9px] after:rounded-full after:bg-(--timeline-color) after:shadow-[0_0_0_3px_var(--gray-1)] after:content-['']" aria-hidden="true" /><div className="min-w-0 pb-2.5"><b className="font-medium">{entry.label}</b> <span className="text-muted-foreground">{entry.text}</span>
        {entry.pulses && <div className="relative mt-1.5 h-2.5 rounded-[2px] bg-gray-2" aria-hidden="true">{entry.pulses.map((pulse, i) => {
          const first = entry.pulses![0].from, last = Math.max(entry.pulses![entry.pulses!.length - 1].to, first + 1);
          return <span className="absolute inset-y-0 min-w-0.5 rounded-[1px] bg-(--timeline-color)" key={i} style={{ left: `${100 * (pulse.from - first) / (last - first)}%`, width: `${Math.max(0.6, 100 * (pulse.to - pulse.from) / (last - first))}%` }} />;
        })}</div>}
      </div>
    </li>)}</ol>
  </section>)}{entries.length > shown && <Button variant="outline" size="touch" className="mt-1.5 ms-[calc(4.6rem+34px)] justify-self-start @max-[420px]:ms-0" type="button" onClick={() => setShown(n => n + 12)}>Show {entries.length - shown} earlier changes</Button>}</div>;
}
