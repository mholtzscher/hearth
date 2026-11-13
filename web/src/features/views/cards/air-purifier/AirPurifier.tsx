import { useControls } from "../../data/provider";
import { PowerSwitch } from "../../controls/PowerSwitch";
import { SharedControlStatus, type StatusControl } from "../../controls/SharedControlStatus";
import { supportsOperation } from "../../controls/support";
import { CardFrame, StatusList } from "../CardFrame";
import { useArrival } from "../observation";
import { listArrivals, oldestObservation, shownReading } from "../reading-list/ReadingRows";
import { FanStepper } from "./FanStepper";
import { ModeControl } from "./ModeControl";
import { AirPurifierTile } from "./AirPurifierTile";
import { choiceWords, choicesOf } from "./category";
import { Category, usePurifier } from "./data";
import type { AirPurifierProps, AirPurifierRole } from "./schema";

type Row = ReturnType<typeof usePurifier>["rows"][number];
function NumericRow({ row, hideUnavailable }: { row: Row; hideUnavailable: boolean }) {
  const data = row.data && "value" in row.data ? row.data : undefined;
  const glow = useArrival(data ? { updatedAt: data.observed_at, shown: shownReading(data), eligible: row.status.glows } : undefined);
  return <li className="view-row">
    {glow !== undefined && <span key={glow} className="view-row-arrival" aria-hidden="true" />}
    <span className="view-row-name">{row.name}</span>
    {row.key === "pm25" && data && <Category value={data.value} dimmed={row.status.value === "dimmed"} />}
    <span className="view-row-value" data-value={row.status.value}>{data ? <>{data.value.toFixed(data.format.fraction_digits)}<small>{data.format.unit_label}</small></> : "—"}</span>
    {row.key === "filter_life" && data && data.value <= 10 && <span className="view-air-purifier-note">Replace the filter soon</span>}
    <StatusList entries={hideUnavailable ? row.status.entries.filter(entry => entry.key !== "unavailable") : row.status.entries} />
  </li>;
}

export function AirPurifierCard({ props, title }: { props: AirPurifierProps; title: string }) {
  const { rows, entities, sources, status, allUnavailable, sourceExtra } = usePurifier(props);
  const row = (role: AirPurifierRole) => rows.find(item => item.key === role);
  const power = row("power"), mode = row("mode"), fan = row("fan_level");
  const controllable = (role: AirPurifierRole) => !!entities[role] && supportsOperation(entities[role]!, "set");
  const controls: StatusControl[] = [];
  if (controllable("power")) controls.push({ target: { entityId: props.power.entity_id, operation: "set" }, name: "Power", pending: value => value === true ? "Turning on…" : "Turning off…" });
  if (props.mode && controllable("mode")) controls.push({ target: { entityId: props.mode.entity_id, operation: "set" }, name: "Mode", pending: value => `Switching to ${choiceWords(String(value))}…` });
  if (props.fan_level && controllable("fan_level")) controls.push({ target: { entityId: props.fan_level.entity_id, operation: "set" }, name: "Fan level", pending: value => `Setting fan level to ${value}…` });
  if (props.display && controllable("display")) controls.push({ target: { entityId: props.display.entity_id, operation: "set" }, name: "Display", pending: value => `Turning the display ${value === true ? "on" : "off"}…` });
  if (props.child_lock && controllable("child_lock")) controls.push({ target: { entityId: props.child_lock.entity_id, operation: "set" }, name: "Child lock", pending: value => value === true ? "Locking the controls…" : "Unlocking the controls…" });
  const [modeStates] = useControls(props.mode ? [{ entityId: props.mode.entity_id, operation: "set" }] : []);
  const modeState = modeStates[0];
  const shownMode = modeState && (modeState.phase === "pending" || modeState.phase === "confirming") ? modeState.requested : entities.mode?.state?.value;
  const active = !props.mode || !choicesOf(entities.mode?.support).includes("manual") || shownMode === "manual";
  const observedMode = mode?.data && "text" in mode.data ? mode.data.text : undefined;
  const observedFan = fan?.data && "text" in fan.data ? fan.data.text : undefined;
  const numeric = rows.filter(item => item.key === "pm25" || item.key === "filter_life");
  return <CardFrame title={title} className="view-air-purifier" status={status} observedAt={oldestObservation(rows)} observedLabel="Oldest reading" arrivals={listArrivals(numeric)} sources={sources} sourceExtra={sourceExtra}>
    <div className="view-air-purifier-head"><div><p className="view-metric-value" data-value={power?.status.value}>{power?.data && "text" in power.data ? power.data.text : "—"}</p>
      {observedMode && <p className="view-air-purifier-detail" data-value={mode?.status.value}>{choiceWords(observedMode)}{observedMode === "manual" && observedFan && ` · fan ${observedFan}`}</p>}</div>
      {controllable("power") && <PowerSwitch entity={entities.power!} label={`${title} power`} />}
    </div>
    {power && <StatusList entries={allUnavailable ? power.status.entries.filter(entry => entry.key !== "unavailable") : power.status.entries} />}
    <ul className="view-rows view-air-purifier-rows">{numeric.map(item => <NumericRow key={item.key} row={item} hideUnavailable={allUnavailable} />)}</ul>
    {controllable("mode") && <ModeControl entity={entities.mode!} label="Mode" />}
    {controllable("fan_level") && <FanStepper entity={entities.fan_level!} levels={choicesOf(entities.fan_level?.support).length} active={active} label="Fan level" />}
    {(["mode", "fan_level"] as const).map(role => { const item = row(role); return item && item.status.entries.length > 0 && <div key={role} className="view-air-purifier-role-status"><span>{item.name}</span><StatusList entries={allUnavailable ? item.status.entries.filter(entry => entry.key !== "unavailable") : item.status.entries} /></div>; })}
    {(controllable("display") || controllable("child_lock")) && <div className="view-air-purifier-settings">{(["display", "child_lock"] as const).map(role => controllable(role) && <div className="view-air-purifier-setting" key={role}><span>{role === "display" ? "Display" : "Child lock"}</span><PowerSwitch entity={entities[role]!} label={role === "display" ? "Display" : "Child lock"} /></div>)}</div>}
    {(["display", "child_lock"] as const).map(role => { const item = row(role); return item && item.status.entries.length > 0 && <div key={role} className="view-air-purifier-role-status"><span>{item.name}</span><StatusList entries={allUnavailable ? item.status.entries.filter(entry => entry.key !== "unavailable") : item.status.entries} /></div>; })}
    <SharedControlStatus controls={controls} />
  </CardFrame>;
}

export function BoundAirPurifier(props: AirPurifierProps) {
  const title = props.label ?? "Air purifier";
  return props.layout === "tile" ? <AirPurifierTile props={props} title={title} /> : <AirPurifierCard props={props} title={title} />;
}
