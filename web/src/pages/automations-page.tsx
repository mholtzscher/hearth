import { useState } from "react";
import { Link as RouterLink } from "react-router-dom";

import {
  automationFetch,
  automationJsonSource,
  automationJsonText,
} from "../api/automation-json.ts";
import { useBaseUrlVersion } from "../api/client.ts";
import { useApi } from "../api/hooks.ts";
import type { Automation, Collection } from "../api/types.ts";
import { Button } from "../components/button.tsx";
import { EmptyRow } from "../components/empty-row.tsx";
import { ErrorBox } from "../components/error-box.tsx";
import { linkClass } from "../components/link-styles.ts";
import { MonoId } from "../components/mono-id.tsx";
import { Notice } from "../components/notice.tsx";
import { PageHeader } from "../components/page-header.tsx";
import { RawJson } from "../components/raw-json.tsx";
import { SegmentedControl } from "../components/segmented-control.tsx";
import { StatusChip } from "../components/status-chip.tsx";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../components/table.tsx";
import { AutomationCard } from "../features/automations/automation-card";
import { readAutomationDocument } from "../features/automations/document";
import { useOptionalWorkspace } from "../features/workspace/workspace";
import { describeAutomationSteps } from "./automation-step-tree.ts";

type Layout = "cards" | "table";

/** One page read from the ID-ascending Automation keyset. */
const automationsPagePath = (cursor: string | undefined): string =>
  `/v1/automations?limit=50${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`;

const AutomationsLibrary = ({
  layout,
  onLayoutChange,
}: {
  layout: Layout;
  onLayoutChange: (layout: Layout) => void;
}) => {
  const workspace = useOptionalWorkspace();
  const [cursor, setCursor] = useState<string | undefined>(undefined);
  const path = automationsPagePath(cursor);
  const { data, error, loading, refresh } = useApi(`automations-${path}`, () =>
    automationFetch<Collection<Automation>>(path)
  );

  return (
    <div>
      <PageHeader
        title="Automations"
        description="Current definitions in ID order (GET /v1/automations). Enablement governs automatic Runs; a manual Run from the detail page works either way."
      >
        {workspace ? (
          <Button size="default" onClick={() => workspace.newAutomation()}>
            New Automation
          </Button>
        ) : (
          <Button size="default" asChild>
            <RouterLink to="/automations/new">New Automation</RouterLink>
          </Button>
        )}
        <SegmentedControl
          label="Library layout"
          value={layout}
          onValueChange={onLayoutChange}
          options={[
            { value: "cards", label: "Cards" },
            { value: "table", label: "Table" },
          ]}
        />
        <Button
          size="default"
          variant="outline"
          onClick={() => {
            void refresh();
          }}
        >
          Refresh
        </Button>
        {loading && (
          <span className="text-muted-foreground text-sm">Loading…</span>
        )}
      </PageHeader>
      {error && <ErrorBox error={error} />}
      {data && (
        <>
          {layout === "cards" ? (
            <div className="my-[18px] grid grid-cols-[repeat(auto-fit,minmax(min(100%,420px),1fr))] items-start gap-4">
              {data.items.length === 0 && (
                <p className="min-w-0">
                  No automations. Create one here or ask Hearth for a proposal.
                </p>
              )}
              {data.items.map((automation) => {
                try {
                  return (
                    <AutomationCard
                      key={automation.id}
                      document={readAutomationDocument(
                        automationJsonSource(automation.definition)
                      )}
                      automationId={automation.id}
                      compact
                      headingLevel={2}
                    >
                      <p className="text-label text-muted-foreground my-[5px]">
                        Revision {automation.revision} ·{" "}
                        {automation.definition.enabled ? "Enabled" : "Disabled"}
                      </p>
                      <RouterLink
                        className="text-link focus-visible:outline-ring underline-offset-4 hover:underline focus-visible:underline focus-visible:outline-2 focus-visible:outline-offset-3 focus-visible:outline-solid"
                        to={`/automations/${automation.id}`}
                      >
                        Open {automation.definition.name}
                      </RouterLink>
                    </AutomationCard>
                  );
                } catch {
                  return (
                    <Notice
                      key={automation.id}
                      role="alert"
                      className="min-w-0"
                    >
                      <p>
                        {automation.definition.name} cannot be shown in this
                        version.
                      </p>
                      <RouterLink
                        className={linkClass}
                        to={`/automations/${automation.id}`}
                      >
                        Inspect {automation.definition.name}
                      </RouterLink>
                    </Notice>
                  );
                }
              })}
            </div>
          ) : (
            <Table className="mt-3" aria-label="Automations">
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Enabled</TableHead>
                  <TableHead>Revision</TableHead>
                  <TableHead>Triggers</TableHead>
                  <TableHead>Defined Steps</TableHead>
                  <TableHead>Commands</TableHead>
                  <TableHead>Updated</TableHead>
                  <TableHead>Automation id</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.items.length === 0 && (
                  <EmptyRow colSpan={8} message="No automations." />
                )}
                {data.items.map((automation) => {
                  const tree = describeAutomationSteps(
                    automation.definition.steps
                  );
                  return (
                    <TableRow key={automation.id}>
                      <TableCell className="font-medium">
                        <RouterLink
                          to={`/automations/${automation.id}`}
                          className={linkClass}
                        >
                          {automation.definition.name}
                        </RouterLink>
                      </TableCell>
                      <TableCell>
                        <StatusChip
                          status={
                            automation.definition.enabled
                              ? "enabled"
                              : "disabled"
                          }
                        />
                      </TableCell>
                      <TableCell className="font-mono text-xs">
                        {automation.revision}
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {automation.definition.triggers.length}
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {tree.stepCount}
                        {tree.truncated ? "+ (incomplete)" : ""}
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {tree.commandCount}
                        {tree.truncated ? "+ (incomplete)" : ""}
                      </TableCell>
                      <TableCell className="text-muted-foreground font-mono text-xs">
                        {automation.updated_at}
                      </TableCell>
                      <TableCell className="max-w-[22rem]">
                        <MonoId
                          value={automation.id}
                          className="text-muted-foreground"
                        />
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          )}
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
          <RawJson
            value={data}
            code={automationJsonText(data)}
            title="Raw list JSON"
          />
        </>
      )}
    </div>
  );
};

export default function AutomationsPage() {
  const [layout, setLayout] = useState<Layout>("cards");
  const baseVersion = useBaseUrlVersion();
  // Cursors are scoped to one server. A new server remounts the library so it
  // starts at the first page and never receives another server's cursor; the
  // layout choice lives outside so it survives the remount.
  return (
    <AutomationsLibrary
      key={baseVersion}
      layout={layout}
      onLayoutChange={setLayout}
    />
  );
}
