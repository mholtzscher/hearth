import { useEffect, useState } from "react";
import { Link as RouterLink } from "react-router-dom";
import { apiFetch, useBaseUrlVersion } from "../api/client.ts";
import { useApi } from "../api/hooks.ts";
import type { Collection, Entity } from "../api/types.ts";
import { EmptyRow, ErrorBox, linkClass, MonoId, RawJson, StatusChip } from "../components/common.tsx";
import { Button } from "../components/ui/button.tsx";
import { Input } from "../components/ui/input.tsx";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../components/ui/table.tsx";

export default function EntitiesPage() {
  const [cursor, setCursor] = useState<string | undefined>(undefined);
  const baseVersion = useBaseUrlVersion();
  useEffect(() => {
    // Cursors are scoped to one server: restart from the first page there.
    setCursor(undefined);
  }, [baseVersion]);
  const [deviceId, setDeviceId] = useState("");
  const [appliedDeviceId, setAppliedDeviceId] = useState("");
  const query = `/v1/entities?limit=50${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}${appliedDeviceId ? `&device_id=${encodeURIComponent(appliedDeviceId)}` : ""}`;
  const { data, error, loading, refresh } = useApi(`entities-${query}`, () =>
    apiFetch<Collection<Entity>>(query),
  );

  return (
    <div>
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="mr-2 text-lg font-semibold">Entities</h1>
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
          size="sm"
          onClick={() => {
            setAppliedDeviceId(deviceId.trim());
            setCursor(undefined);
          }}
        >
          Apply
        </Button>
        <Button size="sm" variant="outline" onClick={() => void refresh()}>
          Refresh
        </Button>
        {loading && <span className="text-sm text-muted-foreground">Loading…</span>}
      </div>
      {error && <ErrorBox error={error} />}
      {data && (
        <>
          <Table className="mt-3">
            <TableHeader>
              <TableRow className="hover:bg-transparent">
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
              {data.items.length === 0 && <EmptyRow colSpan={7} message="No entities." />}
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
                  <TableCell className="font-mono text-xs">{e.type}</TableCell>
                  <TableCell className="text-muted-foreground">{e.adapter_id}</TableCell>
                  <TableCell>{e.enabled ? "yes" : "no"}</TableCell>
                  <TableCell>
                    <StatusChip status={e.availability.status} />
                  </TableCell>
                  <TableCell className="font-mono text-xs">
                    {e.state ? JSON.stringify(e.state.value) : "—"}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {data.next_cursor && (
            <Button size="sm" variant="outline" className="mt-2" onClick={() => setCursor(data.next_cursor)}>
              Next page
            </Button>
          )}
          <RawJson value={data} title="Raw list JSON" />
        </>
      )}
    </div>
  );
}
