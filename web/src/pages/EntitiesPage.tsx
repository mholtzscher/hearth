import {
  Box,
  Button,
  Link,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableRow,
  TextField,
  Typography,
} from "@mui/material";
import { useEffect, useState } from "react";
import { Link as RouterLink } from "react-router-dom";
import { apiFetch, useBaseUrlVersion } from "../api/client.ts";
import { useApi } from "../api/hooks.ts";
import type { Collection, Entity } from "../api/types.ts";
import { ErrorBox, RawJson, StatusChip } from "../components/common.tsx";

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
    <Box>
      <Typography variant="h5">Entities</Typography>
      <Box sx={{ display: "flex", gap: 1, mt: 1, alignItems: "center" }}>
        <TextField
          size="small"
          label="device_id filter"
          value={deviceId}
          onChange={(e) => setDeviceId(e.target.value)}
          sx={{ minWidth: 320 }}
        />
        <Button
          variant="contained"
          onClick={() => {
            setAppliedDeviceId(deviceId.trim());
            setCursor(undefined);
          }}
        >
          Apply
        </Button>
        <Button variant="outlined" onClick={() => void refresh()}>
          Refresh
        </Button>
      </Box>
      {loading && <Typography>Loading…</Typography>}
      {error && <ErrorBox error={error} />}
      {data && (
        <>
          <Table size="small" sx={{ mt: 1 }}>
            <TableHead>
              <TableRow>
                <TableCell>ID / Name</TableCell>
                <TableCell>Type</TableCell>
                <TableCell>Enabled</TableCell>
                <TableCell>Availability</TableCell>
                <TableCell>State</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {data.items.map((e) => (
                <TableRow key={e.id} hover>
                  <TableCell>
                    <Link component={RouterLink} to={`/entities/${e.id}`}>
                      {e.name}
                    </Link>
                    <Typography variant="caption" display="block" color="text.secondary">
                      {e.id} · {e.adapter_id}
                    </Typography>
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
          {data.next_cursor && <Button onClick={() => setCursor(data.next_cursor)}>Next page</Button>}
          <RawJson value={data} title="Raw list JSON" />
        </>
      )}
    </Box>
  );
}
