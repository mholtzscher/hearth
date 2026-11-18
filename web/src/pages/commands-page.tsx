import { useEffect, useState } from "react";
import { Link as RouterLink, useSearchParams } from "react-router-dom";

import { apiFetch, useBaseUrlVersion } from "../api/client.ts";
import { useApi } from "../api/hooks.ts";
import type { Collection, CommandRecord } from "../api/types.ts";
import { Button } from "../components/button.tsx";
import { Card } from "../components/card.tsx";
import { EmptyRow } from "../components/empty-row.tsx";
import { ErrorBox } from "../components/error-box.tsx";
import { Facts } from "../components/facts.tsx";
import { Input } from "../components/input.tsx";
import { Label } from "../components/label.tsx";
import { linkClass } from "../components/link-styles.ts";
import { MonoId } from "../components/mono-id.tsx";
import { NativeSelect } from "../components/native-select.tsx";
import { PageHeader } from "../components/page-header.tsx";
import { RawJson } from "../components/raw-json.tsx";
import { StatusChip } from "../components/status-chip.tsx";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../components/table.tsx";

const RECENT_KEY = "hearth.recentCommands";
const RECENT_LIMIT = 10;

/** Command IDs looked up from this browser, newest first. IDs are opaque
    audit keys, so they are stored verbatim and never interpreted. */
const readRecentIds = (): string[] => {
  try {
    const raw = localStorage.getItem(RECENT_KEY);
    if (!raw) {
      return [];
    }
    const parsed: unknown = JSON.parse(raw);
    return Array.isArray(parsed)
      ? parsed
          .filter((v): v is string => typeof v === "string")
          .slice(0, RECENT_LIMIT)
      : [];
  } catch {
    return [];
  }
};

const rememberId = (id: string) => {
  try {
    localStorage.setItem(
      RECENT_KEY,
      JSON.stringify(
        [id, ...readRecentIds().filter((v) => v !== id)].slice(0, RECENT_LIMIT)
      )
    );
  } catch {
    // Recent IDs are a convenience; a full or unavailable store never blocks lookup.
  }
};

const STATUS_OPTIONS = [
  "satisfied",
  "dispatched",
  "requested",
  "accepted",
  "rejected",
  "adapter_unhealthy",
  "entity_unavailable",
  "outcome_timeout",
  "entity_disabled",
  "internal_failure",
  "interrupted",
];

/** Plain-language meaning of a terminal Command status for triage. */
const statusNote = (record: CommandRecord): string => {
  switch (record.status) {
    case "satisfied": {
      return `Satisfied by observation ${record.outcome_observation_id ?? "—"}: the post-dispatch State matched the requested outcome.`;
    }
    case "dispatched": {
      return "Dispatched: accepted with no observation to verify. The effect was not confirmed.";
    }
    case "requested":
    case "accepted": {
      return "Still in flight: no terminal outcome was recorded yet.";
    }
    case "rejected": {
      return "Rejected upstream without dispatch. Check adapter logs around the requested time.";
    }
    case "adapter_unhealthy": {
      return "Never dispatched: the owning Adapter had no healthy runtime. Check adapter health.";
    }
    case "entity_unavailable": {
      return "Rejected as unavailable after dispatch was attempted. Check availability history.";
    }
    case "outcome_timeout": {
      return "Dispatched but no matching observation arrived before the deadline. Check state history.";
    }
    case "entity_disabled": {
      return "Never dispatched: the Entity was disabled. Enable it and send again.";
    }
    case "internal_failure": {
      return "Core failed before a terminal outcome. Check hearthd logs around the requested time.";
    }
    case "interrupted": {
      return "Interrupted by a Core restart while in flight. Never redispatches: send again if needed.";
    }
    default: {
      return "";
    }
  }
};

/** Milliseconds between two RFC3339 timestamps, or null when unparseable. */
const durationMs = (from: string, to: string | undefined): number | null => {
  if (!to) {
    return null;
  }
  const ms = Date.parse(to) - Date.parse(from);
  return Number.isFinite(ms) && ms >= 0 ? ms : null;
};

