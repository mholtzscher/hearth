import {
  Box,
  Button,
  Card,
  CardContent,
  FormControlLabel,
  Stack,
  Switch,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableRow,
  TextField,
  Typography,
} from "@mui/material";
import { useEffect, useRef, useState } from "react";
import { useParams } from "react-router-dom";
import { ApiError, apiFetch } from "../api/client.ts";
import { useApi } from "../api/hooks.ts";
import type { Collection, CommandRecord, CommandResult, Entity, HealthTransition } from "../api/types.ts";
import { ErrorBox, Facts, RawJson, Section, StatusChip } from "../components/common.tsx";

function presetsFor(type: string | undefined, support?: Record<string, unknown>): { label: string; params: string }[] {
  switch (type) {
    case "hearth.power/v1":
      return [
        { label: "power on", params: '{"value":true}' },
        { label: "power off", params: '{"value":false}' },
      ];
    case "hearth.brightness/v1":
      return brightnessPresets(support);
    default:
      return [];
  }
}

function exampleParams(type: string | undefined, support?: Record<string, unknown>): string {
  if (type === "hearth.brightness/v1") {
    const { maximum, step } = brightnessBounds(support);
    return `{"value":${step <= maximum ? step : 0}}`;
  }
  return '{"value":true}';
}

/** Per-entity brightness bounds; defaults preserve the old fixed presets. */
function brightnessBounds(support?: Record<string, unknown>): { maximum: number; step: number } {
  const state = support?.state as { maximum?: unknown } | undefined;
  const set = (support?.operations as { set?: { step?: unknown } } | undefined)?.set;
  return {
    maximum: typeof state?.maximum === "number" ? state.maximum : 100,
    step: typeof set?.step === "number" && set.step > 0 ? set.step : 1,
  };
}

/** Brightness presets that satisfy the entity's own support (maximum and step). */
function brightnessPresets(support?: Record<string, unknown>): { label: string; params: string }[] {
  const { maximum, step } = brightnessBounds(support);
  return [0, 50, 100]
    .filter((value) => value <= maximum && value % step === 0)
    .map((value) => ({ label: `brightness ${value}`, params: `{"value":${value}}` }));
}

