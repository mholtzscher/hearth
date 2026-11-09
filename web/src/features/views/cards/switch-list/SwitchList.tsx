import { useEffect, useState } from "react";
import type { z } from "zod";
import { usePowerReadings, type PowerReading } from "../../bindings/power";
import { useControls } from "../../data/provider";
import { supportsOperation } from "../../controls/support";
import { PowerSwitch } from "../../controls/PowerSwitch";
import { ControlStatus } from "../../controls/ControlStatus";
import { CardFrame, StatusList } from "../CardFrame";
import { cardStatus } from "../status";
import { useArrival } from "../observation";
import { listArrivals, oldestObservation, rowSources, rowStates, type ReadingRow } from "../reading-list/ReadingRows";
import { switchListSchema } from "./schema";

function SwitchRow({ power, label }: { power: PowerReading; label: string }) {
  const status = cardStatus(power.resource);
  const data = status.value === "empty" ? undefined : power.resource.data;
  const glow = useArrival(data ? { updatedAt: data.observed_at, shown: String(data.on), eligible: status.glows } : undefined);
  const canSet = !!power.entity && supportsOperation(power.entity, "set");
  return <li className="view-switch-row">
    {glow !== undefined && <span key={glow} className="view-row-arrival" aria-hidden="true" />}
    <div className="view-switch-row-name"><span>{label}</span><StatusList entries={status.entries} />
      {canSet ? <ControlStatus entityId={power.source.entity_id} subject="power" pending={value => value === true ? "Turning on…" : "Turning off…"} idle={power.since} /> : <p className="view-power-idle">{power.entity ? "Read-only" : power.since}</p>}
    </div>
    <span className="view-row-value" data-value={status.value}>{data ? data.on ? "On" : "Off" : "—"}</span>
    {canSet ? <PowerSwitch entity={power.entity!} label={`${label} power`} /> : <span className="view-switch-unavailable" aria-hidden="true" />}
  </li>;
}

export function BoundSwitchList({ title, rows, all_off }: z.infer<typeof switchListSchema>) {
  const powers = usePowerReadings(rows.map(row => row.source));
  const targets = rows.map(row => ({ entityId: row.source.entity_id, operation: "set" as const }));
  const [controls, request] = useControls(targets);
  const [attempted, setAttempted] = useState<string[]>([]);
  const identity = JSON.stringify(rows.map(row => row.source));
  useEffect(() => setAttempted([]), [identity]);
  const readingRows: ReadingRow[] = powers.map((power, index) => ({
    key: power.source.entity_id, name: rows[index].label ?? power.resource.metadata?.label ?? "Power",
    resource: { ...power.resource, data: power.resource.data && { text: power.resource.data.on ? "On" : "Off", observed_at: power.resource.data.observed_at } },
  }));
  const displayed = rowStates(readingRows), sources = rowSources(readingRows);
  for (const row of readingRows) if (!sources.some(source => source.entityId === row.key)) sources.push({ entityId: row.key, label: row.name });
  const working = controls.some(control => control.phase === "pending" || control.phase === "confirming");
  const eligible = powers.flatMap((power, index) => power.entity?.enabled && supportsOperation(power.entity, "set") && power.resource.data?.on !== false ? [index] : []);
  const canGroup = powers.some(power => power.entity && supportsOperation(power.entity, "set"));
  const members = powers.flatMap((power, index) => attempted.includes(power.source.entity_id) ? [index] : []);
  const inProgress = members.some(index => controls[index].phase === "pending" || controls[index].phase === "confirming");
  const off = members.filter(index => powers[index].resource.status === "ready" && powers[index].resource.data?.on === false).length;
  const failed = members.filter(index => controls[index].phase === "failed").length;
  return <CardFrame className="view-switch-list" title={title} status={[]} sources={sources}
    arrivals={listArrivals(displayed)} observedAt={oldestObservation(displayed)} observedLabel="Oldest reading"
    sourceExtra={<><p>Each switch uses its Entity's current support. Turn all off requests separate Commands for enabled, supported rows that are not already observed Off.</p><p>Group feedback counts observed Off readings; it is not an atomic multi-Device outcome.</p>{powers.filter(power => power.historyError).map(power => <p key={power.source.entity_id}>{power.resource.metadata?.label ?? "Power"}: since time unavailable.</p>)}</>}>
    <ul className="view-switch-rows">{powers.map((power, index) => <SwitchRow key={power.source.entity_id} power={power} label={readingRows[index].name} />)}</ul>
    {all_off && canGroup && <div className="view-switch-group">
      <button type="button" disabled={working || eligible.length === 0} onClick={() => {
        setAttempted(eligible.map(index => powers[index].source.entity_id));
        eligible.forEach(index => request(targets[index], false));
      }}>Turn all off</button>
      <p role="status" data-failed={failed > 0 || undefined}>{members.length > 0 && (inProgress ? `Updating ${members.length} switches…` : `${off} of ${members.length} Off${failed ? ` · ${failed} failed` : off < members.length ? " · check rows" : ""}`)}</p>
    </div>}
  </CardFrame>;
}
