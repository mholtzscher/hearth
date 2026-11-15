import type { ReactNode } from "react";

import { cn } from "../../../../lib/utils";
import type { Reading } from "../../bindings/registry";
import type { Resolved } from "../../bindings/types";
import type { SourceItem } from "../data-card";
import type { Arrival } from "../observation";
import { useArrival } from "../observation";
import { ReadingRow as SharedReadingRow, ReadingUnit } from "../reading";
import { cardStatus } from "../status";
import type { CardStatus } from "../status";

export interface ReadingRow {
  key: string;
  name: string;
  nameTreatment?: "live" | "dimmed";
  resource: Resolved<Reading>;
}

/** What a row shows, for comparing successive readings. */
export const shownReading = (data: Reading): string =>
  "text" in data
    ? data.text
    : `${data.value.toFixed(data.format.fraction_digits)} ${data.format.unit_label}`;

/** Each row's status and the reading it shows: dimmed or empty treatments follow the card state table. */
export const rowStates = (
  rows: ReadingRow[]
): (ReadingRow & { status: CardStatus; data?: Reading })[] =>
  rows.map((row) => {
    const status = cardStatus(row.resource);
    return {
      ...row,
      status,
      data: status.value === "empty" ? undefined : row.resource.data,
    };
  });

/** The oldest reading a list shows, so its footer never overstates how current it is. */
export const oldestObservation = (
  rows: { data?: Reading }[]
): string | undefined => {
  const times = rows.flatMap((row) => (row.data ? [row.data.observed_at] : []));
  return times.toSorted((a, b) => Date.parse(a) - Date.parse(b))[0];
};

/** A list glows when any eligible row changes what it shows. */
export const listArrivals = (
  rows: { key: string; status: CardStatus; data?: Reading }[]
): (Arrival & { key: string })[] =>
  rows.flatMap((row) =>
    row.data
      ? [
          {
            key: row.key,
            updatedAt: row.data.observed_at,
            shown: shownReading(row.data),
            eligible: row.status.glows,
          },
        ]
      : []
  );

export const rowSources = (rows: ReadingRow[]): SourceItem[] => {
  const sources = new Map<string, SourceItem>();
  for (const row of rows) {
    const { metadata } = row.resource;
    if (metadata && !sources.has(metadata.id)) {
      sources.set(metadata.id, {
        entityId: metadata.id,
        label:
          metadata.label.toLowerCase() === row.name.toLowerCase()
            ? row.name
            : `${row.name} · ${metadata.label}`,
        availability: metadata.availability,
        enabled: metadata.enabled,
      });
    }
  }
  return [...sources.values()];
};

const Row = ({ row }: { row: ReturnType<typeof rowStates>[number] }) => {
  const { data, status } = row;
  // The rule beneath a changed row lights with the card's edge, naming which reading changed.
  const glow = useArrival(
    data
      ? {
          updatedAt: data.observed_at,
          shown: shownReading(data),
          eligible: status.glows,
        }
      : undefined
  );
  let shown: ReactNode;
  if (!data) {
    shown = "—";
  } else if ("text" in data) {
    shown = data.text;
  } else {
    shown = (
      <>
        {data.value.toFixed(data.format.fraction_digits)}
        {data.format.unit_label && (
          <ReadingUnit className="ms-[3px]">
            {data.format.unit_label}
          </ReadingUnit>
        )}
      </>
    );
  }
  return (
    <SharedReadingRow
      name={row.name}
      nameValue={row.nameTreatment}
      value={status.value}
      arrival={glow}
      status={status.entries}
    >
      {shown}
    </SharedReadingRow>
  );
};

/** Name and reading rows, each carrying its own status. */
export const ReadingRows = ({
  rows,
  className,
}: {
  rows: ReturnType<typeof rowStates>;
  className?: string;
}) => (
  <ul className={cn("-my-1 grid list-none", className)}>
    {rows.map((row) => (
      <Row key={row.key} row={row} />
    ))}
  </ul>
);
