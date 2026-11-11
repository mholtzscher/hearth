import type { Reading } from "../../bindings/registry";
import type { Resolved } from "../../bindings/types";
import type { Arrival } from "../observation";
import { useArrival } from "../observation";
import { StatusList, type SourceItem } from "../CardFrame";
import { cardStatus, type CardStatus } from "../status";

export type ReadingRow = { key: string; name: string; nameTreatment?: "live" | "dimmed"; resource: Resolved<Reading> };

/** What a row shows, for comparing successive readings. */
export function shownReading(data: Reading): string {
  return "text" in data ? data.text : `${data.value.toFixed(data.format.fraction_digits)} ${data.format.unit_label}`;
}

/** Each row's status and the reading it shows: dimmed or empty treatments follow the card state table. */
export function rowStates(rows: ReadingRow[]): Array<ReadingRow & { status: CardStatus; data?: Reading }> {
  return rows.map(row => {
    const status = cardStatus(row.resource);
    return { ...row, status, data: status.value === "empty" ? undefined : row.resource.data };
  });
}

/** The oldest reading a list shows, so its footer never overstates how current it is. */
export function oldestObservation(rows: Array<{ data?: Reading }>): string | undefined {
  const times = rows.flatMap(row => row.data ? [row.data.observed_at] : []);
  return times.sort((a, b) => Date.parse(a) - Date.parse(b))[0];
}

/** A list glows when any eligible row changes what it shows. */
export function listArrivals(rows: Array<{ key: string; status: CardStatus; data?: Reading }>): Array<Arrival & { key: string }> {
  return rows.flatMap(row => row.data ? [{ key: row.key, updatedAt: row.data.observed_at, shown: shownReading(row.data), eligible: row.status.glows }] : []);
}

export function rowSources(rows: ReadingRow[]): SourceItem[] {
  const sources = new Map<string, SourceItem>();
  for (const row of rows) {
    const metadata = row.resource.metadata;
    if (metadata && !sources.has(metadata.id)) sources.set(metadata.id, { entityId: metadata.id, label: metadata.label.toLowerCase() === row.name.toLowerCase() ? row.name : `${row.name} · ${metadata.label}`, availability: metadata.availability, enabled: metadata.enabled });
  }
  return [...sources.values()];
}

function Row({ row }: { row: ReturnType<typeof rowStates>[number] }) {
  const { data, status } = row;
  // The rule beneath a changed row lights with the card's edge, naming which reading changed.
  const glow = useArrival(data ? { updatedAt: data.observed_at, shown: shownReading(data), eligible: status.glows } : undefined);
  return <li className="view-row">
    {glow !== undefined && <span key={glow} className="view-row-arrival" aria-hidden="true" />}
    <span className="view-row-name" data-value={row.nameTreatment}>{row.name}</span>
    <span className="view-row-value" data-value={status.value}>
      {data ? "text" in data ? data.text : <>{data.value.toFixed(data.format.fraction_digits)}{data.format.unit_label && <small>{data.format.unit_label}</small>}</> : "—"}
    </span>
    <StatusList entries={status.entries} />
  </li>;
}

/** Name and reading rows, each carrying its own status. */
export function ReadingRows({ rows }: { rows: ReturnType<typeof rowStates> }) {
  return <ul className="view-rows">{rows.map(row => <Row key={row.key} row={row} />)}</ul>;
}
