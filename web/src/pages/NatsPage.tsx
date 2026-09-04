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
import {
  EmptyRow,
  ErrorBox,
  Facts,
  JsonCode,
  RawJson,
  Section,
  StatusChip,
} from "../components/common.tsx";
import { Button } from "../components/ui/button.tsx";
import { Card, CardContent } from "../components/ui/card.tsx";
import { Input } from "../components/ui/input.tsx";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../components/ui/table.tsx";

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
    <div>
      <div className="flex flex-wrap items-center gap-2">
        <Input
          aria-label="NATS websocket URL"
          placeholder="ws://127.0.0.1:4223"
          className="w-56 font-mono text-xs"
          value={wsUrl}
          onChange={(e) => {
            setWsUrlState(e.target.value);
            setNatsWsUrl(e.target.value.trim());
          }}
        />
        <Input
          aria-label="Subject (supports * and >)"
          placeholder="hearth.v1.adapter.>"
          className="w-96 flex-1 font-mono text-xs"
          value={subject}
          onChange={(e) => setSubject(e.target.value)}
        />
        <StatusChip label="nats" status={status === "connected" ? "ok" : status} />
      </div>
      <div className="mt-2 flex flex-wrap gap-1.5">
        {SUBJECT_PRESETS.map((p) => (
          <Button
            key={p.label}
            size="xs"
            variant={subject === p.subject ? "secondary" : "ghost"}
            onClick={() => setSubject(p.subject)}
          >
            {p.label}
          </Button>
        ))}
      </div>
      <div className="mt-3 flex flex-wrap items-center gap-2">
        <Button size="sm" disabled={status !== "disconnected"} onClick={() => void subscribe()}>
          Subscribe
        </Button>
        <Button size="sm" variant="outline" disabled={status === "disconnected"} onClick={unsubscribe}>
          Unsubscribe
        </Button>
        <Button size="sm" variant="outline" onClick={() => setPaused((p) => !p)}>
          {paused ? "Resume" : "Pause"}
        </Button>
        <Button size="sm" variant="ghost" onClick={() => setMessages([])}>
          Clear
        </Button>
        <Input
          aria-label="Filter by subject"
          placeholder="Filter buffered subjects…"
          className="w-56"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        <span className="text-sm text-muted-foreground">
          {messages.length} buffered (max {MAX_MESSAGES}), newest first
          {paused && " · paused"}
        </span>
      </div>
      {statusError && <ErrorBox error={statusError} />}
      <Table className="mt-3">
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead className="w-24">Received</TableHead>
            <TableHead className="w-[28rem]">Subject</TableHead>
            <TableHead>Payload</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {visible.length === 0 && (
            <EmptyRow
              colSpan={3}
              message={
                status === "connected"
                  ? "Subscribed — waiting for messages."
                  : "Not subscribed. Pick a subject and press Subscribe."
              }
            />
          )}
          {visible.map((m, i) => (
            <TableRow key={`${m.at}-${i}`} className="align-top">
              <TableCell className="font-mono text-xs whitespace-nowrap text-muted-foreground">
                {m.at}
              </TableCell>
              <TableCell className="font-mono text-xs break-all whitespace-normal">
                {m.subject}
              </TableCell>
              <TableCell className="whitespace-normal">
                {m.json !== null ? (
                  <JsonCode code={JSON.stringify(m.json, null, 1).slice(0, 2000)} />
                ) : (
                  <pre className="m-0 text-xs break-all whitespace-pre-wrap">
                    {m.payload.slice(0, 2000)}
                  </pre>
                )}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
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
    <div className="grid items-start gap-3 xl:grid-cols-2">
      <Card size="sm">
        <CardContent>
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium">Server</span>
            {varz.loading && <span className="text-xs text-muted-foreground">Loading…</span>}
          </div>
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
        </CardContent>
      </Card>

      <Card size="sm">
        <CardContent>
          <div className="flex items-center justify-between gap-2">
            <span className="text-sm font-medium">JetStream: HEARTH_OBSERVATIONS_V1</span>
            <Button size="xs" variant="outline" onClick={() => void jsz.refresh()}>
              Refresh
            </Button>
          </div>
          {jsz.error && <ErrorBox error={jsz.error} />}
          {observations?.state && (
            <Facts
              rows={Object.entries(observations.state).map(([k, v]) => [k, JSON.stringify(v)] as [string, string])}
            />
          )}
          {(observations?.consumer_detail ?? []).map((c) => (
            <div key={String(c.name ?? "consumer")} className="mt-3">
              <span className="text-sm font-medium">Consumer {String(c.name ?? "?")}</span>
              <Facts
                rows={Object.entries(c).map(([k, v]) => [k, JSON.stringify(v)] as [string, string])}
              />
            </div>
          ))}
          {!jsz.loading && !jsz.error && !observations && (
            <p className="mt-2 text-sm text-muted-foreground">
              Stream not found (is hearthd running?).
            </p>
          )}
          <RawJson value={jsz.data} title="Raw /jsz JSON" />
        </CardContent>
      </Card>

      <Card size="sm" className="xl:col-span-2">
        <CardContent>
          <div className="flex items-center justify-between gap-2">
            <span className="text-sm font-medium">Connections</span>
            <Button size="xs" variant="outline" onClick={() => void conns.refresh()}>
              Refresh
            </Button>
          </div>
          {conns.error && <ErrorBox error={conns.error} />}
          <Table className="mt-2">
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead className="w-16">CID</TableHead>
                <TableHead className="w-20">Kind</TableHead>
                <TableHead className="w-48">Remote</TableHead>
                <TableHead>Subscriptions</TableHead>
                <TableHead className="w-32">Msgs in/out</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {(conns.data?.connections ?? []).length === 0 && (
                <EmptyRow colSpan={5} message="No connections reported." />
              )}
              {(conns.data?.connections ?? []).map((c) => (
                <TableRow key={c.cid} className="align-top">
                  <TableCell className="font-mono text-xs">{c.cid}</TableCell>
                  <TableCell>{c.kind}</TableCell>
                  <TableCell className="font-mono text-xs">
                    {c.ip}:{c.port}
                  </TableCell>
                  <TableCell className="font-mono text-xs break-all whitespace-normal text-muted-foreground">
                    {c.subscriptions ?? "—"}
                    {(c.subscriptions_list ?? []).length > 0 && `: ${(c.subscriptions_list ?? []).join(", ")}`}
                  </TableCell>
                  <TableCell className="font-mono text-xs">
                    {c.in_msgs} / {c.out_msgs}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </div>
  );
}

export default function NatsPage() {
  return (
    <div>
      <h1 className="text-lg font-semibold">NATS</h1>
      <p className="mt-1 text-sm text-muted-foreground">
        Live wire traffic and server introspection. Requires the websocket and monitoring listeners in
        configs/nats-server.conf (restart NATS after changing it).
      </p>
      <Section title="Live messages (websocket)">
        <LiveMessages />
      </Section>
      <Section title="Server info (monitoring)">
        <ServerInfo />
      </Section>
    </div>
  );
}
