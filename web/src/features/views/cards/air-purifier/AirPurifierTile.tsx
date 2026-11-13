import { useControl } from "../../data/provider";
import { ControlStatus } from "../../controls/ControlStatus";
import { supportsOperation } from "../../controls/support";
import { CardFrame, StatusList } from "../CardFrame";
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
  const body = <span className="view-metric-value" data-value={power.status.value}>{power.data && "text" in power.data ? power.data.text : "—"}{on && modeWord && <small className="view-air-purifier-tile-mode" data-value={mode?.status.value}>{modeWord}</small>}</span>;
  const displayed = rows.filter(row => row.key === "power" || row.key === "pm25" || (on && row.key === "mode"));
  return <CardFrame title={title} className={`view-toggle-tile view-air-purifier-tile${on && power.status.value === "live" ? " is-on" : ""}${canSet ? " is-interactive" : ""}`} status={status} compactTimeLabel observedAt={oldestObservation(displayed)} observedLabel="Oldest reading" arrivals={listArrivals([pm])} sources={sources} sourceExtra={sourceExtra}>
    {canSet ? <button type="button" role="switch" className="view-toggle-tile-action" aria-label={`${title} power`} aria-checked={on} aria-busy={working || undefined} disabled={!entities.power!.enabled} onClick={() => request(!(working ? control.requested === true : on))}>{body}</button> : <p>{body}</p>}
    <div className="view-air-purifier-tile-air"><span data-value={pm.status.value}>PM2.5 {value ? <>{value.value.toFixed(value.format.fraction_digits)} <small>{value.format.unit_label}</small></> : "—"}</span>{value && <Category value={value.value} dimmed={pm.status.value === "dimmed"} />}</div>
    {displayed.map(row => row.status.entries.length > 0 && <div key={row.key} className="view-air-purifier-role-status"><span>{row.name}</span><StatusList entries={allUnavailable ? row.status.entries.filter(entry => entry.key !== "unavailable") : row.status.entries} /></div>)}
    {canSet ? <ControlStatus entityId={props.power.entity_id} subject="power" pending={value => value === true ? "Turning on…" : "Turning off…"} /> : <p className="view-power-idle">{entities.power ? "Read-only" : ""}</p>}
  </CardFrame>;
}