const CommandDetail = ({ commandId }: { commandId: string }) => {
  const { data, error, loading, refresh } = useApi<CommandRecord>(
    `command-${commandId}`,
    () => apiFetch<CommandRecord>(`/v1/commands/${commandId}`)
  );
  useEffect(() => {
    if (data) {
      rememberId(data.id);
    }
  }, [data]);
  const latency = data
    ? durationMs(data.requested_at, data.completed_at)
    : null;

  return (
    <Card className="mt-3" size="compact">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-caption font-mono">{commandId}</span>
        {data && <StatusChip status={data.status} />}
        <Button
          size="default"
          variant="outline"
          onClick={async () => {
            await refresh();
          }}
        >
          Refresh
        </Button>
        {loading && (
          <span className="text-muted-foreground text-label">Loading…</span>
        )}
      </div>
      {error && <ErrorBox error={error} />}
      {data && (
        <>
          <p className="text-muted-foreground text-label mt-1.5">
            {statusNote(data)}
          </p>
          <Facts
            rows={[
              [
                "Entity",
                <RouterLink
                  key="entity"
                  to={`/entities/${data.entity_id}`}
                  className={linkClass}
                >
                  {data.entity_id}
                </RouterLink>,
              ],
              ["Operation", data.operation],
              ["Parameters", JSON.stringify(data.parameters)],
              ["Requested at", data.requested_at],
              ["Accepted at", data.accepted_at ?? "—"],
              [
                "Completed at",
                `${data.completed_at ?? "—"}${latency === null ? "" : ` (${latency} ms after request)`}`,
              ],
              ["Deadline at", data.deadline_at],
              ["Outcome observation", data.outcome_observation_id ?? "—"],
              ["Failure", data.failure_code ?? "—"],
            ]}
          />
          <RawJson value={data} title="Raw command JSON" />
        </>
      )}
    </Card>
  );
};

