import { StatusList } from "../CardFrame";
import { cardStatus } from "../status";
import { Axis, rowSummary, sinceText, totalsText, Track, type TimelineRow } from "./Lanes";

export function TimelineLedger({ rows, windowWords }: { rows: TimelineRow[]; windowWords: string }) {
  const first = rows.find(row => row.resource.data?.timeline)?.resource.data?.timeline;
  return <div className="view-timeline-ledger-wrap" role="region" tabIndex={0} aria-label="State timeline">
    <ul className="sr-only">{rows.map(row => <li key={row.key}>{rowSummary(row, windowWords)}</li>)}</ul>
    <ol className="view-timeline-ledger">{rows.map(row => {
      const status = cardStatus(row.resource);
      return <li key={row.key}>
        <span className="view-timeline-ledger-name">{row.label}</span><span className="view-timeline-total">{totalsText(row.resource.data?.timeline, row.trueWord)}</span>
        <div className="view-timeline-current"><strong data-value={status.value}>{row.resource.data?.current?.text ?? "—"}</strong><span>{sinceText(row)}</span><StatusList entries={status.entries} /></div>
        <Track data={row.resource.data?.timeline} color="var(--gray-9)" dimmed={status.value === "dimmed"} compact />
      </li>;
    })}</ol>
    <Axis data={first} />
  </div>;
}
