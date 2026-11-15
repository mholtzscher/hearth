import { useEffect, useState } from "react";
import { Button } from "../../../../components/button";
import type { z } from "zod";
import { usePowerReadings, type PowerReading } from "../../bindings/power";
import { useControls } from "../../data/provider";
import { supportsOperation } from "../../controls/support";
import { PowerSwitch } from "../../controls/PowerSwitch";
import { ControlStatus } from "../../controls/ControlStatus";
import { DataCard, StatusList } from "../DataCard";
import { cardStatus } from "../status";
import { useArrival } from "../observation";
import { listArrivals, oldestObservation, rowSources, rowStates, type ReadingRow } from "../reading-list/ReadingRows";
import { toggleListSchema } from "./schema";

function SwitchRow({ power, label }: { power: PowerReading; label: string }) {
  const status = cardStatus(power.resource);
  const data = status.value === "empty" ? undefined : power.resource.data;
  const glow = useArrival(data ? { updatedAt: data.observed_at, shown: String(data.on), eligible: status.glows } : undefined);
  const canSet = !!power.entity && supportsOperation(power.entity, "set");
  return <li className="relative grid grid-cols-[minmax(0,1fr)_auto_44px] items-center gap-3 py-3 not-first:border-t not-first:border-border">
    {glow !== undefined && <span key={glow} className="pointer-events-none absolute inset-x-0 -bottom-px z-[1] h-0.5 animate-arrival bg-orange" aria-hidden="true" />}
    <div className="min-w-0 text-label text-gray-9 [overflow-wrap:anywhere]"><span>{label}</span><StatusList entries={status.entries} />
      {canSet ? <ControlStatus className="mt-0.5 min-h-[42px] text-muted-foreground" entityId={power.source.entity_id} subject="power" pending={value => value === true ? "Turning on…" : "Turning off…"} idle={power.since} /> : <p className="mt-0.5 min-h-[42px] text-label leading-normal text-muted-foreground">{power.entity ? "Read-only" : power.since}</p>}
    </div>
    <span className="ms-auto max-w-full flex-none whitespace-nowrap text-right text-compact-reading leading-[1.3] font-normal tracking-[-.02em] text-foreground [overflow-wrap:break-word] data-[value=dimmed]:text-gray-8 data-[value=empty]:text-gray-6" data-value={status.value}>{data ? data.on ? "On" : "Off" : "—"}</span>
    {canSet ? <PowerSwitch className="m-0" entity={power.entity!} label={`${label} power`} /> : <span className="w-11" aria-hidden="true" />}
  </li>;
}

export function BoundToggleList({ title, rows, all_off }: z.infer<typeof toggleListSchema>) {
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
  return <DataCard title={title} status={[]} sources={sources}
    arrivals={listArrivals(displayed)} observedAt={oldestObservation(displayed)} observedLabel="Oldest reading"
    sourceExtra={<><p>Each switch uses its Entity's current support. Turn all off requests separate Commands for enabled, supported rows that are not already observed Off.</p><p>Group feedback counts observed Off readings; it is not an atomic multi-Device outcome.</p>{powers.filter(power => power.historyError).map(power => <p key={power.source.entity_id}>{power.resource.metadata?.label ?? "Power"}: since time unavailable.</p>)}</>}>
    <ul className="-my-1 grid list-none">{powers.map((power, index) => <SwitchRow key={power.source.entity_id} power={power} label={readingRows[index].name} />)}</ul>
    {all_off && canGroup && <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-2 border-t border-border pt-3.5">
      <Button variant="outline" size="touch" type="button" disabled={working || eligible.length === 0} onClick={() => {
        setAttempted(eligible.map(index => powers[index].source.entity_id));
        eligible.forEach(index => request(targets[index], false));
      }}>Turn all off</Button>
      <p className="flex-[1_1_9em] text-label leading-normal text-muted-foreground data-[failed]:text-destructive" role="status" data-failed={failed > 0 || undefined}>{members.length > 0 && (inProgress ? `Updating ${members.length} switches…` : `${off} of ${members.length} Off${failed ? ` · ${failed} failed` : off < members.length ? " · check rows" : ""}`)}</p>
    </div>}
  </DataCard>;
}
