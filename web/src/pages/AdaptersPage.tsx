import {
  Box,
  Button,
  Link,
  Paper,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableRow,
  TextField,
  Typography,
} from "@mui/material";
import { useState } from "react";
import type { ReactNode } from "react";
import { Link as RouterLink } from "react-router-dom";
import { apiFetch } from "../api/client.ts";
import { useApi } from "../api/hooks.ts";
import type { Adapter, Collection } from "../api/types.ts";
import { ErrorBox, Facts, RawJson, Section, StatusChip } from "../components/common.tsx";

function AdapterHealthHistory({ adapterId }: { adapterId: string }) {
  const [cursor, setCursor] = useState<string | undefined>(undefined);
  const { data, error, loading } = useApi(
    `adapter-health-${adapterId}-${cursor ?? "first"}`,
    () =>
      apiFetch<Collection<import("../api/types.ts").HealthTransition>>(
        `/v1/adapters/${adapterId}/health/history?limit=50${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`,
      ),
  );
  if (loading) return <Typography>Loading history…</Typography>;
  if (error) return <ErrorBox error={error} />;
  return (
    <>
      <Table size="small">
        <TableHead>
          <TableRow>
            <TableCell>Status</TableCell>
            <TableCell>Source</TableCell>
            <TableCell>Reason</TableCell>
            <TableCell>Observed at</TableCell>
          </TableRow>
        </TableHead>
        <TableBody>
          {data?.items.map((t, i) => (
            <TableRow key={i}>
              <TableCell>
                <StatusChip status={t.status} />
              </TableCell>
              <TableCell>{t.source}</TableCell>
              <TableCell>{t.reason?.code ?? "—"}</TableCell>
              <TableCell>{t.observed_at}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {data?.next_cursor && <Button onClick={() => setCursor(data.next_cursor)}>Next page</Button>}
    </>
  );
}

function AdapterDetail({ adapterId }: { adapterId: string }) {
  const { data, error, loading, refresh } = useApi(`adapter-${adapterId}`, () =>
    apiFetch<Adapter>(`/v1/adapters/${adapterId}`),
  );
  if (loading) return <Typography>Loading…</Typography>;
  if (error)
    return (
      <>
        <ErrorBox error={error} />
        <Button onClick={() => void refresh()}>Retry</Button>
      </>
    );
  if (!data) return null;
  return (
    <Paper variant="outlined" sx={{ p: 2, mt: 2 }}>
      <Typography variant="subtitle1">
        {data.id} <StatusChip label="health" status={data.health.status} />
      </Typography>
      <Facts
        rows={[
          ["Source", data.health.source],
          ["Since", data.health.since],
          ["Evidence at", data.health.evidence_at],
          ...(data.health.reason ? [["Reason", data.health.reason.code] as [string, ReactNode]] : []),
          ...(data.health.runtime
            ? [
                ["Runtime", data.health.runtime.id] as [string, ReactNode],
                [
                  "Software",
                  `${data.health.runtime.software_name} ${data.health.runtime.software_version}`,
                ] as [string, ReactNode],
                ["Lease expires", data.health.runtime.lease_expires_at] as [string, ReactNode],
              ]
            : []),
        ]}
      />
      <Section title="Health history">
        <AdapterHealthHistory adapterId={adapterId} />
      </Section>
      <RawJson value={data} />
    </Paper>
  );
}

export default function AdaptersPage() {
  const [cursor, setCursor] = useState<string | undefined>(undefined);
  const [selected, setSelected] = useState("");
  const [filter, setFilter] = useState("");
  const { data, error, loading, refresh } = useApi(`adapters-${cursor ?? "first"}`, () =>
    apiFetch<Collection<Adapter>>(`/v1/adapters?limit=50${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`),
  );
  const items = (data?.items ?? []).filter((a) => !filter || a.id.includes(filter));

  return (
    <Box>
      <Typography variant="h5">Adapters</Typography>
      <Box sx={{ display: "flex", gap: 1, mt: 1 }}>
        <TextField size="small" label="Filter by id" value={filter} onChange={(e) => setFilter(e.target.value)} />
        <Button variant="outlined" onClick={() => void refresh()}>
          Refresh
        </Button>
        <Button
          component={RouterLink}
          to="/adapters"
          onClick={() => {
            setSelected("");
            setCursor(undefined);
          }}
        >
          Reset
        </Button>
      </Box>
      {loading && <Typography>Loading…</Typography>}
      {error && <ErrorBox error={error} />}
      {data && (
        <>
          <Table size="small" sx={{ mt: 1 }}>
            <TableHead>
              <TableRow>
                <TableCell>ID</TableCell>
                <TableCell>Health</TableCell>
                <TableCell>Source</TableCell>
                <TableCell>Reason</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {items.map((a) => (
                <TableRow key={a.id} selected={a.id === selected} hover>
                  <TableCell>
                    <Link component="button" onClick={() => setSelected(a.id)}>
                      {a.id}
                    </Link>
                  </TableCell>
                  <TableCell>
                    <StatusChip status={a.health.status} />
                  </TableCell>
                  <TableCell>{a.health.source}</TableCell>
                  <TableCell>{a.health.reason?.code ?? "—"}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {data.next_cursor && <Button onClick={() => setCursor(data.next_cursor)}>Next page</Button>}
          <RawJson value={data} title="Raw list JSON" />
        </>
      )}
      {selected && <AdapterDetail adapterId={selected} />}
    </Box>
  );
}
