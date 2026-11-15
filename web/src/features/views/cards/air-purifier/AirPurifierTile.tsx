import { useControl } from "../../data/provider";
import { ControlStatus } from "../../controls/ControlStatus";
import { supportsOperation } from "../../controls/support";
import { StatusList } from "../CardFrame";
import { TileFrame, tileActionClass } from "../TileFrame";
import { Reading } from "../Reading";
import { listArrivals, oldestObservation } from "../reading-list/ReadingRows";
import { choiceWords } from "./category";
import { Category, usePurifier } from "./data";
import type { AirPurifierProps } from "./schema";

export function AirPurifierTile({ props, title }: { props: AirPurifierProps; title: string }) {
  const { rows, entities, sources, status, allUnavailable, sourceExtra } = usePurifier(props);
  const [control, request] = useControl({ entityId: props.power.entity_id, operation: "set" });
  const working = control.phase === "pending" || control.phase === "confirming";
  const power = rows.find(row => row.key === "power")!, pm = rows.find(row => row.key === "pm25")!, mode = rows.find(row => row.key === "mode");
  const on = entities.power?.state?.value === true;
  const canSet = !!entities.power && supportsOperation(entities.power, "set");
  const value = pm.data && "value" in pm.data ? pm.data : undefined;
  const modeWord = mode?.data && "text" in mode.data ? choiceWords(mode.data.text) : undefined;
  const body = <Reading as="span" value={power.status.value} className="block @max-[200px]:text-reading-narrow">{power.data && "text" in power.data ? power.data.text : "—"}{on && modeWord && <small className="ms-1.5 text-compact-reading tracking-[-.02em] text-gray-9 data-[value=dimmed]:text-gray-8" data-value={mode?.status.value}>{modeWord}</small>}</Reading>;
  const displayed = rows.filter(row => row.key === "power" || row.key === "pm25" || (on && row.key === "mode"));
  return <TileFrame title={title} on={on && power.status.value === "live"} interactive={canSet} status={status} observedAt={oldestObservation(displayed)} observedLabel="Oldest reading" arrivals={listArrivals([pm])} sources={sources} sourceExtra={sourceExtra}>
    {canSet ? <button type="button" role="switch" data-slot="tile-action" className={tileActionClass} aria-label={`${title} power`} aria-checked={on} aria-busy={working || undefined} disabled={!entities.power!.enabled} onClick={() => request(!(working ? control.requested === true : on))}>{body}</button> : <p>{body}</p>}
    <div className="flex flex-wrap items-baseline gap-x-2.5 text-gray-9"><span className="data-[value=dimmed]:text-gray-8" data-value={pm.status.value}>PM2.5 {value ? <>{value.value.toFixed(value.format.fraction_digits)} <small className="text-muted-foreground">{value.format.unit_label}</small></> : "—"}</span>{value && <Category value={value.value} dimmed={pm.status.value === "dimmed"} />}</div>
    {displayed.map(row => { const entries = allUnavailable ? row.status.entries.filter(entry => entry.key !== "unavailable") : row.status.entries; return entries.length > 0 && <div key={row.key} className="mb-2 flex flex-wrap gap-x-2 gap-y-0.5 text-muted-foreground"><span>{row.name}</span><StatusList entries={entries} /></div>; })}
    {canSet ? <ControlStatus className="mt-0 min-h-[21px]" entityId={props.power.entity_id} subject="power" pending={value => value === true ? "Turning on…" : "Turning off…"} /> : <p className="min-h-[21px] text-label leading-normal text-muted-foreground">{entities.power ? "Read-only" : ""}</p>}
  </TileFrame>;
}
