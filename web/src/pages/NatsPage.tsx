import {
  Box,
  Button,
  Chip,
  Paper,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableRow,
  TextField,
  Typography,
} from "@mui/material";
import { useEffect, useRef, useState } from "react";
import { ApiError } from "../api/client.ts";
import type { ProblemDetail } from "../api/types.ts";
import { useApi, usePolling } from "../api/hooks.ts";
import type { NatsMessage, Subscription } from "../api/nats.ts";
import {
  decodeNatsMessage,
  getNatsWsUrl,
  natsConnect,
  natsDisconnect,
  setNatsWsUrl,
} from "../api/nats.ts";
import { ErrorBox, Facts, JsonCode, RawJson, Section, StatusChip } from "../components/common.tsx";

const MAX_MESSAGES = 200;

/** Monitoring endpoints live on the vite proxy origin, never under the
    toolbar's hearthd base URL (hearthd serves no /nats-monitor routes). */
async function monitorFetch<T>(path: string): Promise<T> {
  const res = await fetch(`/nats-monitor${path}`);
  const text = await res.text();
  const body = text ? (JSON.parse(text) as T) : (undefined as T);
  if (!res.ok) {
    throw new ApiError(res.status, (body as ProblemDetail) ?? res.statusText);
  }
  return body;
}

const SUBJECT_PRESETS = [
  { label: "all adapter traffic", subject: "hearth.v1.adapter.>" },
  { label: "claims", subject: "hearth.v1.adapter.*.claim" },
  { label: "heartbeats", subject: "hearth.v1.adapter.*.runtime.*.heartbeat" },
  { label: "registrations", subject: "hearth.v1.adapter.*.runtime.*.register" },
  { label: "observations", subject: "hearth.v1.adapter.*.runtime.*.observation.*" },
  { label: "commands", subject: "hearth.v1.adapter.*.runtime.*.command.*.*" },
  { label: "availability", subject: "hearth.v1.adapter.*.runtime.*.availability" },
];

