import { Link } from "react-router-dom";
import { z } from "zod";
import { apiFetch } from "../../../../api/client";
import { entityStateRequest, type FieldEntity } from "../../bindings/entity";
import { useReadings } from "../../bindings/registry";
import { useRead, useReads } from "../../data/provider";
import { statusEntry } from "../status";
import { rowSources, rowStates, type ReadingRow } from "../reading-list/ReadingRows";
import { boundRoles, type AirPurifierProps, type AirPurifierRole } from "./schema";
import { airCategory } from "./category";

const names: Record<AirPurifierRole, string> = { power: "Power", pm25: "PM2.5", filter_life: "Filter life", mode: "Mode", fan_level: "Fan level", display: "Display", child_lock: "Child lock" };

export function Category({ value, dimmed }: { value: number; dimmed: boolean }) {
  const category = airCategory(value);
  return <span className="view-air-purifier-category" data-tone={category.tone} data-dimmed={dimmed || undefined}><span className="view-status-dot" aria-hidden="true" />{category.words}</span>;
}

/** Read each bound role explicitly; support and observed State share the read cache. */
export function usePurifier(props: AirPurifierProps) {
  const roles = boundRoles(props);
  const sources = roles.map(([role]) => props[role]!);
  const readings = useReadings(sources.map(source => ({ source })));
  const snapshots = useReads(sources.map(source => entityStateRequest(source)));
  const rows: ReadingRow[] = roles.map(([role], index) => ({ key: role, name: names[role], resource: readings[index] }));
  const states = rowStates(rows);
  const entities = Object.fromEntries(roles.map(([role], index) => [role, snapshots[index].data as FieldEntity | undefined])) as Partial<Record<AirPurifierRole, FieldEntity>>;
  const sourcesList = rowSources(rows);
  roles.forEach(([role, source]) => { if (!sourcesList.some(item => item.entityId === source.entity_id)) sourcesList.push({ entityId: source.entity_id, label: names[role] }); });
  const allUnavailable = states.length > 0 && states.every(row => row.resource.metadata?.availability === "unavailable");
  const allLoading = states.every(row => row.resource.status === "loading");
  const status = allUnavailable ? [statusEntry("unavailable")] : allLoading ? [statusEntry("loading")] : [];
  // The shared FieldEntity intentionally omits Device membership; fetch only that
  // metadata for the Source link, without changing the reading's cache contract.
  const device = useRead({ key: `purifier-device:${props.power.entity_id}`, kind: "state", load: signal => apiFetch(`/v1/entities/${encodeURIComponent(props.power.entity_id)}`, { signal }, text => z.object({ device_id: z.string().optional() }).parse(JSON.parse(text)).device_id) });
  const sourceExtra = <><p>Air category uses EPA 2024 24-hour outdoor PM2.5 bands; applying them to an instant indoor reading is approximate.</p>{typeof device.data === "string" && <p><Link to={`/devices/${device.data}`}>Device details</Link> <code>{device.data}</code></p>}</>;
  return { rows: states, entities, sources: sourcesList, status, allUnavailable, sourceExtra };
}
