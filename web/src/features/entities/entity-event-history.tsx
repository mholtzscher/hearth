import { useState } from "react";

import { apiFetch, useBaseUrlVersion } from "../../api/client.ts";
import { useApi } from "../../api/hooks.ts";
import type { Collection, EntityEventEntry } from "../../api/types.ts";
import { Button } from "../../components/button.tsx";
import { ErrorBox } from "../../components/error-box.tsx";
import { MonoId } from "../../components/mono-id.tsx";
import { StatusChip } from "../../components/status-chip.tsx";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../../components/table.tsx";

const EVENT_HISTORY_LIMIT = 50;

/** Entity Event history for one event-source Entity: newest-first reports with
    refresh and cursor paging. Owns its request, so a scope change remounts into
    an empty cursor. */
const EntityEventHistory = ({
  entityId,
  supportedNames,
}: {
  entityId: string;
  supportedNames: string[];
}) => {
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
      <Button
        size="default"
        variant="outline"
        onClick={() => setNonce((n) => n + 1)}
      >
        Latest / Refresh history
      </Button>
      <p className="text-muted-foreground text-label mt-1.5">
        Newer events appear after refreshing. Older entries may expire while
        paging.
      </p>
      <EventHistoryScope
        key={scopeKey}
        entityId={entityId}
        supportedNames={supportedNames}
      />
    </div>
  );
};

/** One Entity/server/refresh scope: owns the cursor and the request, so a
    scope change remounts with an empty cursor and no carried-over rows. */
const EventHistoryScope = ({
  entityId,
  supportedNames,
}: {
  entityId: string;
  supportedNames: string[];
}) => {
  const [cursor, setCursor] = useState<string | undefined>(undefined);
  const { data, error, loading } = useApi(
    `event-history-${entityId}-${cursor ?? "first"}`,
    () =>
      apiFetch<Collection<EntityEventEntry>>(
        `/v1/entities/${entityId}/events?limit=${EVENT_HISTORY_LIMIT}${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`
      )
  );

  return (
    <div>
      {loading && (
        <span className="text-muted-foreground text-label mt-1.5 block">
          Loading…
        </span>
      )}
      {error && <ErrorBox error={error} />}
      {data && (
        <>
          {data.items.length === 0 ? (
            <p className="text-muted-foreground text-label mt-3">
              No Entity Events are recorded.
              {supportedNames.length > 0 &&
                ` Supported event names: ${supportedNames.join(", ")}.`}
            </p>
          ) : (
            <Table className="mt-3" aria-label="Entity events">
              <TableHeader>
                <TableRow>
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
                        tone={
                          entry.disposition === "accepted"
                            ? "success"
                            : undefined
                        }
                      />
                    </TableCell>
                    <TableCell className="text-label font-mono">
                      {entry.rejection_code ?? "—"}
                    </TableCell>
                    <TableCell className="max-w-[16rem]">
                      <MonoId
                        value={entry.event_id}
                        className="text-muted-foreground"
                      />
                    </TableCell>
                    <TableCell className="text-muted-foreground text-label font-mono">
                      {entry.emitted_at}
                    </TableCell>
                    <TableCell className="text-muted-foreground text-label font-mono">
                      {entry.received_at}
                    </TableCell>
                    <TableCell className="text-muted-foreground text-label font-mono">
                      {entry.recorded_at}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
          {data.next_cursor && (
            <Button
              size="default"
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
};

export default EntityEventHistory;
