import { useState } from "react";
import { apiFetch } from "../api/client.ts";
import { useApi } from "../api/hooks.ts";
import type { CommandRecord } from "../api/types.ts";
import { ErrorBox, Facts, RawJson, StatusChip } from "../components/common.tsx";
import { Button } from "../components/ui/button.tsx";
import { Card, CardContent } from "../components/ui/card.tsx";
import { Input } from "../components/ui/input.tsx";

export default function CommandsPage() {
  const [commandId, setCommandId] = useState("");
  const [applied, setApplied] = useState("");
  const { data, error, loading, refresh } = useApi<CommandRecord | null>(
    `command-${applied || "none"}`,
    () => (applied ? apiFetch<CommandRecord>(`/v1/commands/${applied}`) : Promise.resolve(null)),
  );

  function lookUp() {
    const id = commandId.trim();
    if (id && id === applied) void refresh();
    else setApplied(id);
  }

  return (
    <div>
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="mr-2 text-lg font-semibold">Commands</h1>
        <Input
          aria-label="command_id"
          placeholder="cmd_…"
          className="w-96 font-mono text-xs"
          value={commandId}
          onChange={(e) => setCommandId(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") lookUp();
          }}
        />
        <Button size="sm" onClick={lookUp}>
          Look up
        </Button>
        {loading && <span className="text-sm text-muted-foreground">Loading…</span>}
      </div>
      <p className="mt-1 text-sm text-muted-foreground">
        Durable command audit read (GET /v1/commands/{"{command_id}"}). Per-entity history lives on each
        entity page.
      </p>
      {error && <ErrorBox error={error} />}
      {data && (
        <Card className="mt-3" size="sm">
          <CardContent>
            <div className="flex items-center gap-2">
              <span className="font-mono text-xs">{data.id}</span>
              <StatusChip status={data.status} />
            </div>
            <Facts
              rows={[
                ["Operation", data.operation],
                ["Parameters", JSON.stringify(data.parameters)],
                ["Failure", data.failure_code ?? "—"],
                ["Requested at", data.requested_at],
              ]}
            />
            <RawJson value={data} title="Raw command JSON" />
          </CardContent>
        </Card>
      )}
    </div>
  );
}