function LiveMessages() {
  const [wsUrl, setWsUrlState] = useState(getNatsWsUrl());
  const [subject, setSubject] = useState(SUBJECT_PRESETS[0].subject);
  const [status, setStatus] = useState<"disconnected" | "connecting" | "connected" | "error">(
    "disconnected",
  );
  const [statusError, setStatusError] = useState<Error | null>(null);
  const [paused, setPaused] = useState(false);
  const [filter, setFilter] = useState("");
  const [messages, setMessages] = useState<NatsMessage[]>([]);
  const subRef = useRef<Subscription | null>(null);
  const pausedRef = useRef(false);
  const mountedRef = useRef(true);
  const attemptRef = useRef(0);
  // Generation of the latest connection attempt. Completions from an attempt
  // superseded by Unsubscribe (or a newer Subscribe) are discarded instead of
  // resurrecting a canceled subscription.

  useEffect(() => {
    pausedRef.current = paused;
  }, [paused]);

  useEffect(
    () => {
      mountedRef.current = true;
      return () => {
        mountedRef.current = false;
        subRef.current?.unsubscribe();
        subRef.current = null;
        void natsDisconnect();
      };
    },
    [],
  );

  async function subscribe() {
    setStatusError(null);
    if (subRef.current) return;
    const attempt = ++attemptRef.current;
    const target = subject.trim() || SUBJECT_PRESETS[0].subject;
    setStatus("connecting");
    try {
      const nc = await natsConnect();
      if (attempt !== attemptRef.current) {
        // Superseded by Unsubscribe: don't resurrect the subscription. Tear
        // down the just-opened connection unless a newer attempt already
        // claimed it for a live subscription.
        if (subRef.current == null) await natsDisconnect();
        return;
      }
      if (!mountedRef.current) {
        await natsDisconnect();
        return;
      }
      const sub = nc.subscribe(target);
      subRef.current = sub;
      setStatus("connected");
      void (async () => {
        try {
          for await (const m of sub) {
            if (pausedRef.current) continue;
            const entry = decodeNatsMessage(m.subject, m.data);
            setMessages((prev) => [entry, ...prev].slice(0, MAX_MESSAGES));
          }
        } catch (e) {
          setStatusError(e instanceof Error ? e : new Error(String(e)));
          setStatus("error");
        } finally {
          if (subRef.current === sub) {
            subRef.current = null;
            setStatus("disconnected");
          }
        }
      })();
    } catch (e) {
      setStatusError(e instanceof Error ? e : new Error(String(e)));
      setStatus("error");
    }
  }

  function unsubscribe() {
    // Invalidate any in-flight connection attempt so its completion is discarded.
    attemptRef.current++;
    subRef.current?.unsubscribe();
    subRef.current = null;
    setStatus("disconnected");
  }

  const visible = messages.filter((m) => !filter || m.subject.includes(filter));

  return (
    <Box>
      <Box sx={{ display: "flex", gap: 1.5, flexWrap: "wrap", alignItems: "center" }}>
        <TextField
          size="small"
          label="NATS websocket URL"
          value={wsUrl}
          onChange={(e) => {
            setWsUrlState(e.target.value);
            setNatsWsUrl(e.target.value.trim());
          }}
          sx={{ minWidth: 260 }}
        />
        <StatusChip label="nats" status={status === "connected" ? "ok" : status} />
      </Box>
      {statusError && <ErrorBox error={statusError} />}
      <TextField
        size="small"
        label="Subject (supports * and >)"
        value={subject}
        onChange={(e) => setSubject(e.target.value)}
        fullWidth
        sx={{ mt: 2 }}
      />
      <Box sx={{ display: "flex", gap: 1, flexWrap: "wrap", mt: 1 }}>
        {SUBJECT_PRESETS.map((p) => (
          <Chip
            key={p.label}
            label={p.label}
            size="small"
            variant={subject === p.subject ? "filled" : "outlined"}
            onClick={() => setSubject(p.subject)}
          />
        ))}
      </Box>
      <Box sx={{ display: "flex", gap: 1, flexWrap: "wrap", mt: 2, alignItems: "center" }}>
        <Button variant="contained" size="small" disabled={status !== "disconnected"} onClick={() => void subscribe()}>
          Subscribe
        </Button>
        <Button variant="outlined" size="small" disabled={status === "disconnected"} onClick={unsubscribe}>
          Unsubscribe
        </Button>
        <Button variant="outlined" size="small" onClick={() => setPaused((p) => !p)}>
          {paused ? "Resume" : "Pause"}
        </Button>
        <Button variant="outlined" size="small" onClick={() => setMessages([])}>
          Clear
        </Button>
        <TextField
          size="small"
          label="Filter by subject"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          sx={{ minWidth: 200 }}
        />
        <Typography variant="body2" color="text.secondary">
          {messages.length} buffered (max {MAX_MESSAGES}), newest first
        </Typography>
      </Box>
      {visible.length > 0 && (
        <Table size="small" sx={{ mt: 1 }}>
          <TableHead>
            <TableRow>
              <TableCell>Received</TableCell>
              <TableCell>Subject</TableCell>
              <TableCell>Payload</TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {visible.map((m, i) => (
              <TableRow key={`${m.at}-${i}`} hover>
                <TableCell sx={{ fontSize: 11, whiteSpace: "nowrap" }}>{m.at}</TableCell>
                <TableCell sx={{ fontSize: 11, wordBreak: "break-all" }}>{m.subject}</TableCell>
                <TableCell sx={{ fontSize: 11, maxWidth: 560, overflow: "hidden", textOverflow: "ellipsis" }}>
                  {m.json !== null ? (
                    <JsonCode code={JSON.stringify(m.json, null, 1).slice(0, 2000)} />
                  ) : (
                    <pre style={{ margin: 0, whiteSpace: "pre-wrap", wordBreak: "break-all" }}>
                      {m.payload.slice(0, 2000)}
                    </pre>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </Box>
  );
}

interface Varz {
  version: string;
  uptime: string;
  connections: number;
  total_connections: number;
  in_msgs: number;
  out_msgs: number;
  in_bytes: number;
  out_bytes: number;
  mem: number;
}

interface ConnInfo {
  cid: number;
  kind: string;
  ip: string;
  port: number;
  subscriptions?: number;
  subscriptions_list?: string[];
  in_msgs: number;
  out_msgs: number;
}

interface JetStreamStreamDetail {
  name: string;
  state?: Record<string, unknown>;
  consumer_detail?: Record<string, unknown>[];
}

interface JetStreamAccountDetail {
  name?: string;
  account?: string;
  stream_detail?: JetStreamStreamDetail[];
}

function ServerInfo() {
  const varz = usePolling<Varz>("nats-varz", () => monitorFetch<Varz>("/varz"), 10_000);
  const conns = useApi<{ connections?: ConnInfo[] }>("nats-connz", () =>
    monitorFetch<{ connections?: ConnInfo[] }>("/connz?subs=1"),
  );
  const jsz = useApi<{ account_details?: JetStreamAccountDetail[] }>("nats-jsz", () =>
    monitorFetch<{ account_details?: JetStreamAccountDetail[] }>(
      "/jsz?streams=true&consumers=true",
    ),
  );

  const streams = (jsz.data?.account_details ?? []).flatMap((a) =>
    (a.stream_detail ?? []).map((s) => ({ account: a.name ?? a.account ?? "", ...s })),
  );
  const observations = streams.find((s) => s.name === "HEARTH_OBSERVATIONS_V1");

  return (
    <Box>
      <Typography variant="subtitle1">Server</Typography>
      {varz.loading && <Typography>Loading…</Typography>}
      {varz.error && <ErrorBox error={varz.error} />}
      {varz.data && (
        <Facts
          rows={[
            ["Version", varz.data.version],
            ["Uptime", varz.data.uptime],
            ["Connections", `${varz.data.connections} current / ${varz.data.total_connections} total`],
            ["Messages in/out", `${varz.data.in_msgs} / ${varz.data.out_msgs}`],
            ["Bytes in/out", `${varz.data.in_bytes} / ${varz.data.out_bytes}`],
            ["Memory", `${varz.data.mem} bytes`],
          ]}
        />
      )}
      <RawJson value={varz.data} title="Raw /varz JSON" />

      <Typography variant="subtitle1" sx={{ mt: 2 }}>
        Connections
      </Typography>
      <Button size="small" onClick={() => void conns.refresh()}>
        Refresh connections
      </Button>
      {conns.error && <ErrorBox error={conns.error} />}
      {conns.data && (
        <Table size="small" sx={{ mt: 1 }}>
          <TableHead>
            <TableRow>
              <TableCell>CID</TableCell>
              <TableCell>Kind</TableCell>
              <TableCell>Remote</TableCell>
              <TableCell>Subscriptions</TableCell>
              <TableCell>Msgs in/out</TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {(conns.data.connections ?? []).map((c) => (
              <TableRow key={c.cid} hover>
                <TableCell>{c.cid}</TableCell>
                <TableCell>{c.kind}</TableCell>
                <TableCell sx={{ fontSize: 11 }}>
                  {c.ip}:{c.port}
                </TableCell>
                <TableCell sx={{ fontSize: 11, wordBreak: "break-all" }}>
                  {c.subscriptions ?? "—"}
                  {(c.subscriptions_list ?? []).length > 0 && `: ${(c.subscriptions_list ?? []).join(", ")}`}
                </TableCell>
                <TableCell>
                  {c.in_msgs} / {c.out_msgs}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <Typography variant="subtitle1" sx={{ mt: 2 }}>
        JetStream: HEARTH_OBSERVATIONS_V1
      </Typography>
      <Button size="small" onClick={() => void jsz.refresh()}>
        Refresh JetStream
      </Button>
      {jsz.error && <ErrorBox error={jsz.error} />}
      {observations?.state && (
        <Facts
          rows={Object.entries(observations.state).map(([k, v]) => [k, JSON.stringify(v)] as [string, string])}
        />
      )}
      {(observations?.consumer_detail ?? []).map((c) => (
        <Box key={String(c.name ?? "consumer")} sx={{ mt: 1 }}>
          <Typography variant="subtitle2">Consumer {String(c.name ?? "?")}</Typography>
          <Facts
            rows={Object.entries(c).map(([k, v]) => [k, JSON.stringify(v)] as [string, string])}
          />
        </Box>
      ))}
      {!jsz.loading && !jsz.error && !observations && (
        <Typography color="text.secondary">Stream not found (is hearthd running?).</Typography>
      )}
      <RawJson value={jsz.data} title="Raw /jsz JSON" />
    </Box>
  );
}

export default function NatsPage() {
  return (
    <Box>
      <Typography variant="h5">NATS</Typography>
      <Typography variant="body2" color="text.secondary" sx={{ mt: 0.5 }}>
        Live wire traffic and server introspection. Requires the websocket and monitoring listeners in
        configs/nats-server.conf (restart NATS after changing it).
      </Typography>
      <Section title="Live messages (websocket)">
        <LiveMessages />
      </Section>
      <Section title="Server info (monitoring)">
        <Paper variant="outlined" sx={{ p: 2 }}>
          <ServerInfo />
        </Paper>
      </Section>
    </Box>
  );
}
