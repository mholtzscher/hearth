import { useReadings } from "../../bindings/registry";
import type { EntitySource, Words } from "../../bindings/types";
import { CardFrame } from "../CardFrame";
import { listArrivals, oldestObservation, ReadingRows, rowSources, rowStates, type ReadingRow } from "./ReadingRows";

/**
 * Several readings as rows: numeric readings with their units, yes/no and text
 * fields in words. Each row carries its own status, so one unavailable sensor
 * doesn't flag the whole list. The footer states the oldest reading shown.
 */
export function ReadingList({ title, rows }: { title: string; rows: ReadingRow[] }) {
  const states = rowStates(rows);
  return <CardFrame className="view-reading-list" title={title} status={[]} observedAt={oldestObservation(states)} observedLabel="Oldest reading"
    arrivals={listArrivals(states)} sources={rowSources(rows)}>
    <ReadingRows rows={states} />
  </CardFrame>;
}

export function BoundReadingList({ title, rows }: { title: string; rows: Array<{ source: EntitySource; label?: string; words?: Words }> }) {
  const results = useReadings(rows.map(row => ({ source: row.source, words: row.words })));
  return <ReadingList title={title} rows={rows.map((row, index) => ({
    key: `${index}:${row.source.entity_id}:${row.source.field ?? ""}`,
    name: row.label ?? results[index].metadata?.label ?? "Reading",
    resource: results[index],
  }))} />;
}
