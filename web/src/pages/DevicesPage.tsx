import { useEffect, useState } from "react";
import { Link as RouterLink } from "react-router-dom";
import { apiFetch, useBaseUrlVersion } from "../api/client.ts";
import { useApi } from "../api/hooks.ts";
import type { Collection, Device, DeviceDetail } from "../api/types.ts";
import {
  EmptyRow,
  ErrorBox,
  linkClass,
  MonoId,
  RawJson,
  Section,
  StatusChip,
} from "../components/common.tsx";
import { Button } from "../components/ui/button.tsx";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../components/ui/table.tsx";

export default function DevicesPage() {
  const [cursor, setCursor] = useState<string | undefined>(undefined);
  const baseVersion = useBaseUrlVersion();
  useEffect(() => {
    // Cursors are scoped to one server: restart from the first page there.
    setCursor(undefined);
  }, [baseVersion]);
  const { data, error, loading, refresh } = useApi(`devices-${cursor ?? "first"}`, () =>
    apiFetch<Collection<Device>>(`/v1/devices?limit=50${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`),
  );
  const [selected, setSelected] = useState<string | null>(null);
  const detail = useApi<DeviceDetail | null>(`device-${selected ?? "none"}`, () =>
    selected
      ? apiFetch<DeviceDetail>(`/v1/devices/${selected}?entity_limit=200`)
      : Promise.resolve(null),
  );

  return (
    <div>
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="mr-2 text-lg font-semibold">Devices</h1>
        <Button size="sm" variant="outline" onClick={() => { void refresh(); void detail.refresh(); }}>
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
                <TableHead>Kind</TableHead>
                <TableHead>Device id</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.items.length === 0 && <EmptyRow colSpan={3} message="No devices." />}
              {data.items.map((d) => (
                <TableRow
                  key={d.id}
                  className="cursor-pointer"
                  data-state={d.id === selected ? "selected" : undefined}
                  onClick={() => setSelected(d.id)}
                >
                  <TableCell className="font-medium">
                    <button type="button" className={linkClass}>
                      {d.name}
                    </button>
                  </TableCell>
                  <TableCell>{d.kind}</TableCell>
                  <TableCell className="max-w-[22rem]">
                    <MonoId value={d.id} className="text-muted-foreground" />
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
        </>
      )}
      {selected && (
        <Section title="Device detail">
          {detail.loading && <p className="text-sm text-muted-foreground">Loading detail…</p>}
          {detail.error && <ErrorBox error={detail.error} />}
          {detail.data && (
            <>
              <p className="text-sm">
                <span className="font-medium">{detail.data.name}</span>{" "}
                <span className="font-mono text-xs text-muted-foreground">
                  {selected} · kind={detail.data.kind}
                </span>
              </p>
              <Table className="mt-3">
                <TableHeader>
                  <TableRow className="hover:bg-transparent">
                    <TableHead>Entity</TableHead>
                    <TableHead>Entity id</TableHead>
                    <TableHead>Type</TableHead>
                    <TableHead>Enabled</TableHead>
                    <TableHead>Availability</TableHead>
                    <TableHead>State value</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {detail.data.entities.length === 0 && (
                    <EmptyRow colSpan={6} message="No entities on this device." />
                  )}
                  {detail.data.entities.map((e) => (
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
              <RawJson value={detail.data} title="Raw device detail JSON" />
            </>
          )}
        </Section>
      )}
    </div>
  );
}
