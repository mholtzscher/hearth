import type { CSSProperties } from "react";
import { durationWords } from "../../bindings/changes";
import type { Resolved, TextData, TimelineData } from "../../bindings/types";
import { StatusList } from "../CardFrame";
import { cardStatus } from "../status";
import { timeLabel, timeTicks } from "../time-series/scale";

export type TimelineRow = { key: string; label: string; entityId: string; trueWord?: string; resource: Resolved<{ current?: TextData; timeline?: TimelineData }> };

export function totalsText(data?: TimelineData, trueWord = "active"): string {
  if (!data) return "No history";
  const boolean = data.totals.some(total => total.truthy !== null);
  const totals = boolean ? data.totals.filter(total => total.truthy) : data.totals;
  if (!totals.length) return boolean ? `Never ${trueWord.toLowerCase()}` : "No recorded values";
  // Counts belong to yes/no fields ("Open 43 min · 7×"); text values show time per value.
  return totals.map(total => `${total.text} ${durationWords(total.seconds)}${boolean && total.entries ? ` · ${total.entries}×` : ""}`).join(" · ");
}

export function sinceText(row: TimelineRow): string | undefined {
  const timeline = row.resource.data?.timeline, current = row.resource.data?.current;
  const last = timeline?.intervals[timeline.intervals.length - 1];
  return last?.kind === "value" && current?.text === last.text ? `since ${timeLabel(last.from, { weekday: false })}` : undefined;
}

export function Axis({ data }: { data?: TimelineData }) {
  if (!data) return null;
  return <div className="view-timeline-axis" aria-hidden="true">{timeTicks(data.from, data.to).filter(tick => tick.value > data.from && tick.value < data.to - 0.12 * (data.to - data.from)).map(tick => <span key={tick.value} style={{ left: `${100 * (tick.value - data.from) / (data.to - data.from)}%` }}>{tick.label}</span>)}<span className="view-timeline-axis-end">Now</span></div>;
}

export function Track({ data, color, dimmed, compact = false }: { data?: TimelineData; color: string; dimmed: boolean; compact?: boolean }) {
  if (!data) return <div className="view-timeline-track" data-dimmed={dimmed || undefined} aria-hidden="true" />;
  const ranked = [...data.totals].filter(total => total.truthy === null).sort((a, b) => b.seconds - a.seconds);
  const ranks = new Map(ranked.map((total, index) => [total.text, index]));
  return <div className={`view-timeline-track${compact ? " view-timeline-track-compact" : ""}`} data-dimmed={dimmed || undefined} style={{ "--timeline-color": color } as CSSProperties} aria-hidden="true">
    {data.intervals.map((interval, index) => {
      if (interval.kind === "absent" || (interval.kind === "value" && interval.truthy === false)) return null;
      const rank = interval.kind === "value" && interval.truthy === null ? ranks.get(interval.text) ?? 0 : 0;
      const text = interval.kind === "unknown" ? "No history" : interval.text;
      return <span key={index} className="view-timeline-segment" data-unknown={interval.kind === "unknown" || undefined} data-rank={rank > 1 ? 2 : rank} style={{ left: `${100 * (interval.from - data.from) / (data.to - data.from)}%`, width: `${100 * (interval.to - interval.from) / (data.to - data.from)}%` }}>
        {!compact && (interval.kind === "unknown" || interval.truthy === null) && <span>{text}</span>}
      </span>;
    })}
  </div>;
}

export function rowSummary(row: TimelineRow, windowWords: string): string {
  const current = row.resource.data?.current?.text ?? "no current value";
  return `${row.label}: ${current}${sinceText(row) ? ` ${sinceText(row)}` : ""}; ${totalsText(row.resource.data?.timeline, row.trueWord)} in the last ${windowWords}.`;
}

export function TimelineLanes({ rows, windowWords }: { rows: TimelineRow[]; windowWords: string }) {
  const first = rows.find(row => row.resource.data?.timeline)?.resource.data?.timeline;
  return <div className="view-timeline-lanes" role="region" tabIndex={0} aria-label="State timeline">
    <ul className="sr-only">{rows.map(row => <li key={row.key}>{rowSummary(row, windowWords)}</li>)}</ul>
    {rows.map((row, index) => {
      const status = cardStatus(row.resource);
      return <div className="view-timeline-lane" key={row.key}>
        <div className="view-timeline-name"><span>{row.label}</span><span data-value={status.value}>{row.resource.data?.current?.text ?? "—"}</span><StatusList entries={status.entries} /></div>
        <Track data={row.resource.data?.timeline} color={`var(--chart-${index % 8 + 1})`} dimmed={status.value === "dimmed"} />
        <div className="view-timeline-total">{totalsText(row.resource.data?.timeline, row.trueWord)}</div>
      </div>;
    })}
    <div className="view-timeline-axis-spacer" /><Axis data={first} /><div className="view-timeline-axis-spacer" />
  </div>;
}
