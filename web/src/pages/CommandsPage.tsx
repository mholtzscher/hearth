import { Box, Button, TextField, Typography } from "@mui/material";
import { useState } from "react";
import { apiFetch } from "../api/client.ts";
import { useApi } from "../api/hooks.ts";
import type { CommandRecord } from "../api/types.ts";
import { ErrorBox, RawJson, StatusChip } from "../components/common.tsx";

export default function CommandsPage() {
  const [commandId, setCommandId] = useState("");
  const [applied, setApplied] = useState("");
  const { data, error, loading } = useApi<CommandRecord | null>(
    `command-${applied || "none"}`,
    () => (applied ? apiFetch<CommandRecord>(`/v1/commands/${applied}`) : Promise.resolve(null)),
  );

  return (
    <Box>
      <Typography variant="h5">Commands</Typography>
      <Typography variant="body2" color="text.secondary">
        Durable command audit read (GET /v1/commands/{"{command_id}"}). Per-entity history lives on each
        entity page.
      </Typography>
      <Box sx={{ display: "flex", gap: 1, mt: 1 }}>
        <TextField
          size="small"
          label="command_id (cmd_…)"
          value={commandId}
          onChange={(e) => setCommandId(e.target.value)}
          sx={{ minWidth: 360 }}
        />
        <Button variant="contained" onClick={() => setApplied(commandId.trim())}>
          Look up
        </Button>
      </Box>
      {loading && <Typography>Loading…</Typography>}
      {error && <ErrorBox error={error} />}
      {data && (
        <>
          <Typography sx={{ mt: 2 }}>
            {data.id} <StatusChip status={data.status} /> operation={data.operation}
          </Typography>
          <RawJson value={data} title="Raw command JSON" />
        </>
      )}
    </Box>
  );
}
