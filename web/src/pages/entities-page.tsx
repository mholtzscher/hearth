import { useState } from "react";
import { Link as RouterLink } from "react-router-dom";

import { apiFetch, useBaseUrlVersion } from "../api/client.ts";
import { formatEntityState } from "../api/entity-state";
import { useApi } from "../api/hooks.ts";
import type { Collection, Entity } from "../api/types.ts";
import { Button } from "../components/button.tsx";
import { EmptyRow } from "../components/empty-row.tsx";
import { ErrorBox } from "../components/error-box.tsx";
import { Input } from "../components/input.tsx";
import { linkClass } from "../components/link-styles.ts";
import { MonoId } from "../components/mono-id.tsx";
import { PageHeader } from "../components/page-header.tsx";
import { RawJson } from "../components/raw-json.tsx";
import { StatusChip } from "../components/status-chip.tsx";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../components/table.tsx";
import { forecastSummary } from "../features/entities/forecast-summary.ts";

export default function EntitiesPage() {
  const baseVersion = useBaseUrlVersion();
  const [cursor, setCursor] = useState<string | undefined>(undefined);
  const [cursorServer, setCursorServer] = useState(baseVersion);
  // Cursors are scoped to one server: restart from the first page there.
  if (cursorServer !== baseVersion) {
    setCursorServer(baseVersion);
    setCursor(undefined);
  }
  const [deviceId, setDeviceId] = useState("");
  const [appliedDeviceId, setAppliedDeviceId] = useState("");
  const query = `/v1/entities?limit=50${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}${appliedDeviceId ? `&device_id=${encodeURIComponent(appliedDeviceId)}` : ""}`;
  const { data, error, loading, refresh } = useApi(`entities-${query}`, () =>
    apiFetch<Collection<Entity>>(query)
  );

  return (
    <div>
      <PageHeader title="Entities">
        <Input
          aria-label="device_id filter"
          placeholder="Filter by device_id…"
          className="w-72"
          value={deviceId}
          onChange={(e) => setDeviceId(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              setAppliedDeviceId(deviceId.trim());
              setCursor(undefined);
            }
          }}
        />
        <Button
          size="default"
          variant="outline"
          onClick={() => {
            setAppliedDeviceId(deviceId.trim());
            setCursor(undefined);
          }}
        >
          Apply
        </Button>
        <Button
          size="default"
          variant="outline"
          onClick={async () => {
            await refresh();
          }}
        >
          Refresh
        </Button>
        {loading && (
          <span className="text-muted-foreground text-label">Loading…</span>
        )}
      </PageHeader>
      {error && <ErrorBox error={error} />}
      {data && (
        <>
          <Table aria-label="Entities">
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Entity id</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Adapter</TableHead>
                <TableHead>Enabled</TableHead>
                <TableHead>Availability</TableHead>
                <TableHead>State</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.items.length === 0 && (
                <EmptyRow colSpan={7} message="No entities." />
              )}
              {data.items.map((e) => (
                <TableRow key={e.id}>
                  <TableCell className="font-medium">
                    <RouterLink to={`/entities/${e.id}`} className={linkClass}>
                      {e.name}
                    </RouterLink>
                  </TableCell>
                  <TableCell className="max-w-[22rem]">
                    <MonoId value={e.id} className="text-muted-foreground" />
                  </TableCell>
                  <TableCell className="text-label font-mono">
                    {e.type}
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {e.adapter_id}
                  </TableCell>
                  <TableCell>{e.enabled ? "yes" : "no"}</TableCell>
                  <TableCell>
                    <StatusChip status={e.availability.status} />
                  </TableCell>
                  <TableCell className="text-label font-mono">
                    {e.state
                      ? (forecastSummary(e.type, e.state.value) ??
                        formatEntityState(e))
                      : "—"}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
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
          <RawJson value={data} title="Raw list JSON" headingLevel={2} />
        </>
      )}
    </div>
  );
}
