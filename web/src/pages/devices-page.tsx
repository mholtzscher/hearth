import { useState } from "react";
import { Link as RouterLink } from "react-router-dom";

import { apiFetch, useBaseUrlVersion } from "../api/client.ts";
import { formatEntityState } from "../api/entity-state";
import { useApi } from "../api/hooks.ts";
import type { Collection, Device, DeviceDetail } from "../api/types.ts";
import { Button } from "../components/button.tsx";
import { EmptyRow } from "../components/empty-row.tsx";
import { ErrorBox } from "../components/error-box.tsx";
import { linkClass } from "../components/link-styles.ts";
import { MonoId } from "../components/mono-id.tsx";
import { RawJson } from "../components/raw-json.tsx";
import { Section } from "../components/section.tsx";
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
import NameEditor from "../features/naming/name-editor.tsx";

export default function DevicesPage() {
  const baseVersion = useBaseUrlVersion();
  const [cursor, setCursor] = useState<string | undefined>(undefined);
  const [cursorServer, setCursorServer] = useState(baseVersion);
  // Cursors are scoped to one server: restart from the first page there.
  if (cursorServer !== baseVersion) {
    setCursorServer(baseVersion);
    setCursor(undefined);
  }
  const { data, error, loading, refresh } = useApi(
    `devices-${cursor ?? "first"}`,
    () =>
      apiFetch<Collection<Device>>(
        `/v1/devices?limit=50${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`
      )
  );
  const [selected, setSelected] = useState<string | null>(null);
  const detail = useApi<DeviceDetail | null>(
    `device-${selected ?? "none"}`,
    () =>
      selected
        ? apiFetch<DeviceDetail>(`/v1/devices/${selected}?entity_limit=200`)
        : Promise.resolve(null)
  );

  return (
    <div>
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="mr-2 text-lg font-semibold">Devices</h1>
        <Button
          size="default"
          variant="outline"
          onClick={() => {
            void refresh();
            void detail.refresh();
          }}
        >
          Refresh
        </Button>
        {loading && (
          <span className="text-muted-foreground text-sm">Loading…</span>
        )}
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
              {data.items.length === 0 && (
                <EmptyRow colSpan={3} message="No devices." />
              )}
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
      {selected && (
        <Section title="Device detail">
          {detail.loading && (
            <p className="text-muted-foreground text-sm">Loading detail…</p>
          )}
          {detail.error && <ErrorBox error={detail.error} />}
          {detail.data && (
            <>
              <p className="text-sm">
                <span className="font-medium">{detail.data.name}</span>{" "}
                <span className="text-muted-foreground font-mono text-xs">
                  {selected} · kind={detail.data.kind}
                </span>
              </p>
              <NameEditor
                objectId={selected}
                kind="device"
                metadata={detail.data}
                onUpdated={(metadata) => {
                  detail.setData((current) =>
                    current ? { ...current, ...metadata } : current
                  );
                  void refresh();
                  void detail.refresh();
                }}
              />
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
                    <EmptyRow
                      colSpan={6}
                      message="No entities on this device."
                    />
                  )}
                  {detail.data.entities.map((e) => (
                    <TableRow key={e.id}>
                      <TableCell className="font-medium">
                        <RouterLink
                          to={`/entities/${e.id}`}
                          className={linkClass}
                        >
                          {e.name}
                        </RouterLink>
                      </TableCell>
                      <TableCell className="max-w-[22rem]">
                        <MonoId
                          value={e.id}
                          className="text-muted-foreground"
                        />
                      </TableCell>
                      <TableCell className="font-mono text-xs">
                        {e.type}
                      </TableCell>
                      <TableCell>{e.enabled ? "yes" : "no"}</TableCell>
                      <TableCell>
                        <StatusChip status={e.availability.status} />
                      </TableCell>
                      <TableCell className="font-mono text-xs">
                        {e.state
                          ? (forecastSummary(e.type, e.state.value) ??
                            formatEntityState(e))
                          : "—"}
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