function CommandHistory({ entityId }: { entityId: string }) {
  const [cursor, setCursor] = useState<string | undefined>(undefined);
  const { data, error, loading, refresh } = useApi(
    `cmds-${entityId}-${cursor ?? "first"}`,
    () =>
      apiFetch<Collection<CommandRecord>>(
        `/v1/entities/${entityId}/commands?limit=50${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`,
      ),
  );
  return (
    <Box>
      <Button size="small" onClick={() => void refresh()}>
        Refresh history
      </Button>
      {loading && <Typography>Loading…</Typography>}
      {error && <ErrorBox error={error} />}
      {data && (
        <>
          <Table size="small">
            <TableHead>
              <TableRow>
                <TableCell>ID</TableCell>
                <TableCell>Operation</TableCell>
                <TableCell>Params</TableCell>
                <TableCell>Status</TableCell>
                <TableCell>Failure</TableCell>
                <TableCell>Requested</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {data.items.map((c) => (
                <TableRow key={c.id} hover>
                  <TableCell sx={{ fontSize: 11, maxWidth: 220, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }} title={c.id}>{c.id}</TableCell>
                  <TableCell>{c.operation}</TableCell>
                  <TableCell sx={{ fontSize: 11 }}>{JSON.stringify(c.parameters)}</TableCell>
                  <TableCell>
                    <StatusChip status={c.status} />
                  </TableCell>
                  <TableCell>{c.failure_code ?? "—"}</TableCell>
                  <TableCell sx={{ fontSize: 11 }}>{c.requested_at}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {data.next_cursor && <Button onClick={() => setCursor(data.next_cursor)}>Next page</Button>}
        </>
      )}
    </Box>
  );
}

function AvailabilityHistory({ entityId }: { entityId: string }) {
  const [cursor, setCursor] = useState<string | undefined>(undefined);
  const { data, error, loading } = useApi(
    `avail-${entityId}-${cursor ?? "first"}`,
    () =>
      apiFetch<Collection<HealthTransition>>(
        `/v1/entities/${entityId}/availability/history?limit=50${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`,
      ),
  );
  if (loading) return <Typography>Loading…</Typography>;
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

export default function EntityDetailPage() {
  const { entityId = "" } = useParams();
  const entityIdRef = useRef(entityId);
  entityIdRef.current = entityId;
  const { data, error, loading, refresh } = useApi(`entity-${entityId}`, () =>
    apiFetch<Entity>(`/v1/entities/${entityId}`),
  );
  const [operation, setOperation] = useState("set");
  const [paramsText, setParamsText] = useState('{"value":true}');
  const [paramsTouched, setParamsTouched] = useState(false);
  useEffect(() => {
    if (data && !paramsTouched) setParamsText(exampleParams(data.type, data.support));
  }, [data, paramsTouched]);
  useEffect(() => {
    // The route reuses this page across entities: reset form and outcome state.
    setOperation("set");
    setParamsText('{"value":true}');
    setParamsTouched(false);
    setParamsError(null);
    setResult(null);
    setSendError(null);
  }, [entityId]);
  const [paramsError, setParamsError] = useState<string | null>(null);
  const [sending, setSending] = useState(false);
  const [result, setResult] = useState<CommandResult | null>(null);
  const [sendError, setSendError] = useState<Error | null>(null);
  const [toggling, setToggling] = useState(false);

  async function sendCommand() {
    const target = entityId;
    setParamsError(null);
    setSendError(null);
    setResult(null);
    let params: unknown;
    try {
      params = paramsText.trim() ? JSON.parse(paramsText) : {};
    } catch {
      setParamsError("Parameters must be valid JSON.");
      return;
    }
    setSending(true);
    try {
      const res = await apiFetch<CommandResult>(`/v1/entities/${target}/commands`, {
        method: "POST",
        body: JSON.stringify({ operation, parameters: params }),
      });
      if (entityIdRef.current !== target) return;
      setResult(res);
      void refresh();
    } catch (e) {
      if (entityIdRef.current !== target) return;
      setSendError(e instanceof Error ? e : new Error(String(e)));
    } finally {
      if (entityIdRef.current === target) setSending(false);
    }
  }

  async function setEnabled(enabled: boolean) {
    const target = entityId;
    setToggling(true);
    try {
      await apiFetch(`/v1/entities/${target}`, {
        method: "PATCH",
        body: JSON.stringify({ enabled }),
      });
      if (entityIdRef.current !== target) return;
      void refresh();
    } catch (e) {
      if (entityIdRef.current !== target) return;
      setSendError(e instanceof Error ? e : new Error(String(e)));
    } finally {
      if (entityIdRef.current === target) setToggling(false);
    }
  }

  return (
    <Box>
      <Typography variant="h5" sx={{ wordBreak: "break-all" }}>
        Entity {entityId}
      </Typography>
      <Button variant="outlined" sx={{ mt: 1 }} onClick={() => void refresh()}>
        Refresh
      </Button>
      {loading && <Typography>Loading…</Typography>}
      {error && <ErrorBox error={error} />}
      {data && (
        <>
          <Card variant="outlined" sx={{ mt: 2 }}>
            <CardContent>
              <Typography variant="subtitle1">
                {data.name} <StatusChip label="availability" status={data.availability.status} />{" "}
                <StatusChip label={data.enabled ? "enabled" : "disabled"} status={data.enabled ? "ok" : "disabled"} />
              </Typography>
              <Facts
                rows={[
                  ["Type", data.type],
                  ["Device", data.device_id],
                  ["Adapter", data.adapter_id],
                  [
                    "Availability",
                    `${data.availability.source} since ${data.availability.since}${data.availability.reason ? ` (${data.availability.reason.code})` : ""}`,
                  ],
                  [
                    "State",
                    data.state
                      ? `${JSON.stringify(data.state.value)} obs=${data.state.observation_id} at ${data.state.observed_at}`
                      : "null (never observed)",
                  ],
                ]}
              />
              <FormControlLabel
                control={
                  <Switch
                    checked={data.enabled}
                    disabled={toggling}
                    onChange={(e) => void setEnabled(e.target.checked)}
                  />
                }
                label="Enabled (PATCH /v1/entities/{id})"
              />
            </CardContent>
          </Card>

          <Section title="Send command (POST /v1/entities/{id}/commands)">
            <Stack spacing={2}>
            {presetsFor(data?.type, data?.support).length > 0 && (
            <Box sx={{ display: "flex", gap: 1, flexWrap: "wrap" }}>
              {presetsFor(data?.type, data?.support).map((p) => (
                <Button key={p.label} size="small" variant="outlined" onClick={() => { setParamsText(p.params); setParamsTouched(true); }}>
                  {p.label}
                </Button>
              ))}
            </Box>
            )}
            <Box sx={{ display: "flex", gap: 2, flexWrap: "wrap" }}>
              <TextField
                size="small"
                label="operation"
                value={operation}
                onChange={(e) => setOperation(e.target.value)}
                sx={{ width: 160 }}
              />
              <TextField
                size="small"
                label="parameters (JSON)"
                value={paramsText}
                onChange={(e) => { setParamsText(e.target.value); setParamsTouched(true); }}
                error={!!paramsError}
                helperText={paramsError ?? `e.g. ${exampleParams(data?.type, data?.support)}${data ? ` for ${data.type} set` : ""}`}
                multiline
                minRows={2}
                sx={{ flexGrow: 1, minWidth: 280 }}
              />
            </Box>
            <Box>
            <Button variant="contained" disabled={sending} onClick={() => void sendCommand()}>
              {sending ? "Sending…" : "Send command"}
            </Button>
            </Box>
            {sendError && (
              <ErrorBox error={sendError instanceof ApiError ? sendError : sendError} />
            )}
            {result && (
              <>
                <Typography sx={{ mt: 1 }}>
                  <StatusChip status={result.status} /> observation={result.observation_id} value=
                  {JSON.stringify(result.value)}
                </Typography>
                <RawJson value={result} title="Command result JSON" />
              </>
            )}
            </Stack>
          </Section>

          <Section title="Support">
            <RawJson value={data.support} title="support JSON" />
          </Section>

          <Section title="Command history">
            <CommandHistory key={entityId} entityId={entityId} />
          </Section>

          <Section title="Availability history">
            <AvailabilityHistory key={entityId} entityId={entityId} />
          </Section>

          <RawJson value={data} title="Raw entity JSON" />
        </>
      )}
    </Box>
  );
}
