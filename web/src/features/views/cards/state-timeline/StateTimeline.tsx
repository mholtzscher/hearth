import type { z } from "zod";
import { useTimelines } from "../../bindings/registry";
import { textField } from "../../bindings/state-fields";
import { stateWords } from "../../bindings/words";
import type { SeriesWindow } from "../../bindings/types";
import { CardFrame, type SourceItem } from "../CardFrame";
import { cardStatus, type StatusEntry } from "../status";
import { TimelineFeed } from "./Feed";
import { TimelineLanes, type TimelineRow } from "./Lanes";
import { TimelineLedger } from "./Ledger";
import { stateTimelineSchema, type TimelineStyle } from "./schema";

export const intervalMethod = "Intervals hold each recorded change until the next one; dashed spans have no earlier record.";
export const windowNames: Record<SeriesWindow, string> = { "1h": "1 hour", "6h": "6 hours", "24h": "24 hours", "7d": "7 days" };

export function StateTimeline({ title, style, window, rows }: { title: string; style: TimelineStyle; window: SeriesWindow; rows: TimelineRow[] }) {
  const sources = new Map<string, SourceItem>();
  const header = new Map<StatusEntry["key"], StatusEntry>();
  const arrivals = rows.flatMap(row => {
    const status = cardStatus(row.resource), metadata = row.resource.metadata;
    if (!sources.has(row.entityId)) sources.set(row.entityId, { entityId: row.entityId, label: metadata?.label ?? row.label, availability: metadata?.availability, enabled: metadata?.enabled });
    if (style === "feed") for (const entry of status.entries) {
      const text = `${row.label}: ${entry.text}`;
      const existing = header.get(entry.key);
      header.set(entry.key, { ...entry, text: existing ? `${existing.text} · ${text}` : text });
    }
    const timeline = row.resource.data?.timeline, last = timeline?.intervals[timeline.intervals.length - 1];
    if (!timeline || !last) return [];
    return [{ key: row.key, updatedAt: new Date(timeline.to).toISOString(), shown: last.kind === "value" ? last.text : last.kind, eligible: status.glows }];
  });
  const first = rows.find(row => row.resource.data?.timeline)?.resource.data?.timeline;
  const method = <><p>{intervalMethod}</p>{first && <p>{new Date(first.from).toLocaleString()} to {new Date(first.to).toLocaleString()}</p>}</>;
  const words = windowNames[window];
  return <CardFrame className="view-timeline-card" title={title} unit={`Last ${words}`} status={[...header.values()]} arrivals={arrivals} sources={[...sources.values()]} sourceExtra={method}>
    {style === "lanes" ? <TimelineLanes rows={rows} windowWords={words} /> : style === "ledger" ? <TimelineLedger rows={rows} windowWords={words} /> : <TimelineFeed key={JSON.stringify([window, rows.map(row => row.key)])} rows={rows} windowWords={words} />}
  </CardFrame>;
}

export function BoundStateTimeline({ title, style, window, rows }: z.infer<typeof stateTimelineSchema>) {
  const results = useTimelines(rows, window);
  return <StateTimeline title={title} style={style} window={window} rows={rows.map((row, index) => ({ key: `${index}:${row.source.entity_id}:${row.source.field ?? ""}`, label: row.label ?? results[index].metadata?.label ?? "Reading", entityId: row.source.entity_id, trueWord: textField(row.source)?.value_type === "boolean" ? stateWords(true, row.source, row.words) : undefined, resource: results[index] }))} />;
}
