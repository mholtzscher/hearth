import { Link } from "react-router-dom";

import { entityStateRequest, readEntity } from "../../bindings/entity";
import type { FieldEntity } from "../../bindings/entity";
import { useReadings } from "../../bindings/registry";
import { StatusDot } from "../../controls/status-dot";
import { useReads } from "../../data/provider";
import { rowSources, rowStates } from "../reading-list/reading-rows";
import type { ReadingRow } from "../reading-list/reading-rows";
import { statusEntry } from "../status";
import type { StatusEntry } from "../status";
import { airCategory } from "./category";
import { boundRoles } from "./schema";
import type { AirPurifierProps, AirPurifierRole } from "./schema";

const names: Record<AirPurifierRole, string> = {
  power: "Power",
  pm25: "PM2.5",
  filter_life: "Filter life",
  mode: "Mode",
  fan_level: "Fan level",
  display: "Display",
  child_lock: "Child lock",
};

const boundSource = (props: AirPurifierProps, role: AirPurifierRole) => {
  const source = props[role];
  if (!source) {
    throw new Error(`Missing ${role} binding`);
  }
  return source;
};

const purifierStatus = (
  allUnavailable: boolean,
  allLoading: boolean
): StatusEntry[] => {
  if (allUnavailable) {
    return [statusEntry("unavailable")];
  }
  if (allLoading) {
    return [statusEntry("loading")];
  }
  return [];
};

export const Category = ({
  value,
  dimmed,
}: {
  value: number;
  dimmed: boolean;
}) => {
  const category = airCategory(value);
  return (
    <span
      className="text-label text-gray-9 data-[tone=warn]:text-warning data-[tone=error]:text-destructive data-[dimmed]:text-gray-8 inline-flex items-baseline gap-1.5"
      data-tone={category.tone}
      data-dimmed={dimmed || undefined}
    >
      <StatusDot tone={dimmed ? "dimmed" : category.tone} />
      {category.words}
    </span>
  );
};

/** Read each bound role explicitly; support and observed State share the read cache. */
export const usePurifier = (props: AirPurifierProps) => {
  const roles = boundRoles(props);
  const sources = roles.map(([role]) => boundSource(props, role));
  const readings = useReadings(sources.map((source) => ({ source })));
  const snapshots = useReads(
    sources.map((source) => entityStateRequest(source))
  );
  const rows: ReadingRow[] = roles.map(([role], index) => ({
    key: role,
    name: names[role],
    resource: readings[index],
  }));
  const states = rowStates(rows);
  const entities = Object.fromEntries(
    roles.map(([role, source], index) => {
      const raw = snapshots[index].data;
      return [
        role,
        raw === undefined ? undefined : readEntity(raw, source, "canonical"),
      ];
    })
  ) as Partial<Record<AirPurifierRole, FieldEntity>>;
  const sourcesList = rowSources(rows);
  for (const [role, source] of roles) {
    if (!sourcesList.some((item) => item.entityId === source.entity_id)) {
      sourcesList.push({ entityId: source.entity_id, label: names[role] });
    }
  }
  const allUnavailable =
    states.length > 0 &&
    states.every(
      (row) => row.resource.metadata?.availability === "unavailable"
    );
  const allLoading = states.every((row) => row.resource.status === "loading");
  const status = purifierStatus(allUnavailable, allLoading);
  const deviceId = entities.power?.device_id;
  const sourceExtra = (
    <>
      <p>
        Air category uses EPA 2024 24-hour outdoor PM2.5 bands; applying them to
        an instant indoor reading is approximate.
      </p>
      {deviceId && (
        <p>
          <Link
            className="text-link focus-visible:outline-ring underline underline-offset-[3px] focus-visible:outline-2 focus-visible:outline-offset-3"
            to={`/devices/${deviceId}`}
          >
            Device details
          </Link>{" "}
          <code className="text-caption text-muted-foreground mt-1 block">
            {deviceId}
          </code>
        </p>
      )}
    </>
  );
  return {
    rows: states,
    entities,
    sources: sourcesList,
    status,
    allUnavailable,
    sourceExtra,
  };
};
