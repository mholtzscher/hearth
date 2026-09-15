import { useEffect, useState } from "react";
import { Link as RouterLink } from "react-router-dom";
import { apiFetch, useBaseUrlVersion } from "../api/client.ts";
import { useApi } from "../api/hooks.ts";
import type { Automation, Collection } from "../api/types.ts";
import {
  EmptyRow,
  ErrorBox,
  MonoId,
  RawJson,
  StatusChip,
  linkClass,
} from "../components/common.tsx";
import { Button } from "../components/ui/button.tsx";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../components/ui/table.tsx";

/** One page read from the ID-ascending Automation keyset. */
function automationsPagePath(cursor: string | undefined): string {
  return `/v1/automations?limit=50${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`;
}

export default function AutomationsPage() {
  const [cursor, setCursor] = useState<string | undefined>(undefined);
  const baseVersion = useBaseUrlVersion();
  useEffect(() => {
    // Cursors are scoped to one server: restart from the first page there.
    setCursor(undefined);
  }, [baseVersion]);
  const path = automationsPagePath(cursor);
  const { data, error, loading, refresh } = useApi(`automations-${path}`, () =>
    apiFetch<Collection<Automation>>(path),
  );

  return (
    <div>
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="mr-2 text-lg font-semibold">Automations</h1>
        <Button size="sm" variant="outline" onClick={() => void refresh()}>
          Refresh
        </Button>
        {loading && <span className="text-sm text-muted-foreground">Loading…</span>}
      </div>
      <p className="mt-1 text-sm text-muted-foreground">
        Current definitions in ID order (GET /v1/automations). Enablement governs automatic Runs;
        a manual Run from the detail page works either way.
      </p>
      {error && <ErrorBox error={error} />}
      {data && (
        <>
          <Table className="mt-3">
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead>Name</TableHead>
                <TableHead>Enabled</TableHead>
                <TableHead>Revision</TableHead>
                <TableHead>Triggers</TableHead>
                <TableHead>Steps</TableHead>
                <TableHead>Updated</TableHead>
                <TableHead>Automation id</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.items.length === 0 && <EmptyRow colSpan={7} message="No automations." />}
              {data.items.map((automation) => (
                <TableRow key={automation.id}>
                  <TableCell className="font-medium">
                    <RouterLink to={`/automations/${automation.id}`} className={linkClass}>
                      {automation.definition.name}
                    </RouterLink>
                  </TableCell>
                  <TableCell>
                    <StatusChip
                      label={automation.definition.enabled ? "enabled" : "disabled"}
                      status={automation.definition.enabled ? "enabled" : "disabled"}
                    />
                  </TableCell>
                  <TableCell className="font-mono text-xs">{automation.revision}</TableCell>
                  <TableCell className="text-muted-foreground">
                    {automation.definition.triggers.length}
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {automation.definition.steps.length}
                  </TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">
                    {automation.updated_at}
                  </TableCell>
                  <TableCell className="max-w-[22rem]">
                    <MonoId value={automation.id} className="text-muted-foreground" />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {data.next_cursor && (
            <Button
              size="sm"
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
}
