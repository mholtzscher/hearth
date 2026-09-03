import {
  Box,
  Button,
  Link,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableRow,
  Typography,
} from "@mui/material";
import { useState } from "react";
import { Link as RouterLink } from "react-router-dom";
import { apiFetch } from "../api/client.ts";
import { useApi } from "../api/hooks.ts";
import type { Collection, Device, DeviceDetail } from "../api/types.ts";
import { ErrorBox, RawJson, StatusChip } from "../components/common.tsx";

export default function DevicesPage() {
  const [cursor, setCursor] = useState<string | undefined>(undefined);
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
    <Box>
      <Typography variant="h5">Devices</Typography>
      <Button variant="outlined" sx={{ mt: 1 }} onClick={() => void refresh()}>
        Refresh
      </Button>
      {loading && <Typography>Loading…</Typography>}
      {error && <ErrorBox error={error} />}
      {data && (
        <>
          <Table size="small" sx={{ mt: 1 }}>
            <TableHead>
              <TableRow>
                <TableCell>ID</TableCell>
                <TableCell>Kind</TableCell>
                <TableCell>Name</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {data.items.map((d) => (
                <TableRow key={d.id} hover selected={d.id === selected}>
                  <TableCell>
                    <Link component="button" onClick={() => setSelected(d.id)}>
                      {d.id}
                    </Link>
                  </TableCell>
                  <TableCell>{d.kind}</TableCell>
                  <TableCell>{d.name}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {data.next_cursor && <Button onClick={() => setCursor(data.next_cursor)}>Next page</Button>}
        </>
      )}
      {selected && (
        <Box sx={{ mt: 2 }}>
          <Typography variant="h6">Device {selected}</Typography>
          {detail.loading && <Typography>Loading detail…</Typography>}
          {detail.error && <ErrorBox error={detail.error} />}
          {detail.data && (
            <>
              <Typography variant="body2" color="text.secondary">
                kind={detail.data.kind} name={detail.data.name}
              </Typography>
              <Table size="small" sx={{ mt: 1 }}>
                <TableHead>
                  <TableRow>
                    <TableCell>Entity</TableCell>
                    <TableCell>Type</TableCell>
                    <TableCell>Enabled</TableCell>
                    <TableCell>Availability</TableCell>
                    <TableCell>State value</TableCell>
                  </TableRow>
                </TableHead>
                <TableBody>
                  {detail.data.entities.map((e) => (
                    <TableRow key={e.id} hover>
                      <TableCell>
                        <Link component={RouterLink} to={`/entities/${e.id}`}>
                          {e.name} ({e.id})
                        </Link>
                      </TableCell>
                      <TableCell>{e.type}</TableCell>
                      <TableCell>{e.enabled ? "yes" : "no"}</TableCell>
                      <TableCell>
                        <StatusChip status={e.availability.status} />
                      </TableCell>
                      <TableCell>{e.state ? JSON.stringify(e.state.value) : "—"}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
              <RawJson value={detail.data} title="Raw device detail JSON" />
            </>
          )}
        </Box>
      )}
    </Box>
  );
}
