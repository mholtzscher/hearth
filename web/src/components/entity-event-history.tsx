import { useState } from "react";
import { apiFetch, useBaseUrlVersion } from "../api/client.ts";
import { useApi } from "../api/hooks.ts";
import type { Collection, EntityEventEntry } from "../api/types.ts";
import { ErrorBox, MonoId, StatusChip } from "./common.tsx";
import { Button } from "./ui/button.tsx";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "./ui/table.tsx";

const EVENT_HISTORY_LIMIT = 50;

/** Entity Event history for one event-source Entity: newest-first reports with
    refresh and cursor paging. Owns its request, so a scope change remounts into
    an empty cursor. */
export default function EntityEventHistory({
  entityId,
  supportedNames,
}: {
  entityId: string;
  supportedNames: string[];
}) {
  const [nonce, setNonce] = useState(0);
  const baseVersion = useBaseUrlVersion();
  // Remount the scope below on Entity, server, or refresh change: the fresh
  // instance starts with no cursor and fires exactly one request, so neither a
  // stale page nor an in-flight request from a previous scope can appear under
  // the new scope. The nonce makes Latest / Refresh history refetch the first
  // page even when already on it.
  const scopeKey = `${entityId}|${baseVersion}|${nonce}`;
  return (
    <div>
      <Button size="sm" variant="outline" onClick={() => setNonce((n) => n + 1)}>
        Latest / Refresh history
      </Button>
      <p className="mt-1.5 text-xs text-muted-foreground">
        Newer events appear after refreshing. Older entries may expire while paging.
      </p>
      <EventHistoryScope key={scopeKey} entityId={entityId} supportedNames={supportedNames} />
    </div>
  );
}

/** One Entity/server/refresh scope: owns the cursor and the request, so a
    scope change remounts with an empty cursor and no carried-over rows. */
function EventHistoryScope({
  entityId,
  supportedNames,
}: {
  entityId: string;
  supportedNames: string[];
}) {
  const [cursor, setCursor] = useState<string | undefined>(undefined);
  const { data, error, loading } = useApi(
    `event-history-${entityId}-${cursor ?? "first"}`,
    () =>
      apiFetch<Collection<EntityEventEntry>>(
        `/v1/entities/${entityId}/events?limit=${EVENT_HISTORY_LIMIT}${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`,
      ),
  );

  return (
    <div>
      {loading && <span className="mt-1.5 block text-sm text-muted-foreground">Loading…</span>}
      {error && <ErrorBox error={error} />}
      {data && (
        <>
          {data.items.length === 0 ? (
            <p className="mt-3 text-sm text-muted-foreground">
              No Entity Events are recorded.
              {supportedNames.length > 0 &&
                ` Supported event names: ${supportedNames.join(", ")}.`}
            </p>
          ) : (
            <Table className="mt-3">
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead>Event</TableHead>
                  <TableHead>Disposition</TableHead>
                  <TableHead>Rejection</TableHead>
                  <TableHead>Event ID</TableHead>
                  <TableHead>SDK emitted</TableHead>
                  <TableHead>Broker received</TableHead>
                  <TableHead>Core recorded</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.items.map((entry) => (
                  <TableRow key={entry.event_id}>
                    <TableCell>{entry.name}</TableCell>
                    <TableCell>
                      <StatusChip
                        status={entry.disposition}
                        tone={entry.disposition === "accepted" ? "success" : undefined}
                      />
                    </TableCell>
                    <TableCell className="font-mono text-xs">
                      {entry.rejection_code ?? "—"}
                    </TableCell>
                    <TableCell className="max-w-[16rem]">
                      <MonoId value={entry.event_id} className="text-muted-foreground" />
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {entry.emitted_at}
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {entry.received_at}
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {entry.recorded_at}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
          {data.next_cursor && (
            <Button
              size="sm"
              variant="outline"
              className="mt-2"
              onClick={() => setCursor(data.next_cursor)}
            >
              Next page
            </Button>
          )}
        </>
      )}
    </div>
  );
}