const CommandsPage = () => {
  const [searchParams, setSearchParams] = useSearchParams();
  const baseVersion = useBaseUrlVersion();
  const selectedId = searchParams.get("command_id") ?? "";
  const [commandId, setCommandId] = useState(selectedId);
  const [recentIds, setRecentIds] = useState<string[]>(readRecentIds);
  const [entityFilter, setEntityFilter] = useState("");
  const [appliedEntity, setAppliedEntity] = useState("");
  const [statusFilter, setStatusFilter] = useState("");
  const [cursorState, setCursorState] = useState<{
    server: number;
    value: string | undefined;
  }>({ server: baseVersion, value: undefined });
  const [listNonce, setListNonce] = useState(0);
  // Cursors are scoped to one server: restart from the first page there.
  const cursor =
    cursorState.server === baseVersion ? cursorState.value : undefined;
  const setCursor = (value: string | undefined) => {
    setCursorState({ server: baseVersion, value });
  };
  const [lastSelectedId, setLastSelectedId] = useState(selectedId);
  if (lastSelectedId !== selectedId) {
    setLastSelectedId(selectedId);
    setCommandId(selectedId);
  }

  const lookUp = (id: string) => {
    const trimmed = id.trim();
    if (!trimmed) {
      return;
    }
    setSearchParams(
      trimmed === selectedId ? searchParams : { command_id: trimmed }
    );
    setRecentIds(readRecentIds());
  };

  const applyFilters = () => {
    setAppliedEntity(entityFilter.trim());
    setCursor(undefined);
    setListNonce((n) => n + 1);
  };

  const listQuery =
    `/v1/commands?limit=50${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}` +
    `${appliedEntity ? `&entity_id=${encodeURIComponent(appliedEntity)}` : ""}` +
    `${statusFilter ? `&status=${encodeURIComponent(statusFilter)}` : ""}`;
  const { data, error, loading, refresh } = useApi<Collection<CommandRecord>>(
    `commands-${listQuery}-${listNonce}`,
    () => apiFetch<Collection<CommandRecord>>(listQuery)
  );

  return (
    <div>
      <PageHeader
        title="Commands"
        description={
          <>
            Household command audit, newest first (GET /v1/commands). Select a
            row or look up a <span className="font-mono">cmd_…</span> id for the
            durable outcome; per-entity history also lives on each entity page.
          </>
        }
      >
        <Input
          aria-label="command_id"
          placeholder="Look up cmd_…"
          className="text-caption w-96 font-mono"
          value={commandId}
          onChange={(e) => setCommandId(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              lookUp(commandId);
            }
          }}
        />
        <Button size="default" onClick={() => lookUp(commandId)}>
          Look up
        </Button>
        <Button
          size="default"
          variant="outline"
          onClick={async () => {
            await refresh();
          }}
        >
          Refresh list
        </Button>
        {loading && (
          <span className="text-muted-foreground text-label">Loading…</span>
        )}
      </PageHeader>
      {recentIds.length > 0 && (
        <div className="mt-2 flex flex-wrap items-center gap-1.5">
          <span className="text-muted-foreground text-caption">Recent:</span>
          {recentIds.map((id) => (
            <Button
              key={id}
              size="default"
              variant="outline"
              className="font-mono"
              onClick={() => lookUp(id)}
            >
              {id.length > 18 ? `${id.slice(0, 18)}…` : id}
            </Button>
          ))}
        </div>
      )}

      {selectedId && <CommandDetail key={selectedId} commandId={selectedId} />}

      <h2 className="sr-only">Command history</h2>
      <div className="mt-6 flex flex-wrap items-end gap-2">
        <div className="grid gap-1.5">
          <Label htmlFor="cmd-entity-filter">Entity filter</Label>
          <Input
            id="cmd-entity-filter"
            placeholder="Filter by entity_id…"
            className="text-caption w-72 font-mono"
            value={entityFilter}
            onChange={(e) => setEntityFilter(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                applyFilters();
              }
            }}
          />
        </div>
        <div className="grid gap-1.5">
          <Label htmlFor="cmd-status-filter">Status filter</Label>
          <NativeSelect
            id="cmd-status-filter"
            value={statusFilter}
            onChange={(e) => {
              setStatusFilter(e.target.value);
              setCursor(undefined);
              setListNonce((n) => n + 1);
            }}
          >
            <option value="">all statuses</option>
            {STATUS_OPTIONS.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </NativeSelect>
        </div>
        <Button size="default" variant="outline" onClick={applyFilters}>
          Apply
        </Button>
        {(appliedEntity || statusFilter) && (
          <Button
            size="default"
            variant="outline"
            onClick={() => {
              setEntityFilter("");
              setAppliedEntity("");
              setStatusFilter("");
              setCursor(undefined);
              setListNonce((n) => n + 1);
            }}
          >
            Clear
          </Button>
        )}
      </div>

      {error && <ErrorBox error={error} />}
      {data && (
        <>
          <Table className="mt-3" aria-label="Commands">
            <TableHeader>
              <TableRow>
                <TableHead>Status</TableHead>
                <TableHead>Entity</TableHead>
                <TableHead>Operation</TableHead>
                <TableHead>Params</TableHead>
                <TableHead>Requested</TableHead>
                <TableHead>Command id</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.items.length === 0 && (
                <EmptyRow colSpan={6} message="No commands match." />
              )}
              {data.items.map((c) => (
                <TableRow
                  key={c.id}
                  data-state={c.id === selectedId ? "selected" : undefined}
                >
                  <TableCell>
                    <StatusChip status={c.status} />
                  </TableCell>
                  <TableCell className="max-w-[16rem]">
                    <RouterLink
                      to={`/entities/${c.entity_id}`}
                      className={linkClass}
                    >
                      <MonoId value={c.entity_id} />
                    </RouterLink>
                  </TableCell>
                  <TableCell>{c.operation}</TableCell>
                  <TableCell className="text-caption max-w-[16rem] truncate font-mono">
                    {JSON.stringify(c.parameters)}
                  </TableCell>
                  <TableCell className="text-muted-foreground text-caption font-mono">
                    {c.requested_at}
                  </TableCell>
                  <TableCell className="max-w-[16rem]">
                    <Button
                      variant="link"
                      size="default"
                      className="font-mono"
                      aria-current={c.id === selectedId ? "true" : undefined}
                      onClick={() => lookUp(c.id)}
                    >
                      <MonoId value={c.id} className="text-muted-foreground" />
                    </Button>
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
    </div>
  );
};

export default CommandsPage;
