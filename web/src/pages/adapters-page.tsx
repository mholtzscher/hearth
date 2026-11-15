import { useState } from "react";
import type { ReactNode } from "react";

import { apiFetch, useBaseUrlVersion } from "../api/client.ts";
import { useApi } from "../api/hooks.ts";
import type { Adapter, Collection, HealthTransition } from "../api/types.ts";
import { Button } from "../components/button.tsx";
import { Card } from "../components/card.tsx";
import { EmptyRow } from "../components/empty-row.tsx";
import { ErrorBox } from "../components/error-box.tsx";
import { Facts } from "../components/facts.tsx";
import { Input } from "../components/input.tsx";
import { linkClass } from "../components/link-styles.ts";
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

const AdapterHealthHistory = ({ adapterId }: { adapterId: string }) => {
  const baseVersion = useBaseUrlVersion();
  const [cursor, setCursor] = useState<string | undefined>(undefined);
  const [cursorServer, setCursorServer] = useState(baseVersion);
  // Cursors are scoped to one server: restart from the first page there.
  if (cursorServer !== baseVersion) {
    setCursorServer(baseVersion);
    setCursor(undefined);
  }
  const { data, error, loading } = useApi(
    `adapter-health-${adapterId}-${cursor ?? "first"}`,
    () =>
      apiFetch<Collection<HealthTransition>>(
        `/v1/adapters/${adapterId}/health/history?limit=50${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`
      )
  );
  if (loading) {
    return <p className="text-muted-foreground text-sm">Loading history…</p>;
  }
  if (error) {
    return <ErrorBox error={error} />;
  }
  return (
    <>
      <Table>
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead>Status</TableHead>
            <TableHead>Source</TableHead>
            <TableHead>Reason</TableHead>
            <TableHead>Observed at</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {(data?.items ?? []).length === 0 && (
            <EmptyRow colSpan={4} message="No health transitions recorded." />
          )}
          {data?.items.map((t, i) => (
            <TableRow key={i}>
              <TableCell>
                <StatusChip status={t.status} />
              </TableCell>
              <TableCell>{t.source}</TableCell>
              <TableCell className="font-mono text-xs">
                {t.reason?.code ?? "—"}
              </TableCell>
              <TableCell className="text-muted-foreground font-mono text-xs">
                {t.observed_at}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {data?.next_cursor && (
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
  );
};

const AdapterDetail = ({ adapterId }: { adapterId: string }) => {
  const { data, error, loading, refresh } = useApi(`adapter-${adapterId}`, () =>
    apiFetch<Adapter>(`/v1/adapters/${adapterId}`)
  );
  if (loading) {
    return <p className="text-muted-foreground text-sm">Loading…</p>;
  }
  if (error) {
    return (
      <>
        <ErrorBox error={error} />
        <Button
          size="default"
          variant="outline"
          onClick={async () => {
            await refresh();
          }}
        >
          Retry
        </Button>
      </>
    );
  }
  if (!data) {
    return null;
  }
  return (
    <Card className="mt-3" size="compact">
      <div className="flex items-center gap-2">
        <span className="font-medium">{data.id}</span>
        <StatusChip label="health" status={data.health.status} />
      </div>
      <Facts
        rows={[
          ["Source", data.health.source],
          ["Since", data.health.since],
          ["Evidence at", data.health.evidence_at],
          ...(data.health.reason
            ? [["Reason", data.health.reason.code] as [string, ReactNode]]
            : []),
          ...(data.health.runtime
            ? [
                ["Runtime", data.health.runtime.id] as [string, ReactNode],
                [
                  "Software",
                  `${data.health.runtime.software_name} ${data.health.runtime.software_version}`,
                ] as [string, ReactNode],
                ["Lease expires", data.health.runtime.lease_expires_at] as [
                  string,
                  ReactNode,
                ],
              ]
            : []),
        ]}
      />
      <Section title="Health history">
        <AdapterHealthHistory key={adapterId} adapterId={adapterId} />
      </Section>
      <RawJson value={data} />
    </Card>
  );
};

const AdaptersPage = () => {
  const baseVersion = useBaseUrlVersion();
  const [cursor, setCursor] = useState<string | undefined>(undefined);
  const [cursorServer, setCursorServer] = useState(baseVersion);
  const [selected, setSelected] = useState("");
  const [filter, setFilter] = useState("");
  const [detailNonce, setDetailNonce] = useState(0);
  // Cursors are scoped to one server: restart from the first page there.
  if (cursorServer !== baseVersion) {
    setCursorServer(baseVersion);
    setCursor(undefined);
  }
  const { data, error, loading, refresh } = useApi(
    `adapters-${cursor ?? "first"}`,
    () =>
      apiFetch<Collection<Adapter>>(
        `/v1/adapters?limit=50${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`
      )
  );
  const items = (data?.items ?? []).filter(
    (a) => !filter || a.id.includes(filter)
  );

  return (
    <div>
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="mr-2 text-lg font-semibold">Adapters</h1>
        <Input
          aria-label="Filter by adapter id"
          placeholder="Filter by id…"
          className="w-56"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        <Button
          size="default"
          variant="outline"
          onClick={() => {
            refresh().catch(() => null);
            setDetailNonce((n) => n + 1);
          }}
        >
          Refresh
        </Button>
        <Button
          size="default"
          variant="ghost"
          onClick={() => {
            setSelected("");
            setCursor(undefined);
          }}
        >
          Reset
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
                <TableHead>ID</TableHead>
                <TableHead>Health</TableHead>
                <TableHead>Source</TableHead>
                <TableHead>Reason</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.length === 0 && (
                <EmptyRow colSpan={4} message="No adapters match." />
              )}
              {items.map((a) => (
                <TableRow
                  key={a.id}
                  className="cursor-pointer"
                  data-state={a.id === selected ? "selected" : undefined}
                  onClick={() => setSelected(a.id)}
                >
                  <TableCell className="font-medium">
                    <button type="button" className={linkClass}>
                      {a.id}
                    </button>
                  </TableCell>
                  <TableCell>
                    <StatusChip status={a.health.status} />
                  </TableCell>
                  <TableCell>{a.health.source}</TableCell>
                  <TableCell className="font-mono text-xs">
                    {a.health.reason?.code ?? "—"}
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
          <RawJson value={data} title="Raw list JSON" />
        </>
      )}
      {selected && (
        <AdapterDetail
          key={`${selected}-${detailNonce}`}
          adapterId={selected}
        />
      )}
    </div>
  );
};

export default AdaptersPage;
