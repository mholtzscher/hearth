import { useState, type CSSProperties } from "react";
import { feedEntries } from "../../bindings/changes";
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
  return <div className="view-timeline-feed">{visible.length === 0 ? <p className="view-empty">{hasHistory ? `No changes in the last ${windowWords}` : "Historical readings will appear here."}</p> : groups.map(group => <section key={group.key}>
    <h5>{group.label}</h5><ol>{group.entries.map(entry => <li key={`${entry.row}:${entry.at}`} className="view-timeline-feed-entry" style={{ "--timeline-color": `var(--chart-${(indices[entry.row] ?? 0) % 8 + 1})` } as CSSProperties}>
      <time dateTime={new Date(entry.at).toISOString()}>{new Date(entry.at).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}</time><span className="view-timeline-rail" aria-hidden="true" /><div><b>{entry.label}</b> <span>{entry.text}</span>
        {entry.pulses && <div className="view-timeline-pulses" aria-hidden="true">{entry.pulses.map((pulse, i) => {
          const first = entry.pulses![0].from, last = Math.max(entry.pulses![entry.pulses!.length - 1].to, first + 1);
          return <span key={i} style={{ left: `${100 * (pulse.from - first) / (last - first)}%`, width: `${Math.max(0.6, 100 * (pulse.to - pulse.from) / (last - first))}%` }} />;
        })}</div>}
      </div>
    </li>)}</ol>
  </section>)}{entries.length > shown && <button className="view-timeline-more" type="button" onClick={() => setShown(n => n + 12)}>Show {entries.length - shown} earlier changes</button>}</div>;
}
